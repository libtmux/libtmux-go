package tmux

import (
	"context"
	"maps"
	"strconv"
)

var newPaneVersion37 = Version{raw: "3.7", major: 3, minor: 7}

// NewPaneRequest configures a floating pane and requires tmux 3.7 or later.
// Its zero value creates a detached floating pane with tmux's default size,
// position, styles, and command. Nil pointer fields omit their options;
// nonnil pointers are explicit, including empty style or message strings.
// Width and Height must be nonnegative. Empty and Command are mutually
// exclusive. Unsupported tmux versions return [VersionTooLowError] rather than
// omitting fields.
//
// Methods copy pointers and Environment before I/O; concurrent mutation during
// the copy is unsafe.
type NewPaneRequest struct {
	// Attach lets the created pane become active in the exact target winlink;
	// false preserves its active pane.
	Attach bool
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// Command starts the pane with this shell command; empty uses tmux's default.
	Command string
	// Environment is emitted in lexically sorted key order. The map is not
	// retained; nil and an empty map both add no entries.
	Environment map[string]string
	// Width sets a nonnegative pane width; nil lets tmux choose.
	Width *int
	// Height sets a nonnegative pane height; nil lets tmux choose.
	Height *int
	// X sets the horizontal position; nil lets tmux choose.
	X *int
	// Y sets the vertical position; nil lets tmux choose.
	Y *int
	// Zoom preserves the target window's zoomed state.
	Zoom bool
	// Empty creates a pane without starting a command.
	Empty bool
	// Style sets the pane style; nil omits it.
	Style *string
	// ActiveBorderStyle sets the active border style; nil omits it.
	ActiveBorderStyle *string
	// InactiveBorderStyle sets the inactive border style; nil omits it.
	InactiveBorderStyle *string
	// Message sets the pane message; nil omits it.
	Message *string
	// Keep preserves the pane after its command exits.
	Keep bool
}

// SplitPaneRequest configures tiled pane creation on tmux 3.2a or later. Its
// zero value creates a detached pane below the exact target with tmux's
// default size and command. Nil pointer fields omit their options; nonnil
// pointers are explicit, including empty style or message strings. Size and
// Percentage are mutually exclusive. Invalid values are rejected before tmux
// is mutated.
//
// Methods copy pointer and map values before I/O; concurrent mutation is unsafe.
// Empty, styles, Message, and Keep require tmux 3.7 and follow
// [UnsupportedPolicy]. Empty and Command conflict only when Empty is supported.
type SplitPaneRequest struct {
	// Attach lets the created pane become active in the exact target winlink;
	// false preserves its active pane.
	Attach bool
	// Direction selects the side of the target; zero means below.
	Direction PaneDirection
	// Size selects a nonnegative absolute pane size; nil omits it.
	Size *int
	// Percentage selects a size from 0 through 100; nil omits it. It is sent
	// as tmux's "-l N%", which every supported version accepts; the older -p
	// spelling is rejected by tmux 3.4.
	Percentage *int
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// Command starts the pane with this shell command; empty uses tmux's default.
	Command string
	// FullWindow lets the new pane span the full window size.
	FullWindow bool
	// Zoom preserves the window's zoomed state after the split.
	Zoom bool
	// Environment is emitted in lexically sorted key order and is not retained;
	// nil and an empty map both add no entries.
	Environment map[string]string
	// Empty requests an empty pane on tmux 3.7 or later.
	Empty bool
	// Style sets the pane style on tmux 3.7 or later; nil omits it.
	Style *string
	// ActiveBorderStyle sets the active border style on tmux 3.7 or later; nil
	// omits it.
	ActiveBorderStyle *string
	// InactiveBorderStyle sets the inactive border style on tmux 3.7 or later;
	// nil omits it.
	InactiveBorderStyle *string
	// Message sets the pane message on tmux 3.7 or later; nil omits it.
	Message *string
	// Keep preserves the pane after its command exits on tmux 3.7 or later.
	Keep bool
}

// NewPane creates a floating pane in the receiver's exact winlink. Attach
// makes the new pane active in that session and winlink; it is not a global
// client-focus guarantee. The returned [Pane] is freshly materialized in the
// receiver SessionID and WindowID rather than by canonical ID-only refresh.
//
// If tmux reports a [PaneID] before a transport or refresh failure, the partial
// result includes the receiver SessionID and WindowID. Other failures return
// zero; creation is not rolled back.
func (w Window) NewPane(ctx context.Context, request NewPaneRequest) (Pane, error) {
	request = captureNewPaneRequest(request)
	target, err := exactWindowTarget(w)
	if err != nil {
		return Pane{}, err
	}
	return newPane(ctx, w.server, w.sessionID, w.windowID, w.windowIndex, target, request)
}

