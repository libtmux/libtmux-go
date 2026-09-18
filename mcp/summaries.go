package mcp

import (
	"context"
	"strings"

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

// summarizeSession leaves every one of this server's own attached clients out
// of the attached count: ownAttached is how many of them (the command
// connection, a wait_for_text or capture_since observation, or several at
// once) are attached to this exact session.
func summarizeSession(session tmux.Session, windows int, ownAttached int) sessionSummary {
	formats := session.Formats()
	name, _ := formats.SessionName()
	attached, _ := formats.SessionAttached()
	attached -= ownAttached
	if attached < 0 {
		attached = 0
	}
	return sessionSummary{
		ID:       session.ID().String(),
		Name:     name,
		Windows:  windows,
		Attached: attached,
	}
}

// ownClients is every control client this process currently has open: the
// command connection and any open wait_for_text or capture_since
// observation. A listing must leave all of them out, or a detached
// session reads as watched.
type ownClients struct {
	names      map[tmux.ClientName]struct{}
	perSession map[tmux.SessionID]int
}

// isOwn reports whether name is one of this process's own control clients.
func (o ownClients) isOwn(name tmux.ClientName) bool {
	_, ok := o.names[name]
	return ok
}

// attachedIn is how many of this process's own clients are attached to
// sessionID.
func (o ownClients) attachedIn(sessionID tmux.SessionID) int {
	return o.perSession[sessionID]
}

// ownAttachment collects every control client this process currently owns.
// The command connection's own client is asked on every call because tmux
// 3.6 moves it when its session is destroyed; with no connection bound there
// is none. Every open observation is already tracked by the runtime, so no
// further tmux round trip is needed for those.
func (t *tools) ownAttachment(ctx context.Context) ownClients {
	names, perSession := t.runtime.ownObservationSnapshot()
	server := t.tmux(ctx)
	if server.ConnectionBound() {
		format := "#{client_name} #{session_id}"
		if lines, err := server.DisplayMessage(
			ctx, tmux.DisplayMessageRequest{Print: true, Format: &format},
		); err == nil && len(lines) == 1 {
			if separator := strings.LastIndex(lines[0], " "); separator > 0 {
				names[tmux.ClientName(lines[0][:separator])] = struct{}{}
				perSession[tmux.SessionID(lines[0][separator+1:])]++
			}
		}
	}
	return ownClients{names: names, perSession: perSession}
}
