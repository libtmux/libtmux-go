package tmux

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/layout"
)

// SelectLayoutRequest selects one layout operation. Its zero value reapplies
// the last preset layout. Layout, Spread, Next, and Previous are mutually
// exclusive and are validated before execution; an empty Layout is omitted
// rather than passed explicitly. The request contains no retained
// caller-owned storage and is supported on tmux 3.2a or later.
type SelectLayoutRequest struct {
	// Layout names a preset or supplies a tmux layout string; empty selects the
	// zero-value behavior.
	Layout string
	// Spread distributes pane space evenly.
	Spread bool
	// Next selects the next preset layout.
	Next bool
	// Previous selects the previous preset layout.
	Previous bool
}

// Validate checks mode exclusivity, NUL bytes, candidate preset names and
// checksummed custom-layout syntax without running tmux. Its zero value is valid.
// Invalid requests match [ErrInvalidServerCommandRequest]. Names and abbreviations
// valid on any supported tmux are accepted here; execution checks daemon support.
// tmux validates geometry and live pane counts when applying the layout.
func (request SelectLayoutRequest) Validate() error {
	if err := validateServerCommandArgument("select-layout", "Layout", request.Layout, true); err != nil {
		return err
	}
	modes := 0
	if request.Layout != "" {
		modes++
	}
	if request.Spread {
		modes++
	}
	if request.Next {
		modes++
	}
	if request.Previous {
		modes++
	}
	if modes > 1 {
		return invalidServerCommandRequest(
			"select-layout",
			"Mode",
			"",
			"Layout, Spread, Next, and Previous are mutually exclusive",
		)
	}

	if request.Layout == "" || layoutName(request.Layout, false) || layoutName(request.Layout, true) {
		return nil
	}
	if _, custom := layout.Cells(request.Layout); custom {
		return nil
	}
	// tmux 3.3a exits the daemon on unknown layout names.
	return invalidServerCommandRequest(
		"select-layout", "Layout", request.Layout,
		"is neither a layout preset nor a tmux layout string; tmux 3.3a exits "+
			"on an unrecognised layout and destroys every session on the socket",
	)
}

// SelectLayout applies one layout operation to the receiver's exact winlink.
// It changes pane geometry without selecting the window or promising client
// focus. The materialized receiver is not refreshed, so callers that need
// current pane geometry must obtain a new snapshot or refresh related models.
// A transport or context error can be delivery-ambiguous; the void result
// cannot carry partial state and no rollback is attempted.
func (w Window) SelectLayout(ctx context.Context, request SelectLayoutRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	version, err := w.server.validateLayouts(ctx, func(yield func(string, int) bool) {
		yield(request.Layout, 1)
	})
	if err != nil {
		return err
	}
	arguments, err := selectLayoutArguments(target, request, version)
	if err != nil {
		return err
	}
	return runWindowLayoutCommand(ctx, w.server, "select-layout", arguments)
}

var layoutPresets = [...]string{
	"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled",
	"main-horizontal-mirrored", "main-vertical-mirrored",
}

func layoutName(value string, mirrored bool) bool {
	names := layoutPresets[:5]
	if mirrored {
		names = layoutPresets[:]
	}
	if slices.Contains(names, value) {
		return true
	}
	matches := 0
	for _, name := range names {
		if strings.HasPrefix(name, value) {
			matches++
		}
	}
	return matches == 1
}

func layoutNeedsVersion(value string) bool {
	return layoutName(value, false) != layoutName(value, true)
}

func validateLayoutVersion(value string, version Version) error {
	if !layoutNeedsVersion(value) || layoutName(value, version.AtLeast(layoutMirroredVersion)) {
		return nil
	}
	if layoutName(value, true) {
		return &VersionTooLowError{Current: version, Minimum: layoutMirroredVersion}
	}
	return invalidServerCommandRequest("select-layout", "Layout", value,
		"is ambiguous on the running tmux version")
}

var layoutMirroredVersion = Version{raw: "3.5", major: 3, minor: 5}

// layoutPanePattern matches one layout cell that holds a pane. tmux dumps such
// a cell as width x height, offsets, and the pane's own number; cells that only
// arrange other cells stop after the offsets.
var layoutPanePattern = regexp.MustCompile(`[0-9]+x[0-9]+,-?[0-9]+,-?[0-9]+,([0-9]+)`)

// layoutListsPane reports whether layout still arranges pane. A layout holding
// no readable cell reports true, so an arrangement this does not recognise is
// never mistaken for a pane that closed.
func layoutListsPane(layout string, pane PaneID) bool {
	cells := layoutPanePattern.FindAllStringSubmatch(layout, -1)
	if len(cells) == 0 {
		return true
	}
	return slices.ContainsFunc(cells, func(cell []string) bool {
		return PaneID("%"+cell[1]) == pane
	})
}

// selectLayoutArguments renders one select-layout argument vector. It performs
// no I/O, so a [Plan] can render a layout it has not applied.
func selectLayoutArguments(
	target string,
	request SelectLayoutRequest,
	version Version,
) ([]string, error) {
	if err := validateServerCommandArgument("select-layout", "Target", target, true); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := validateLayoutVersion(request.Layout, version); err != nil {
		return nil, err
	}

	arguments := []string{"select-layout", "-t", target}
	switch {
	case request.Layout != "":
		arguments = append(arguments, request.Layout)
	case request.Spread:
		arguments = append(arguments, "-E")
	case request.Next:
		arguments = append(arguments, "-n")
	case request.Previous:
		arguments = append(arguments, "-p")
	}
	return arguments, nil
}

// NextLayout applies the next preset layout to the receiver's exact winlink.
// It changes pane geometry without selecting or refreshing the window. A
// transport or context error can be delivery-ambiguous; the void result cannot
// carry partial state and no rollback is attempted.
func (w Window) NextLayout(ctx context.Context) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	return runWindowLayoutCommand(
		ctx,
		w.server,
		"next-layout",
		[]string{"next-layout", "-t", target},
	)
}

// PreviousLayout applies the previous preset layout to the receiver's exact
// winlink. It changes pane geometry without selecting or refreshing the
// window. A transport or context error can be delivery-ambiguous; the void
// result cannot carry partial state and no rollback is attempted.
func (w Window) PreviousLayout(ctx context.Context) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	return runWindowLayoutCommand(
		ctx,
		w.server,
		"previous-layout",
		[]string{"previous-layout", "-t", target},
	)
}

func runWindowLayoutCommand(
	ctx context.Context,
	server Server,
	subcommand string,
	arguments []string,
) error {
	result, err := server.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr(subcommand, result, err)
}
