package tmux

import (
	"context"
	"strconv"
)

// WindowResizeDirection selects one directional resize operation on tmux 3.2a
// or later. Its zero value selects no directional adjustment.
type WindowResizeDirection uint8

// Supported window resize directions.
const (
	// WindowResizeDirectionNone selects no directional adjustment.
	WindowResizeDirectionNone WindowResizeDirection = iota
	// WindowResizeDirectionUp adjusts the window upward.
	WindowResizeDirectionUp
	// WindowResizeDirectionDown adjusts the window downward.
	WindowResizeDirectionDown
	// WindowResizeDirectionLeft adjusts the window leftward.
	WindowResizeDirectionLeft
	// WindowResizeDirectionRight adjusts the window rightward.
	WindowResizeDirectionRight
)

// ResizeWindowRequest selects one directional, manual, expand, or shrink
// operation. Height and Width may be set together. A zero request asks tmux to
// reapply the current manual size. Direction requires a positive Adjustment;
// that pair, dimensions, Expand, and Shrink are mutually exclusive and are
// validated before execution. Zero Adjustment, Height, and Width omit their
// modes; nonzero values must be positive. The request is supported on tmux
// 3.2a or later.
type ResizeWindowRequest struct {
	// Direction selects a directional adjustment; zero selects another mode.
	Direction WindowResizeDirection
	// Adjustment is the required positive amount for Direction; zero omits it.
	Adjustment int
	// Height sets a positive manual height; zero preserves the current height.
	Height int
	// Width sets a positive manual width; zero preserves the current width.
	Width int
	// Expand sets the window to the size of the largest session containing it.
	Expand bool
	// Shrink sets the window to the size of the smallest session containing it.
	Shrink bool
}

// Resize changes the stable window's size through the receiver's exact
// winlink. It does not select the window or promise any client focus. Resize
// returns a canonical freshly materialized [Window], which may use another
// linked session for the same WindowID. If the command succeeds but refresh
// fails, it returns the receiver with that error; other command failures return
// a zero Window. A transport or context error can be delivery-ambiguous and no
// rollback is attempted.
func (w Window) Resize(ctx context.Context, request ResizeWindowRequest) (Window, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	arguments, err := resizeWindowArguments(target, request)
	if err != nil {
		return Window{}, err
	}

	result, err := w.server.literalCmd(ctx, arguments...)
	if err := requireServerCommandNoStderr("resize-window", result, err); err != nil {
		return Window{}, err
	}
	refreshed, err := w.Refresh(ctx)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

// resizeWindowArguments renders one resize-window argument vector. It performs
// no I/O, so a [Plan] can render a resize it has not applied.
func resizeWindowArguments(
	target string,
	request ResizeWindowRequest,
) ([]string, error) {
	if err := validateResizeWindowRequest(request); err != nil {
		return nil, err
	}
	arguments, err := targetedArguments("resize-window", target)
	if err != nil {
		return nil, err
	}
	switch {
	case request.Direction != WindowResizeDirectionNone:
		arguments = append(
			arguments,
			windowResizeDirectionFlag(request.Direction),
			strconv.Itoa(request.Adjustment),
		)
	case request.Height != 0 || request.Width != 0:
		if request.Height != 0 {
			arguments = append(arguments, "-y"+strconv.Itoa(request.Height))
		}
		if request.Width != 0 {
			arguments = append(arguments, "-x"+strconv.Itoa(request.Width))
		}
	case request.Expand:
		arguments = append(arguments, "-A")
	case request.Shrink:
		arguments = append(arguments, "-a")
	}
	return arguments, nil
}

func validateResizeWindowRequest(request ResizeWindowRequest) error {
	if request.Direction > WindowResizeDirectionRight {
		return invalidServerCommandRequest(
			"resize-window",
			"Direction",
			strconv.FormatUint(uint64(request.Direction), 10),
			"is unsupported",
		)
	}
	hasDirection := request.Direction != WindowResizeDirectionNone
	hasAdjustment := request.Adjustment != 0
	if hasDirection != hasAdjustment {
		return invalidServerCommandRequest(
			"resize-window",
			"Adjustment",
			"",
			"Direction and Adjustment must be set together",
		)
	}
	if request.Adjustment < 0 {
		return invalidServerCommandRequest(
			"resize-window",
			"Adjustment",
			strconv.Itoa(request.Adjustment),
			"must be positive",
		)
	}
	if request.Height < 0 {
		return invalidServerCommandRequest(
			"resize-window",
			"Height",
			strconv.Itoa(request.Height),
			"must be positive",
		)
	}
	if request.Width < 0 {
		return invalidServerCommandRequest(
			"resize-window",
			"Width",
			strconv.Itoa(request.Width),
			"must be positive",
		)
	}

	modes := 0
	if hasDirection {
		modes++
	}
	if request.Height != 0 || request.Width != 0 {
		modes++
	}
	if request.Expand {
		modes++
	}
	if request.Shrink {
		modes++
	}
	if modes > 1 {
		return invalidServerCommandRequest(
			"resize-window",
			"Mode",
			"",
			"adjustment, dimensions, Expand, and Shrink are mutually exclusive",
		)
	}
	return nil
}

func windowResizeDirectionFlag(direction WindowResizeDirection) string {
	switch direction {
	case WindowResizeDirectionNone:
		return ""
	case WindowResizeDirectionUp:
		return "-U"
	case WindowResizeDirectionDown:
		return "-D"
	case WindowResizeDirectionLeft:
		return "-L"
	case WindowResizeDirectionRight:
		return "-R"
	default:
		return ""
	}
}
