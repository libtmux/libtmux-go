package mcp

import (
	"context"

	tmux "github.com/libtmux/libtmux-go"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// listPanesInput takes no arguments; every pane on the configured server is
// listed.
type listPanesInput struct{}

// listPanesOutput carries the pane list. Panes is omitempty because the MCP
// SDK validates structured output against its generated schema even when a
// tool reports failure, and a nil slice is not an array.
type listPanesOutput struct {
	// Panes are every pane on the configured server.
	Panes []paneSummary `json:"panes,omitempty"`
}

// listPanes reports every pane on the server.
func (t *tools) listPanes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ listPanesInput,
) (*mcp.CallToolResult, listPanesOutput, error) {
	// One snapshot rather than a listing per pane, so session and window names
	// come from the same observation as the panes themselves.
	// An empty list rather than no list: a client reading .panes.length on an
	// unreachable server should see zero, not fail on a missing field.
	snapshot, err := t.target.Snapshot(ctx)
	if err != nil {
		return nil, listPanesOutput{Panes: []paneSummary{}}, err
	}

	// The socket is read once rather than per pane: every pane in one snapshot
	// belongs to the same server, which the snapshot itself guarantees.
	socket := t.socketPath(ctx)
	caller := t.callerIdentityFor(ctx)

	summaries := make([]paneSummary, 0, len(snapshot.Panes()))
	for _, pane := range snapshot.Panes() {
		summaries = append(summaries, summarizePane(pane, caller, socket))
	}
	return nil, listPanesOutput{Panes: summaries}, nil
}

// listWindowsInput takes no arguments; every window on the server is listed.
type listWindowsInput struct{}

// listWindowsOutput reports the windows found.
type listWindowsOutput struct {
	// Windows is every window on the configured server.
	Windows []windowSummary `json:"windows"`
}

// listWindows reports every window on the server.
//
// A client orienting itself asks what windows exist before it asks about
// panes, and a window is what a person switches between, so this is usually
// the first question rather than a coarser form of the second.
func (t *tools) listWindows(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ listWindowsInput,
) (*mcp.CallToolResult, listWindowsOutput, error) {
	snapshot, err := t.target.Snapshot(ctx)
	if err != nil {
		return nil, listWindowsOutput{}, err
	}
	summaries := make([]windowSummary, 0, len(snapshot.Windows()))
	for _, window := range snapshot.Windows() {
		summaries = append(summaries, summarizeWindow(window, len(window.Panes())))
	}
	return nil, listWindowsOutput{Windows: summaries}, nil
}

// listSessionsInput takes no arguments; every session on the server is listed.
type listSessionsInput struct{}

// listSessionsOutput reports the sessions found.
type listSessionsOutput struct {
	// Sessions is every session on the configured server.
	Sessions []sessionSummary `json:"sessions"`
}

// listSessions reports every session on the server.
func (t *tools) listSessions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ listSessionsInput,
) (*mcp.CallToolResult, listSessionsOutput, error) {
	snapshot, err := t.target.Snapshot(ctx)
	if err != nil {
		return nil, listSessionsOutput{}, err
	}
	summaries := make([]sessionSummary, 0, len(snapshot.Sessions()))
	for _, session := range snapshot.Sessions() {
		summaries = append(summaries, summarizeSession(session, len(session.Windows())))
	}
	return nil, listSessionsOutput{Sessions: summaries}, nil
}

// selectWindowInput chooses the window a session shows.
type selectWindowInput struct {
	// WindowID is the tmux window id to make current, such as @1.
	WindowID string `json:"windowId" jsonschema:"the tmux window id to make current"`
}

// selectWindowOutput reports the window that is now current.
type selectWindowOutput struct {
	// WindowID is the window that is now its session's current one.
	WindowID string `json:"windowId"`
}

// selectWindow makes a window its session's current one.
//
// Nothing else here changes what a session shows, so an agent that created a
// window had no way to put a person in front of it.
func (t *tools) selectWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectWindowInput,
) (*mcp.CallToolResult, selectWindowOutput, error) {
	window, err := t.strict().Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, selectWindowOutput{}, err
	}
	selected, err := window.Select(ctx)
	if err != nil {
		return nil, selectWindowOutput{}, err
	}
	return nil, selectWindowOutput{WindowID: selected.ID().String()}, nil
}

// addOrientationTools advertises the tools a client uses to find its way
// around before it does anything.
func addOrientationTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "list_panes",
		Annotations: readOnly("List tmux Panes"),
		Description: "List every tmux pane with its session, window, index, and " +
			"current command. Returns an empty list when the server is not running.",
	}, t.listPanes)
	register(server, t, &mcp.Tool{
		Name:        "list_windows",
		Annotations: readOnly("List tmux Windows"),
		Description: "Every window with its session, name, index, pane count, " +
			"and whether it is its session's current one.",
	}, t.listWindows)
	register(server, t, &mcp.Tool{
		Name:        "list_sessions",
		Annotations: readOnly("List tmux Sessions"),
		Description: "Every session with its name, window count, and how many " +
			"clients are attached to it.",
	}, t.listSessions)
	register(server, t, &mcp.Tool{
		Name:        "select_window",
		Annotations: mutating("Select a tmux Window"),
		Description: "Make one window its session's current window, which is what " +
			"puts a person in front of a window this client created.",
	}, t.selectWindow)
}
