package tmux

import (
	"context"
	"strconv"
)

var paneModesVersion35 = Version{raw: "3.5", major: 3, minor: 5}

// CopyModeRequest configures tmux copy mode. Its zero value enters copy mode
// on the receiver without scrolling. Fields may be combined. tmux resolves the
// receiver and optional SourcePane before processing Cancel, so a stale target
// can fail the request without resetting any mode. After successful resolution,
// Cancel resets the pane's modes and returns before ScrollUp, ExitOnBottom,
// MouseDrag, or PageDown has an effect. The library still validates SourcePane
// and, when PageDown is true, probes the tmux version before invoking copy-mode.
// SourcePane is a value and is not retained.
type CopyModeRequest struct {
	// ScrollUp enters copy mode one page above the current position.
	ScrollUp bool
	// ExitOnBottom makes copy mode exit after scrolling back to the visible
	// screen, until another non-scrolling key disables that behavior.
	ExitOnBottom bool
	// MouseDrag begins a copy-mode mouse drag and is meaningful from a mouse
	// binding.
	MouseDrag bool
	// PageDown enters copy mode one page below the current position. It requires
	// tmux 3.5; older versions warn and omit the flag. Its version probe still
	// occurs when Cancel is set.
	PageDown bool
	// SourcePane selects a stable pane whose content is copied while the
	// receiver remains the pane placed in copy mode. Zero uses the receiver. A
	// nonzero value is validated by the library and resolved by tmux even when
	// Cancel is set; a stale or nonexistent pane can prevent cancellation.
	SourcePane PaneID
	// Cancel exits copy mode and any other active pane mode after tmux resolves
	// the receiver and optional SourcePane. A resolution failure prevents the
	// reset; after successful resolution, tmux ignores the other action fields.
	Cancel bool
}

// TreeSortOrder selects one initial tmux choose-tree ordering. Its zero value
// leaves tmux's configured ordering unchanged.
type TreeSortOrder uint8

const (
	// TreeSortDefault leaves the configured choose-tree ordering unchanged.
	TreeSortDefault TreeSortOrder = iota
	// TreeSortIndex orders tree entries by index.
	TreeSortIndex
	// TreeSortName orders tree entries by name.
	TreeSortName
	// TreeSortTime orders tree entries by activity time.
	TreeSortTime
	// TreeSortSize orders tree entries by size.
	TreeSortSize
)

// ChooseTreeRequest configures the interactive session and window chooser. Its
// zero value uses tmux's configured presentation and ordering. Format and
// Filter are copied before execution; callers retain ownership and must not
// mutate them concurrently.
type ChooseTreeRequest struct {
	// SessionsCollapsed starts with session nodes collapsed.
	SessionsCollapsed bool
	// WindowsCollapsed starts with window nodes collapsed.
	WindowsCollapsed bool
	// Format is a tmux format expression evaluated for each tree item. Nil
	// leaves the configured format; nonnil empty remains an explicit format.
	Format *string
	// Filter is a tmux format expression whose truth value selects tree items.
	// Nil omits filtering; nonnil empty remains an explicit expression.
	Filter *TmuxFilter
	// Sort selects the initial ordering. Unknown values are rejected before
	// execution.
	Sort TreeSortOrder
	// Reverse reverses the initial tree ordering.
	Reverse bool
	// Zoom zooms the chooser pane while tree mode is active.
	Zoom bool
}

// FindWindowRequest configures the interactive window search chooser. Match is
// a tmux search operand, not a shell command. The zero value searches with an
// empty glob using tmux's default name, title, and visible-content scopes.
type FindWindowRequest struct {
	// Match is the glob pattern, or regular expression when Regex is true.
	Match string
	// MatchContent restricts matching to visible window content, not history.
	MatchContent bool
	// CaseInsensitive makes matching ignore case.
	CaseInsensitive bool
	// MatchName restricts matching to window names.
	MatchName bool
	// Regex interprets Match as a regular expression instead of a glob.
	Regex bool
	// MatchTitle restricts matching to window titles.
	MatchTitle bool
}

// DisplayPanesRequest configures pane-number display for tmux's current
// client. This command is client-scoped and does not inherit the Pane target.
// Duration is read before execution and is not retained; callers must not
// mutate it concurrently.
type DisplayPanesRequest struct {
	// Duration is the display time in milliseconds. Nil uses tmux's
	// display-panes-time option; zero waits for a key press. Negative values are
	// rejected before execution.
	Duration *int
	// NoSelect prevents number keys from selecting a pane, so the display
	// closes only after its duration. With Duration set to zero, it may remain
	// until the context is canceled.
	NoSelect bool
}

