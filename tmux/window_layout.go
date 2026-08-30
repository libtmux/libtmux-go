package tmux

import (
	"context"
	"regexp"
	"slices"
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

// SelectLayout applies one layout operation to the receiver's exact winlink.
// It changes pane geometry without selecting the window or promising client
// focus. The materialized receiver is not refreshed, so callers that need
// current pane geometry must obtain a new snapshot or refresh related models.
// A transport or context error can be delivery-ambiguous; the void result
// cannot carry partial state and no rollback is attempted.
func (w Window) SelectLayout(ctx context.Context, request SelectLayoutRequest) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	// The version is only needed for a name tmux learned partway through the
	// supported range, so it is not asked for otherwise.
	var version Version
	if layoutMirroredPresets[request.Layout] {
		if version, err = w.server.Version(ctx); err != nil {
			return err
		}
	}
	arguments, err := selectLayoutArguments(target, request, version)
	if err != nil {
		return err
	}
	return runWindowLayoutCommand(ctx, w.server, "select-layout", arguments)
}

// layoutPresets are the arrangements tmux names, and are accepted on every
// supported version.
var layoutPresets = map[string]bool{
	"even-horizontal": true,
	"even-vertical":   true,
	"main-horizontal": true,
	"main-vertical":   true,
	"tiled":           true,
}

// layoutMirroredPresets are the arrangements tmux added at 3.5, which put the
// main pane on the far side. Below that they are names tmux does not know, and
// an unrecognised name is what the check below exists to stop.
var layoutMirroredPresets = map[string]bool{
	"main-horizontal-mirrored": true,
	"main-vertical-mirrored":   true,
}

var layoutMirroredVersion = Version{raw: "3.5", major: 3, minor: 5}

// layoutStringPattern matches tmux's own description of an arrangement, which
// #{window_layout} reports and select-layout accepts back. It begins with a
// checksum, which is what makes it distinguishable from a name.
var layoutStringPattern = regexp.MustCompile(`^[0-9a-f]{4},[0-9x,\[\]{}]+$`)

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

// tmux 3.3a exits the server for an unknown layout instead of returning an
// error, so reject names that are neither presets nor layout strings.
func validateLayout(layout string, version Version) error {
	if layout == "" || layoutPresets[layout] || layoutStringPattern.MatchString(layout) {
		return nil
	}
	if layoutMirroredPresets[layout] {
		if version.AtLeast(layoutMirroredVersion) {
			return nil
		}
		return &VersionTooLowError{Current: version, Minimum: layoutMirroredVersion}
	}
	return invalidServerCommandRequest(
		"select-layout",
		"Layout",
		layout,
		"is neither a layout preset nor a tmux layout string; tmux 3.3a exits "+
			"on an unrecognised layout and destroys every session on the socket",
	)
}

// selectLayoutArguments renders one select-layout argument vector. It performs
// no I/O, so a [Plan] can render a layout it has not applied.
func selectLayoutArguments(
	target string,
	request SelectLayoutRequest,
	version Version,
) ([]string, error) {
	if err := validateServerCommandArguments(
		"select-layout",
		serverCommandArgument{field: "Target", value: target},
		serverCommandArgument{field: "Layout", value: request.Layout},
	); err != nil {
		return nil, err
	}
	if err := validateLayout(request.Layout, version); err != nil {
		return nil, err
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
		return nil, invalidServerCommandRequest(
			"select-layout",
			"Mode",
			"",
			"Layout, Spread, Next, and Previous are mutually exclusive",
		)
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
