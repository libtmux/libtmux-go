package tmux

import (
	"context"
	"strconv"
)

var paneInputVersion34 = Version{raw: "3.4", major: 3, minor: 4}

// SendKeysRequest configures tmux key input. Pointer values are copied before
// I/O and must not be mutated concurrently.
//
// Command must be nonnil unless Reset, Repeat, or CopyModeCommand is set. A
// nonnil empty Command is an explicit key operand and is followed by Enter
// unless SkipEnter is set. CopyModeCommand takes precedence over Command and
// suppresses Enter. HexKeys takes precedence over Literal when both are set.
type SendKeysRequest struct {
	// Command is one tmux key operand. Nil selects a flag-only invocation;
	// nonnil, including an empty string, supplies the operand.
	Command *string
	// SkipEnter prevents the separate Enter key sent after Command.
	SkipEnter bool
	// SuppressHistory prefixes Command with one space. Whether that excludes
	// the text from history depends on the application or shell in the pane.
	SuppressHistory bool
	// Literal disables tmux key-name lookup and treats Command as literal UTF-8.
	// It does not bypass interpretation by the pane's application or shell.
	Literal bool
	// Reset asks tmux to reset terminal state before processing key input.
	Reset bool
	// CopyModeCommand sends a tmux copy-mode command instead of Command. A
	// nonnil empty string remains an explicit operand.
	CopyModeCommand *string
	// Repeat repeats the key input a positive number of times. Zero omits the
	// repeat count.
	Repeat int
	// ExpandFormats asks tmux to expand format expressions in key operands.
	ExpandFormats bool
	// HexKeys interprets key operands as hexadecimal ASCII values and takes
	// precedence over Literal.
	HexKeys bool
	// TargetClient selects the client used by KeyName. Zero leaves client
	// selection to tmux; without KeyName it does not redirect pane input.
	TargetClient ClientName
	// KeyName routes keys through the selected client's key table rather than
	// delivering them to the pane.
	KeyName bool
}

// SendKeys invokes tmux send-keys with the receiver's exact linked
// session-window-pane target. This is tmux key input, not shell execution:
// Literal only changes tmux key parsing, and any delivered text may still be
// interpreted by the pane's application. KeyName routes through a client's key
// table instead of delivering to the pane.
//
// KeyName and TargetClient require tmux 3.4 and follow [UnsupportedPolicy].
//
// Completed exit status and stderr are ignored. Transport and context errors
// are delivery-ambiguous, including between Command and the separate Enter.
func (p Pane) SendKeys(ctx context.Context, request SendKeysRequest) error {
	request = captureSendKeysRequest(request)
	if err := validateSendKeysRequest(p, request); err != nil {
		return err
	}

	var current Version
	if sendKeysRequiresVersion(request) {
		var err error
		current, err = p.server.Version(ctx)
		if err != nil {
			return err
		}
	}
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	arguments, warnings, err := sendKeysArguments(target, request, current)
	if err != nil {
		return err
	}
	if err := p.server.reportUnsupported(warnings); err != nil {
		return err
	}
	if _, err := p.server.literalCmd(ctx, arguments...); err != nil {
		return err
	}
	if !sendKeysNeedsEnter(request) {
		return nil
	}
	return p.Enter(ctx)
}

// sendKeysRequiresVersion avoids a probe for requests with no gated flags.
func sendKeysRequiresVersion(request SendKeysRequest) bool {
	return request.KeyName || request.TargetClient != ""
}

// Enter is a separate command and therefore a separate [Plan] step.
func sendKeysNeedsEnter(request SendKeysRequest) bool {
	return request.CopyModeCommand == nil && request.Command != nil && !request.SkipEnter
}

