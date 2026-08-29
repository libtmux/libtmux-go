package tmux

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// AttachSessionOptions configures terminal ownership and attach behavior
// shared by server- and session-scoped attachment. Nil StartDirectory omits
// its flag and a nonnil empty string is explicit; ClientFlags is copied. The
// Stdin, Stdout, and Stderr pointers are retained for the attach call and are
// never owned or closed by this package.
type AttachSessionOptions struct {
	// DetachOthers disconnects other clients attached to the target session.
	DetachOthers bool
	// DetachParent detaches the invoking client from its parent session first.
	DetachParent bool
	// NoUpdateEnvironment preserves the invoking client's environment.
	NoUpdateEnvironment bool
	// ReadOnly attaches the client with read-only permissions.
	ReadOnly bool
	// StartDirectory selects the attached client's initial directory.
	StartDirectory *string
	// ClientFlags are comma-joined client flags copied before attachment starts.
	ClientFlags []string
	// Stdin is the attached tmux client's input stream; nil inherits process stdin.
	Stdin *os.File
	// Stdout is the attached tmux client's output stream; nil inherits process stdout.
	Stdout *os.File
	// Stderr is the attached tmux client's error stream; nil inherits process stderr.
	Stderr *os.File
}

// AttachSessionRequest selects a session name or tmux target pattern and
// configures a blocking terminal attachment. An empty Target lets tmux choose.
type AttachSessionRequest struct {
	// Target is a session name or tmux target pattern; empty leaves selection to tmux.
	Target string
	// AttachSessionOptions supplies terminal streams and attach flags.
	AttachSessionOptions
}

type attachSessionValues struct {
	target              string
	startDirectory      string
	clientFlags         []string
	stdin               *os.File
	stdout              *os.File
	stderr              *os.File
	detachOthers        bool
	detachParent        bool
	noUpdateEnvironment bool
	readOnly            bool
	hasStartDirectory   bool
}

// Start starts the configured tmux server. It is idempotent when that
// server is already running. Completed stderr is an error; cancellation does
// not prove that a daemon was not started.
//
// With tmux's exit-empty default, a server holding no sessions may exit before
// the next command even though Start succeeded. Create a session or disable
// exit-empty in [ServerOptions.ConfigFile] before startup. Use [Server.IsAlive]
// or [Server.RaiseIfDead] to check it.
func (s Server) Start(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "start-server")
	return requireServerCommandNoStderr("start-server", result, err)
}

// AttachSession attaches the caller's terminal to a matching session and
// blocks until detach or context cancellation. Nil standard streams inherit
// the process streams; each stream must be a concrete terminal descriptor.
// The call retains caller-supplied files while attached and never owns or
// closes them. A completed nonzero exit returns [CommandError]; cancellation
// can end waiting but does not prove that attachment or detachment did not occur.
func (s Server) AttachSession(ctx context.Context, request AttachSessionRequest) error {
	values, err := captureAttachSessionRequest(request.Target, request.AttachSessionOptions)
	if err != nil {
		return err
	}
	return s.attachSession(ctx, values)
}

// Attach attaches the caller's terminal to this session's stable identifier
// and blocks until detach or context cancellation. It validates the receiver's
// stable SessionID, retains but never closes caller-supplied streams, and
// returns [CommandError] for a completed nonzero exit. Cancellation does not
// prove attachment or detachment did not occur.
func (s Session) Attach(ctx context.Context, options AttachSessionOptions) error {
	target := s.sessionID.String()
	if err := validateTypedTarget(
		"attach-session", "Target", "session", target,
	); err != nil {
		return err
	}
	values, err := captureAttachSessionRequest(target, options)
	if err != nil {
		return err
	}
	return s.server.attachSession(ctx, values)
}

func (s Server) attachSession(ctx context.Context, values attachSessionValues) error {
	arguments := []string{"attach-session"}
	if values.detachOthers {
		arguments = append(arguments, "-d")
	}
	if values.detachParent {
		arguments = append(arguments, "-x")
	}
	if values.noUpdateEnvironment {
		arguments = append(arguments, "-E")
	}
	if values.readOnly {
		arguments = append(arguments, "-r")
	}
	if values.hasStartDirectory {
		arguments = append(arguments, "-c", values.startDirectory)
	}
	if len(values.clientFlags) != 0 {
		arguments = append(arguments, "-f", strings.Join(values.clientFlags, ","))
	}
	if values.target != "" {
		arguments = append(arguments, "-t", values.target)
	}
	result, err := s.streamingLiteralCmd(
		ctx,
		tmuxcmd.Stdio{Stdin: values.stdin, Stdout: values.stdout, Stderr: values.stderr},
		arguments...,
	)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newRedactedCommandError("attach-session", result)
	}
	return nil
}

func (s Server) streamingLiteralCmd(
	ctx context.Context,
	stdio tmuxcmd.Stdio,
	args ...string,
) (CommandResult, error) {
	if err := validateLiteralCommandArguments(args); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	result, err := s.runCommand(ctx, commandProcess, args, &stdio, false)
	commandResult := CommandResult{
		Command:  slices.Clone(result.Command),
		ExitCode: result.ExitCode,
	}
	if err != nil {
		return commandResult, commandTransportFailure(err)
	}
	return commandResult, nil
}

func captureAttachSessionRequest(
	target string,
	options AttachSessionOptions,
) (attachSessionValues, error) {
	values := attachSessionValues{
		target:              target,
		clientFlags:         slices.Clone(options.ClientFlags),
		stdin:               options.Stdin,
		stdout:              options.Stdout,
		stderr:              options.Stderr,
		detachOthers:        options.DetachOthers,
		detachParent:        options.DetachParent,
		noUpdateEnvironment: options.NoUpdateEnvironment,
		readOnly:            options.ReadOnly,
	}
	if err := validateServerCommandArgument(
		"attach-session", "Target", values.target, true,
	); err != nil {
		return attachSessionValues{}, err
	}
	if options.StartDirectory != nil {
		values.startDirectory = *options.StartDirectory
		values.hasStartDirectory = true
		if err := validateServerCommandArgument(
			"attach-session", "StartDirectory", values.startDirectory, true,
		); err != nil {
			return attachSessionValues{}, err
		}
	}
	for index, flag := range values.clientFlags {
		if err := validateServerCommandArgument(
			"attach-session",
			"ClientFlags["+strconv.Itoa(index)+"]",
			flag,
			true,
		); err != nil {
			return attachSessionValues{}, err
		}
	}
	return values, nil
}
