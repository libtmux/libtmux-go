package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported to MCP clients during initialization.
//
// It is read from the build when there is one to read, so a binary somebody
// installed reports the version they installed rather than whatever was
// written down when the source was last edited. A build from a working tree
// has no module version, and falls back to the constant below.
var Version = buildVersion()

// fallbackVersion is what a build from source reports, where the module
// version is unknown to the toolchain.
const fallbackVersion = "v0.0.1-alpha.7"

// buildVersion reports the module version this binary was built from.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallbackVersion
	}
	return info.Main.Version
}

// register advertises a tool when the operator's safety level permits it, and
// records how a batch reaches it.
//
// Withholding at registration rather than refusing at call time is the point:
// a tool that is never advertised cannot be reached by any prompt, while one
// that is advertised and refused invites a model to keep trying. Recording the
// batch entry here is the same idea one layer down — a tool a level withheld
// was never registered, so it is not in the table a batch dispatches through
// either, and there is no second list to keep in step.
// closedArguments names, per tool, the arguments whose set of accepted values
// is closed, and what that set holds.
//
// A struct tag cannot say this: jsonschema-go reads the whole tag as the
// description and offers nothing else, so a set the code closes was closed
// only in prose on the wire. A client had nothing to validate against and a
// model had to infer the words out of a sentence.
//
// Keyed by tool as well as argument, because the same argument name is not the
// same set twice. Empty is listed wherever the tool documents it as a default,
// so the schema accepts exactly what the tool accepts.
var closedArguments = map[string]map[string][]any{
	"show_option":  {"scope": scopeValues},
	"set_option":   {"scope": scopeValues},
	"show_hooks":   {"scope": scopeValues},
	"split_window": {"direction": placementValues},
	"move_pane":    {"direction": placementValues},
	// Required, and no default: which side to look toward is the whole
	// question find_pane_by_position asks.
	"find_pane_by_position": {"direction": {"above", "below", "left", "right"}},
	"list_panes":            {"detail": {"", detailStandard, detailFull}},
	"get_recipe":            {"name": recipeValues},
}

// scopeValues is what a scope takes. Every tool taking one reads empty as pane
// scope.
var scopeValues = []any{"", scopeServer, scopeSession, scopeWindow, scopePane}

// placementValues is where a pane goes. Both tools taking one read empty as
// below.
var placementValues = []any{"", "below", "above", "right", "left"}

// recipeValues comes from the recipes themselves rather than a second list of
// their names, which would be wrong the first time one is added.
var recipeValues = func() []any {
	values := make([]any, 0, len(recipes))
	for _, offered := range recipes {
		values = append(values, offered.name)
	}
	return values
}()

// constrain writes a tool's closed sets into the schema clients validate
// against.
func constrain(name string, schema *jsonschema.Schema) {
	for argument, values := range closedArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			property.Enum = values
		}
	}
}

func register[In, Out any](
	server *mcp.Server,
	t *tools,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	if !t.level.permits(tool.Annotations) {
		return
	}
	handler = recovering(t, handler)
	// Inferred here rather than left to the SDK, so the closed sets above can
	// be written into it before anything validates against it.
	if tool.InputSchema == nil {
		if schema, err := jsonschema.For[In](nil); err == nil {
			constrain(tool.Name, schema)
			tool.InputSchema = schema
		}
	}
	mcp.AddTool(server, tool, handler)
	if !t.batchable {
		// Advertised, but not reachable from inside a batch. Remembering which
		// tools those are is what lets a batch say so, rather than telling a
		// client that a tool it can see listed does not exist.
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
			return batched(ctx, request, handler, arguments)
		},
	}
}

// toolGroups are the sets of tools this server offers, each declared beside
// the handlers it advertises.
//
// A tool that is described in one file and registered in another drifts: the
// description stops matching the handler, and adding one means remembering the
// second place. Each group here is one file's worth of tools, so a file is the
// unit of both.
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

// NewServer returns an MCP server exposing target through the tools below.
//
// Anything the server holds beyond the process is released by [Run]. A caller
// driving the returned server itself keeps a bounded number of temporary
// directories, which the operating system reclaims with the process.
func NewServer(target tmux.Server) *mcp.Server {
	server, _ := newServer(target)
	return server
}

// newServer builds the server and hands back the tools behind it, so that Run
// can release what they hold when it stops.
func newServer(target tmux.Server) (*mcp.Server, *tools) {
	level := safetyFromEnvironment()
	tools := &tools{
		level:       level,
		dispatchers: map[string]dispatcher{},
		unbatchable: map[string]struct{}{},
		batchable:   true,
		jobs:        newJobs(),
	}
	tools.reaching.Store(&target)
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "libtmux",
		Version: Version,
	}, &mcp.ServerOptions{
		Instructions:       callerInstructions(),
		CompletionHandler:  completionFor(target),
		SubscribeHandler:   tools.subscribe,
		UnsubscribeHandler: tools.unsubscribe,
	})
	tools.watchers = newWatchers(server, target)
	// The backstop wraps everything the tools produce; the record, when an
	// operator asked for one, wraps the backstop so a refused reply is
	// recorded as refused.
	server.AddReceivingMiddleware(backstop())
	if writer := auditWriter(); writer != nil {
		server.AddReceivingMiddleware(audit(writer))
	}

	for _, add := range toolGroups {
		add(server, tools)
	}
	addResources(server, tools)
	addPrompts(server, level)

	return server, tools
}

