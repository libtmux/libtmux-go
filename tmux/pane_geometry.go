package tmux

import (
	"context"
	"strconv"
)

// PaneResizeDirection selects a directional [Pane.Resize]. Its zero value
// selects no directional adjustment.
type PaneResizeDirection uint8

const (
	// PaneResizeDirectionNone omits a directional resize.
	PaneResizeDirectionNone PaneResizeDirection = iota
	// PaneResizeDirectionUp selects tmux's upward adjustment.
	PaneResizeDirectionUp
	// PaneResizeDirectionDown selects tmux's downward adjustment.
	PaneResizeDirectionDown
	// PaneResizeDirectionLeft selects tmux's leftward adjustment.
	PaneResizeDirectionLeft
	// PaneResizeDirectionRight selects tmux's rightward adjustment.
	PaneResizeDirectionRight
)

type paneSizeKind uint8

const (
	paneSizeUnset paneSizeKind = iota
	paneSizeCells
	paneSizePercent
)

// PaneSize is an opaque absolute cell count or percentage for [Pane.Resize].
// Its zero value omits the dimension. [PaneCells](0) and [PanePercent](0) are
// explicit sizes, not zero PaneSize values.
type PaneSize struct {
	kind  paneSizeKind
	value int
}

// PaneCells returns an explicit pane size measured in cells. Resize validates
// that cells is nonnegative; zero remains an explicit request.
func PaneCells(cells int) PaneSize {
	return PaneSize{kind: paneSizeCells, value: cells}
}

// PanePercent returns an explicit percentage of the window size. Resize
// validates that percent is between 0 and 100 inclusive; zero remains an
// explicit request.
func PanePercent(percent int) PaneSize {
	return PaneSize{kind: paneSizePercent, value: percent}
}

// ResizePaneRequest selects one resize mode. Direction with Adjustment,
// dimensions, Zoom, Mouse, and TrimBelow are mutually exclusive modes; Height
// and Width may be set together. Its zero value sends a bare tmux resize-pane
// command rather than selecting a mode. When Direction is
// [PaneResizeDirectionNone], Adjustment must be zero.
type ResizePaneRequest struct {
	// Direction selects a directional resize and requires Adjustment.
	Direction PaneResizeDirection
	// Adjustment is the positive number of cells moved in Direction. It must
	// be zero when Direction is PaneResizeDirectionNone.
	Adjustment int
	// Height sets an absolute or percentage height. A zero PaneSize omits it.
	Height PaneSize
	// Width sets an absolute or percentage width. A zero PaneSize omits it.
	Width PaneSize
	// Zoom toggles the pane between its normal layout and full-window zoom.
	Zoom bool
	// Mouse begins tmux mouse resizing and is meaningful from a mouse binding.
	Mouse bool
	// TrimBelow removes lines below the cursor and refills from history.
	TrimBelow bool
}

// Resize runs tmux resize-pane against the receiver's exact linked
// session-window-pane target, then refreshes by PaneID. The returned canonical
// [Pane] may therefore carry a different linked-session or winlink context.
// If the resize is accepted but refresh fails, Resize returns the original
// receiver with that error.
//
// Invalid requests fail before execution. A completed command produces a
// [CommandError] only when tmux writes stderr; the library-created error
// retains only the exit code. A nonzero exit without stderr is followed by
// refresh. Transport and context errors return a zero Pane and may be
// delivery-ambiguous. Context errors remain detectable with [errors.Is], and
// cancellation cannot roll back an accepted resize.
func (p Pane) Resize(ctx context.Context, request ResizePaneRequest) (Pane, error) {
	if err := validateTypedTarget(
		"resize-pane", "Pane", "pane", p.paneID.String(),
	); err != nil {
		return Pane{}, err
	}
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	arguments, err := resizePaneArguments(target, request)
	if err != nil {
		return Pane{}, err
	}

	result, err := p.server.literalCmd(ctx, arguments...)
	if err := requireRedactedServerCommandNoStderr("resize-pane", result, err); err != nil {
		return Pane{}, err
	}
	refreshed, err := p.Refresh(ctx)
	if err != nil {
		return p, err
	}
	return refreshed, nil
}

// resizePaneArguments renders one resize-pane argument vector. It performs no
// I/O, so a [Plan] can render a resize it has not applied.
func resizePaneArguments(
	target string,
	request ResizePaneRequest,
) ([]string, error) {
	if err := validateServerCommandArgument(
		"resize-pane", "Target", target, true,
	); err != nil {
		return nil, err
	}
	if err := validateResizePaneRequest(request); err != nil {
		return nil, err
	}

	arguments := []string{"resize-pane", "-t", target}
	switch {
	case request.Direction != PaneResizeDirectionNone:
		arguments = append(
			arguments,
			paneResizeDirectionFlag(request.Direction),
			strconv.Itoa(request.Adjustment),
		)
	case request.Height.kind != paneSizeUnset || request.Width.kind != paneSizeUnset:
		if request.Height.kind != paneSizeUnset {
			arguments = append(arguments, "-y"+paneSizeArgument(request.Height))
		}
		if request.Width.kind != paneSizeUnset {
			arguments = append(arguments, "-x"+paneSizeArgument(request.Width))
		}
	case request.Zoom:
		arguments = append(arguments, "-Z")
	case request.Mouse:
		arguments = append(arguments, "-M")
	case request.TrimBelow:
		arguments = append(arguments, "-T")
	}
	return arguments, nil
}

