package tmux

import "context"

// PaneSelectDirection selects a pane relative to the command target on tmux
// 3.2a or later. Its zero value selects no relative direction.
type PaneSelectDirection uint8

// Supported pane-selection directions.
const (
	// PaneSelectDirectionNone selects no relative pane.
	PaneSelectDirectionNone PaneSelectDirection = iota
	// PaneSelectDirectionUp selects the pane above the target.
	PaneSelectDirectionUp
	// PaneSelectDirectionDown selects the pane below the target.
	PaneSelectDirectionDown
	// PaneSelectDirectionLeft selects the pane to the left of the target.
	PaneSelectDirectionLeft
	// PaneSelectDirectionRight selects the pane to the right of the target.
	PaneSelectDirectionRight
	// PaneSelectDirectionLast selects the previously active pane.
	PaneSelectDirectionLast
)

// PaneMarkMode changes tmux's server-wide marked pane on tmux 3.2a or later.
// Its zero value leaves the mark unchanged.
type PaneMarkMode uint8

// Supported marked-pane changes.
const (
	// PaneMarkUnchanged leaves the server-wide marked pane unchanged.
	PaneMarkUnchanged PaneMarkMode = iota
	// PaneMarkSet makes the target the server-wide marked pane without selecting
	// it.
	PaneMarkSet
	// PaneMarkClear clears the server-wide marked pane without selecting the
	// target.
	PaneMarkClear
)

// PaneInputMode changes whether a pane accepts input on tmux 3.2a or later.
// Its zero value leaves input state unchanged.
type PaneInputMode uint8

// Supported pane-input changes.
const (
	// PaneInputUnchanged leaves the target pane's input state unchanged.
	PaneInputUnchanged PaneInputMode = iota
	// PaneInputDisable disables input without selecting the target pane.
	PaneInputDisable
	// PaneInputEnable enables input without selecting the target pane.
	PaneInputEnable
)

// WindowSelectPaneRequest selects either one exact pane or one relative pane.
// Its zero value is invalid: exactly one of Target and Direction is required.
// Target must be a complete pane handle in the receiver's exact winlink and
// must be proven to share a daemon through connection state or the same
// nonempty SocketPath; matching socket names alone are insufficient. Invalid
// values are rejected before execution.
type WindowSelectPaneRequest struct {
	// Target selects one exact pane in the receiver winlink; a zero Pane omits
	// this choice.
	Target Pane
	// Direction selects a pane relative to the receiver window's active pane;
	// zero omits this choice.
	Direction PaneSelectDirection
	// KeepZoom preserves the window's zoomed state.
	KeepZoom bool
}

// PaneSelectRequest configures select-pane for one exact receiver. Closed
// enums prevent conflicting direction, mark, and input flags. Its zero value
// selects the receiver. Mark rejects every other option. Input rejects
// Direction and KeepZoom because changing input does not select a pane. These
// combinations and enum ranges are validated before execution.
type PaneSelectRequest struct {
	// Direction selects a pane relative to the receiver; zero selects the
	// receiver unless Mark or Input is set.
	Direction PaneSelectDirection
	// KeepZoom preserves the window's zoomed state while selecting.
	KeepZoom bool
	// Mark changes the server-wide marked pane without selecting; zero leaves it
	// unchanged.
	Mark PaneMarkMode
	// Input changes whether the receiver accepts input without selecting; zero
	// leaves input state unchanged.
	Input PaneInputMode
}

// LastPaneRequest configures last-pane on tmux 3.2a or later. Its zero value
// selects the previously active pane. Input and KeepZoom are mutually
// exclusive and are validated before execution; an Input mode changes the
// previous pane's input state without selecting it.
type LastPaneRequest struct {
	// Input changes the previous pane's input state; zero performs selection.
	Input PaneInputMode
	// KeepZoom preserves the window's zoomed state during selection.
	KeepZoom bool
}

// RotateWindowDirection selects rotate-window's direction on tmux 3.2a or
// later. Its zero value uses tmux's default rotation.
type RotateWindowDirection uint8

// Supported window-rotation directions.
const (
	// RotateWindowDefault uses tmux's default rotation.
	RotateWindowDefault RotateWindowDirection = iota
	// RotateWindowUp rotates pane positions toward lower indexes.
	RotateWindowUp
	// RotateWindowDown rotates pane positions toward higher indexes.
	RotateWindowDown
)

