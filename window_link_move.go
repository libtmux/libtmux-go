package tmux

import (
	"context"
	"strconv"
)

// LinkWindowRequest configures linking a window into another session on tmux
// 3.2a or later. Its zero value is invalid because TargetSession is required.
// Nil TargetIndex asks tmux for a destination index; a nonnil value is
// explicit and must be nonnegative. After and Before are mutually exclusive
// and are rejected before execution. TargetIndex is read during the call and
// is not retained; callers must not mutate it concurrently.
type LinkWindowRequest struct {
	// TargetSession identifies the destination session in the receiver's tmux
	// server.
	TargetSession SessionID
	// TargetIndex selects a destination winlink index; nil lets tmux choose.
	TargetIndex *int
	// KillExisting destroys a window already occupying the destination.
	KillExisting bool
	// After inserts the new winlink after the destination target.
	After bool
	// Before inserts the new winlink before the destination target.
	Before bool
	// Detach leaves the target session's current window unchanged.
	Detach bool
}

// UnlinkWindowRequest configures unlinking one exact winlink on tmux 3.2a or
// later. Its zero value removes the link only when the stable window has
// another link. The request contains no retained caller-owned storage and has
// no invalid field combinations.
type UnlinkWindowRequest struct {
	// KillIfLast permits unlinking and destroying a stable window with no other
	// links.
	KillIfLast bool
}

// MoveWindowRequest configures moving one exact winlink on tmux 3.2a or later.
// Its zero value moves the receiver within its current session to a tmux-chosen
// index and lets the moved winlink become current. Nil TargetIndex asks tmux
// for that index; a nonnil value is explicit and must be nonnegative. After
// and Before are mutually exclusive. Renumber is a standalone mode mutually
// exclusive with TargetIndex, After, Before, NoSelect, and KillTarget. Invalid
// combinations are rejected before execution. TargetIndex is read during the
// call and is not retained; callers must not mutate it concurrently.
type MoveWindowRequest struct {
	// TargetSession identifies the destination session; zero uses the receiver
	// session.
	TargetSession SessionID
	// TargetIndex selects a destination winlink index; nil lets tmux choose.
	TargetIndex *int
	// After inserts the moved winlink after the destination target.
	After bool
	// Before inserts the moved winlink before the destination target.
	Before bool
	// NoSelect leaves the destination session's current window unchanged.
	NoSelect bool
	// KillTarget destroys a window already occupying the destination.
	KillTarget bool
	// Renumber renumbers the target session according to its base-index option
	// instead of moving the receiver to a new destination.
	Renumber bool
}

// Link adds the stable window to TargetSession as another winlink, targeting
// the source through the receiver's exact session. Unless Detach is set, tmux
// makes the new link current in the target session; this is not a global
// client-focus guarantee. Link does not refresh or mutate the materialized
// source [Window]. A transport or context error can be delivery-ambiguous; the
// void result cannot carry the new winlink identity and no rollback is
// attempted.
func (w Window) Link(ctx context.Context, request LinkWindowRequest) error {
	source, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	if err := validateTypedTarget(
		"link-window", "TargetSession", "session", request.TargetSession.String(),
	); err != nil {
		return err
	}
	if request.TargetIndex != nil && *request.TargetIndex < 0 {
		return invalidWindowIndex("link-window", *request.TargetIndex)
	}
	if request.After && request.Before {
		return invalidServerCommandRequest(
			"link-window",
			"Position",
			"",
			"After and Before are mutually exclusive",
		)
	}

	arguments, err := linkWindowArguments(request.TargetSession.String(), source, request)
	if err != nil {
		return err
	}

	result, err := w.server.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("link-window", result, err)
}

// linkWindowArguments renders one link-window argument vector, linking source
// into destination. It performs no I/O, so a [Plan] can render a link it has not
// made. destination carries the session, and TargetIndex the position in it.
func linkWindowArguments(
	destination string,
	source string,
	request LinkWindowRequest,
) ([]string, error) {
	if request.TargetIndex != nil && *request.TargetIndex < 0 {
		return nil, invalidWindowIndex("link-window", *request.TargetIndex)
	}
	if request.After && request.Before {
		return nil, invalidServerCommandRequest(
			"link-window",
			"Position",
			"",
			"After and Before are mutually exclusive",
		)
	}
	arguments := []string{"link-window"}
	if request.KillExisting {
		arguments = append(arguments, "-k")
	}
	if request.After {
		arguments = append(arguments, "-a")
	}
	if request.Before {
		arguments = append(arguments, "-b")
	}
	if request.Detach {
		arguments = append(arguments, "-d")
	}
	target := destination
	if request.TargetIndex != nil {
		target += ":" + strconv.Itoa(*request.TargetIndex)
	}
	return append(arguments, "-s", source, "-t", target), nil
}

