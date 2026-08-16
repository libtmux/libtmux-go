package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

// Resolving what a client named into what tmux addresses.
//
// A client that has read a listing has an id, and an id is what every tool
// prefers. A client that has not read one has a description: the session, or
// nothing at all because there is only one thing it could mean. Making it read
// a listing first would be a round trip to learn something the server can work
// out, so an omitted target resolves to the only candidate when there is one
// and is refused when there is not.
//
// Refused rather than guessed. A tool that picks one of several sessions makes
// a client's next call address something it did not choose, and the mistake
// surfaces as a command run in the wrong pane rather than as an error.

// resolveSession finds the session a call names, or the only one there is.
func (t *tools) resolveSession(ctx context.Context, name string) (tmux.Session, error) {
	sessions, err := t.target.Sessions(ctx)
	if err != nil {
		return tmux.Session{}, err
	}
	// A name is matched against the sessions there are rather than turned into
	// a target: a session id is tmux's own $N, and a name is not one.
	if wanted := strings.TrimSpace(name); wanted != "" {
		for _, session := range sessions {
			if actual, ok := session.Formats().SessionName(); ok && actual == wanted {
				return session, nil
			}
		}
		return tmux.Session{}, fmt.Errorf("no session named %q", wanted)
	}
	switch len(sessions) {
	case 0:
		return tmux.Session{}, fmt.Errorf("the tmux server has no sessions")
	case 1:
		return sessions[0], nil
	default:
		return tmux.Session{}, fmt.Errorf(
			"the tmux server has %d sessions, so sessionName is required", len(sessions))
	}
}

// resolveWindow finds the window a call names, or the current one.
//
// An empty id is the current window of the session resolved the same way,
// which is what "this window" means to someone looking at a terminal.
func (t *tools) resolveWindow(ctx context.Context, id, sessionName string) (tmux.Window, error) {
	if wanted := strings.TrimSpace(id); wanted != "" {
		return t.target.Window(ctx, tmux.WindowID(wanted))
	}
	session, err := t.resolveSession(ctx, sessionName)
	if err != nil {
		return tmux.Window{}, err
	}
	return session.ResolveActiveWindow(ctx)
}

// resolvePane finds the pane a call names, or the active one.
//
// An empty id is the active pane of the current window, which is the pane a
// person is typing in. It is the one a client means by "this pane" and the one
// it would otherwise spend a list_panes call to name.
func (t *tools) resolvePane(ctx context.Context, id, sessionName string) (tmux.Pane, error) {
	if wanted := strings.TrimSpace(id); wanted != "" {
		return t.target.Pane(ctx, tmux.PaneID(wanted))
	}
	window, err := t.resolveWindow(ctx, "", sessionName)
	if err != nil {
		return tmux.Pane{}, err
	}
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil {
		return tmux.Pane{}, err
	}
	if !ok {
		return tmux.Pane{}, fmt.Errorf("window %s has no active pane", window.ID())
	}
	return pane, nil
}
