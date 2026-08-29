package tmux

import (
	"context"
	"strconv"
)

var breakPanePrintSoleVersion = Version{raw: "3.6", major: 3, minor: 6}

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

// SwapWindowRequest configures swapping the receiver with one exact winlink on
// tmux 3.2a or later. Its zero value is invalid because Target must be a
// complete Window handle. The endpoints must be proven to share a daemon
// through connection state or the same nonempty SocketPath; matching socket
// names alone are insufficient. Validation completes before execution.
type SwapWindowRequest struct {
	// Target is the exact other winlink to swap with the receiver.
	Target Window
	// Detach leaves the affected sessions' current-window selection unchanged.
	Detach bool
}

// WindowSwapResult contains both original stable window identities in their
// post-swap exact winlink views. Its zero value contains no usable endpoints.
type WindowSwapResult struct {
	// Window is the receiver's original WindowID at its post-swap exact winlink.
	Window Window
	// Target is the target's original WindowID at its post-swap exact winlink.
	Target Window
}

// MovePaneRequest configures move-pane on tmux 3.2a or later. Its zero value is
// invalid: exactly one complete TargetPane or TargetWindow handle is required.
// Cross-object handles must be proven to share a daemon through connection
// state or the same nonempty SocketPath; matching socket names alone are
// insufficient. Size and Percentage are mutually exclusive. Invalid enum,
// target, size, and percentage values are rejected before execution.
//
// Size pointers are read during the call and not retained; nil omits the mode
// and nonnil is explicit. Do not mutate them concurrently.
type MovePaneRequest struct {
	// TargetPane selects an exact destination pane; a zero Pane omits it.
	TargetPane Pane
	// TargetWindow selects an exact destination winlink; a zero Window omits it.
	TargetWindow Window
	// Attach lets tmux make the moved pane active in the destination winlink.
	Attach bool
	// Direction selects placement relative to the destination; zero means below.
	Direction PaneDirection
	// FullWindow lets the moved pane span the full destination window.
	FullWindow bool
	// Size selects a nonnegative absolute pane size; nil omits it.
	Size *int
	// Percentage selects a size from 0 through 100; nil omits it.
	Percentage *int
}

// JoinPaneRequest configures join-pane with the same target, cross-daemon,
// sizing, validation, and pointer-ownership rules as [MovePaneRequest].
type JoinPaneRequest struct {
	// TargetPane selects an exact destination pane; a zero Pane omits it.
	TargetPane Pane
	// TargetWindow selects an exact destination winlink; a zero Window omits it.
	TargetWindow Window
	// Attach lets tmux make the joined pane active in the destination winlink.
	Attach bool
	// Direction selects placement relative to the destination; zero means below.
	Direction PaneDirection
	// FullWindow lets the joined pane span the full destination window.
	FullWindow bool
	// Size selects a nonnegative absolute pane size; nil omits it.
	Size *int
	// Percentage selects a size from 0 through 100; nil omits it.
	Percentage *int
}

// BreakPaneRequest configures break-pane on tmux 3.2a or later. Its zero value
// moves the receiver to a detached new window with tmux's default name, except
// for the documented raw-version 3.7 workaround in [Pane.BreakPane]. Empty
// Name means omission and is validated before the version probe.
type BreakPaneRequest struct {
	// Attach lets the new winlink become current in the receiver session.
	Attach bool
	// Name requests the new window name; empty normally lets tmux choose.
	Name string
}

// SwapPaneDirection selects an adjacent pane for swap-pane on tmux 3.2a or
// later. Its zero value selects no adjacent pane.
type SwapPaneDirection uint8

// Supported adjacent-pane swap directions.
const (
	// SwapPaneDirectionNone selects no adjacent pane.
	SwapPaneDirectionNone SwapPaneDirection = iota
	// SwapPaneDirectionUp swaps with the previous pane in index order.
	SwapPaneDirectionUp
	// SwapPaneDirectionDown swaps with the next pane in index order.
	SwapPaneDirectionDown
)

// SwapPaneRequest configures swapping with an exact or adjacent pane on tmux
// 3.2a or later. Its zero value is invalid: exactly one complete Target or
// Direction is required. An explicit Target must differ from the receiver and
// must be proven to share a daemon through connection state or the same
// nonempty SocketPath; matching socket names alone are insufficient. Invalid
// choices are rejected before execution.
type SwapPaneRequest struct {
	// Target selects an exact other pane; a zero Pane omits it.
	Target Pane
	// Direction selects an adjacent pane; zero omits it.
	Direction SwapPaneDirection
	// Detach leaves the active-pane selection unchanged.
	Detach bool
	// KeepZoom preserves the affected window's zoomed state.
	KeepZoom bool
}