// NewPane creates a floating pane relative to the receiver's exact
// linked-pane view. Attach makes the new pane active in that session and
// winlink; it is not a global client-focus guarantee. The returned [Pane] is
// freshly materialized in the receiver SessionID and WindowID.
//
// If tmux reports a [PaneID] before a transport or refresh failure, the partial
// result includes the receiver SessionID and WindowID. Other failures return
// zero; creation is not rolled back.
func (p Pane) NewPane(ctx context.Context, request NewPaneRequest) (Pane, error) {
	request = captureNewPaneRequest(request)
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	return newPane(ctx, p.server, p.sessionID, p.windowID, p.windowIndex, target, request)
}

func newPane(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
	windowIndex int,
	target string,
	request NewPaneRequest,
) (Pane, error) {
	if err := validateNewPaneRequest(target, request); err != nil {
		return Pane{}, err
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return Pane{}, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return Pane{}, err
	}
	if err := server.RequireVersion(ctx, newPaneVersion37); err != nil {
		return Pane{}, err
	}

	arguments := []string{"new-pane", "-t", target}
	for _, option := range []struct {
		flag  string
		value *int
	}{
		{flag: "-x", value: request.Width},
		{flag: "-y", value: request.Height},
		{flag: "-X", value: request.X},
		{flag: "-Y", value: request.Y},
	} {
		if option.value != nil {
			arguments = append(arguments, option.flag+strconv.Itoa(*option.value))
		}
	}
	if request.Zoom {
		arguments = append(arguments, "-Z")
	}
	for _, option := range []struct {
		flag  string
		value *string
	}{
		{flag: "-s", value: request.Style},
		{flag: "-S", value: request.ActiveBorderStyle},
		{flag: "-R", value: request.InactiveBorderStyle},
		{flag: "-m", value: request.Message},
	} {
		if option.value != nil {
			arguments = append(arguments, option.flag, *option.value)
		}
	}
	if request.Keep {
		arguments = append(arguments, "-k")
	}
	arguments = append(arguments, "-P", "-F#{pane_id}")
	if startDirectory != "" {
		arguments = append(arguments, "-c"+startDirectory)
	}
	if !request.Attach {
		arguments = append(arguments, "-d")
	}
	arguments = append(arguments, environment...)
	if request.Empty {
		arguments = append(arguments, "-E")
	}
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}

	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("pane", result.Stdout); identityErr == nil {
			return Pane{
				server: server, sessionID: sessionID,
				windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
			}, err
		}
		return Pane{}, err
	}
	result, err = requireRedactedLifecycleSuccess("new-pane", result, nil)
	if err != nil {
		return Pane{}, err
	}
	identity, err := lifecycleStableIdentity("pane", result.Stdout)
	if err != nil {
		return Pane{}, err
	}
	predicted := Pane{
		server: server, sessionID: sessionID,
		windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
	}
	refreshed, err := refreshExactPane(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return refreshed, nil
}

func validateNewPaneRequest(target string, request NewPaneRequest) error {
	if err := validateServerCommandArguments(
		"new-pane",
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "Command", value: request.Command},
		serverCommandArgument{field: "Target", value: target},
	); err != nil {
		return err
	}
	if request.Width != nil && *request.Width < 0 {
		return invalidLifecycleRequest("Width must be nonnegative")
	}
	if request.Height != nil && *request.Height < 0 {
		return invalidLifecycleRequest("Height must be nonnegative")
	}
	if request.Empty && request.Command != "" {
		return invalidLifecycleRequest("Empty and Command are mutually exclusive")
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "Style", value: request.Style},
		{name: "ActiveBorderStyle", value: request.ActiveBorderStyle},
		{name: "InactiveBorderStyle", value: request.InactiveBorderStyle},
		{name: "Message", value: request.Message},
	} {
		if field.value != nil {
			if err := validateServerCommandArgument("new-pane", field.name, *field.value, true); err != nil {
				return err
			}
		}
	}
	_, err := lifecycleEnvironmentArguments(request.Environment)
	return err
}

