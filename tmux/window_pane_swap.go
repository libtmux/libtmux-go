package tmux

import "context"

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
