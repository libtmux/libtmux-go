package tmux

import (
	"context"
	"encoding/json"
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
	// Layout names a preset or supplies a tmux layout string - either the
	// classic checksum-prefixed grammar every supported version accepts, or
	// the JSON shape tmux 3.8+ also accepts, exactly as #{window_layout}
	// reported it; empty selects the zero-value behavior.
	Layout string
	// Spread distributes pane space evenly.
	Spread bool
	// Next selects the next preset layout.
	Next bool
	// Previous selects the previous preset layout.
	Previous bool
}

// Validate checks mode exclusivity, NUL bytes, recognised preset names and
// checksummed custom-layout syntax without running tmux. Its zero value is valid.
// Invalid requests match [ErrInvalidServerCommandRequest]. Mirrored presets are
// accepted here; execution checks tmux version support. Geometry and the number
// of panes are validated by tmux when the layout is applied.
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

	if request.Layout == "" || layoutPresets[request.Layout] || layoutMirroredPresets[request.Layout] || layoutLooksLikeJSON(request.Layout) {
		return nil
	}
	if _, custom := layout.Cells(request.Layout); custom {
		return nil
	}
	for _, preset := range layoutAllPresetNames {
		if strings.HasPrefix(preset, request.Layout) {
			return nil
		}
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
	// The version is only needed for a name tmux learned partway through the
	// supported range, a layout shape tmux learned at 3.8, or to resolve a
	// prefix of either against the presets that exist on this connection, so
	// it is not asked for otherwise.
	var version Version
	if layoutNeedsVersion(request.Layout) {
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

// layoutPresetNames are the arrangements tmux names, and are accepted on
// every supported version.
var layoutPresetNames = []string{
	"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled",
}

// layoutPresets are the arrangements tmux names, and are accepted on every
// supported version.
var layoutPresets = presetSet(layoutPresetNames)

// layoutMirroredPresetNames are the arrangements tmux added at 3.5, which put
// the main pane on the far side. Below that they are names tmux does not
// know, and an unrecognised name is what the check below exists to stop.
var layoutMirroredPresetNames = []string{
	"main-horizontal-mirrored", "main-vertical-mirrored",
}

// layoutMirroredPresets are the arrangements tmux added at 3.5, which put the
// main pane on the far side. Below that they are names tmux does not know, and
// an unrecognised name is what the check below exists to stop.
var layoutMirroredPresets = presetSet(layoutMirroredPresetNames)

// layoutAllPresetNames is every preset name this package knows on any
// version, for resolving a prefix against - version-gating which of them are
// valid candidates happens separately.
var layoutAllPresetNames = append(slices.Clone(layoutPresetNames), layoutMirroredPresetNames...)

func presetSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// layoutNeedsVersion reports whether resolving layout requires knowing the
// connected tmux's version: the JSON shape and the mirrored presets both
// depend on it, and so does any prefix that could match a mirrored preset
// name, because whether that candidate exists changes the resolution. An
// exact base preset name, a classic checksum-prefixed layout string, a
// prefix that can only ever match a base preset name, and garbage that
// prefixes nothing all skip the round trip.
func layoutNeedsVersion(layout string) bool {
	if layout == "" || layoutStringPattern.MatchString(layout) || layoutPresets[layout] {
		return false
	}
	if layoutLooksLikeJSON(layout) || layoutMirroredPresets[layout] {
		return true
	}
	for _, preset := range layoutMirroredPresetNames {
		if strings.HasPrefix(preset, layout) {
			return true
		}
	}
	return false
}

var layoutMirroredVersion = Version{raw: "3.5", major: 3, minor: 5}

// layoutJSONVersion is the tmux feature level at which #{window_layout}
// switched to a JSON subset for non-control clients and select-layout learned
// to accept that shape back, alongside the classic grammar it still accepts.
// tmux CHANGES, 3.7c to 3.8: "Layout strings now use a JSON subset format
// which includes floating panes. The old format is still accepted."
var layoutJSONVersion = Version{raw: "3.8", major: 3, minor: 8}

// layoutStringPattern matches tmux's classic description of an arrangement,
// which #{window_layout} reports (and select-layout accepts back) on every
// supported version up to and including tmux 3.7c, and which newer tmux still
// accepts alongside the JSON shape below. It begins with a checksum, which is
// what makes it distinguishable from a name.
var layoutStringPattern = regexp.MustCompile(`^[0-9a-f]{4},[0-9x,\[\]{}]+$`)

// layoutLooksLikeJSON reports whether layout is shaped like the JSON layout
// tmux 3.8+ reports from #{window_layout} and accepts back from
// select-layout. It checks only the outer envelope - this package treats a
// layout as an opaque string tmux hands back and forth, never a structure to
// parse - so a caller's own well-formed JSON round-trips exactly like tmux's.
func layoutLooksLikeJSON(layout string) bool {
	trimmed := strings.TrimSpace(layout)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	return json.Valid([]byte(layout))
}

// layoutPanePattern matches one layout cell that holds a pane. tmux dumps such
// a cell as width x height, offsets, and the pane's own number; cells that only
// arrange other cells stop after the offsets. It never matches inside the JSON
// shape, which carries no such comma-joined run.
var layoutPanePattern = regexp.MustCompile(`[0-9]+x[0-9]+,-?[0-9]+,-?[0-9]+,([0-9]+)`)

// layoutJSONPanePattern matches one pane's id in the JSON layout shape. Unlike
// the classic grammar's bare pane number, the JSON id already carries its %
// sigil.
var layoutJSONPanePattern = regexp.MustCompile(`"I":"(%[0-9]+)"`)

// layoutListsPane reports whether layout still arranges pane. A layout holding
// no readable cell reports true, so an arrangement this does not recognise is
// never mistaken for a pane that closed.
func layoutListsPane(layout string, pane PaneID) bool {
	if layoutLooksLikeJSON(layout) {
		ids := layoutJSONPanePattern.FindAllStringSubmatch(layout, -1)
		if len(ids) == 0 {
			return true
		}
		return slices.ContainsFunc(ids, func(id []string) bool {
			return PaneID(id[1]) == pane
		})
	}
	cells := layoutPanePattern.FindAllStringSubmatch(layout, -1)
	if len(cells) == 0 {
		return true
	}
	return slices.ContainsFunc(cells, func(cell []string) bool {
		return PaneID("%"+cell[1]) == pane
	})
}

// validateLayout resolves layout against the presets valid on version and
// returns the exact string to send tmux. tmux's own layout_set_lookup is
// already a prefix match, so a unique prefix ("tile", "even-h") applies on
// every version and can never reach layout_parse, the 3.3a crash path; this
// accepts one and refuses an ambiguous prefix by naming its candidates,
// rather than forwarding an ambiguous or unrecognised value for tmux itself
// to reject. A resolved preset's canonical full name is what reaches tmux,
// never the prefix the caller typed, so tmux's own resolution never runs on
// a value this package already classified.
//
// tmux 3.3a exited the server for an unknown layout instead of returning an
// error, so an unrecognised name is refused on every version, not only 3.3a.
func validateLayout(layout string, version Version) (string, error) {
	if layout == "" || layoutStringPattern.MatchString(layout) {
		return layout, nil
	}
	if layoutPresets[layout] {
		return layout, nil
	}
	if layoutMirroredPresets[layout] {
		if version.AtLeast(layoutMirroredVersion) {
			return layout, nil
		}
		return "", &VersionTooLowError{Current: version, Minimum: layoutMirroredVersion}
	}
	if layoutLooksLikeJSON(layout) {
		if version.AtLeast(layoutJSONVersion) {
			return layout, nil
		}
		return "", &VersionTooLowError{Current: version, Minimum: layoutJSONVersion}
	}
	candidates := layoutPresetNames
	if version.AtLeast(layoutMirroredVersion) {
		candidates = layoutAllPresetNames
	}
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, layout) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", invalidServerCommandRequest(
			"select-layout",
			"Layout",
			layout,
			"is neither a layout preset nor a tmux layout string; refused on "+
				"every version, since tmux 3.3a once exited the server and "+
				"destroyed every session on the socket for exactly this",
		)
	default:
		return "", invalidServerCommandRequest(
			"select-layout",
			"Layout",
			layout,
			"is ambiguous among "+strings.Join(matches, ", ")+"; use one of those exact spellings",
		)
	}
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
	resolvedLayout, err := validateLayout(request.Layout, version)
	if err != nil {
		return nil, err
	}

	arguments := []string{"select-layout", "-t", target}
	switch {
	case request.Layout != "":
		arguments = append(arguments, resolvedLayout)
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