func captureNewPaneRequest(request NewPaneRequest) NewPaneRequest {
	request.Width = copyOptionalInt(request.Width)
	request.Height = copyOptionalInt(request.Height)
	request.X = copyOptionalInt(request.X)
	request.Y = copyOptionalInt(request.Y)
	request.Style = copyOptionalString(request.Style)
	request.ActiveBorderStyle = copyOptionalString(request.ActiveBorderStyle)
	request.InactiveBorderStyle = copyOptionalString(request.InactiveBorderStyle)
	request.Message = copyOptionalString(request.Message)
	request.Environment = maps.Clone(request.Environment)
	return request
}

// SplitPane creates a tiled pane in the receiver's exact winlink. Attach makes
// the new pane active in that session and winlink; it is not a global
// client-focus guarantee. The returned [Pane] is freshly materialized in the
// receiver SessionID and WindowID rather than by canonical ID-only refresh.
//
// If tmux reports a [PaneID] before a transport or refresh failure, the partial
// result also contains the receiver SessionID and WindowID. Other failures
// return zero; creation is not rolled back.
func (w Window) SplitPane(ctx context.Context, request SplitPaneRequest) (Pane, error) {
	request = captureSplitPaneRequest(request)
	target, err := exactWindowTarget(w)
	if err != nil {
		return Pane{}, err
	}
	return splitPane(ctx, w.server, w.sessionID, w.windowID, w.windowIndex, target, request)
}

// Split creates a tiled pane relative to the receiver's exact linked-pane
// view. Attach makes the new pane active in that session and winlink; it is not
// a global client-focus guarantee. The returned [Pane] is freshly materialized
// in the receiver SessionID and WindowID.
//
// If tmux reports a [PaneID] before a transport or refresh failure, the partial
// result also contains the receiver SessionID and WindowID. Other failures
// return zero; creation is not rolled back.
func (p Pane) Split(ctx context.Context, request SplitPaneRequest) (Pane, error) {
	request = captureSplitPaneRequest(request)
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	return splitPane(ctx, p.server, p.sessionID, p.windowID, p.windowIndex, target, request)
}

func splitPane(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
	windowIndex int,
	target string,
	request SplitPaneRequest,
) (Pane, error) {
	var version Version
	if splitPaneRequiresVersion(request) {
		probed, err := server.Version(ctx)
		if err != nil {
			return Pane{}, err
		}
		version = probed
	}
	arguments, warnings, err := splitPaneArguments(target, request, version)
	if err != nil {
		return Pane{}, err
	}
	if err := server.reportUnsupported(warnings); err != nil {
		return Pane{}, err
	}
	result, err := server.literalCmd(ctx, arguments...)
	return splitPaneResult(ctx, server, sessionID, windowID, windowIndex, result, err)
}

// splitPaneRequiresVersion reports whether a request asks for a flag this
// package gates on the tmux version, and so cannot be rendered without probing
// it. Nearly every split asks for none, which is why the probe is conditional:
// it costs a tmux process of its own the first time.
func splitPaneRequiresVersion(request SplitPaneRequest) bool {
	return request.Empty || splitPaneStylingRequested(request)
}

