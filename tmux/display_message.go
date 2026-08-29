package tmux

import (
	"context"
	"strconv"
	"strings"
)

var (
	displayMessageVersion34 = Version{raw: "3.4", major: 3, minor: 4}
	displayMessageVersion36 = Version{raw: "3.6", major: 3, minor: 6}
)

// DisplayMessageRequest configures display-message options shared by server,
// window, and pane scope. A zero request displays tmux's default message and
// returns nil. Print returns an owned stdout slice instead of displaying only
// to a client status line. Nil pointer fields omit their flags while pointers
// to empty values are explicit; a zero TargetClient omits its flag.
type DisplayMessageRequest struct {
	// Message is the optional status message; an empty message leaves tmux's default behavior.
	Message string
	// Print returns message output instead of only displaying it.
	Print bool
	// Format selects tmux output formatting.
	Format *string
	// AllFormats includes every available format value.
	AllFormats bool
	// Verbose requests verbose tmux output.
	Verbose bool
	// NoExpand disables format expansion and requires tmux 3.4; see
	// UnsupportedPolicy.
	NoExpand bool
	// TargetClient selects the stable client receiving the display; zero omits -c.
	TargetClient ClientName
	// Delay sets display duration in milliseconds when nonnil.
	Delay *int
	// Notify triggers a notification rather than only a status message.
	Notify bool
}

// PaneDisplayMessageRequest adds the pane-only update behavior to the common
// display-message options.
type PaneDisplayMessageRequest struct {
	// DisplayMessageRequest supplies the shared display options.
	DisplayMessageRequest
	// UpdatePane updates pane state and requires tmux 3.6; see
	// UnsupportedPolicy.
	UpdatePane bool
}

type displayMessageValues struct {
	message      string
	format       string
	targetClient string
	delay        int
	print        bool
	allFormats   bool
	verbose      bool
	noExpand     bool
	notify       bool
	hasFormat    bool
	hasClient    bool
	hasDelay     bool
}

// DisplayMessage displays or prints a server-scoped tmux message. Print
// returns an owned stdout slice even when tmux exits nonzero; completed stderr
// is delivered synchronously through [WarningCommandStderr], not returned as a
// completed-error classification. Unsupported flags follow [UnsupportedPolicy].
// Any warnings run on the caller goroutine before return. Cancellation does not
// prove display did not occur.
func (s Server) DisplayMessage(
	ctx context.Context,
	request DisplayMessageRequest,
) ([]string, error) {
	values, err := captureDisplayMessageRequest(request)
	if err != nil {
		return nil, err
	}
	return runDisplayMessage(ctx, s, "", values, false)
}

// DisplayMessage displays or prints a message at this window's exact target.
// Print returns an owned stdout slice even on a completed nonzero exit;
// completed stderr reaches [WarningHandler] as [WarningCommandStderr]. NoExpand
// follows [UnsupportedPolicy]. Warnings run synchronously before return; context
// cancellation does not prove display did not occur.
func (w Window) DisplayMessage(
	ctx context.Context,
	request DisplayMessageRequest,
) ([]string, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return nil, err
	}
	values, err := captureDisplayMessageRequest(request)
	if err != nil {
		return nil, err
	}
	return runDisplayMessage(ctx, w.server, target, values, false)
}

// DisplayMessage displays or prints a message at this pane's exact target.
// Embedded NoExpand requires tmux 3.4 and UpdatePane requires tmux 3.6; each
// follows [UnsupportedPolicy]. Print returns an owned stdout slice even on a
// completed nonzero exit. Completed stderr synchronously reaches
// [WarningHandler] as [WarningCommandStderr], not an error; cancellation does
// not prove display or pane update did not occur.
func (p Pane) DisplayMessage(
	ctx context.Context,
	request PaneDisplayMessageRequest,
) ([]string, error) {
	target, err := exactPaneTarget(p)
	if err != nil {
		return nil, err
	}
	values, err := captureDisplayMessageRequest(request.DisplayMessageRequest)
	if err != nil {
		return nil, err
	}
	return runDisplayMessage(ctx, p.server, target, values, request.UpdatePane)
}

func captureDisplayMessageRequest(
	request DisplayMessageRequest,
) (displayMessageValues, error) {
	if err := validateServerCommandArgument(
		"display-message", "Message", request.Message, true,
	); err != nil {
		return displayMessageValues{}, err
	}
	values := displayMessageValues{
		message:    request.Message,
		print:      request.Print,
		allFormats: request.AllFormats,
		verbose:    request.Verbose,
		noExpand:   request.NoExpand,
		notify:     request.Notify,
	}
	if request.Format != nil {
		if err := validateServerCommandArgument(
			"display-message", "Format", *request.Format, true,
		); err != nil {
			return displayMessageValues{}, err
		}
		values.format = *request.Format
		values.hasFormat = true
	}
	if request.TargetClient != "" {
		values.targetClient = request.TargetClient.String()
		if err := validateServerCommandArgument(
			"display-message", "TargetClient", values.targetClient, true,
		); err != nil {
			return displayMessageValues{}, err
		}
		if err := validateTypedTarget(
			"display-message", "TargetClient", "client", values.targetClient,
		); err != nil {
			return displayMessageValues{}, err
		}
		values.hasClient = true
	}
	if request.Delay != nil {
		values.delay = *request.Delay
		values.hasDelay = true
	}
	return values, nil
}

func runDisplayMessage(
	ctx context.Context,
	server Server,
	target string,
	request displayMessageValues,
	updatePane bool,
) ([]string, error) {
	var current Version
	if request.noExpand || updatePane {
		var err error
		current, err = server.Version(ctx)
		if err != nil {
			return nil, err
		}
	}

	arguments := []string{"display-message"}
	if target != "" {
		arguments = append(arguments, "-t", target)
	}
	if request.print {
		arguments = append(arguments, "-p")
	}
	if request.allFormats {
		arguments = append(arguments, "-a")
	}
	if request.verbose {
		arguments = append(arguments, "-v")
	}
	if request.noExpand {
		if current.AtLeast(displayMessageVersion34) {
			arguments = append(arguments, "-l")
		} else if err := server.unsupportedFeature(
			"display-message",
			"no_expand",
			current,
			displayMessageVersion34,
		); err != nil {
			return nil, err
		}
	}
	if request.notify {
		arguments = append(arguments, "-N")
	}
	if updatePane {
		if current.AtLeast(displayMessageVersion36) {
			arguments = append(arguments, "-C")
		} else if err := server.unsupportedFeature(
			"display-message",
			"update_pane",
			current,
			displayMessageVersion36,
		); err != nil {
			return nil, err
		}
	}
	if request.hasClient {
		arguments = append(arguments, "-c", request.targetClient)
	}
	if request.hasDelay {
		arguments = append(arguments, "-d", strconv.Itoa(request.delay))
	}
	if request.hasFormat {
		arguments = append(arguments, "-F", request.format)
	}
	if request.message != "" {
		if strings.HasPrefix(request.message, "-") {
			arguments = append(arguments, "--")
		}
		arguments = append(arguments, request.message)
	}

	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if len(result.Stderr) != 0 {
		server.warn(newCommandStderrWarning("display-message", result.Stderr))
	}
	if !request.print {
		return nil, nil
	}
	output := make([]string, len(result.Stdout))
	copy(output, result.Stdout)
	return output, nil
}
