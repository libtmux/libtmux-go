package tmux

import "context"

// PasteBufferRequest configures one tmux paste-buffer operation. Its zero value
// pastes the top buffer, replaces linefeeds with tmux's default carriage-return
// separator, and uses the version's default control-character handling.
// Pointer values are read and copied before any version probe or command; the
// call retains none of the caller's storage. Callers must not mutate them
// concurrently.
type PasteBufferRequest struct {
	// BufferName selects a named tmux buffer. Nil selects the top buffer;
	// nonnil empty remains an explicit buffer name.
	BufferName *string
	// DeleteAfter asks tmux to delete an existing selected buffer after
	// processing it. Deletion does not prove bytes reached a pane whose input
	// may be disabled.
	DeleteAfter bool
	// LinefeedSeparator preserves linefeeds instead of replacing them with
	// Separator or tmux's default carriage return.
	LinefeedSeparator bool
	// Bracket surrounds the paste with bracketed-paste control codes when the
	// pane application has requested bracketed paste mode.
	Bracket bool
	// Separator replaces linefeeds. Nil uses tmux's default carriage return;
	// nonnil empty removes linefeeds. LinefeedSeparator takes precedence.
	Separator *string
	// NoVis requests raw bytes on tmux 3.7 or newer. Older versions already
	// paste raw bytes; the unsupported flag is omitted with a warning.
	NoVis bool
}

// PipePaneRequest configures a pane pipe. OutputOnly, InputOnly, and Toggle may
// be combined. Command is read and copied before tmux is called; the call
// retains none of the caller's storage, and callers must not mutate it
// concurrently.
type PipePaneRequest struct {
	// Command is a shell command for tmux's pipe process. Nil closes the current
	// pipe; nonnil empty is an explicit operand that tmux also treats as stop.
	Command *string
	// OutputOnly connects pane output to the pipe command's standard input.
	// When neither direction is set, tmux uses this direction by default.
	OutputOnly bool
	// InputOnly connects the pipe command's standard output to pane input.
	InputOnly bool
	// Toggle closes an existing pipe or opens Command when no pipe exists.
	Toggle bool
}

