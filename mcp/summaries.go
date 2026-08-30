package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
)

// Shared summaries keep each tmux object's wire shape consistent across tools.

type paneSummary struct {
	ID             string `json:"id"`
	Session        string `json:"session"`
	Window         string `json:"window"`
	WindowID       string `json:"windowId"`
	Index          int    `json:"index"`
	CurrentCommand string `json:"currentCommand"`
	Active         bool   `json:"active"`
	// Geometry is where the pane sits in its window, so a client can tell
	// which pane is above another rather than only which was made first.
	Geometry paneGeometry `json:"geometry"`
	// IsCaller reports whether this is the pane the server itself runs in.
	// True needs the pane id and the server's socket to agree; false means the
	// id matched and the socket did not, or could not be proven; null means
	// this server is not running inside tmux, where the question has no
	// answer.
	IsCaller *bool `json:"isCaller"`
}

// paneStatus adds snapshot-only process state to full pane listings.
type paneStatus struct {
	Dead bool `json:"dead"`
	// ExitStatus is present only after the pane process exits.
	ExitStatus *int   `json:"exitStatus,omitempty"`
	Path       string `json:"path,omitempty"`
	Title      string `json:"title,omitempty"`
	// HistoryLines is the number of scrollback rows.
	HistoryLines int `json:"historyLines"`
	// InMode reports that the pane is in a tmux mode, such as copy mode,
	// where keys sent to it are read by tmux and never reach the program.
	InMode bool `json:"inMode"`
}

// readPaneStatus uses only the existing snapshot.
func readPaneStatus(pane tmux.Pane) *paneStatus {
	formats := pane.Formats()
	dead, _ := formats.PaneDead()
	path, _ := formats.PaneCurrentPath()
	title, _ := formats.PaneTitle()
	history, _ := formats.HistorySize()
	mode, _ := formats.PaneInMode()
	status := paneStatus{
		Dead:         dead,
		Path:         path,
		Title:        title,
		HistoryLines: history,
		InMode:       mode != 0,
	}
	if exit, ok := formats.PaneDeadStatus(); ok && dead {
		status.ExitStatus = &exit
	}
	return &status
}

type windowSummary struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Name    string `json:"name"`
	Index   int    `json:"index"`
	Panes   int    `json:"panes"`
	Active  bool   `json:"active"`
}

type sessionSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Windows  int    `json:"windows"`
	Attached int    `json:"attached"`
}

// summarizePane reuses caller and socket state across a listing.
func summarizePane(pane tmux.Pane, caller callerIdentity, socket string) paneSummary {
	formats := pane.Formats()
	session, _ := formats.SessionName()
	window, _ := formats.WindowName()
	command, _ := pane.CurrentCommand()
	active, _ := pane.Active()
	return paneSummary{
		ID:             pane.ID().String(),
		Session:        session,
		Window:         window,
		WindowID:       pane.WindowID().String(),
		Index:          pane.Index(),
		CurrentCommand: command,
		Active:         active,
		Geometry:       readPaneGeometry(pane),
		IsCaller:       caller.isCaller(pane, socket),
	}
}

// summarize reads caller and socket state for one pane.
func (t *tools) summarize(ctx context.Context, pane tmux.Pane) (paneSummary, error) {
	caller, err := t.callerIdentityFor(ctx)
	if err != nil {
		return paneSummary{}, err
	}
	return summarizePane(pane, caller, t.socketPath(ctx)), nil
}

// Standalone windows may not carry materialized pane relations.
func summarizeWindow(window tmux.Window, panes int) windowSummary {
	formats := window.Formats()
	session, _ := formats.SessionName()
	name, _ := formats.WindowName()
	index, _ := formats.WindowIndex()
	active, _ := formats.WindowActive()
	return windowSummary{
		ID:      window.ID().String(),
		Session: session,
		Name:    name,
		Index:   index,
		Panes:   panes,
		Active:  active,
	}
}

func summarizeSession(session tmux.Session, windows int) sessionSummary {
	formats := session.Formats()
	name, _ := formats.SessionName()
	attached, _ := formats.SessionAttached()
	return sessionSummary{
		ID:       session.ID().String(),
		Name:     name,
		Windows:  windows,
		Attached: attached,
	}
}