// splitPaneArguments renders a split's argument vector and the warnings that
// rendering it produced. It performs no I/O, so a [Plan] can render a split it
// has not run and a caller can read what would be sent.
//
// version is consulted only for the flags [splitPaneRequiresVersion] reports,
// so a caller that skipped the probe may pass the zero Version.
func splitPaneArguments(
	target string,
	request SplitPaneRequest,
	version Version,
) ([]string, []Warning, error) {
	if err := validateServerCommandArguments(
		"split-window",
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "Command", value: request.Command},
		serverCommandArgument{field: "Target", value: target},
	); err != nil {
		return nil, nil, err
	}
	if err := validateSplitPaneRequest(request); err != nil {
		return nil, nil, err
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return nil, nil, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return nil, nil, err
	}
	var warnings []Warning
	useEmpty, useStyling := request.Empty, splitPaneStylingRequested(request)
	if useEmpty || useStyling {
		required := Version{raw: "3.7", major: 3, minor: 7}
		if !version.AtLeast(required) {
			if useEmpty {
				warnings = append(warnings, newUnsupportedFeatureWarning(
					"split-window", "empty", version, required,
				))
				useEmpty = false
			}
			if useStyling {
				warnings = append(warnings, newUnsupportedFeatureWarning(
					"split-window", "style/border/message/keep", version, required,
				))
				useStyling = false
			}
		}
	}
	if useEmpty && request.Command != "" {
		return nil, warnings, invalidLifecycleRequest(
			"Empty and Command are mutually exclusive",
		)
	}

	arguments := []string{"split-window", "-t", target}
	switch request.Direction {
	case PaneDirectionBelow:
		arguments = append(arguments, "-v")
	case PaneDirectionAbove:
		arguments = append(arguments, "-v", "-b")
	case PaneDirectionRight:
		arguments = append(arguments, "-h")
	case PaneDirectionLeft:
		arguments = append(arguments, "-h", "-b")
	}
	if request.Size != nil {
		arguments = append(arguments, "-l"+strconv.Itoa(*request.Size))
	}
	if request.Percentage != nil {
		// "-l N%" rather than "-p N". tmux deprecated -p in 3.1 when -l
		// learned percentages, and 3.4 stopped accepting it outright: a
		// percentage split there fails with "size missing". The percentage
		// form of -l works on every version this supports, 3.2a through 3.7c,
		// so there is nothing to gate on.
		arguments = append(arguments, "-l"+strconv.Itoa(*request.Percentage)+"%")
	}
	if request.FullWindow {
		arguments = append(arguments, "-f")
	}
	if request.Zoom {
		arguments = append(arguments, "-Z")
	}
	arguments = append(arguments, "-P", "-F#{pane_id}")
	if startDirectory != "" {
		arguments = append(arguments, "-c"+startDirectory)
	}
	if !request.Attach {
		arguments = append(arguments, "-d")
	}
	arguments = append(arguments, environment...)
	if useEmpty {
		arguments = append(arguments, "-E")
	}
	if useStyling {
		for _, option := range []struct {
			flag  string
			value *string
		}{
			{flag: "-s", value: request.Style},
			{flag: "-S", value: request.ActiveBorderStyle},
			{flag: "-R", value: request.InactiveBorderStyle},
			{flag: "-m", value: request.Message},
		} {
			if option.value != nil {
				arguments = append(arguments, option.flag, *option.value)
			}
		}
		if request.Keep {
			arguments = append(arguments, "-k")
		}
	}
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}
	return arguments, warnings, nil
}

// splitPaneResult materializes the pane a split produced.
func splitPaneResult(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
	windowIndex int,
	result CommandResult,
	err error,
) (Pane, error) {
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("pane", result.Stdout); identityErr == nil {
			return Pane{
				server: server, sessionID: sessionID,
				windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
			}, err
		}
		return Pane{}, err
	}
	result, err = requireRedactedLifecycleSuccess("split-window", result, nil)
	if err != nil {
		return Pane{}, err
	}
	identity, err := lifecycleStableIdentity("pane", result.Stdout)
	if err != nil {
		return Pane{}, err
	}
	predicted := Pane{
		server: server, sessionID: sessionID,
		windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
	}
	pane, err := refreshExactPane(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return pane, nil
}

func validateSplitPaneRequest(request SplitPaneRequest) error {
	if request.Direction > PaneDirectionLeft {
		return invalidLifecycleRequest("unsupported pane direction")
	}
	if request.Size != nil && request.Percentage != nil {
		return invalidLifecycleRequest("Size and Percentage are mutually exclusive")
	}
	if request.Size != nil && *request.Size < 0 {
		return invalidLifecycleRequest("Size must be nonnegative")
	}
	if request.Percentage != nil && (*request.Percentage < 0 || *request.Percentage > 100) {
		return invalidLifecycleRequest("Percentage must be between 0 and 100")
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "Style", value: request.Style},
		{name: "ActiveBorderStyle", value: request.ActiveBorderStyle},
		{name: "InactiveBorderStyle", value: request.InactiveBorderStyle},
		{name: "Message", value: request.Message},
	} {
		if field.value != nil {
			if err := validateServerCommandArgument(
				"split-window", field.name, *field.value, true,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func captureSplitPaneRequest(request SplitPaneRequest) SplitPaneRequest {
	request.Size = copyOptionalInt(request.Size)
	request.Percentage = copyOptionalInt(request.Percentage)
	request.Style = copyOptionalString(request.Style)
	request.ActiveBorderStyle = copyOptionalString(request.ActiveBorderStyle)
	request.InactiveBorderStyle = copyOptionalString(request.InactiveBorderStyle)
	request.Message = copyOptionalString(request.Message)
	request.Environment = maps.Clone(request.Environment)
	return request
}

func splitPaneStylingRequested(request SplitPaneRequest) bool {
	return request.Style != nil ||
		request.ActiveBorderStyle != nil ||
		request.InactiveBorderStyle != nil ||
		request.Message != nil ||
		request.Keep
}
