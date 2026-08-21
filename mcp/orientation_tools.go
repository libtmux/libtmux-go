package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// orientationSnapshot reads the server for the tools that answer "what is
// there", and reports a server that is not running as one holding nothing.
//
// The tmux module declines to make that call, because it cannot tell an absent
// server from a socket path a program got wrong, and answering either with an
// empty result would hide the second. Here the call is answerable: a client
// asking what panes exist before starting anything is the ordinary opening
// move, and failing it would make every session begin with an error. A socket
// this server cannot use still fails, because [tmux.ErrNoServer] classifies
// only what tmux refused before running the command.
func (t *tools) orientationSnapshot(ctx context.Context) (tmux.Snapshot, error) {
	snapshot, err := t.tmux().Snapshot(ctx)
	if errors.Is(err, tmux.ErrNoServer) {
		return tmux.Snapshot{}, nil
	}
	return snapshot, err
}

// A listing narrows where it is read rather than where it is used.
//
// Every one of these tools used to answer with the whole server, which is the
// answer to a question nobody asks: a caller wants the pane running the dev
// server, not the forty around it. The cost of the difference is paid in the
// caller's context, once per call, and it grows with somebody else's tmux
// rather than with the question.
//
// The criteria are matched here against the snapshot already taken, not pushed
// into tmux as a -f expression. tmux's filter language is a format, and a
// format containing #(...) runs it as a shell command against the server's own
// client -- which this server holds open, so it would run rather than losing
// the teardown race a one-shot tmux client wins. Compiling a caller's words
// into that language would make every listing tool an execution vector while
// still reporting readOnlyHint. Matching in Go has no such surface, and the
// pushdown it gives up is a local pipe carrying a few kilobytes.
//
// Every reply reports the total it selected from, so a caller can tell a
// filter that matched one pane from a server that only has one, and can see
// what the filter saved it.

// Detail levels a listing may be read at.
const (
	// detailStandard is what a listing has always returned: identity,
	// position, and the current command.
	detailStandard = "standard"
	// detailFull adds the per-pane state a supervising caller would otherwise
	// call get_pane_info once per pane to learn.
	detailFull = "full"
)

// listPanesInput narrows which panes are listed and how much is said about
// each. Every field is optional; omitting all of them lists the server.
type listPanesInput struct {
	// SessionName limits the listing to one session's panes.
	SessionName string `json:"sessionName,omitempty" jsonschema:"list only this session's panes"`
	// WindowID limits the listing to one window's panes.
	WindowID string `json:"windowId,omitempty" jsonschema:"list only this window's panes, such as @1"`
	// Command keeps panes whose current command contains this text, ignoring
	// case. Substring rather than exact because tmux reports the foreground
	// program, which is "node" for something a caller thinks of as npm.
	Command string `json:"command,omitempty" jsonschema:"keep panes whose current command contains this text, ignoring case"`
	// PathUnder keeps panes whose working directory is at or below this path.
	PathUnder string `json:"pathUnder,omitempty" jsonschema:"keep panes whose working directory is at or below this path"`
	// Dead keeps panes whose process has exited, or only those still running.
	Dead *bool `json:"dead,omitempty" jsonschema:"keep only panes whose process has exited, or only those still running"`
	// Active keeps only each window's active pane, or only the others.
	Active *bool `json:"active,omitempty" jsonschema:"keep only active panes, or only inactive ones"`
	// Detail chooses how much is reported per pane.
	Detail string `json:"detail,omitempty" jsonschema:"how much to report per pane: standard, or full to add exit status, path, title, and history size"`
}

// listedPane is a pane as a listing reports it: the shape every other tool
// uses, plus the state only a full listing carries.
type listedPane struct {
	paneSummary
	// Status is present only at detail full.
	Status *paneStatus `json:"status,omitempty"`
}

// listPanesOutput carries the pane list.
//
// Panes is always an array, including when nothing matched, so a client can
// count what it got without first checking that the field is there. Every
// return below sets it for that reason: the MCP SDK validates structured
// output against its generated schema even when a tool reports failure, and a
// nil slice is not an array.
type listPanesOutput struct {
	// Panes are the panes that matched.
	Panes []listedPane `json:"panes"`
	// Total is how many panes the server held before the criteria were
	// applied, so a caller can see what its filter selected from.
	Total int `json:"total"`
	// Skipped is how many the criteria left out. Without it a caller
	// reads a shorter list against a larger total as a reply that was
	// shortened, which is what the tools returning pane text do.
	Skipped int `json:"skipped,omitempty"`
}