// SetWidth resizes the pane to a nonnegative absolute width in cells. Zero is
// an explicit width. Targeting, refresh, and error semantics match [Pane.Resize].
func (p Pane) SetWidth(ctx context.Context, width int) (Pane, error) {
	return p.Resize(ctx, ResizePaneRequest{Width: PaneCells(width)})
}

// SetHeight resizes the pane to a nonnegative absolute height in cells. Zero
// is an explicit height. Targeting, refresh, and error semantics match
// [Pane.Resize].
func (p Pane) SetHeight(ctx context.Context, height int) (Pane, error) {
	return p.Resize(ctx, ResizePaneRequest{Height: PaneCells(height)})
}

// SetTitle sets the title of the receiver's exact linked pane. tmux expands
// format expressions in title; the value is not a shell command. SetTitle then
// refreshes by PaneID, so the returned canonical [Pane] may carry a different
// linked-session or winlink context. If the mutation is accepted but refresh
// fails, SetTitle returns the original receiver with that error.
//
// A completed command produces a [CommandError] only when tmux writes stderr;
// the library-created error retains only the exit code. A nonzero exit without
// stderr is followed by refresh. Transport and context errors return a zero
// Pane and may be delivery-ambiguous. Context errors remain detectable with
// [errors.Is], and cancellation cannot roll back an accepted title change.
func (p Pane) SetTitle(ctx context.Context, title string) (Pane, error) {
	result, err := p.literalCmd(ctx, "select-pane", "-T", title)
	if err := requireRedactedServerCommandNoStderr("select-pane", result, err); err != nil {
		return Pane{}, err
	}
	refreshed, err := p.Refresh(ctx)
	if err != nil {
		return p, err
	}
	return refreshed, nil
}

func validateResizePaneRequest(request ResizePaneRequest) error {
	if request.Direction > PaneResizeDirectionRight {
		return invalidServerCommandRequest(
			"resize-pane",
			"Direction",
			strconv.FormatUint(uint64(request.Direction), 10),
			"is unsupported",
		)
	}
	hasDirection := request.Direction != PaneResizeDirectionNone
	hasAdjustment := request.Adjustment != 0
	if hasDirection != hasAdjustment {
		return invalidServerCommandRequest(
			"resize-pane",
			"Adjustment",
			"",
			"Direction and Adjustment must be set together",
		)
	}
	if request.Adjustment < 0 {
		return invalidServerCommandRequest(
			"resize-pane",
			"Adjustment",
			strconv.Itoa(request.Adjustment),
			"must be positive",
		)
	}
	if err := validatePaneSize("Height", request.Height); err != nil {
		return err
	}
	if err := validatePaneSize("Width", request.Width); err != nil {
		return err
	}

	modes := 0
	if hasDirection {
		modes++
	}
	if request.Height.kind != paneSizeUnset || request.Width.kind != paneSizeUnset {
		modes++
	}
	if request.Zoom {
		modes++
	}
	if request.Mouse {
		modes++
	}
	if request.TrimBelow {
		modes++
	}
	if modes > 1 {
		return invalidServerCommandRequest(
			"resize-pane",
			"Mode",
			"",
			"adjustment, dimensions, Zoom, Mouse, and TrimBelow are mutually exclusive",
		)
	}
	return nil
}

func validatePaneSize(field string, size PaneSize) error {
	switch size.kind {
	case paneSizeUnset:
		return nil
	case paneSizeCells:
		if size.value < 0 {
			return invalidServerCommandRequest(
				"resize-pane", field, strconv.Itoa(size.value), "must not be negative",
			)
		}
	case paneSizePercent:
		if size.value < 0 || size.value > 100 {
			return invalidServerCommandRequest(
				"resize-pane", field, strconv.Itoa(size.value), "must be between 0 and 100",
			)
		}
	default:
		return invalidServerCommandRequest(
			"resize-pane", field, "", "has an unsupported size kind",
		)
	}
	return nil
}

func paneSizeArgument(size PaneSize) string {
	value := strconv.Itoa(size.value)
	if size.kind == paneSizePercent {
		return value + "%"
	}
	return value
}

func paneResizeDirectionFlag(direction PaneResizeDirection) string {
	switch direction {
	case PaneResizeDirectionNone:
		return ""
	case PaneResizeDirectionUp:
		return "-U"
	case PaneResizeDirectionDown:
		return "-D"
	case PaneResizeDirectionLeft:
		return "-L"
	case PaneResizeDirectionRight:
		return "-R"
	default:
		return ""
	}
}
