package mcp

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"sync"
	"time"

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
const fallbackVersion = "v0.0.1-alpha.3"

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
func register[In, Out any](
	server *mcp.Server,
	t *tools,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	if !t.level.permits(tool.Annotations) {
		return
	}
	mcp.AddTool(server, tool, handler)
	if !t.batchable {
		return
	}
	t.dispatchers[tool.Name] = dispatcher{
		annotations: tool.Annotations,
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			return batched(ctx, handler, arguments)
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
// driving the returned server itself keeps at most [jobsRetained] temporary
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
		target:      target,
		level:       level,
		dispatchers: map[string]dispatcher{},
		batchable:   true,
		jobs:        newJobs(),
	}
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
	return server.Run(ctx, handshakeOrderedTransport{inner: &mcp.StdioTransport{}})
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
	target   tmux.Server
	level    SafetyLevel
	watchers *watchers
	// dispatchers is how a batch reaches each advertised tool, filled in by
	// register as the tools are advertised.
	dispatchers map[string]dispatcher
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

// socketPath asks tmux where its socket is, so a pane's server can be compared
// with the one this process was told it belongs to. An unreachable server
// reports nothing, which makes every caller answer false rather than true.
func (t *tools) socketPath(ctx context.Context) string {
	result, err := t.target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
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