// sendKeysArguments renders without I/O and returns compatibility warnings.
func sendKeysArguments(
	target string,
	request SendKeysRequest,
	version Version,
) ([]string, []Warning, error) {
	if err := validateServerCommandArgument(
		"send-keys", "Target", target, true,
	); err != nil {
		return nil, nil, err
	}
	var warnings []Warning
	arguments := []string{"send-keys", "-t", target}
	if request.Reset {
		arguments = append(arguments, "-R")
	}
	if request.ExpandFormats {
		arguments = append(arguments, "-F")
	}
	if request.HexKeys {
		arguments = append(arguments, "-H")
	}
	if request.KeyName {
		if version.AtLeast(paneInputVersion34) {
			arguments = append(arguments, "-K")
		} else {
			warnings = append(warnings, newUnsupportedFeatureWarning(
				"send-keys", "key_name", version, paneInputVersion34,
			))
		}
	}
	if request.Literal {
		arguments = append(arguments, "-l")
	}
	if request.Repeat != 0 {
		arguments = append(arguments, "-N", strconv.Itoa(request.Repeat))
	}
	if request.TargetClient != "" {
		if version.AtLeast(paneInputVersion34) {
			arguments = append(arguments, "-c", request.TargetClient.String())
		} else {
			warnings = append(warnings, newUnsupportedFeatureWarning(
				"send-keys", "target_client", version, paneInputVersion34,
			))
		}
	}

	if request.CopyModeCommand != nil {
		return append(arguments, "-X", "--", *request.CopyModeCommand), warnings, nil
	}
	if request.Command == nil {
		return arguments, warnings, nil
	}
	command := *request.Command
	if request.SuppressHistory {
		command = " " + command
	}
	return append(arguments, "--", command), warnings, nil
}

// Enter sends the Enter key to the receiver's exact linked pane. Completed
// exit status and stderr are ignored; transport errors are delivery-ambiguous.
func (p Pane) Enter(ctx context.Context) error {
	_, err := p.literalCmd(ctx, "send-keys", "--", "Enter")
	return err
}

// enterArguments renders [Pane.Enter] for a [Plan] without I/O.
func enterArguments(target string) ([]string, error) {
	if err := validateServerCommandArgument(
		"send-keys", "Target", target, true,
	); err != nil {
		return nil, err
	}
	return []string{"send-keys", "-t", target, "--", "Enter"}, nil
}

// sendPrefixArguments renders a prefix for a [Plan] without I/O.
func sendPrefixArguments(target string, key PrefixKey) ([]string, error) {
	arguments, err := targetedArguments("send-prefix", target)
	if err != nil {
		return nil, err
	}
	switch key {
	case PrefixPrimary:
		return arguments, nil
	case PrefixSecondary:
		return append(arguments, "-2"), nil
	default:
		return nil, invalidServerCommandRequest(
			"send-prefix", "Key", strconv.FormatUint(uint64(key), 10), "is unsupported",
		)
	}
}

// PrefixKey selects the primary or secondary tmux prefix key. Its zero value
// selects the primary prefix.
type PrefixKey uint8

const (
	// PrefixPrimary selects tmux's primary prefix key.
	PrefixPrimary PrefixKey = iota
	// PrefixSecondary selects tmux's secondary prefix key.
	PrefixSecondary
)