// Run serves target over stdin and stdout until ctx is done.
func Run(ctx context.Context, target tmux.Server) error {
	connected, pool := Connect(ctx, target)
	if pool != nil {
		defer func() { _ = pool.Close() }()
	}
	server, tools := newServer(connected)
	// A command left running records itself in a temporary directory this
	// process owns. Collecting it removes that directory; a server that stops
	// while some are uncollected removes the rest here.
	defer tools.jobs.close()
	return server.Run(ctx, handshakeOrderedTransport{inner: stdio()})
}

// Connect puts the server on a control-mode transport when it can, so a client
// issuing many small reads does not start a tmux process for each one.
//
// A control connection has to attach to a session, and this command is often
// started against a tmux server that has none yet. That is not a reason to
// create one: a session invented here would outlive the pool and appear in the
// user's own tmux. So a server with no session keeps starting a process per
// command, which is what it did before, and the tools behave identically
// either way.
//
// The connection is a tmux client for as long as this process runs, not for
// the length of one call. It appears in list-clients, counts toward
// session_attached, and keeps destroy-unattached from reclaiming the session
// it attached to. A caller that would rather pay a process per command than
// hold a client open passes a target already carrying
// [tmux.Server.SubprocessEngine], which this leaves alone.
//
// Opening it is bounded. This runs before the server has answered anything, so
// a control connection that does not come up would otherwise hold the whole
// process short of its first reply, and a client would see a server that never
// speaks rather than one that is slower than it could be. The connection is an
// optimisation and the fallback is the behaviour without it, so giving up on
// it costs a process per command and nothing else.
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

// connectTimeout bounds opening the control connection at startup. Well beyond
// what attaching takes, and well inside what a client waits before deciding the
// server is not going to answer.
const connectTimeout = 5 * time.Second

type tools struct {
	// reaching holds the tmux server every tool goes through. It is a pointer
	// that is replaced rather than a value that is written, because a pooled
	// control connection is retired from whichever call first finds it dead
	// while others are in flight.
	reaching atomic.Pointer[tmux.Server]
	level    SafetyLevel
	watchers *watchers
	// dispatchers is how a batch reaches each advertised tool, filled in by
	// register as the tools are advertised.
	dispatchers map[string]dispatcher
	// unbatchable names the tools this server advertises but does not put in
	// that table, so a batch asked for one can say which it is.
	unbatchable map[string]struct{}
	// batchable records whether a tool being registered belongs in that table.
	// The batch tools themselves do not: nesting one inside another buries
	// which call failed.
	batchable bool
	// caller is which pane this process runs in, worked out once. The pane a
	// process is in does not change, and finding it costs a walk up the
	// process tree and a listing.
	caller     callerIdentity
	callerOnce sync.Once
	// jobs holds the commands a caller detached and has not collected.
	jobs *jobs
}

// tmux is the server tools reach through now, which is not always the one
// this process started with.
func (t *tools) tmux() tmux.Server { return *t.reaching.Load() }

// retirePool drops a pooled control connection that has died.
//
// The pool is an optimisation: before it existed every command spawned a
// process, and that is what this falls back to. tmux going away and coming
// back is an ordinary thing for a person to do, and it used to cost every tool
// for the life of this process -- each call answering "control client is
// closed" because nothing reopened what died with the old server.
func (t *tools) retirePool() {
	current := t.tmux()
	if current.Engine() == nil {
		return
	}
	plain := current.WithEngine(current.SubprocessEngine())
	t.reaching.Store(&plain)
}

// recovering retires a pooled connection that has died, so the calls after
// this one work again.
//
// The failed call is not run a second time. A wait opens a control connection
// of its own and reports the same error when it ends, so this cannot tell a
// dead pool from a connection that did its job -- and retrying the second kind
// runs a wait that already finished for the whole of its timeout again. One
// call reporting the restart is the honest cost of not knowing which it was.
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

// socketPath asks tmux where its socket is, so a pane's server can be compared
// with the one this process was told it belongs to. An unreachable server
// reports nothing, which makes every caller answer false rather than true.
func (t *tools) socketPath(ctx context.Context) string {
	result, err := t.tmux().Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(result.Stdout) == 0 {
		return ""
	}
	return result.Stdout[0]
}

// toolFailure reports a tmux failure alongside a result worth keeping, which
// is why it exists rather than returning the error: the SDK turns a returned
// error into the same tool-level error content, but returns before it
// serializes anything a handler produced. A failure with nothing to report
// therefore returns its error, and only a partial result comes through here.
func toolFailure(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