// CopyMode enters or cancels copy mode using the receiver's exact linked
// session-window-pane target. PageDown requires tmux 3.5; older versions emit a
// synchronous warning and omit that flag. A version-probe error stops the
// operation. tmux resolves the receiver and optional SourcePane before
// processing Cancel; a stale target can therefore fail without resetting the
// current mode. Once resolution succeeds, Cancel resets pane modes before tmux
// considers the other action fields. SourcePane validation and the PageDown
// version probe still happen in the library before tmux is invoked.
//
// A completed command produces a [CommandError] only when tmux writes stderr;
// the library-created error retains only the exit code. A nonzero exit without
// stderr is ignored. Transport and context errors remain detectable with
// [errors.Is], but an accepted mode change is not rolled back.
func (p Pane) CopyMode(ctx context.Context, request CopyModeRequest) error {
	arguments, err := exactPaneCommandArguments(p, "copy-mode")
	if err != nil {
		return err
	}

	var sourcePane string
	if request.SourcePane != "" {
		sourcePane = request.SourcePane.String()
		if err := validateTypedTarget(
			"copy-mode", "SourcePane", "pane", sourcePane,
		); err != nil {
			return err
		}
	}

	var current Version
	if request.PageDown {
		current, err = p.server.Version(ctx)
		if err != nil {
			return err
		}
	}
	if request.ScrollUp {
		arguments = append(arguments, "-u")
	}
	if request.ExitOnBottom {
		arguments = append(arguments, "-e")
	}
	if request.MouseDrag {
		arguments = append(arguments, "-M")
	}
	if request.PageDown {
		if current.AtLeast(paneModesVersion35) {
			arguments = append(arguments, "-d")
		} else if err := p.server.unsupportedFeature(
			"copy-mode", "page_down", current, paneModesVersion35,
		); err != nil {
			return err
		}
	}
	if sourcePane != "" {
		arguments = append(arguments, "-s", sourcePane)
	}
	if request.Cancel {
		arguments = append(arguments, "-q")
	}
	return runRedactedPaneModeCommand(ctx, p.server, "copy-mode", arguments)
}

// ClockMode enters clock mode using the receiver's exact linked target. A
// completed-command and cancellation semantics match [Pane.CopyMode].
func (p Pane) ClockMode(ctx context.Context) error {
	return p.runSimplePaneMode(ctx, "clock-mode")
}

// ChooseBuffer enters tmux's interactive buffer chooser using the receiver's
// exact linked target. It requires an attached client. Completed-command and
// cancellation semantics match [Pane.CopyMode].
func (p Pane) ChooseBuffer(ctx context.Context) error {
	return p.runSimplePaneMode(ctx, "choose-buffer")
}

// ChooseClient enters tmux's interactive client chooser using the receiver's
// exact linked target. It requires an attached client. Completed-command and
// cancellation semantics match [Pane.CopyMode].
func (p Pane) ChooseClient(ctx context.Context) error {
	return p.runSimplePaneMode(ctx, "choose-client")
}

// CustomizeMode enters tmux's interactive option browser using the receiver's
// exact linked target. It requires an attached client. Completed-command and
// cancellation semantics match [Pane.CopyMode].
func (p Pane) CustomizeMode(ctx context.Context) error {
	return p.runSimplePaneMode(ctx, "customize-mode")
}