// listPanes reports the panes matching a caller's criteria.
func (t *tools) listPanes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listPanesInput,
) (*mcp.CallToolResult, listPanesOutput, error) {
	detail, err := resolveDetail(input.Detail)
	if err != nil {
		return nil, listPanesOutput{Panes: []listedPane{}}, err
	}
	// One snapshot rather than a listing per pane, so session and window names
	// come from the same observation as the panes themselves.
	snapshot, err := t.orientationSnapshot(ctx)
	if err != nil {
		return nil, listPanesOutput{Panes: []listedPane{}}, err
	}

	// The socket is read once rather than per pane: every pane in one snapshot
	// belongs to the same server, which the snapshot itself guarantees.
	socket := t.socketPath(ctx)
	caller := t.callerIdentityFor(ctx)

	panes := snapshot.Panes()
	summaries := make([]listedPane, 0, len(panes))
	for _, pane := range panes {
		summary := summarizePane(pane, caller, socket)
		if !input.keeps(pane, summary) {
			continue
		}
		listed := listedPane{paneSummary: summary}
		if detail == detailFull {
			listed.Status = readPaneStatus(pane)
		}
		summaries = append(summaries, listed)
	}
	return nil, listPanesOutput{
		Panes: summaries, Total: len(panes),
		Skipped: len(panes) - len(summaries),
	}, nil
}

// keeps reports whether one pane satisfies every criterion given. Criteria
// combine with AND, which is what a caller naming two of them means.
func (in listPanesInput) keeps(pane tmux.Pane, summary paneSummary) bool {
	formats := pane.Formats()
	switch {
	case in.SessionName != "" && summary.Session != in.SessionName:
		return false
	case in.WindowID != "" && summary.WindowID != in.WindowID:
		return false
	case in.Command != "" && !containsFold(summary.CurrentCommand, in.Command):
		return false
	case in.Active != nil && summary.Active != *in.Active:
		return false
	}
	if in.Dead != nil {
		dead, _ := formats.PaneDead()
		if dead != *in.Dead {
			return false
		}
	}
	if in.PathUnder != "" {
		path, _ := formats.PaneCurrentPath()
		if !isAtOrUnder(path, in.PathUnder) {
			return false
		}
	}
	return true
}

// resolveDetail validates the level a caller asked for. An unknown level is
// refused rather than treated as the default, because a caller that asked for
// more and silently got less would report the absent fields as absent state.
func resolveDetail(level string) (string, error) {
	switch level {
	case "", detailStandard:
		return detailStandard, nil
	case detailFull:
		return detailFull, nil
	default:
		return "", fmt.Errorf("detail %q is not standard or full", level)
	}
}

// containsFold reports whether text contains part, ignoring case, matching how
// search_panes treats the text a caller repeats from prose.
func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(part))
}

// isAtOrUnder reports whether path is the directory root or inside it.
//
// The separator check is what keeps /home/work from matching a filter for
// /home/wo, which a plain prefix test would accept.
func isAtOrUnder(path, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if cleanPath == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanPath, strings.TrimSuffix(cleanRoot, "/")+"/")
}

// listWindowsInput narrows which windows are listed. Every field is optional.
type listWindowsInput struct {
	// SessionName limits the listing to one session's windows.
	SessionName string `json:"sessionName,omitempty" jsonschema:"list only this session's windows"`
	// Name keeps windows whose name contains this text, ignoring case.
	Name string `json:"name,omitempty" jsonschema:"keep windows whose name contains this text, ignoring case"`
	// Active keeps only each session's current window, or only the others.
	Active *bool `json:"active,omitempty" jsonschema:"keep only current windows, or only the others"`
}

// listWindowsOutput reports the windows found.
type listWindowsOutput struct {
	// Windows are the windows that matched.
	Windows []windowSummary `json:"windows"`
	// Total is how many windows the server held before the criteria were
	// applied.
	Total int `json:"total"`
	// Skipped is how many the criteria left out. Without it a caller
	// reads a shorter list against a larger total as a reply that was
	// shortened, which is what the tools returning pane text do.
	Skipped int `json:"skipped,omitempty"`
}

