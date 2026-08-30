package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

// notFound adds an actionable listing hint while preserving
// [tmux.ErrSnapshotNotFound].
func notFound(err error, what, id, lister string) error {
	if !errors.Is(err, tmux.ErrSnapshotNotFound) {
		return err
	}
	return missing{fmt.Errorf(
		"no %s %s on this tmux server; %s reports the %ss that exist",
		what, id, lister, what)}
}

// missing preserves [tmux.ErrSnapshotNotFound] after rewriting its message.
type missing struct{ error }

func (missing) Is(target error) bool { return target == tmux.ErrSnapshotNotFound }

// resolveSession finds the session a call names, or the only one there is.
func (t *tools) resolveSession(ctx context.Context, name string) (tmux.Session, error) {
	sessions, err := t.tmux(ctx).Sessions(ctx)
	if err != nil {
		return tmux.Session{}, err
	}
	// Match materialized names; tmux targets also accept prefixes and patterns.
	if wanted := strings.TrimSpace(name); wanted != "" {
		for _, session := range sessions {
			if actual, ok := session.Formats().SessionName(); ok && actual == wanted {
				return session, nil
			}
		}
		return tmux.Session{}, missing{fmt.Errorf(
			"no session named %q on this tmux server; list_sessions reports "+
				"the sessions that exist", wanted)}
	}
	switch len(sessions) {
	case 0:
		return tmux.Session{}, errors.New("the tmux server has no sessions")
	case 1:
		return sessions[0], nil
	default:
		return tmux.Session{}, fmt.Errorf(
			"the tmux server has %d sessions, so sessionName is required", len(sessions))
	}
}

// resolveWindow uses the current window when id is empty.
func (t *tools) resolveWindow(ctx context.Context, id, sessionName string) (tmux.Window, error) {
	if wanted := strings.TrimSpace(id); wanted != "" {
		window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(wanted))
		return window, notFound(err, "window", wanted, "list_windows")
	}
	session, err := t.resolveSession(ctx, sessionName)
	if err != nil {
		return tmux.Window{}, err
	}
	return session.ResolveActiveWindow(ctx)
}

// resolvePane uses the active pane when id is empty.
func (t *tools) resolvePane(ctx context.Context, id, sessionName string) (tmux.Pane, error) {
	if wanted := strings.TrimSpace(id); wanted != "" {
		pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(wanted))
		return pane, notFound(err, "pane", wanted, "list_panes")
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

// processPane re-resolves a command-lane pane on the exact daemon's process
// lane for operations whose stdout bytes cannot cross control mode.
func (t *tools) processPane(ctx context.Context, pane tmux.Pane) (tmux.Pane, error) {
	server, err := t.runtime.process(ctx)
	if err != nil {
		return tmux.Pane{}, err
	}
	return server.Pane(ctx, pane.ID())
}
