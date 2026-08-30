package tmux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// SessionKillRequest configures kill-session on tmux 3.2a or later. Its zero
// value destroys the receiver session. AllExcept destroys other sessions
// instead, while ClearAlerts is non-destructive. AllExcept, ClearAlerts, and
// Group are mutually exclusive because tmux applies a hidden precedence when
// more than one mode is supplied; the package rejects those combinations
// before execution. Group's compatibility behavior is documented on that field.
type SessionKillRequest struct {
	// AllExcept terminates every other session and detaches their clients while
	// preserving the receiver session.
	AllExcept bool
	// ClearAlerts clears bell, activity, and silence alerts in every window linked
	// to the receiver without destroying a session or detaching its clients.
	ClearAlerts bool
	// Group terminates every session in the receiver's group and detaches their
	// clients. It requires tmux 3.7; see UnsupportedPolicy.
	Group bool
}

// KillWindowRequest selects a window for [Session.KillWindow] on tmux 3.2a or
// later. A zero request kills the receiver session's current window, and Index
// is scoped to that session. Target is unrestricted tmux target syntax and may
// select a window in another session. Target and Index are mutually exclusive
// and are checked before execution. Pointer values are read during the call
// and are not retained; callers must not mutate them concurrently. A nil
// pointer omits its selector, while a nonnil pointer is explicit even when
// Target points to an empty string.
type KillWindowRequest struct {
	// Target is passed through as unrestricted tmux target syntax when nonnil.
	Target *string
	// Index selects a winlink index within the receiver session when nonnil.
	Index *int
}

// Kill terminates the configured tmux server and all of its sessions, windows,
// panes, and clients. After a completed failed kill, a liveness probe makes
// repeated calls harmless when no daemon answers. Transport or context errors
// can be delivery-ambiguous; no rollback is attempted.
//
// tmux leaves the socket file in place, so cleanup that waits for the path to
// disappear does not finish. Use [Server.IsAlive] to observe the daemon, and
// remove a socket path this process chose itself.
func (s Server) Kill(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "kill-server")
	if err != nil {
		return err
	}
	if result.ExitCode == 0 && len(result.Stderr) == 0 {
		return nil
	}
	commandErr := newCommandError("kill-server", result)
	alive, probeErr := s.IsAlive(ctx)
	if probeErr != nil {
		return errors.Join(
			commandErr,
			fmt.Errorf("verify server stopped: %w", probeErr),
		)
	}
	if !alive {
		return nil
	}
	return commandErr
}

// KillSession terminates the session selected by tmux target syntax. Pattern
// and prefix matching are deliberately left to tmux. Completed nonzero exits
// without stderr are ignored. Transport and context errors are delivery-ambiguous.
func (s Server) KillSession(ctx context.Context, target string) error {
	if err := validateServerCommandArgument(
		"kill-session", "Target", target, true,
	); err != nil {
		return err
	}
	result, err := s.literalCmd(ctx, "kill-session", "-t", target)
	return requireServerCommandNoStderr("kill-session", result, err)
}

// Kill destroys the receiver session and removes its winlinks. Ordinary
// attached clients detach; on tmux 3.6 or later, clients carrying
// no-detach-on-destroy move to another session when one exists. A window with
// no remaining links and all of that window's panes are also destroyed. The
// materialized receiver is not refreshed. Only stderr makes a completed
// command a [CommandError]; nonzero exits without stderr are ignored.
// Transport errors are delivery-ambiguous. Kill is equivalent to
// [Session.KillWith] with a zero [SessionKillRequest].
func (s Session) Kill(ctx context.Context) error {
	return s.KillWith(ctx, SessionKillRequest{})
}

// KillWith applies one kill-session mode without refreshing the materialized
// receiver. The zero request destroys the receiver; AllExcept destroys every
// other session while preserving the receiver and its current window;
// ClearAlerts only clears alerts in windows linked to the receiver and leaves
// sessions, winlinks, panes, client attachments, and current-window selections
// unchanged. Destroying a session handles clients as [Session.Kill] describes
// and removes its winlinks; windows left without links and their panes are
// also destroyed. Group destroys the receiver's group and requires tmux 3.7;
// on older versions it follows [UnsupportedPolicy].
//
// The three modes are mutually exclusive. Only stderr makes a completed command
// a [CommandError]; nonzero exits without stderr are ignored. Transport errors
// are delivery-ambiguous.
func (s Session) KillWith(ctx context.Context, request SessionKillRequest) error {
	modes := 0
	if request.AllExcept {
		modes++
	}
	if request.ClearAlerts {
		modes++
	}
	if request.Group {
		modes++
	}
	if modes > 1 {
		return invalidLifecycleRequest(
			"AllExcept, ClearAlerts, and Group are mutually exclusive",
		)
	}
	if err := validateTypedTarget(
		"kill-session", "Target", "session", s.sessionID.String(),
	); err != nil {
		return err
	}
	arguments := []string{"kill-session"}
	if request.AllExcept {
		arguments = append(arguments, "-a")
	}
	if request.ClearAlerts {
		arguments = append(arguments, "-C")
	}
	if request.Group {
		required := Version{raw: "3.7", major: 3, minor: 7}
		current, err := s.server.Version(ctx)
		if err != nil {
			return err
		}
		if current.AtLeast(required) {
			arguments = append(arguments, "-g")
		} else if err := s.server.unsupportedFeature(
			"kill-session", "group", current, required,
		); err != nil {
			return err
		}
	}
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("kill-session", result, err)
}