// ChooseTree enters tmux's interactive session, window, and pane chooser using
// the receiver's exact linked target. It requires an attached client. Format
// and Filter are tmux expressions; neither is interpreted by a shell.
// Unsupported sort values and invalid arguments fail before execution. A
// completed command produces a [CommandError] only when tmux writes stderr;
// the library-created error retains only the exit code. A nonzero exit without
// stderr is ignored. Transport and context errors may leave the pane in tree
// mode and remain detectable with [errors.Is].
func (p Pane) ChooseTree(ctx context.Context, request ChooseTreeRequest) error {
	arguments, err := exactPaneCommandArguments(p, "choose-tree")
	if err != nil {
		return err
	}
	format := copyOptionalString(request.Format)
	var filter *TmuxFilter
	if request.Filter != nil {
		value := *request.Filter
		filter = &value
	}
	if format != nil {
		if err := validateServerCommandArgument("choose-tree", "Format", *format, true); err != nil {
			return err
		}
	}
	if filter != nil {
		if err := validateServerCommandArgument(
			"choose-tree", "Filter", string(*filter), true,
		); err != nil {
			return err
		}
	}
	sortOrder, err := treeSortArgument(request.Sort)
	if err != nil {
		return err
	}

	if request.SessionsCollapsed {
		arguments = append(arguments, "-s")
	}
	if request.WindowsCollapsed {
		arguments = append(arguments, "-w")
	}
	if request.Zoom {
		arguments = append(arguments, "-Z")
	}
	if request.Reverse {
		arguments = append(arguments, "-r")
	}
	if format != nil {
		arguments = append(arguments, "-F", *format)
	}
	if filter != nil {
		arguments = append(arguments, "-f", string(*filter))
	}
	if sortOrder != "" {
		arguments = append(arguments, "-O", sortOrder)
	}
	return runRedactedPaneModeCommand(ctx, p.server, "choose-tree", arguments)
}

// FindWindow opens a tree chooser filtered by Match using the receiver's exact
// linked target. It requires an attached client. Match is passed as one tmux
// search operand and protected from leading-dash option parsing; it is not a
// tmux format or shell command. A completed command produces a [CommandError]
// only when tmux writes stderr; the library-created error retains only the exit
// code. A nonzero exit without stderr is ignored. Transport and context errors
// may leave the chooser open and remain detectable with [errors.Is].
func (p Pane) FindWindow(ctx context.Context, request FindWindowRequest) error {
	arguments, err := exactPaneCommandArguments(p, "find-window")
	if err != nil {
		return err
	}
	if err := validateServerCommandArgument(
		"find-window", "Match", request.Match, true,
	); err != nil {
		return err
	}
	if request.MatchContent {
		arguments = append(arguments, "-C")
	}
	if request.CaseInsensitive {
		arguments = append(arguments, "-i")
	}
	if request.MatchName {
		arguments = append(arguments, "-N")
	}
	if request.Regex {
		arguments = append(arguments, "-r")
	}
	if request.MatchTitle {
		arguments = append(arguments, "-T")
	}
	arguments = append(arguments, "--", request.Match)
	return runRedactedPaneModeCommand(ctx, p.server, "find-window", arguments)
}

// DisplayPanes displays pane numbers for tmux's current client and waits for
// the indicator to close. It is client-scoped: the receiver supplies only the
// server connection and no pane target. A zero request uses tmux's configured
// duration and permits number-key selection.
//
// A completed command produces a [CommandError] only when tmux writes stderr;
// the library-created error retains only the exit code. A nonzero exit without
// stderr is ignored. Transport and context errors remain detectable with
// [errors.Is], but cancellation cannot revoke an accepted display.
func (p Pane) DisplayPanes(ctx context.Context, request DisplayPanesRequest) error {
	arguments := []string{"display-panes"}
	if request.Duration != nil {
		duration := *request.Duration
		if duration < 0 {
			return invalidServerCommandRequest(
				"display-panes", "Duration", strconv.Itoa(duration), "must be nonnegative",
			)
		}
		arguments = append(arguments, "-d", strconv.Itoa(duration))
	}
	if request.NoSelect {
		arguments = append(arguments, "-N")
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("display-panes", result, err)
}

func (p Pane) runSimplePaneMode(ctx context.Context, subcommand string) error {
	arguments, err := exactPaneCommandArguments(p, subcommand)
	if err != nil {
		return err
	}
	return runRedactedPaneModeCommand(ctx, p.server, subcommand, arguments)
}

func exactPaneCommandArguments(pane Pane, subcommand string) ([]string, error) {
	target, err := exactPaneTarget(pane)
	if err != nil {
		return nil, err
	}
	return []string{subcommand, "-t", target}, nil
}

func runRedactedPaneModeCommand(
	ctx context.Context,
	server Server,
	subcommand string,
	arguments []string,
) error {
	result, err := server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr(subcommand, result, err)
}

func treeSortArgument(order TreeSortOrder) (string, error) {
	switch order {
	case TreeSortDefault:
		return "", nil
	case TreeSortIndex:
		return "index", nil
	case TreeSortName:
		return "name", nil
	case TreeSortTime:
		return "time", nil
	case TreeSortSize:
		return "size", nil
	default:
		return "", invalidServerCommandRequest(
			"choose-tree",
			"Sort",
			strconv.FormatUint(uint64(order), 10),
			"is unsupported",
		)
	}
}
