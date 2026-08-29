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

// orientationSnapshot treats [tmux.ErrNoServer] as an empty topology; other
// failures remain errors.
func (t *tools) orientationSnapshot(ctx context.Context) (tmux.Snapshot, bool, error) {
	snapshot, err := t.tmux(ctx).Snapshot(ctx)
	if errors.Is(err, tmux.ErrNoServer) {
		return tmux.Snapshot{}, false, nil
	}
	return snapshot, err == nil, err
}

// noServerNote distinguishes an absent daemon from an empty result.
const noServerNote = "no tmux server is running on this socket; " +
	"get_server_info says which socket that is, and create_session starts one"

func noteWhenAbsent(running bool) string {
	if running {
		return ""
	}
	return noServerNote
}

// Listing filters run against a snapshot in Go; tmux -f expressions may execute
// #() shell commands.

const (
	detailStandard = "standard"
	detailFull     = "full"
)

type listPanesInput struct {
	SessionName string `json:"sessionName,omitempty" jsonschema:"list only this session's panes"`
	WindowID    string `json:"windowId,omitempty" jsonschema:"list only this window's panes, such as @1"`
	Command     string `json:"command,omitempty" jsonschema:"keep panes whose current command contains this text, ignoring case"`
	PathUnder   string `json:"pathUnder,omitempty" jsonschema:"keep panes whose working directory is at or below this path"`
	Dead        *bool  `json:"dead,omitempty" jsonschema:"keep only panes whose process has exited, or only those still running"`
	Active      *bool  `json:"active,omitempty" jsonschema:"keep only active panes, or only inactive ones"`
	Detail      string `json:"detail,omitempty" jsonschema:"how much to report per pane; full adds exit status, path, title, and history size"`
}

type listedPane struct {
	paneSummary
	Status *paneStatus `json:"status,omitempty"`
}

type listPanesOutput struct {
	// Panes encodes as an array in successful structured results, including
	// when no panes match.
	Panes      []listedPane `json:"panes"`
	Total      int          `json:"total"`
	Skipped    int          `json:"skipped,omitempty"`
	ServerNote string       `json:"serverNote,omitempty"`
}

func (t *tools) listPanes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listPanesInput,
) (*mcp.CallToolResult, listPanesOutput, error) {
	detail, err := resolveDetail(input.Detail)
	if err != nil {
		return nil, listPanesOutput{Panes: []listedPane{}}, err
	}
	// One snapshot keeps pane and relation data observationally consistent.
	snapshot, running, err := t.orientationSnapshot(ctx)
	if err != nil {
		return nil, listPanesOutput{Panes: []listedPane{}}, err
	}

	panes := snapshot.Panes()
	// Every pane in one snapshot belongs to the same server. An empty snapshot
	// needs no process-tree query, preserving the absent-server result.
	socket := t.socketPath(ctx)
	caller := callerIdentity{}
	if len(panes) > 0 {
		caller, err = t.callerIdentityFor(ctx)
		if err != nil {
			return nil, listPanesOutput{Panes: []listedPane{}}, err
		}
	}
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
		Skipped: len(panes) - len(summaries), ServerNote: noteWhenAbsent(running),
	}, nil
}

// keeps combines all supplied criteria with AND.
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

// resolveDetail rejects unknown values instead of silently omitting requested
// fields.
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

func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(part))
}

// The separator prevents sibling-prefix matches such as /home/work and
// /home/wo.
func isAtOrUnder(path, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if cleanPath == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanPath, strings.TrimSuffix(cleanRoot, "/")+"/")
}

type listWindowsInput struct {
	SessionName string `json:"sessionName,omitempty" jsonschema:"list only this session's windows"`
	Name        string `json:"name,omitempty" jsonschema:"keep windows whose name contains this text, ignoring case"`
	Active      *bool  `json:"active,omitempty" jsonschema:"keep only current windows, or only the others"`
}

type listWindowsOutput struct {
	Windows    []windowSummary `json:"windows"`
	Total      int             `json:"total"`
	Skipped    int             `json:"skipped,omitempty"`
	ServerNote string          `json:"serverNote,omitempty"`
}

func (t *tools) listWindows(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listWindowsInput,
) (*mcp.CallToolResult, listWindowsOutput, error) {
	snapshot, running, err := t.orientationSnapshot(ctx)
	if err != nil {
		return nil, listWindowsOutput{}, err
	}
	windows := snapshot.Windows()
	summaries := make([]windowSummary, 0, len(windows))
	for _, window := range windows {
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
		Skipped: len(windows) - len(summaries), ServerNote: noteWhenAbsent(running),
	}, nil
}

type listSessionsInput struct {
	Name     string `json:"name,omitempty" jsonschema:"keep sessions whose name contains this text, ignoring case"`
	Attached *bool  `json:"attached,omitempty" jsonschema:"keep only sessions a client is attached to, or only those nobody is watching"`
}

type listSessionsOutput struct {
	Sessions   []sessionSummary `json:"sessions"`
	Total      int              `json:"total"`
	Skipped    int              `json:"skipped,omitempty"`
	ServerNote string           `json:"serverNote,omitempty"`
}

func (t *tools) listSessions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listSessionsInput,
) (*mcp.CallToolResult, listSessionsOutput, error) {
	snapshot, running, err := t.orientationSnapshot(ctx)
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
		Skipped: len(sessions) - len(summaries), ServerNote: noteWhenAbsent(running),
	}, nil
}

type selectWindowInput struct {
	WindowID string `json:"windowId" jsonschema:"the tmux window id to make current"`
}

type selectWindowOutput struct {
	WindowID string `json:"windowId"`
}

func (t *tools) selectWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectWindowInput,
) (*mcp.CallToolResult, selectWindowOutput, error) {
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, selectWindowOutput{}, notFound(err, "window", input.WindowID, "list_windows")
	}
	selected, err := window.Select(ctx)
	if err != nil {
		return nil, selectWindowOutput{}, err
	}
	return nil, selectWindowOutput{WindowID: selected.ID().String()}, nil
}

func addOrientationTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
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
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "list_windows",
		Annotations: readOnly("List tmux Windows"),
		Description: "Windows with their session, name, index, pane count, and " +
			"whether each is its session's current one. Narrow with sessionName, " +
			"name, or active.",
	}, t.listWindows)
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "list_sessions",
		Annotations: readOnly("List tmux Sessions"),
		Description: "Sessions with their name, window count, and how many " +
			"clients are attached. Narrow with name, or with attached to find " +
			"which sessions somebody is actually looking at.",
	}, t.listSessions)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "select_window",
		Annotations: settling("Select a tmux Window"),
		Description: "Make one window its session's current window, which is what " +
			"puts a person in front of a window this client created.",
	}, t.selectWindow)
}
