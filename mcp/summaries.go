package mcp

import (
	"context"

	tmux "github.com/libtmux/libtmux-go"
)

// One description of each kind of tmux object, shared by every tool and
// resource that reports one.
//
// A client that reads a pane from list_panes, from search_panes, from a
// snapshot, and from tmux://panes/{pane} sees the same fields with the same
// names each time, so what it learned from the first answer applies to the
// rest. Describing a pane in each tool that returns one is how those drift.

// paneSummary describes one pane.
type paneSummary struct {
	// ID is the stable tmux pane identifier, such as %1.
	ID string `json:"id"`
	// Session is the name of the session holding the pane.
	Session string `json:"session"`
	// Window is the name of the window holding the pane.
	Window string `json:"window"`
	// WindowID is that window's stable identifier, such as @1, which is what
	// the window tools address.
	WindowID string `json:"windowId"`
	// Index is the pane's index within its window.
	Index int `json:"index"`
	// CurrentCommand is the command tmux reports running in the pane.
	CurrentCommand string `json:"currentCommand"`
	// Active reports whether the pane is its window's active pane.
	Active bool `json:"active"`
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

// windowSummary describes one window.
type windowSummary struct {
	// ID is the stable tmux window identifier, such as @1.
	ID string `json:"id"`
	// Session is the name of the session holding the window.
	Session string `json:"session"`
	// Name is the window's name.
	Name string `json:"name"`
	// Index is the window's index within its session.
	Index int `json:"index"`
	// Panes is how many panes the window holds.
	Panes int `json:"panes"`
	// Active reports whether this is its session's current window.
	Active bool `json:"active"`
}

// sessionSummary describes one session.
type sessionSummary struct {
	// ID is the stable tmux session identifier, such as $1.
	ID string `json:"id"`
	// Name is the session's name.
	Name string `json:"name"`
	// Windows is how many windows the session holds.
	Windows int `json:"windows"`
	// Attached reports how many clients are attached to it.
	Attached int `json:"attached"`
}

// summarizePane describes one pane against an already-read caller identity and
// socket, which is what a loop over many panes wants: the socket is the same
// for every pane in one server, and reading it per pane is a tmux command per
// pane for an answer that cannot differ.
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

// summarize describes one pane, reading the socket for it. Use it for a single
// pane; summarizePane is for a loop.
func (t *tools) summarize(ctx context.Context, pane tmux.Pane) paneSummary {
	return summarizePane(pane, t.callerIdentityFor(ctx), t.socketPath(ctx))
}

// summarizeWindow describes one window. The pane count is passed in because a
// materialized window carries its panes only when it came from a snapshot, and
// a window fetched on its own has to be asked.
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

// summarizeSession describes one session. The window count is passed in for
// the same reason summarizeWindow takes its pane count.
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
