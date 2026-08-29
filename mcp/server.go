package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported during MCP initialization. Installed binaries derive it
// from build metadata; source builds use fallbackVersion.
var Version = buildVersion()

const fallbackVersion = "v0.0.1-alpha.7"

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallbackVersion
	}
	return info.Main.Version
}

// closedArguments adds enums that jsonschema-go cannot express through these tags.
var closedArguments = map[string]map[string][]any{
	"show_option":                  {"scope": scopeValues},
	"set_option":                   {"scope": scopeValues},
	"show_hooks":                   {"scope": scopeValues},
	"split_window":                 {"direction": placementValues},
	"move_pane":                    {"direction": placementValues},
	"find_pane_by_position":        {"direction": {"above", "below", "left", "right"}},
	"list_panes":                   {"detail": {"", detailStandard, detailFull}},
	"get_recipe":                   {"name": recipeValues},
	"call_readonly_tools_batch":    {"onError": onErrorValues},
	"call_mutating_tools_batch":    {"onError": onErrorValues},
	"call_destructive_tools_batch": {"onError": onErrorValues},
}

var scopeValues = []any{"", scopeServer, scopeSession, scopeWindow, scopePane}

var placementValues = []any{"", "below", "above", "right", "left"}

var onErrorValues = []any{"", onErrorStop, onErrorContinue}

// recipeValues is derived from recipes to avoid a second name list.
var recipeValues = func() []any {
	values := make([]any, 0, len(recipes))
	for _, offered := range recipes {
		values = append(values, offered.name)
	}
	return values
}()

// listsAreLists removes nullable alternatives inferred for slices.
func listsAreLists(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if len(schema.Types) > 1 && slices.Contains(schema.Types, "array") {
		schema.Types = slices.DeleteFunc(schema.Types, func(kind string) bool {
			return kind == "null"
		})
	}
	listsAreLists(schema.Items)
	for _, property := range schema.Properties {
		listsAreLists(property)
	}
}

func constrain(name string, schema *jsonschema.Schema) {
	for argument, values := range closedArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			property.Enum = values
		}
	}
}

// register advertises permitted tools and records the same surface for batches.
func register[In, Out any](
	server *mcp.Server,
	t *tools,
	capability Capability,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	if !t.level.permits(tool.Annotations) || !t.capabilities.permits(capability) {
		return
	}
	if tool.Meta == nil {
		tool.Meta = mcp.Meta{}
	}
	tool.Meta[CapabilityMetaKey] = string(capability)
	handler = recovering(t, handler)
	// Infer early so closed argument enums can be applied before registration.
	if tool.InputSchema == nil {
		if schema, err := jsonschema.For[In](nil); err == nil {
			constrain(tool.Name, schema)
			tool.InputSchema = schema
		}
	}
	// Normalize inferred slices to the non-null reply contract.
	if tool.OutputSchema == nil {
		if schema, err := jsonschema.For[Out](nil); err == nil {
			listsAreLists(schema)
			tool.OutputSchema = schema
		}
	}
	// Retain the direct-call schema for batch validation.
	var resolved *jsonschema.Resolved
	if schema, ok := tool.InputSchema.(*jsonschema.Schema); ok {
		resolved, _ = schema.Resolve(nil)
	}
	mcp.AddTool(server, tool, handler)
	if !t.batchable {
		// Track advertised but unbatchable tools for accurate batch errors.
		t.unbatchable[tool.Name] = struct{}{}
		return
	}
	t.dispatchers[tool.Name] = dispatcher{
		annotations: tool.Annotations,
		call: func(
			ctx context.Context,
			request *mcp.CallToolRequest,
			arguments json.RawMessage,
		) (any, error) {
			return batched(ctx, request, handler, arguments, resolved)
		},
	}
}

// toolGroups keeps each registration group beside its handlers.
var toolGroups = []func(*mcp.Server, *tools){
	addOrientationTools,
	addCaptureTools,
	addInspectTools,
	addInputTools,
	addWaitTools,
	addChannelTools,
	addCreationTools,
	addLayoutTools,
	addPositionTools,
	addLifecycleTools,
	addServerTools,
	addBufferTools,
	addSettingsTools,
	addRecipeTools,
	addBatchTools,
}

// Instance owns an MCP server and the resources its tools allocate. The
// embedded SDK server keeps its methods available directly.
type Instance struct {
	*mcp.Server
	tools *tools
	audit io.Closer

	closeOnce sync.Once
	closeErr  error
}

// Close stops watchers, removes detached-job files, and closes an owned audit
// file. It does not close an Engine supplied through target.
func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	i.closeOnce.Do(func() {
		i.tools.watchers.close()
		i.tools.jobs.close()
		if i.audit != nil {
			i.closeErr = i.audit.Close()
		}
	})
	return i.closeErr
}