// SendPrefix sends a tmux prefix key to the receiver's exact linked pane.
// Unsupported PrefixKey values fail before execution. A completed command
// produces a [CommandError] only when tmux writes stderr; the library-created
// error retains only the exit code. Nonzero exits without stderr are ignored;
// transport errors are delivery-ambiguous.
func (p Pane) SendPrefix(ctx context.Context, key PrefixKey) error {
	if err := validateTypedTarget(
		"send-prefix", "Pane", "pane", p.paneID.String(),
	); err != nil {
		return err
	}
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	arguments, err := sendPrefixArguments(target, key)
	if err != nil {
		return err
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("send-prefix", result, err)
}

// ClearHistoryRequest configures pane history clearing. Its zero value clears
// scrollback without requesting hyperlink cleanup.
type ClearHistoryRequest struct {
	// ResetHyperlinks also removes hyperlinks. It requires tmux 3.4; see
	// UnsupportedPolicy.
	ResetHyperlinks bool
}

// ClearHistory removes scrollback from the receiver's exact linked pane.
// ResetHyperlinks triggers a version probe before mutation. A completed
// command produces a redacted [CommandError] only for stderr; nonzero exits
// without stderr are ignored. Accepted changes are not rolled back.
func (p Pane) ClearHistory(ctx context.Context, request ClearHistoryRequest) error {
	if err := validateTypedTarget(
		"clear-history", "Pane", "pane", p.paneID.String(),
	); err != nil {
		return err
	}
	var current Version
	if request.ResetHyperlinks {
		probed, err := p.server.Version(ctx)
		if err != nil {
			return err
		}
		current = probed
	}
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	arguments, warnings, err := clearHistoryArguments(target, request, current)
	if err != nil {
		return err
	}
	if err := p.server.reportUnsupported(warnings); err != nil {
		return err
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("clear-history", result, err)
}

// clearHistoryArguments renders without I/O and returns compatibility warnings.
func clearHistoryArguments(
	target string,
	request ClearHistoryRequest,
	version Version,
) ([]string, []Warning, error) {
	arguments, err := targetedArguments("clear-history", target)
	if err != nil {
		return nil, nil, err
	}
	var warnings []Warning
	if request.ResetHyperlinks {
		if version.AtLeast(paneInputVersion34) {
			arguments = append(arguments, "-H")
		} else {
			warnings = append(warnings, newUnsupportedFeatureWarning(
				"clear-history", "reset_hyperlinks", version, paneInputVersion34,
			))
		}
	}
	return arguments, warnings, nil
}

// Clear sends the text "reset" and then Enter to the receiver's exact linked
// pane. The pane's current application interprets that input; Clear does not
// invoke a shell itself. A transport error may leave the text delivered without Enter.
func (p Pane) Clear(ctx context.Context) error {
	command := "reset"
	return p.SendKeys(ctx, SendKeysRequest{Command: &command})
}

// Reset submits one tmux command list that resets terminal state and then
// clears history for the exact linked pane. The mutations are not atomic and
// may be partial. Completed exit status and stderr are ignored.
func (p Pane) Reset(ctx context.Context) error {
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	_, err = p.server.Cmd(
		ctx,
		"send-keys", "-t", target, "-R",
		";",
		"clear-history", "-t", target,
	)
	return err
}

func captureSendKeysRequest(request SendKeysRequest) SendKeysRequest {
	if request.Command != nil {
		value := *request.Command
		request.Command = &value
	}
	if request.CopyModeCommand != nil {
		value := *request.CopyModeCommand
		request.CopyModeCommand = &value
	}
	return request
}

func validateSendKeysRequest(p Pane, request SendKeysRequest) error {
	if err := validateTypedTarget(
		"send-keys", "Pane", "pane", p.paneID.String(),
	); err != nil {
		return err
	}
	arguments := []serverCommandArgument{{field: "Pane", value: p.paneID.String()}}
	if request.Command != nil {
		arguments = append(arguments, serverCommandArgument{field: "Command", value: *request.Command})
	}
	if request.CopyModeCommand != nil {
		arguments = append(
			arguments,
			serverCommandArgument{field: "CopyModeCommand", value: *request.CopyModeCommand},
		)
	}
	if request.TargetClient != "" {
		arguments = append(
			arguments,
			serverCommandArgument{field: "TargetClient", value: request.TargetClient.String()},
		)
	}
	if err := validateServerCommandArguments("send-keys", arguments...); err != nil {
		return err
	}
	if request.Repeat < 0 {
		return invalidServerCommandRequest(
			"send-keys", "Repeat", strconv.Itoa(request.Repeat), "must be positive",
		)
	}
	if request.Command == nil && request.CopyModeCommand == nil && !request.Reset && request.Repeat == 0 {
		return invalidServerCommandRequest(
			"send-keys",
			"Command",
			"",
			"must be set unless Reset, Repeat, or CopyModeCommand is set",
		)
	}
	return nil
}