// PaneSwapResult contains original stable pane identities in their post-swap
// exact linked-pane views. Its zero value contains no usable endpoints. Target
// is zero for a directional swap because tmux does not report the adjacent
// pane identity.
type PaneSwapResult struct {
	// Pane is the receiver's original PaneID at its post-swap exact view.
	Pane Pane
	// Target is the explicit target's original PaneID at its post-swap exact
	// view, or zero after a directional swap.
	Target Pane
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

// Swap exchanges the receiver and Target exact winlinks. Their stable
// WindowIDs move to the opposite session-and-index views; a WindowID alone
// does not identify either endpoint. Unless Detach is set, tmux may change the
// affected sessions' current-window selection; this is not a global
// client-focus guarantee.
//
// Swap returns both freshly materialized exact endpoint views. If exact
// refresh fails after the command, it returns a predicted [WindowSwapResult]
// carrying both stable IDs and post-swap session/index contexts with the
// error. Earlier failures return zero; transport errors are delivery-ambiguous.
func (w Window) Swap(ctx context.Context, request SwapWindowRequest) (WindowSwapResult, error) {
	receiverTarget, err := exactWindowTarget(w)
	if err != nil {
		return WindowSwapResult{}, err
	}
	if err := requireTopologyServer(w.server, request.Target.server); err != nil {
		return WindowSwapResult{}, err
	}
	target, err := exactWindowTarget(request.Target)
	if err != nil {
		return WindowSwapResult{}, err
	}
	arguments := []string{"swap-window", "-t", receiverTarget}
	if request.Detach {
		arguments = append(arguments, "-d")
	}
	arguments = append(arguments, "-s", target)
	result, err := w.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("swap-window", result, err); err != nil {
		return WindowSwapResult{}, err
	}
	predicted := WindowSwapResult{
		Window: Window{
			server: w.server, sessionID: request.Target.sessionID,
			windowID: w.windowID, windowIndex: request.Target.windowIndex,
		},
		Target: Window{
			server: w.server, sessionID: w.sessionID,
			windowID: request.Target.windowID, windowIndex: w.windowIndex,
		},
	}
	if w.windowID == request.Target.windowID {
		predicted.Window.sessionID, predicted.Window.windowIndex = w.sessionID, w.windowIndex
		predicted.Target.sessionID = request.Target.sessionID
		predicted.Target.windowIndex = request.Target.windowIndex
	}
	refreshed, err := refreshExactWindowSwap(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return refreshed, nil
}

// Move relocates the receiver pane into one exact destination with tmux's
// move-pane command. Unless Attach is set, the destination's active-pane
// selection remains unchanged; Attach is not a global client-focus guarantee.
// The returned [Pane] is freshly materialized in the destination SessionID and
// WindowID rather than by canonical ID-only refresh.
//
// Removing the sole pane destroys the emptied source window and all of its
// winlinks. Affected source sessions preserve their current selection unless
// that window was current, in which case they select another. A session left
// without windows is destroyed and detaches its clients.
//
// If exact refresh fails after the command, Move returns a partial Pane with
// the receiver PaneID and predicted destination context. Other failures return
// zero. Transport errors are delivery-ambiguous and no rollback is attempted.
func (p Pane) Move(ctx context.Context, request MovePaneRequest) (Pane, error) {
	return p.relocate(ctx, "move-pane", paneRelocateRequestFromValues(
		request.TargetPane,
		request.TargetWindow,
		request.Attach,
		request.Direction,
		request.FullWindow,
		request.Size,
		request.Percentage,
	))
}

// Join relocates the receiver with join-pane. Its selection, source-destruction,
// partial-result, and delivery behavior matches [Pane.Move].
func (p Pane) Join(ctx context.Context, request JoinPaneRequest) (Pane, error) {
	return p.relocate(ctx, "join-pane", paneRelocateRequestFromValues(
		request.TargetPane,
		request.TargetWindow,
		request.Attach,
		request.Direction,
		request.FullWindow,
		request.Size,
		request.Percentage,
	))
}

// BreakPane moves the receiver into a new window in its exact session. Attach
// lets that new winlink become current in the session; it is not a global
// client-focus guarantee. The returned [Window] is freshly materialized in the
// receiver SessionID rather than by canonical ID-only refresh.
//
// Before tmux 3.6, the sole-pane path may print no ID and retain WindowID; this
// method refreshes that identity. Raw version "3.7" alone uses a placeholder
// name and optional second rename; with an empty Name, the window may remain
// named "libtmux". Suffixed 3.7 releases do not use the workaround.
//
// After a reported ID, rename failure, or refresh failure, the partial result
// carries the known WindowID and receiver session with Index -1. Earlier
// failures return zero; no mutation is rolled back.
func (p Pane) BreakPane(ctx context.Context, request BreakPaneRequest) (Window, error) {
	if err := validateServerCommandArgument("break-pane", "Name", request.Name, true); err != nil {
		return Window{}, err
	}
	source, err := exactPaneTarget(p)
	if err != nil {
		return Window{}, err
	}
	version, err := p.server.Version(ctx)
	if err != nil {
		return Window{}, err
	}
	isBroken37 := version.String() == "3.7"
	arguments := []string{"break-pane", "-P", "-F#{window_id}"}
	if !request.Attach {
		arguments = append(arguments, "-d")
	}
	if request.Name != "" {
		arguments = append(arguments, "-n", request.Name)
	} else if isBroken37 {
		arguments = append(arguments, "-n", "libtmux")
	}
	arguments = append(arguments, "-s", source, "-t", p.sessionID.String()+":")
	result, err := p.server.literalCmd(ctx, arguments...)
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("window", result.Stdout); identityErr == nil {
			return Window{
				server: p.server, sessionID: p.sessionID,
				windowID: WindowID(identity), windowIndex: -1,
			}, err
		}
		return Window{}, err
	}
	result, err = requireLifecycleSuccess("break-pane", result, nil)
	if err != nil {
		return Window{}, err
	}
	identity, err := lifecycleStableIdentity("window", result.Stdout)
	if err != nil {
		// Before tmux 3.6, the sole-pane relink path returns before -P is
		// printed. That path preserves the existing stable window ID.
		if len(result.Stdout) == 0 && !version.AtLeast(breakPanePrintSoleVersion) {
			window := Window{
				server: p.server, sessionID: p.sessionID,
				windowID: p.windowID, windowIndex: -1,
			}
			refreshed, refreshErr := refreshCreatedWindow(
				ctx, p.server, p.sessionID, p.windowID,
			)
			if refreshErr != nil {
				return window, refreshErr
			}
			return refreshed, nil
		}
		return Window{}, err
	}
	window := Window{
		server: p.server, sessionID: p.sessionID,
		windowID: WindowID(identity), windowIndex: -1,
	}
	if isBroken37 && request.Name != "" {
		target := p.sessionID.String() + ":" + window.windowID.String()
		renameResult, renameErr := p.server.literalCmd(ctx, "rename-window", "-t", target, request.Name)
		if _, renameErr = requireLifecycleSuccess("rename-window", renameResult, renameErr); renameErr != nil {
			return window, renameErr
		}
	}
	refreshed, err := refreshCreatedWindow(ctx, p.server, p.sessionID, window.windowID)
	if err != nil {
		return window, err
	}
	return refreshed, nil
}

// Swap exchanges the receiver with one exact or adjacent pane. Explicit
// targets use exact linked-pane views; PaneID alone does not distinguish linked
// views. Unless Detach is set, tmux may change active-pane selection; this is
// not a global client-focus guarantee. A directional swap cannot identify the
// adjacent target because tmux does not report it, so the returned
// [PaneSwapResult].Target is zero.
//
// An explicit swap returns freshly materialized exact views for both original
// PaneIDs. If exact refresh fails after the command, Swap returns a predicted
// result carrying every known identity with the error. Earlier failures return
// zero; transport errors are delivery-ambiguous.
func (p Pane) Swap(ctx context.Context, request SwapPaneRequest) (PaneSwapResult, error) {
	receiverTarget, err := exactPaneTarget(p)
	if err != nil {
		return PaneSwapResult{}, err
	}
	if request.Direction > SwapPaneDirectionDown {
		return PaneSwapResult{}, invalidLifecycleRequest("unsupported pane swap direction")
	}
	hasTarget := request.Target.paneID != "" || request.Target.windowID != "" ||
		request.Target.sessionID != ""
	if hasTarget == (request.Direction != SwapPaneDirectionNone) {
		return PaneSwapResult{}, invalidLifecycleRequest("Target and Direction require exactly one choice")
	}
	arguments := []string{"swap-pane", "-t", receiverTarget}
	if request.Detach {
		arguments = append(arguments, "-d")
	}
	switch request.Direction {
	case SwapPaneDirectionNone:
	case SwapPaneDirectionUp:
		arguments = append(arguments, "-U")
	case SwapPaneDirectionDown:
		arguments = append(arguments, "-D")
	}
	if request.KeepZoom {
		arguments = append(arguments, "-Z")
	}
	var target string
	if hasTarget {
		if err := requireTopologyServer(p.server, request.Target.server); err != nil {
			return PaneSwapResult{}, err
		}
		if request.Target.paneID == p.paneID {
			return PaneSwapResult{}, invalidLifecycleRequest("Target must differ from the receiver")
		}
		target, err = exactPaneTarget(request.Target)
		if err != nil {
			return PaneSwapResult{}, err
		}
		arguments = append(arguments, "-s", target)
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess("swap-pane", result, err); err != nil {
		return PaneSwapResult{}, err
	}
	predicted := PaneSwapResult{Pane: p}
	if hasTarget {
		predicted.Pane = Pane{
			server: p.server, sessionID: request.Target.sessionID,
			windowID: request.Target.windowID, windowIndex: request.Target.windowIndex,
			paneID: p.paneID, paneIndex: request.Target.paneIndex,
		}
		predicted.Target = Pane{
			server: p.server, sessionID: p.sessionID,
			windowID: p.windowID, windowIndex: p.windowIndex,
			paneID: request.Target.paneID, paneIndex: p.paneIndex,
		}
	}
	refreshed, err := refreshExactPaneSwap(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return refreshed, nil
}

type paneRelocateRequest struct {
	targetPane   Pane
	targetWindow Window
	attach       bool
	direction    PaneDirection
	fullWindow   bool
	size         *int
	percentage   *int
}

func (p Pane) relocate(
	ctx context.Context,
	subcommand string,
	request paneRelocateRequest,
) (Pane, error) {
	source, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	if err := validatePaneRelocateRequest(request); err != nil {
		return Pane{}, err
	}
	hasPane := request.targetPane.paneID != "" || request.targetPane.windowID != "" ||
		request.targetPane.sessionID != ""
	var target string
	var destination Window
	if hasPane {
		if err := requireTopologyServer(p.server, request.targetPane.server); err != nil {
			return Pane{}, err
		}
		if request.targetPane.paneID == p.paneID {
			return Pane{}, invalidLifecycleRequest("TargetPane must differ from the receiver")
		}
		target, err = exactPaneTarget(request.targetPane)
		destination = Window{
			server: p.server, sessionID: request.targetPane.sessionID,
			windowID: request.targetPane.windowID, windowIndex: request.targetPane.windowIndex,
		}
	} else {
		if err := requireTopologyServer(p.server, request.targetWindow.server); err != nil {
			return Pane{}, err
		}
		target, err = exactWindowTarget(request.targetWindow)
		destination = request.targetWindow
		destination.server = p.server
	}
	if err != nil {
		return Pane{}, err
	}
	arguments := []string{subcommand}
	switch request.direction {
	case PaneDirectionBelow:
		arguments = append(arguments, "-v")
	case PaneDirectionAbove:
		arguments = append(arguments, "-v")
	case PaneDirectionRight:
		arguments = append(arguments, "-h")
	case PaneDirectionLeft:
		arguments = append(arguments, "-h")
	}
	if !request.attach {
		arguments = append(arguments, "-d")
	}
	if request.fullWindow {
		arguments = append(arguments, "-f")
	}
	if request.size != nil {
		arguments = append(arguments, "-l"+strconv.Itoa(*request.size))
	}
	if request.percentage != nil {
		arguments = append(arguments, "-p"+strconv.Itoa(*request.percentage))
	}
	if request.direction == PaneDirectionAbove || request.direction == PaneDirectionLeft {
		arguments = append(arguments, "-b")
	}
	arguments = append(arguments, "-s", source, "-t", target)
	result, err := p.server.literalCmd(ctx, arguments...)
	if _, err = requireLifecycleSuccess(subcommand, result, err); err != nil {
		return Pane{}, err
	}
	predicted := Pane{
		server: p.server, sessionID: destination.sessionID,
		windowID: destination.windowID, windowIndex: destination.windowIndex, paneID: p.paneID,
	}
	refreshed, err := refreshExactPane(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return refreshed, nil
}

func paneRelocateRequestFromValues(
	targetPane Pane,
	targetWindow Window,
	attach bool,
	direction PaneDirection,
	fullWindow bool,
	size *int,
	percentage *int,
) paneRelocateRequest {
	return paneRelocateRequest{targetPane, targetWindow, attach, direction, fullWindow, size, percentage}
}

func validatePaneRelocateRequest(request paneRelocateRequest) error {
	if request.direction > PaneDirectionLeft {
		return invalidLifecycleRequest("unsupported pane direction")
	}
	hasPane := request.targetPane.paneID != "" || request.targetPane.windowID != "" ||
		request.targetPane.sessionID != ""
	hasWindow := request.targetWindow.windowID != "" || request.targetWindow.sessionID != ""
	if hasPane == hasWindow {
		return invalidLifecycleRequest("TargetPane and TargetWindow require exactly one choice")
	}
	if request.size != nil && request.percentage != nil {
		return invalidLifecycleRequest("Size and Percentage are mutually exclusive")
	}
	if request.size != nil && *request.size < 0 {
		return invalidLifecycleRequest("Size must be nonnegative")
	}
	if request.percentage != nil && (*request.percentage < 0 || *request.percentage > 100) {
		return invalidLifecycleRequest("Percentage must be between 0 and 100")
	}
	return nil
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
	leftState := left.connectionState()
	rightState := right.connectionState()
	if leftState == rightState {
		return nil
	}
	leftPath := leftState.options.SocketPath
	if leftPath != "" && leftPath == rightState.options.SocketPath {
		return nil
	}
	return invalidLifecycleRequest("source and target are not proven to use the same tmux server")
}

func refreshExactActivePane(ctx context.Context, window Window) (Pane, error) {
	refreshed, err := refreshExactWindow(ctx, window)
	if err != nil {
		return Pane{}, err
	}
	pane, ok := refreshed.ActivePane()
	if !ok {
		return Pane{}, &SnapshotLookupError{
			Object: "active pane", Identifier: window.sessionID.String() + ":" + window.windowID.String(),
		}
	}
	return pane, nil
}

func refreshExactWindow(ctx context.Context, window Window) (Window, error) {
	snapshot, err := window.server.Snapshot(ctx)
	if err != nil {
		return Window{}, err
	}
	return exactWindowFromSnapshot(snapshot, window)
}

func refreshCreatedWindow(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
) (Window, error) {
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return Window{}, err
	}
	return createdWindowFromSnapshot(snapshot, sessionID, windowID)
}

func refreshExactPane(ctx context.Context, pane Pane) (Pane, error) {
	snapshot, err := pane.server.Snapshot(ctx)
	if err != nil {
		return Pane{}, err
	}
	return exactPaneFromSnapshot(snapshot, pane)
}

func refreshExactWindowSwap(
	ctx context.Context,
	result WindowSwapResult,
) (WindowSwapResult, error) {
	snapshot, err := result.Window.server.Snapshot(ctx)
	if err != nil {
		return WindowSwapResult{}, err
	}
	result.Window, err = exactWindowFromSnapshot(snapshot, result.Window)
	if err != nil {
		return WindowSwapResult{}, err
	}
	result.Target, err = exactWindowFromSnapshot(snapshot, result.Target)
	return result, err
}

func refreshExactPaneSwap(ctx context.Context, result PaneSwapResult) (PaneSwapResult, error) {
	snapshot, err := result.Pane.server.Snapshot(ctx)
	if err != nil {
		return PaneSwapResult{}, err
	}
	result.Pane, err = exactPaneFromSnapshot(snapshot, result.Pane)
	if err != nil || result.Target.paneID == "" {
		return result, err
	}
	result.Target, err = exactPaneFromSnapshot(snapshot, result.Target)
	return result, err
}

func exactWindowFromSnapshot(snapshot Snapshot, window Window) (Window, error) {
	identifier, err := validateWindowView(window)
	if err != nil {
		return Window{}, err
	}
	key := winlinkKey{
		sessionID: window.sessionID,
		windowID:  window.windowID,
		index:     window.windowIndex,
	}
	return lookupSnapshotValue(
		snapshot.state.windows,
		snapshot.state.windowsByWinlink[key],
		"window",
		identifier,
	)
}

func exactPaneFromSnapshot(snapshot Snapshot, pane Pane) (Pane, error) {
	identifier, err := validatePaneView(pane)
	if err != nil {
		return Pane{}, err
	}
	key := paneViewKey{
		winlinkKey: winlinkKey{
			sessionID: pane.sessionID,
			windowID:  pane.windowID,
			index:     pane.windowIndex,
		},
		paneID: pane.paneID,
	}
	return lookupSnapshotValue(
		snapshot.state.panes,
		snapshot.state.panesByView[key],
		"pane",
		identifier,
	)
}

func createdWindowFromSnapshot(
	snapshot Snapshot,
	sessionID SessionID,
	windowID WindowID,
) (Window, error) {
	matches := make([]Window, 0, 1)
	for _, window := range snapshot.WindowsByID(windowID) {
		if window.sessionID == sessionID {
			matches = append(matches, window)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return Window{}, &SnapshotLookupError{
		Object:     "window",
		Identifier: sessionID.String() + ":" + windowID.String(),
		Matches:    len(matches),
	}
}