// NewServer returns a closeable MCP instance exposing target. It rejects an
// invalid target before allocating instance-owned resources.
func NewServer(target tmux.Server) (*Instance, error) {
	if _, err := target.SocketSelection(); err != nil {
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}

	tools := newToolRegistry()
	tools.jobs = newJobs()
	tools.reaching.Store(&target)
	serverOptions := &mcp.ServerOptions{
		Instructions: callerInstructions(),
	}
	if tools.capabilities.permits(CapabilityMetadataRead) {
		serverOptions.CompletionHandler = completionFor(target)
	}
	if tools.capabilities.permits(CapabilityMetadataRead) ||
		tools.capabilities.permits(CapabilityContentRead) {
		serverOptions.SubscribeHandler = tools.subscribe
		serverOptions.UnsubscribeHandler = tools.unsubscribe
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "libtmux",
		Version: Version,
	}, serverOptions)
	tools.watchers = newWatchers(server, target)
	// Audit wraps the backstop so oversized refusals are recorded.
	server.AddReceivingMiddleware(backstop())
	writer, auditOwner := auditWriter()
	if writer != nil {
		server.AddReceivingMiddleware(audit(writer))
	}

	registerToolGroups(server, tools)
	addResources(server, tools)
	addPrompts(server, tools)

	return &Instance{Server: server, tools: tools, audit: auditOwner}, nil
}

func newToolRegistry() *tools {
	capabilities, _ := capabilitiesFromEnvironment()
	return &tools{
		level:        safetyFromEnvironment(),
		capabilities: capabilities,
		dispatchers:  map[string]dispatcher{},
		unbatchable:  map[string]struct{}{},
		batchable:    true,
	}
}

func registerToolGroups(server *mcp.Server, tools *tools) {
	for _, add := range toolGroups {
		add(server, tools)
	}
}

// Run serves target over stdin and stdout until ctx is done.
func Run(ctx context.Context, target tmux.Server) error {
	connected, pool := Connect(ctx, target)
	if pool != nil {
		defer func() { _ = pool.Close() }()
	}
	instance, err := NewServer(connected)
	if err != nil {
		return err
	}
	defer func() { _ = instance.Close() }()
	return instance.Run(ctx, handshakeOrderedTransport{inner: stdio()})
}

// Connect opens a control pool only when target has no engine and has a session.
// Failure leaves subprocess execution in place. The returned pool is an
// attached tmux client that the caller must close.
//
// Each connection appears in list-clients, increments session_attached, and
// may prevent destroy-unattached.
func Connect(ctx context.Context, target tmux.Server) (tmux.Server, *tmux.ControlPool) {
	if target.Engine() != nil {
		return target, nil
	}
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return target, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	connected, _, pool, err := target.OpenControlPool(
		openCtx, sessions[0], tmux.ControlPoolRequest{},
	)
	if err != nil {
		return target, nil
	}
	return connected, pool
}

// connectTimeout bounds control setup before the MCP server begins replying.
const connectTimeout = 5 * time.Second

type tools struct {
	// reaching is replaced atomically when a concurrent call retires the pool.
	reaching     atomic.Pointer[tmux.Server]
	level        SafetyLevel
	capabilities capabilitySet
	watchers     *watchers
	// consentMutex guards per-session caller-pane consent.
	consented    map[*mcp.ServerSession]map[string]bool
	consentMutex sync.Mutex
	dispatchers  map[string]dispatcher
	unbatchable  map[string]struct{}
	// Batch tools set batchable false to prevent nested dispatch.
	batchable bool
	// A process cannot change its containing pane, so caller is resolved once.
	caller     callerIdentity
	callerOnce sync.Once
	jobs       *jobs
}

func (t *tools) tmux() tmux.Server { return *t.reaching.Load() }

// retirePool replaces a dead control engine with subprocess execution.
func (t *tools) retirePool() {
	current := t.tmux()
	if current.Engine() == nil {
		return
	}
	plain := current.WithEngine(current.SubprocessEngine())
	t.reaching.Store(&plain)
}

// recovering retires a dead pool but never retries a call that may have acted.
func recovering[In, Out any](
	t *tools,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(
		ctx context.Context,
		request *mcp.CallToolRequest,
		input In,
	) (*mcp.CallToolResult, Out, error) {
		result, output, err := handler(ctx, request, input)
		if err != nil && errors.Is(err, tmux.ErrControlClosed) {
			t.retirePool()
		}
		return result, output, err
	}
}

// An invalid target has no socket identity and cannot match the caller.
func (t *tools) socketPath(_ context.Context) string {
	selection, err := t.tmux().SocketSelection()
	if err != nil {
		return ""
	}
	return selection.Path
}

// toolFailure preserves a partial result; returning err makes the SDK discard it.
func toolFailure(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
