package tmux

import (
	"context"
	"maps"
	"slices"
)

var (
	panePopupVersion33 = Version{raw: "3.3", major: 3, minor: 3}
	panePopupVersion36 = Version{raw: "3.6", major: 3, minor: 6}
)

// DisplayPopupRequest configures a popup overlay. Pointer and map values are
// read and copied before any version probe or command, and the call retains
// none of the caller's storage. Callers must not mutate them concurrently.
// Pointer fields distinguish omission from an explicit empty value, with the
// Command and StartDirectory behavior documented on those fields.
//
// CloseOnExit and CloseOnSuccess are mutually exclusive. CloseExisting asks
// tmux only to close the existing overlay, so the remaining fields do not
// create a replacement. NoBorder makes BorderLines ineffective. When modifying
// an existing popup, NoKeys clears its configured automatic-close flags before
// tmux applies CloseOnExit, CloseOnSuccess, and CloseOnAnyKey from this request.
// NoKeys does not disable keyboard input.
type DisplayPopupRequest struct {
	// Command is interpreted by tmux as a shell command. Nil lets tmux use
	// default-command and then its default shell; nonnil empty is passed
	// explicitly and tmux resolves it directly to the default shell.
	Command *string
	// CloseOnExit closes the overlay whenever Command exits.
	CloseOnExit bool
	// CloseOnSuccess closes the overlay only when Command exits successfully.
	CloseOnSuccess bool
	// CloseExisting closes the selected client's existing overlay and prevents
	// this request from creating a replacement.
	CloseExisting bool
	// TargetClient selects the client that receives the overlay; zero lets tmux choose.
	TargetClient ClientName
	// Width is an explicit cell count or percentage and may contain tmux format
	// expressions. Nil lets tmux choose the default width.
	Width *string
	// Height is an explicit cell count or percentage and may contain tmux
	// format expressions. Nil lets tmux choose the default height.
	Height *string
	// X is a tmux popup position expression and may contain tmux formats. Nil
	// lets tmux choose the horizontal position.
	X *string
	// Y is a tmux popup position expression and may contain tmux formats. Nil
	// lets tmux choose the vertical position.
	Y *string
	// StartDirectory selects the popup working directory. Nil omits the option;
	// nonnil empty is normalized to ".". The library expands a leading local ~
	// before tmux expands formats in the resulting value.
	StartDirectory *string
	// Title is the popup title format. Nil leaves the tmux default.
	Title *string
	// BorderLines selects tmux's border-line style. NoBorder makes it
	// ineffective.
	BorderLines *string
	// Style selects the tmux style for the popup interior.
	Style *string
	// BorderStyle selects the tmux style for the popup border.
	BorderStyle *string
	// Environment adds validated popup environment entries. Nil and an empty
	// map add none; entries are sent in lexical key order.
	Environment map[string]string
	// NoBorder removes the popup border and makes BorderLines ineffective.
	NoBorder bool
	// CloseOnAnyKey lets a nonmouse, nonpaste key dismiss the popup after its
	// command has exited. It does not dismiss the popup while the job is active;
	// tmux continues to send keys to that job.
	CloseOnAnyKey bool
	// NoKeys clears automatic-close flags previously configured on an existing
	// popup. Same-request close flags are then applied, and key input remains
	// enabled.
	NoKeys bool
}

type displayPopupValues struct {
	command        *string
	targetClient   string
	width          *string
	height         *string
	x              *string
	y              *string
	startDirectory *string
	title          *string
	borderLines    *string
	style          *string
	borderStyle    *string
	environment    []string
	closeOnExit    bool
	closeOnSuccess bool
	closeExisting  bool
	noBorder       bool
	closeOnAnyKey  bool
	noKeys         bool
}

