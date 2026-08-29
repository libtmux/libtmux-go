package tmux

import (
	"context"
	"strconv"
)

var breakPanePrintSoleVersion = Version{raw: "3.6", major: 3, minor: 6}

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