// RotateWindowRequest configures rotate-window. Its zero value uses tmux's
// default rotation; enum validation happens before execution.
type RotateWindowRequest struct {
	// Direction selects the rotation direction; zero uses tmux's default.
	Direction RotateWindowDirection
	// KeepZoom preserves the window's zoomed state.
	KeepZoom bool
}

// SelectPane makes an exact or relative pane active in the receiver's exact
// winlink without selecting the window in its session. Target must belong to
// that same SessionID and WindowID; a PaneID alone does not identify a linked
// view. The returned [Pane] is the freshly materialized active pane in the
// receiver context, not a canonical ID-only view.
//
// Command or refresh failures return zero because active-pane selection has no
// reliable partial identity. Transport errors are delivery-ambiguous.
func (w Window) SelectPane(
	ctx context.Context,
	request WindowSelectPaneRequest,
) (Pane, error) {
	windowTarget, err := exactWindowTarget(w)
	if err != nil {
		return Pane{}, err
	}
	if err := validatePaneSelectDirection(request.Direction); err != nil {
		return Pane{}, err
	}
	hasTarget := request.Target.paneID != "" || request.Target.windowID != "" ||
		request.Target.sessionID != ""
	if hasTarget == (request.Direction != PaneSelectDirectionNone) {
		return Pane{}, invalidLifecycleRequest("Target and Direction require exactly one choice")
	}
	target := windowTarget
	arguments := []string{"select-pane", "-t"}
	if hasTarget {
		if err := requireTopologyServer(w.server, request.Target.server); err != nil {
			return Pane{}, err
		}
		if request.Target.sessionID != w.sessionID || request.Target.windowID != w.windowID {
			return Pane{}, invalidLifecycleRequest("Target must belong to the receiver's exact winlink")
		}
		target, err = exactPaneTarget(request.Target)
		if err != nil {
			return Pane{}, err
		}
	}
	arguments = append(arguments, target)
	if request.Direction != PaneSelectDirectionNone {
		arguments = append(arguments, paneSelectDirectionFlag(request.Direction))
	}
	if request.KeepZoom {
		arguments = append(arguments, "-Z")
	}
	result, err := w.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("select-pane", result, err); err != nil {
		return Pane{}, err
	}
	return refreshExactActivePane(ctx, w)
}

// Select applies select-pane to the receiver's exact linked-pane view.
// Direction makes an adjacent pane active, but the returned [Pane] is a fresh
// exact refresh of the receiver rather than the newly active pane. With no
// Direction, Mark, or Input, the receiver becomes active. Mark changes the
// server-wide marked pane without selection; Input changes the receiver's
// input state without selection. None of these modes selects the window in its
// session or promises client focus.
//
// A refresh failure returns the receiver with the error; earlier failures return zero.
func (p Pane) Select(ctx context.Context, request PaneSelectRequest) (Pane, error) {
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	arguments, err := paneSelectArguments(target, request)
	if err != nil {
		return Pane{}, err
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("select-pane", result, err); err != nil {
		return Pane{}, err
	}
	refreshed, err := refreshExactPane(ctx, p)
	if err != nil {
		return p, err
	}
	return refreshed, nil
}

// LastPane targets the receiver's exact winlink. Its zero request makes the
// previously active pane active without selecting the window in its session.
// With an Input mode, tmux changes the previous pane's input state without
// selecting it; the returned [Pane] is still the freshly materialized active
// pane in the receiver context.
//
// Command or refresh failures return zero because the active view needs refresh.
func (w Window) LastPane(ctx context.Context, request LastPaneRequest) (Pane, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return Pane{}, err
	}
	if request.Input > PaneInputEnable {
		return Pane{}, invalidLifecycleRequest("unsupported pane input mode")
	}
	if request.Input != PaneInputUnchanged && request.KeepZoom {
		return Pane{}, invalidLifecycleRequest("Input and KeepZoom are mutually exclusive")
	}
	arguments := []string{"last-pane", "-t", target}
	switch request.Input {
	case PaneInputUnchanged:
	case PaneInputDisable:
		arguments = append(arguments, "-d")
	case PaneInputEnable:
		arguments = append(arguments, "-e")
	}
	if request.KeepZoom {
		arguments = append(arguments, "-Z")
	}
	result, err := w.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("last-pane", result, err); err != nil {
		return Pane{}, err
	}
	return refreshExactActivePane(ctx, w)
}