// DisplayPopup displays an overlay on the selected client and waits until it
// closes after command exit or user dismissal. A zero request starts tmux's
// default command or default shell and may wait indefinitely. The API exposes
// no popup process handle, stdout, or child exit status.
//
// The command carries the receiver's exact linked session-window-pane target,
// which supplies format and working-directory context. TargetClient selects
// the overlay client; it does not turn the operation into pane delivery. tmux
// interprets Command as a shell command. The library's protection for a final
// semicolon applies only to tmux's outer command parser and does not quote or
// neutralize the inner shell command.
//
// Title, BorderLines, Style, BorderStyle, Environment, and NoBorder require
// tmux 3.3. CloseOnAnyKey and NoKeys require tmux 3.6. Unsupported requested
// fields produce synchronous warnings and are omitted.
//
// When this call modifies an existing popup, NoKeys resets its automatic-close
// flags before tmux applies any close flags in this request. CloseOnAnyKey acts
// only after the popup job exits; while the job is active, keys go to the job.
//
// Invalid fields fail before display. A completed invocation produces a
// [CommandError] only when tmux writes stderr; the library-created error
// retains only the exit code. A nonzero exit without stderr is ignored.
// Context errors remain detectable with [errors.Is], but cancellation cannot
// dismiss or revoke an accepted popup, which may remain visible.
func (p Pane) DisplayPopup(ctx context.Context, request DisplayPopupRequest) error {
	arguments, err := exactPaneCommandArguments(p, "display-popup")
	if err != nil {
		return err
	}
	values, err := captureDisplayPopupRequest(request)
	if err != nil {
		return err
	}

	var current Version
	if values.needsVersion() {
		current, err = p.server.Version(ctx)
		if err != nil {
			return err
		}
	}
	if values.closeExisting {
		arguments = append(arguments, "-C")
	}
	if values.targetClient != "" {
		arguments = append(arguments, "-c", values.targetClient)
	}
	if values.closeOnExit {
		arguments = append(arguments, "-E")
	}
	if values.closeOnSuccess {
		arguments = append(arguments, "-E", "-E")
	}
	if values.width != nil {
		arguments = append(arguments, "-w", *values.width)
	}
	if values.height != nil {
		arguments = append(arguments, "-h", *values.height)
	}
	if values.x != nil {
		arguments = append(arguments, "-x", *values.x)
	}
	if values.y != nil {
		arguments = append(arguments, "-y", *values.y)
	}
	if values.startDirectory != nil {
		arguments = append(arguments, "-d", *values.startDirectory)
	}
	arguments, err = appendPopupVersion33Arguments(p.server, arguments, values, current)
	if err != nil {
		return err
	}
	arguments, err = appendPopupVersion36Arguments(p.server, arguments, values, current)
	if err != nil {
		return err
	}
	if values.command != nil {
		arguments = append(arguments, *values.command)
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("display-popup", result, err)
}

func captureDisplayPopupRequest(request DisplayPopupRequest) (displayPopupValues, error) {
	if request.CloseOnExit && request.CloseOnSuccess {
		return displayPopupValues{}, invalidServerCommandRequest(
			"display-popup",
			"CloseOnExit",
			"true",
			"is mutually exclusive with CloseOnSuccess",
		)
	}
	values := displayPopupValues{
		command:        copyOptionalString(request.Command),
		width:          copyOptionalString(request.Width),
		height:         copyOptionalString(request.Height),
		x:              copyOptionalString(request.X),
		y:              copyOptionalString(request.Y),
		title:          copyOptionalString(request.Title),
		borderLines:    copyOptionalString(request.BorderLines),
		style:          copyOptionalString(request.Style),
		borderStyle:    copyOptionalString(request.BorderStyle),
		closeOnExit:    request.CloseOnExit,
		closeOnSuccess: request.CloseOnSuccess,
		closeExisting:  request.CloseExisting,
		noBorder:       request.NoBorder,
		closeOnAnyKey:  request.CloseOnAnyKey,
		noKeys:         request.NoKeys,
	}
	if request.TargetClient != "" {
		client := request.TargetClient.String()
		if client == "" {
			return displayPopupValues{}, invalidServerCommandRequest(
				"display-popup", "TargetClient", "", "must not be empty",
			)
		}
		if err := validateServerCommandArgument(
			"display-popup", "TargetClient", client, true,
		); err != nil {
			return displayPopupValues{}, err
		}
		values.targetClient = client
	}
	if request.StartDirectory != nil {
		directory, err := expandCommandPath("display-popup", *request.StartDirectory)
		if err != nil {
			return displayPopupValues{}, err
		}
		values.startDirectory = &directory
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "Command", value: values.command},
		{name: "Width", value: values.width},
		{name: "Height", value: values.height},
		{name: "X", value: values.x},
		{name: "Y", value: values.y},
		{name: "Title", value: values.title},
		{name: "BorderLines", value: values.borderLines},
		{name: "Style", value: values.style},
		{name: "BorderStyle", value: values.borderStyle},
	} {
		if field.value == nil {
			continue
		}
		if err := validateServerCommandArgument(
			"display-popup", field.name, *field.value, true,
		); err != nil {
			return displayPopupValues{}, err
		}
	}

	keys := slices.Sorted(maps.Keys(request.Environment))
	values.environment = make([]string, 0, len(keys))
	for _, key := range keys {
		if err := validateEnvironmentName(key); err != nil {
			return displayPopupValues{}, err
		}
		value := request.Environment[key]
		if err := validateEnvironmentValue(value); err != nil {
			return displayPopupValues{}, err
		}
		values.environment = append(values.environment, key+"="+value)
	}
	return values, nil
}