// Unlink removes the receiver's exact session winlink without refreshing the
// materialized receiver. Without KillIfLast, tmux rejects removal of a stable
// window's only link; with it, that window is destroyed. A transport or
// context error can be delivery-ambiguous; the void result cannot carry
// partial identity and no rollback is attempted.
func (w Window) Unlink(ctx context.Context, request UnlinkWindowRequest) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	arguments := []string{"unlink-window"}
	if request.KillIfLast {
		arguments = append(arguments, "-k")
	}
	arguments = append(arguments, "-t", target)

	result, err := w.server.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("unlink-window", result, err)
}

// Move removes the receiver's exact winlink and places it in TargetSession, or
// renumbers the selected session when Renumber is set. Unless NoSelect is set,
// tmux makes the moved winlink current in the destination session; this is not
// a global client-focus guarantee. The stable WindowID does not by itself
// identify either linked view.
//
// Move returns a canonical freshly materialized [Window], which may use a
// different linked session than the destination. If the command succeeds but
// refresh fails, it returns the receiver with that error. Other command errors
// return a zero Window. A transport or context error can be delivery-ambiguous
// and no rollback is attempted.
func (w Window) Move(ctx context.Context, request MoveWindowRequest) (Window, error) {
	source, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	targetSession := request.TargetSession
	if targetSession == "" {
		targetSession = w.sessionID
	}
	if err := validateTypedTarget(
		"move-window", "TargetSession", "session", targetSession.String(),
	); err != nil {
		return Window{}, err
	}
	if request.TargetIndex != nil && *request.TargetIndex < 0 {
		return Window{}, invalidWindowIndex("move-window", *request.TargetIndex)
	}
	if request.After && request.Before {
		return Window{}, invalidServerCommandRequest(
			"move-window",
			"Position",
			"",
			"After and Before are mutually exclusive",
		)
	}
	if request.Renumber && (request.TargetIndex != nil || request.After || request.Before ||
		request.NoSelect || request.KillTarget) {
		return Window{}, invalidServerCommandRequest(
			"move-window",
			"Renumber",
			"true",
			"is mutually exclusive with move options",
		)
	}

	arguments, err := moveWindowArguments(targetSession.String(), source, request)
	if err != nil {
		return Window{}, err
	}

	result, err := w.server.literalCmd(ctx, arguments...)
	if err := requireServerCommandNoStderr("move-window", result, err); err != nil {
		return Window{}, err
	}
	refreshed, err := w.Refresh(ctx)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

func exactWindowTarget(window Window) (string, error) {
	if _, err := validateWindowView(window); err != nil {
		return "", err
	}
	return window.sessionID.String() + ":" + strconv.Itoa(window.windowIndex), nil
}

func invalidWindowIndex(subcommand string, index int) error {
	return invalidServerCommandRequest(
		subcommand,
		"TargetIndex",
		strconv.Itoa(index),
		"must be nonnegative",
	)
}

// moveWindowArguments renders one move-window argument vector, moving source
// into destination. It performs no I/O, so a [Plan] can render a move it has not
// made. destination carries the session, and TargetIndex the position in it.
func moveWindowArguments(
	destination string,
	source string,
	request MoveWindowRequest,
) ([]string, error) {
	if request.TargetIndex != nil && *request.TargetIndex < 0 {
		return nil, invalidWindowIndex("move-window", *request.TargetIndex)
	}
	if request.After && request.Before {
		return nil, invalidServerCommandRequest(
			"move-window", "Position", "", "After and Before are mutually exclusive",
		)
	}
	if request.Renumber && (request.After || request.Before || request.KillTarget ||
		request.TargetIndex != nil) {
		return nil, invalidServerCommandRequest(
			"move-window", "Renumber", "true", "is mutually exclusive with move options",
		)
	}

	arguments := []string{"move-window"}
	if request.After {
		arguments = append(arguments, "-a")
	}
	if request.Before {
		arguments = append(arguments, "-b")
	}
	if request.NoSelect {
		arguments = append(arguments, "-d")
	}
	if request.KillTarget {
		arguments = append(arguments, "-k")
	}
	if request.Renumber {
		arguments = append(arguments, "-r")
	}
	target := destination
	if !request.Renumber {
		target += ":"
		if request.TargetIndex != nil {
			target += strconv.Itoa(*request.TargetIndex)
		}
	}
	return append(arguments, "-s", source, "-t", target), nil
}