// KillWindow destroys one stable window. When Target and Index are nil it
// selects the receiver session's current window, and Index selects a winlink
// in that session. A nonnil Target is forwarded as unrestricted tmux target
// syntax and may select a window in another session. The selected stable
// window is removed from every session and its panes are destroyed. Affected
// sessions preserve their current selection unless this window was current,
// in which case they select another. A session left without windows is
// destroyed; [Session.Kill] describes how tmux handles its clients.
//
// Target and Index are mutually exclusive. The receiver is not refreshed. Only
// stderr makes a completed command a [CommandError]; nonzero exits without
// stderr are ignored. Transport errors are delivery-ambiguous.
func (s Session) KillWindow(ctx context.Context, request KillWindowRequest) error {
	if request.Target != nil && request.Index != nil {
		return invalidLifecycleRequest("Target and Index are mutually exclusive")
	}

	var target string
	if request.Target != nil {
		target = *request.Target
		if err := validateServerCommandArgument(
			"kill-window", "Target", target, true,
		); err != nil {
			return err
		}
	} else {
		if err := validateTypedTarget(
			"kill-window", "Target", "session", s.sessionID.String(),
		); err != nil {
			return err
		}
		target = s.sessionID.String()
		if request.Index != nil {
			target += ":" + strconv.Itoa(*request.Index)
		}
	}

	result, err := s.server.literalCmd(ctx, "kill-window", "-t", target)
	return requireServerCommandNoStderr("kill-window", result, err)
}

// Kill destroys the stable window selected by the receiver's WindowID,
// removes all of its winlinks, and destroys its panes. Affected sessions
// preserve their current selection unless this window was current, in which
// case they select another. A session left without windows is destroyed;
// [Session.Kill] describes how tmux handles its clients. The receiver is not
// refreshed. Only stderr makes a completed command a [CommandError]; nonzero
// exits without stderr are ignored.
func (w Window) Kill(ctx context.Context) error {
	result, err := w.literalCmd(ctx, "kill-window")
	return requireServerCommandNoStderr("kill-window", result, err)
}

// KillOthers destroys every other stable window selected through the
// receiver's exact session and leaves the receiver as that session's current
// and only window. Links in other sessions are not selected merely because
// they share the receiver's [WindowID], but every selected stable window is
// removed from all sessions. Other affected sessions preserve their current
// selection unless a destroyed window was current, in which case they select
// another. A session left without windows is destroyed; [Session.Kill]
// describes how tmux handles its clients. The receiver is not refreshed. Only
// stderr makes a completed command a [CommandError]; nonzero exits without
// stderr are ignored.
func (w Window) KillOthers(ctx context.Context) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	result, err := w.server.literalCmd(ctx, "kill-window", "-t", target, "-a")
	return requireServerCommandNoStderr("kill-window", result, err)
}

// Kill destroys the receiver pane through its exact linked-pane target. If
// panes remain and the receiver was active, tmux selects another active pane.
// If this was the last pane, tmux also destroys the window and removes all of
// its winlinks. Affected sessions preserve their current selection unless the
// destroyed window was current, in which case they select another. A session
// left without windows is destroyed; [Session.Kill] describes how tmux handles
// its clients. The receiver is not refreshed. Only stderr makes a completed
// command a [CommandError]; nonzero exits without stderr are ignored.
func (p Pane) Kill(ctx context.Context) error {
	result, err := p.literalCmd(ctx, "kill-pane")
	return requireServerCommandNoStderr("kill-pane", result, err)
}

// KillOthers destroys every other pane in the receiver's stable window and
// leaves the receiver as its sole active pane. It does not select the exact
// winlink as its session's current window or promise client focus, and it does
// not destroy a window or session. The receiver is not refreshed. Only stderr
// makes a completed command a [CommandError]; nonzero exits without stderr are ignored.
func (p Pane) KillOthers(ctx context.Context) error {
	result, err := p.literalCmd(ctx, "kill-pane", "-a")
	return requireServerCommandNoStderr("kill-pane", result, err)
}
