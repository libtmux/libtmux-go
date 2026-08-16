package tmux

import (
	"context"
	"strconv"
)

// SelectWindowRequest selects one winlink in a [Session] on tmux 3.2a or
// later. Its zero value is invalid: exactly one of WindowID and Index is
// required. That mutual exclusion, stable-ID syntax, and a nonnegative Index
// are validated before execution. Index is read during the call and is not
// retained; nil omits it, a nonnil pointer is explicit, and callers must not
// mutate it concurrently.
type SelectWindowRequest struct {
	// WindowID selects a stable window linked into the receiver session.
	WindowID WindowID
	// Index selects a nonnegative winlink index in the receiver session.
	Index *int
}

// LastWindow makes the receiver session's previously selected winlink current
// and returns that freshly materialized session-specific [Window]. The
// selection is session-scoped; it does not promise focus for clients attached
// to other sessions. A transport or context error can be delivery-ambiguous
// and no rollback is attempted. Command or refresh failure returns a zero
// Window because this navigation operation cannot identify the selected view
// reliably without refresh.
func (s Session) LastWindow(ctx context.Context) (Window, error) {
	return s.navigateWindow(ctx, "last-window", s.sessionID.String())
}

// NextWindow makes the next winlink current in the receiver session and
// returns that freshly materialized session-specific [Window]. The selection
// is session-scoped; it does not promise focus for clients attached to other
// sessions. A transport or context error can be delivery-ambiguous and no
// rollback is attempted. Command or refresh failure returns a zero Window.
func (s Session) NextWindow(ctx context.Context) (Window, error) {
	return s.navigateWindow(ctx, "next-window", s.sessionID.String())
}

// PreviousWindow makes the previous winlink current in the receiver session
// and returns that freshly materialized session-specific [Window]. The
// selection is session-scoped; it does not promise focus for clients attached
// to other sessions. A transport or context error can be delivery-ambiguous
// and no rollback is attempted. Command or refresh failure returns a zero
// Window.
func (s Session) PreviousWindow(ctx context.Context) (Window, error) {
	return s.navigateWindow(ctx, "previous-window", s.sessionID.String())
}

// SelectWindow makes one winlink current in the receiver session and returns
// that freshly materialized session-specific [Window]. WindowID is combined
// with the receiver SessionID, because a WindowID alone does not distinguish
// linked views. The selection is session-scoped; it does not promise focus for
// clients attached to other sessions.
//
// Invalid requests match [ErrInvalidServerCommandRequest] or
// [ErrInvalidRequest] before execution. A transport or context error can be
// delivery-ambiguous and no rollback is attempted. Command or refresh failure
// returns a zero Window because the selected view cannot be identified
// reliably without refresh.
func (s Session) SelectWindow(
	ctx context.Context,
	request SelectWindowRequest,
) (Window, error) {
	if err := validateTypedTarget(
		"select-window", "SessionID", "session", s.sessionID.String(),
	); err != nil {
		return Window{}, err
	}
	hasWindowID := request.WindowID != ""
	hasIndex := request.Index != nil
	if hasWindowID == hasIndex {
		return Window{}, invalidServerCommandRequest(
			"select-window",
			"Target",
			"",
			"requires exactly one of WindowID or Index",
		)
	}

	var selector string
	if hasWindowID {
		if err := validateTypedTarget(
			"select-window", "WindowID", "window", request.WindowID.String(),
		); err != nil {
			return Window{}, err
		}
		selector = request.WindowID.String()
	} else {
		if *request.Index < 0 {
			return Window{}, invalidServerCommandRequest(
				"select-window",
				"Index",
				strconv.Itoa(*request.Index),
				"must be nonnegative",
			)
		}
		selector = strconv.Itoa(*request.Index)
	}

	return s.navigateWindow(
		ctx,
		"select-window",
		s.sessionID.String()+":"+selector,
	)
}

func (s Session) navigateWindow(
	ctx context.Context,
	subcommand string,
	target string,
) (Window, error) {
	if err := validateTypedTarget(
		subcommand, "SessionID", "session", s.sessionID.String(),
	); err != nil {
		return Window{}, err
	}
	result, err := s.server.literalCmd(ctx, subcommand, "-t", target)
	if err := requireServerCommandNoStderr(subcommand, result, err); err != nil {
		return Window{}, err
	}

	refreshed, err := s.Refresh(ctx)
	if err != nil {
		return Window{}, err
	}
	return windowProjectedBySession(refreshed)
}

func windowProjectedBySession(session Session) (Window, error) {
	windowID, _ := session.Formats().WindowID()
	// One window projected out of a session's own row: only windows are listed,
	// so a relation reached from the result is unknown rather than empty.
	snapshot, err := newSnapshotWithIdentity(
		session.server,
		session.formats.tmuxVersion(),
		snapshotRecords{windows: []formatValues{session.formats}},
		listedWindows,
		nil,
	)
	if err != nil {
		return Window{}, err
	}
	return snapshot.WindowByID(windowID)
}
