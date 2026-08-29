package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported during MCP initialization. Installed binaries derive it
// from build metadata; source builds use fallbackVersion.
var Version = buildVersion()

const fallbackVersion = "v0.0.1-alpha.7"

// minimumTmuxVersion follows the terminal Connection capability floor because
// every MCP runtime owns one.
const minimumTmuxVersion = tmux.MinimumConnectionVersion

func (i *Instance) requireTmuxVersion(ctx context.Context) error {
	minimum, err := tmux.ParseVersion(minimumTmuxVersion)
	if err != nil {
		return err
	}
	return i.runtime.base.RequireVersion(ctx, minimum)
}

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

// minimumArguments adds numeric lower bounds that jsonschema-go cannot express
// through struct tags.
var minimumArguments = map[string]map[string]float64{
	"get_job":          {"timeoutSeconds": 0},
	"wait_for_channel": {"timeoutSeconds": 0},
	"wait_for_text":    {"idleSeconds": 0, "timeoutSeconds": 0},
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
	for argument, minimum := range minimumArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			minimum := minimum
			property.Minimum = &minimum
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
	registerHandler(server, t, capability, tool, handler, true)
}

// registerLocal advertises an MCP-only handler that delegates any tmux work
// to already registered handlers. Batch tools use it so their outer request
// does not hold an unbound runtime lease across an inner create_session call.
func registerLocal[In, Out any](
	server *mcp.Server,
	t *tools,
	capability Capability,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	registerHandler(server, t, capability, tool, handler, false)
}

func registerHandler[In, Out any](
	server *mcp.Server,
	t *tools,
	capability Capability,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	withRuntime bool,
) {
	if !t.level.permits(tool.Annotations) || !t.capabilities.permits(capability) {
		return
	}
	if tool.Meta == nil {
		tool.Meta = mcp.Meta{}
	}
	tool.Meta[CapabilityMetaKey] = string(capability)
	if withRuntime {
		handler = withRequestRuntime(t, handler)
	}
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

// NewServer returns a closeable MCP instance exposing target. It rejects an
// invalid target before allocating instance-owned resources.
func NewServer(target tmux.Server) (*Instance, error) {
	if _, err := target.SocketSelection(); err != nil {
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}
	if target.ConnectionBound() {
		return nil, ErrRuntimeTargetBound
	}

	instance := newInstance()
	runtime := newRuntime(instance.ctx, target, instance.terminal)
	instance.runtime = runtime
	tools := newToolRegistry()
	tools.instance = instance
	tools.runtime = runtime
	serverOptions := &mcp.ServerOptions{
		Instructions: callerInstructions(),
	}
	if tools.capabilities.permits(CapabilityMetadataRead) {
		serverOptions.CompletionHandler = tools.completeObserved
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
	instance.server = server
	instance.tools = tools
	tools.watchers = newWatchers(runtime)
	// Audit wraps the backstop so oversized refusals are recorded.
	server.AddReceivingMiddleware(backstop())
	writer, auditOwner := auditWriter()
	if writer != nil {
		server.AddReceivingMiddleware(audit(writer))
	}
	server.AddReceivingMiddleware(instance.scoped)

	registerToolGroups(server, tools)
	addResources(server, tools)
	addPrompts(server, tools)

	instance.audit = auditOwner
	return instance, nil
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
	instance, err := NewServer(target)
	if err != nil {
		return err
	}
	defer func() { _ = instance.Close() }()
	return instance.Run(ctx, stdio())
}

type tools struct {
	instance     *Instance
	runtime      *tmuxRuntime
	level        SafetyLevel
	capabilities capabilitySet
	watchers     *watchers
	dispatchers  map[string]dispatcher
	unbatchable  map[string]struct{}
	// Batch tools set batchable false to prevent nested dispatch.
	batchable bool
	// A process cannot change its containing pane. Failed discovery is not
	// cached because cancellation or transport loss may be transient.
	callerMutex  sync.Mutex
	caller       callerIdentity
	callerCached bool
}

// An invalid target has no socket identity and cannot match the caller.
func (t *tools) socketPath(_ context.Context) string {
	selection, err := t.runtime.base.SocketSelection()
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