// listWindows reports the windows matching a caller's criteria.
//
// A client orienting itself asks what windows exist before it asks about
// panes, and a window is what a person switches between, so this is usually
// the first question rather than a coarser form of the second.
func (t *tools) listWindows(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listWindowsInput,
) (*mcp.CallToolResult, listWindowsOutput, error) {
	snapshot, err := t.orientationSnapshot(ctx)
	if err != nil {
		return nil, listWindowsOutput{}, err
	}
	windows := snapshot.Windows()
	summaries := make([]windowSummary, 0, len(windows))
	for _, window := range windows {
		// Snapshot records carry their relations, so the pane count is there
		// to read rather than another listing.
		panes, _ := window.Panes()
		summary := summarizeWindow(window, len(panes))
		switch {
		case input.SessionName != "" && summary.Session != input.SessionName:
			continue
		case input.Name != "" && !containsFold(summary.Name, input.Name):
			continue
		case input.Active != nil && summary.Active != *input.Active:
			continue
		}
		summaries = append(summaries, summary)
	}
	return nil, listWindowsOutput{
		Windows: summaries, Total: len(windows),
		Skipped: len(windows) - len(summaries),
	}, nil
}

// listSessionsInput narrows which sessions are listed. Every field is optional.
type listSessionsInput struct {
	// Name keeps sessions whose name contains this text, ignoring case.
	Name string `json:"name,omitempty" jsonschema:"keep sessions whose name contains this text, ignoring case"`
	// Attached keeps only sessions a client is attached to, or only those
	// nobody is looking at.
	Attached *bool `json:"attached,omitempty" jsonschema:"keep only sessions a client is attached to, or only those nobody is watching"`
}

// listSessionsOutput reports the sessions found.
type listSessionsOutput struct {
	// Sessions are the sessions that matched.
	Sessions []sessionSummary `json:"sessions"`
	// Total is how many sessions the server held before the criteria were
	// applied.
	Total int `json:"total"`
	// Skipped is how many the criteria left out. Without it a caller
	// reads a shorter list against a larger total as a reply that was
	// shortened, which is what the tools returning pane text do.
	Skipped int `json:"skipped,omitempty"`
}

// listSessions reports the sessions matching a caller's criteria.
func (t *tools) listSessions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listSessionsInput,
) (*mcp.CallToolResult, listSessionsOutput, error) {
	snapshot, err := t.orientationSnapshot(ctx)
	if err != nil {
		return nil, listSessionsOutput{}, err
	}
	sessions := snapshot.Sessions()
	summaries := make([]sessionSummary, 0, len(sessions))
	for _, session := range sessions {
		windows, _ := session.Windows()
		summary := summarizeSession(session, len(windows))
		switch {
		case input.Name != "" && !containsFold(summary.Name, input.Name):
			continue
		case input.Attached != nil && (summary.Attached > 0) != *input.Attached:
			continue
		}
		summaries = append(summaries, summary)
	}
	return nil, listSessionsOutput{
		Sessions: summaries, Total: len(sessions),
		Skipped: len(sessions) - len(summaries),
	}, nil
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
	window, err := t.tmux().Window(ctx, tmux.WindowID(input.WindowID))
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
		Description: "List tmux panes with their session, window, index, and " +
			"current command. Narrow with sessionName, windowId, command, " +
			"pathUnder, dead, or active rather than listing the server and " +
			"discarding most of the answer: on a busy server that is the " +
			"difference between one pane and forty. Ask for detail full to add " +
			"each pane's exit status, path, title, and history size, which is how " +
			"to check on several panes without capturing any of them. Returns an " +
			"empty list when the server is not running.",
	}, t.listPanes)
	register(server, t, &mcp.Tool{
		Name:        "list_windows",
		Annotations: readOnly("List tmux Windows"),
		Description: "Windows with their session, name, index, pane count, and " +
			"whether each is its session's current one. Narrow with sessionName, " +
			"name, or active.",
	}, t.listWindows)
	register(server, t, &mcp.Tool{
		Name:        "list_sessions",
		Annotations: readOnly("List tmux Sessions"),
		Description: "Sessions with their name, window count, and how many " +
			"clients are attached. Narrow with name, or with attached to find " +
			"which sessions somebody is actually looking at.",
	}, t.listSessions)
	register(server, t, &mcp.Tool{
		Name:        "select_window",
		Annotations: settling("Select a tmux Window"),
		Description: "Make one window its session's current window, which is what " +
			"puts a person in front of a window this client created.",
	}, t.selectWindow)
}