// Respawn restarts the process in the receiver's exact linked pane on tmux
// 3.2a or newer. Command, when present, is passed as one tmux shell-command
// operand; Go neither executes it locally nor adds inner-shell quoting. Final
// semicolon protection applies only to tmux's outer command parser. See
// [RespawnRequest] for zero-value, directory, environment, and Kill behavior.
//
// Respawn refreshes by PaneID, so the returned canonical [Pane] may carry a
// different linked-session or winlink context. The process may be restarted
// before refresh fails; in that case Respawn returns the original receiver and
// the refresh error. Validation, transport, context, or completed-stderr
// failures return a zero Pane. Completed stderr produces a [CommandError] that
// retains only the exit code, while a nonzero exit without stderr is followed
// by refresh. Context errors remain detectable with [errors.Is] but cannot
// revoke a respawn already accepted by tmux.
func (p Pane) Respawn(ctx context.Context, request RespawnRequest) (Pane, error) {
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	arguments, err := respawnArguments("respawn-pane", target, request)
	if err != nil {
		return Pane{}, err
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	if err != nil {
		return Pane{}, err
	}
	if len(result.Stderr) != 0 {
		return Pane{}, newRedactedCommandError("respawn-pane", result)
	}
	refreshed, err := p.Refresh(ctx)
	if err != nil {
		return p, err
	}
	return refreshed, nil
}

// pipePaneArguments renders one pipe-pane argument vector. It performs no I/O,
// so a [Plan] can render a pipe it has not opened.
func pipePaneArguments(target string, request PipePaneRequest) ([]string, error) {
	if request.Command != nil {
		if err := validateServerCommandArgument(
			"pipe-pane", "Command", *request.Command, true,
		); err != nil {
			return nil, err
		}
	}
	arguments, err := targetedArguments("pipe-pane", target)
	if err != nil {
		return nil, err
	}
	if request.OutputOnly {
		arguments = append(arguments, "-O")
	}
	if request.InputOnly {
		arguments = append(arguments, "-I")
	}
	if request.Toggle {
		arguments = append(arguments, "-o")
	}
	if request.Command != nil {
		arguments = append(arguments, *request.Command)
	}
	return arguments, nil
}

// respawnArguments renders one respawn-pane or respawn-window argument vector.
// It performs no I/O, so a [Plan] can render a respawn it has not run.
func respawnArguments(
	command string,
	target string,
	request RespawnRequest,
) ([]string, error) {
	options, err := respawnRequestArguments(command, request)
	if err != nil {
		return nil, err
	}
	arguments, err := targetedArguments(command, target)
	if err != nil {
		return nil, err
	}
	return append(arguments, options...), nil
}

// PasteBuffer pastes a named or top buffer into the receiver's exact linked
// pane. DeleteAfter requests deletion after tmux processes an existing buffer;
// an exited target or missing named buffer fails before deletion, while
// deletion after disabled pane input does not prove byte delivery.
//
// NoVis requires tmux 3.7. Older versions already paste raw bytes, so the flag
// is omitted with a synchronous unsupported-feature warning. A version-probe
// error stops execution. A completed command produces a [CommandError] only
// when tmux writes stderr; the library-created error retains only the exit
// code. A nonzero exit without stderr is ignored. Transport and context errors
// remain detectable with [errors.Is], but accepted paste or deletion effects
// are not rolled back.
func (p Pane) PasteBuffer(ctx context.Context, request PasteBufferRequest) error {
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	bufferName := copyOptionalString(request.BufferName)
	separator := copyOptionalString(request.Separator)
	if err := validateBufferName("paste-buffer", bufferName); err != nil {
		return err
	}
	if separator != nil {
		if err := validateServerCommandArgument(
			"paste-buffer", "Separator", *separator, true,
		); err != nil {
			return err
		}
	}

	noVis := false
	if request.NoVis {
		version, err := p.server.Version(ctx)
		if err != nil {
			return err
		}
		if version.AtLeast(serverExecVersion37) {
			noVis = true
		} else {
			p.server.warn(newUnsupportedFeatureWarning(
				"paste-buffer", "no_vis", version, serverExecVersion37,
			))
		}
	}

	arguments := []string{"paste-buffer", "-t", target}
	if request.DeleteAfter {
		arguments = append(arguments, "-d")
	}
	if request.LinefeedSeparator {
		arguments = append(arguments, "-r")
	}
	if request.Bracket {
		arguments = append(arguments, "-p")
	}
	if bufferName != nil {
		arguments = append(arguments, "-b", *bufferName)
	}
	if separator != nil {
		arguments = append(arguments, "-s", *separator)
	}
	if noVis {
		arguments = append(arguments, "-S")
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("paste-buffer", result, err)
}

func copyOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// Pipe starts, toggles, or stops piping for the receiver's exact linked pane.
// tmux expands formats and time formats in Command before running it with
// sh -c. The library's protection for a final semicolon applies only to tmux's
// outer command parser; it does not quote or neutralize the child shell.
//
// A nil Command, and an explicit empty Command under tmux semantics, stops the
// current pipe. Successful return means tmux installed, toggled, or stopped the
// pipe; it does not report child-shell success or pipe lifetime. A completed
// invocation produces a [CommandError] only when tmux writes stderr; the
// library-created error retains only the exit code. A nonzero exit without
// stderr is ignored. Transport and context errors remain detectable with
// [errors.Is], but an accepted pipe change is not rolled back.
func (p Pane) Pipe(ctx context.Context, request PipePaneRequest) error {
	target, err := exactPaneTarget(p)
	if err != nil {
		return err
	}
	arguments, err := pipePaneArguments(target, request)
	if err != nil {
		return err
	}
	result, err := p.server.literalCmd(ctx, arguments...)
	if err != nil {
		return err
	}
	if len(result.Stderr) != 0 {
		return newRedactedCommandError("pipe-pane", result)
	}
	return nil
}