func (values displayPopupValues) needsVersion() bool {
	return values.title != nil || values.borderLines != nil || values.style != nil ||
		values.borderStyle != nil || len(values.environment) != 0 || values.noBorder ||
		values.closeOnAnyKey || values.noKeys
}

func appendPopupVersion33Arguments(
	server Server,
	arguments []string,
	values displayPopupValues,
	current Version,
) ([]string, error) {
	for _, field := range []struct {
		feature string
		flag    string
		value   *string
	}{
		{feature: "title", flag: "-T", value: values.title},
		{feature: "border_lines", flag: "-b", value: values.borderLines},
		{feature: "style", flag: "-s", value: values.style},
		{feature: "border_style", flag: "-S", value: values.borderStyle},
	} {
		if field.value == nil {
			continue
		}
		if current.AtLeast(panePopupVersion33) {
			arguments = append(arguments, field.flag, *field.value)
		} else if err := server.unsupportedFeature(
			"display-popup", field.feature, current, panePopupVersion33,
		); err != nil {
			return nil, err
		}
	}
	if len(values.environment) != 0 {
		if current.AtLeast(panePopupVersion33) {
			for _, variable := range values.environment {
				arguments = append(arguments, "-e"+variable)
			}
		} else if err := server.unsupportedFeature(
			"display-popup", "environment", current, panePopupVersion33,
		); err != nil {
			return nil, err
		}
	}
	if values.noBorder {
		if current.AtLeast(panePopupVersion33) {
			arguments = append(arguments, "-B")
		} else if err := server.unsupportedFeature(
			"display-popup", "no_border", current, panePopupVersion33,
		); err != nil {
			return nil, err
		}
	}
	return arguments, nil
}

func appendPopupVersion36Arguments(
	server Server,
	arguments []string,
	values displayPopupValues,
	current Version,
) ([]string, error) {
	if values.closeOnAnyKey {
		if current.AtLeast(panePopupVersion36) {
			arguments = append(arguments, "-k")
		} else if err := server.unsupportedFeature(
			"display-popup", "close_on_any_key", current, panePopupVersion36,
		); err != nil {
			return nil, err
		}
	}
	if values.noKeys {
		if current.AtLeast(panePopupVersion36) {
			arguments = append(arguments, "-N")
		} else if err := server.unsupportedFeature(
			"display-popup", "no_keys", current, panePopupVersion36,
		); err != nil {
			return nil, err
		}
	}
	return arguments, nil
}