// Rotate rotates pane positions in the receiver's exact winlink without
// selecting the window or promising client focus. It returns a freshly
// materialized exact [Window] preserving the receiver SessionID, rather than
// a canonical ID-only view. If the command succeeds but refresh fails, Rotate
// returns the receiver with that error; earlier failures return zero.
func (w Window) Rotate(ctx context.Context, request RotateWindowRequest) (Window, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	arguments, err := rotateWindowArguments(target, request)
	if err != nil {
		return Window{}, err
	}
	result, err := w.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("rotate-window", result, err); err != nil {
		return Window{}, err
	}
	refreshed, err := refreshExactWindow(ctx, w)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

// paneSelectArguments renders one select-pane argument vector. It performs no
// I/O, so a [Plan] can render a selection it has not made.
func paneSelectArguments(
	target string,
	request PaneSelectRequest,
) ([]string, error) {
	if err := validatePaneSelectDirection(request.Direction); err != nil {
		return nil, err
	}
	if request.Mark > PaneMarkClear {
		return nil, invalidLifecycleRequest("unsupported pane mark mode")
	}
	if request.Input > PaneInputEnable {
		return nil, invalidLifecycleRequest("unsupported pane input mode")
	}
	if request.Mark != PaneMarkUnchanged &&
		(request.Direction != PaneSelectDirectionNone || request.KeepZoom ||
			request.Input != PaneInputUnchanged) {
		return nil, invalidLifecycleRequest(
			"Mark is mutually exclusive with selection and input options")
	}
	if request.Input != PaneInputUnchanged &&
		(request.Direction != PaneSelectDirectionNone || request.KeepZoom) {
		return nil, invalidLifecycleRequest(
			"Input is mutually exclusive with selection and KeepZoom")
	}
	arguments, err := targetedArguments("select-pane", target)
	if err != nil {
		return nil, err
	}
	if request.Direction != PaneSelectDirectionNone {
		arguments = append(arguments, paneSelectDirectionFlag(request.Direction))
	}
	if request.KeepZoom {
		arguments = append(arguments, "-Z")
	}
	switch request.Mark {
	case PaneMarkUnchanged:
	case PaneMarkSet:
		arguments = append(arguments, "-m")
	case PaneMarkClear:
		arguments = append(arguments, "-M")
	}
	switch request.Input {
	case PaneInputUnchanged:
	case PaneInputDisable:
		arguments = append(arguments, "-d")
	case PaneInputEnable:
		arguments = append(arguments, "-e")
	}
	return arguments, nil
}

// rotateWindowArguments renders one rotate-window argument vector. It performs
// no I/O, so a [Plan] can render a rotation it has not applied.
func rotateWindowArguments(
	target string,
	request RotateWindowRequest,
) ([]string, error) {
	if request.Direction > RotateWindowDown {
		return nil, invalidLifecycleRequest("unsupported window rotation direction")
	}
	arguments, err := targetedArguments("rotate-window", target)
	if err != nil {
		return nil, err
	}
	switch request.Direction {
	case RotateWindowDefault:
	case RotateWindowUp:
		arguments = append(arguments, "-U")
	case RotateWindowDown:
		arguments = append(arguments, "-D")
	}
	if request.KeepZoom {
		arguments = append(arguments, "-Z")
	}
	return arguments, nil
}

func validatePaneSelectDirection(direction PaneSelectDirection) error {
	if direction > PaneSelectDirectionLast {
		return invalidLifecycleRequest("unsupported pane selection direction")
	}
	return nil
}

func paneSelectDirectionFlag(direction PaneSelectDirection) string {
	switch direction {
	case PaneSelectDirectionNone:
		return ""
	case PaneSelectDirectionUp:
		return "-U"
	case PaneSelectDirectionDown:
		return "-D"
	case PaneSelectDirectionLeft:
		return "-L"
	case PaneSelectDirectionRight:
		return "-R"
	case PaneSelectDirectionLast:
		return "-l"
	}
	return ""
}

func requireTopologyServer(left, right Server) error {
	leftState, err := left.stateForUse()
	if err != nil {
		return err
	}
	rightState, err := right.stateForUse()
	if err != nil {
		return err
	}
	if leftState == rightState {
		return nil
	}
	leftPath := leftState.config.socketSelection.Path
	if leftPath != "" && leftPath == rightState.config.socketSelection.Path {
		return nil
	}
	return invalidLifecycleRequest("source and target are not proven to use the same tmux server")
}
