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
// exclusive and are rejected before the hard version check; unsupported tmux
// versions return a [VersionTooLowError] matching [ErrVersionTooLow] rather
// than omitting requested features.
//
// [Window.NewPane] and [Pane.NewPane] copy every pointer and Environment
// before validation or the version probe and retain none of that storage.
// Mutation after the copy completes cannot affect the call, but mutation
// during the copy is not race-safe.
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

// NewPane creates a floating pane in the receiver's exact winlink. Attach
// makes the new pane active in that session and winlink; it is not a global
// client-focus guarantee. The returned [Pane] is freshly materialized in the
// receiver SessionID and WindowID rather than by canonical ID-only refresh.
//
// A transport or context error can be delivery-ambiguous and no rollback is
// attempted. If tmux printed a valid [PaneID] before that error, or exact
// refresh fails after creation, NewPane returns a partial Pane containing the
// receiver SessionID and WindowID and the new PaneID. Other failures return a
// zero Pane. See [NewPaneRequest], [ErrVersionTooLow],
// [ErrInvalidCommandOutput], and [CommandError].
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
// A transport or context error can be delivery-ambiguous and no rollback is
// attempted. If tmux printed a valid [PaneID] before that error, or exact
// refresh fails after creation, NewPane returns a partial Pane containing the
// receiver SessionID and WindowID and the new PaneID. Other failures return a
// zero Pane. See [NewPaneRequest] and [ErrVersionTooLow].
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
