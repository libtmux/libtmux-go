package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// ErrUnknownColor identifies an unsupported tmux color mode. It is matched by
// errors.Is for [ColorError].
var ErrUnknownColor = errors.New("tmux: unknown color mode")

// ErrCommand identifies a failed high-level tmux command. It is matched by
// errors.Is for [CommandError].
var ErrCommand = errors.New("tmux: command failed")

// ErrNoServer identifies a command that no tmux server answered: either tmux
// refused it before running it, because it reached no server on the configured
// socket, or the server it reached went away before answering. It is matched by
// errors.Is for [CommandError], alongside [ErrCommand].
//
// It exists because a tmux server holding no sessions exits, so "nothing is
// running yet" is ordinary state rather than a fault, and a program that starts
// what it does not find needs to recognize it:
//
//	sessions, err := server.Sessions(ctx)
//	switch {
//	case errors.Is(err, tmux.ErrNoServer):
//		// nothing is running yet
//	case err != nil:
//		return err
//	}
//
// It classifies an error and never replaces one. A list that cannot be read
// reports the failure either way, so a socket that is unreadable, is not a
// socket, or holds a server this process may not reach is never answered with
// an empty collection.
//
// It does not separate a server that was never reached from one that has just
// gone, because a program killing a server and reading from it immediately
// produces either, depending on whether its client had connected first.
//
// It also does not separate an absent server from one that is present and
// unreachable, because tmux does not either: client.c treats ECONNREFUSED and
// ENOENT alike, prints a constant message only for the first, and renders every
// other errno through strerror, whose text follows the process locale. Matching
// that text would make the classification locale-dependent, which is worse than
// declining to draw the line. Acting on this sentinel stays safe regardless: a
// caller that creates what it did not find gets tmux's own refusal, naming the
// socket and the reason, rather than a session somewhere unintended. A caller
// that needs the distinction should inspect the socket itself.
var ErrNoServer = errors.New("tmux: no server reached")

// ColorMode selects a tmux color-capability override for [ServerOptions].
type ColorMode int

// Supported tmux color-capability overrides.
const (
	// ColorDefault preserves tmux's detected color capability.
	ColorDefault ColorMode = 0
	// Color88 requests tmux's 88-color capability mode.
	Color88 ColorMode = 88
	// Color256 requests tmux's 256-color capability mode.
	Color256 ColorMode = 256
)

// ColorError reports an unsupported color mode. It matches [ErrUnknownColor]
// through errors.Is; callers can recover Mode with errors.As.
type ColorError struct {
	// Mode is the rejected color-capability override.
	Mode ColorMode
}

// Error implements error.
func (e *ColorError) Error() string {
	return fmt.Sprintf("%v %d", ErrUnknownColor, e.Mode)
}

// Unwrap makes ColorError compatible with ErrUnknownColor.
func (e *ColorError) Unwrap() error {
	return ErrUnknownColor
}

// CommandResult contains one completed tmux invocation. [Server.Cmd] returns
// it even when tmux exits nonzero. Library operations clone its slices before
// returning it, so the caller owns Command, Stdout, RawStdout, and Stderr.
type CommandResult struct {
	// Command is the executed tmux argument vector.
	Command []string
	// Stdout contains one decoded standard-output line per element.
	Stdout []string
	// RawStdout contains exact captured standard-output bytes, including line
	// delimiters and trailing newlines. It is nil when no output was captured.
	RawStdout []byte
	// Stderr contains one decoded standard-error line per element.
	Stderr []string
	// ExitCode is tmux's completed process exit code, or -1 when no tmux
	// command ran: this package refused the request, or a transport failed
	// before tmux answered. A negative code is therefore never tmux's opinion
	// of the request, and an error carrying one has a reason of its own.
	ExitCode int
}

// CommandRequest describes one tmux process invocation passed to a
// [CommandRunner]. The runner owns Arguments and Environment and may modify
// them. Stdio retains caller-owned files; neither the runner nor the library
// closes them.
type CommandRequest struct {
	// Binary is the configured executable. Empty means the default "tmux"
	// executable should be resolved through PATH.
	Binary string
	// Arguments contains the complete tmux argv after global socket, config,
	// and color arguments are applied.
	Arguments []string
	// Environment is the configured child environment. Nil inherits the
	// current process environment.
	Environment []string
	// Directory is the child working directory. Empty inherits the current
	// process working directory.
	Directory string
	// Stdio selects direct streaming. Nil requests captured output; nil files
	// within a non-nil value inherit the corresponding process stream.
	Stdio *CommandStdio
	// CommandList reports that Arguments carries tmux command-list syntax, in
	// which a bare ";" element separates two commands. The zero value is one
	// command whose every element is a value, which is what every typed
	// operation in this package sends.
	//
	// It exists because the two transports parse in opposite directions. A tmux
	// process hands its argv to tmux's outer command parser, which reads a bare
	// ";" as a separator, so a value that ends in one is escaped before it gets
	// there. A control connection has no outer parser and quotes every argument
	// instead, so the same value needs no escape and a separator cannot be
	// written as a quoted argument at all. An engine that ignores this field
	// sends one command with every element quoted, which is correct for the
	// zero value.
	CommandList bool
}

// CommandStdio supplies caller-owned files for a streaming [CommandRequest].
type CommandStdio struct {
	// Stdin is the child standard input. Nil inherits os.Stdin.
	Stdin *os.File
	// Stdout is the child standard output. Nil inherits os.Stdout.
	Stdout *os.File
	// Stderr is the child standard error. Nil inherits os.Stderr.
	Stderr *os.File
}

// CommandRunner executes tmux process requests for a [Server]. Run may be
// called concurrently. Completed nonzero exits should remain CommandResult
// data; execution, transport, and context failures should be returned as
// errors. Returned slices are copied before they reach the caller.
type CommandRunner interface {
	// Run executes one command request.
	Run(context.Context, CommandRequest) (CommandResult, error)
}

// CommandRunnerFunc adapts a function to [CommandRunner].
type CommandRunnerFunc func(context.Context, CommandRequest) (CommandResult, error)

// Run invokes function with ctx and request.
func (function CommandRunnerFunc) Run(
	ctx context.Context,
	request CommandRequest,
) (CommandResult, error) {
	return function(ctx, request)
}

type commandTransportError struct {
	err error
}

func (e *commandTransportError) Error() string { return e.err.Error() }

func (e *commandTransportError) Unwrap() error { return e.err }

// commandTransportFailure classifies a runner failure for collection leniency.
//
// A failure to resolve the configured tmux executable is returned unwrapped.
// No process started, so there is no transport that could have failed, and the
// condition is caller configuration rather than the runtime state of a tmux
// server. Normalizing it would report a missing binary as an empty collection,
// which no caller can act on. Every other runner failure stays a transport
// failure and remains subject to leniency.
//
// Resolution failures are detected as [os/exec.Error], which os/exec returns
// both for a name absent from PATH and for an explicit path that does not
// exist. Only the first of those wraps exec.ErrNotFound.
//
// A truncated read is returned unwrapped for a sharper reason. tmux answered,
// and the answer this package holds is part of it: os/exec stops waiting for
// the output of a command that has already exited once its wait delay passes,
// closes the pipe, and keeps whatever had arrived. Leniency exists to report a
// server that said nothing as nothing; a server that said something this
// package failed to finish reading is the opposite, and normalizing it would
// turn a short listing into a confident empty one. That is the single worst
// answer available, so it is refused here.
func commandTransportFailure(err error) error {
	var executableError *exec.Error
	if errors.As(err, &executableError) {
		return err
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return err
	}
	return &commandTransportError{err: err}
}

// CommandError reports a completed failed high-level tmux operation. It matches
// [ErrCommand] through errors.Is; callers can recover its fields with errors.As.
// Generic library operations retain an owned copy of the completed command and
// output so tmux diagnostics remain available. Operations whose primary payload
// may contain secrets return an exit-code-only result instead. Callers must
// treat retained argv and output as potentially sensitive.
type CommandError struct {
	// Subcommand is the failed tmux subcommand.
	Subcommand string
	// Result is the owned completed result, or an exit-code-only redacted result
	// for a secret-bearing operation.
	Result CommandResult

	targetNotFound bool
	noServer       bool
}

// Error implements error.
func (e *CommandError) Error() string {
	detail := strings.Join(e.Result.Stderr, "\n")
	if detail == "" {
		return fmt.Sprintf("%v: %s exited %d", ErrCommand, e.Subcommand, e.Result.ExitCode)
	}
	return fmt.Sprintf("%v: %s exited %d: %s", ErrCommand, e.Subcommand, e.Result.ExitCode, detail)
}

// Unwrap makes CommandError compatible with ErrCommand.
func (e *CommandError) Unwrap() error {
	return ErrCommand
}

// Is reports [ErrNoServer] for a failure that means no tmux server answered:
// either the command never reached one, or the one it reached is gone.
func (e *CommandError) Is(target error) bool {
	return target == ErrNoServer && e.noServer
}

func newCommandError(subcommand string, result CommandResult) *CommandError {
	return &CommandError{
		Subcommand:     subcommand,
		Result:         cloneCommandResult(result),
		targetNotFound: commandTargetNotFound(result.Stderr),
		noServer:       commandServerUnreachable(result.Stderr),
	}
}

func newRedactedCommandError(subcommand string, result CommandResult) *CommandError {
	if commandServerUnreachable(result.Stderr) || commandFixedRefusal(result.Stderr) {
		return newCommandError(subcommand, result)
	}
	return &CommandError{
		Subcommand:     subcommand,
		Result:         CommandResult{ExitCode: result.ExitCode},
		targetNotFound: commandTargetNotFound(result.Stderr),
		noServer:       commandServerUnreachable(result.Stderr),
	}
}

// commandServerUnreachablePrefixes are what tmux prints when no server carried
// the command: the first two from client.c when it cannot reach one, the third
// from server.c when it cannot create the socket for one. All three are
// produced before a command runs, so they carry the caller's own socket path
// and an errno string and never an option value, an environment value, or
// buffer contents. All three are unchanged across every supported version from
// 3.2a through 3.7b.
//
// The third is why the exit code alone is not enough: tmux exits 0 after
// failing to create a socket, so without its message the failure reads as a
// command that succeeded.
var commandServerUnreachablePrefixes = [...]string{
	"no server running on ",
	"error connecting to ",
	"error creating ",
}

// commandFixedRefusals are tmux diagnostics built from a constant string with
// nothing interpolated into them, so they cannot carry a caller's value and are
// safe to disclose from an error that withholds output for secrecy.
//
// Redaction exists because a value or a command may be echoed back. A refusal
// tmux writes as a literal has nothing of the caller's in it, and withholding
// it leaves an exit code where an actionable reason would fit: a split refused
// for want of room is one a caller can answer by resizing or choosing another
// pane.
var commandFixedRefusals = [...]string{
	"no space for a new pane",
}

// commandFixedRefusal reports whether tmux refused with one of those.
func commandFixedRefusal(stderr []string) bool {
	for _, line := range stderr {
		for _, refusal := range commandFixedRefusals {
			if strings.Contains(line, refusal) {
				return true
			}
		}
	}
	return false
}

// commandServerUnreachable reports whether tmux failed before reaching a
// server. An operation that redacts its output for secrecy can still report
// this, because the failure happened before tmux had a value to disclose, and
// withholding it leaves a caller with an exit code and no way to learn that
// nothing was listening.
func commandServerUnreachable(stderr []string) bool {
	for _, line := range stderr {
		for _, prefix := range commandServerUnreachablePrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		for _, message := range commandServerGoneMessages {
			if line == message {
				return true
			}
		}
	}
	return false
}

// commandServerGoneMessages are what tmux prints when a command reached a
// server and did not get an answer from it, which client.c renders from its
// exit reason and writes to stderr verbatim for a command client.
//
// They are matched whole rather than by prefix, because "server exited" is a
// prefix of "server exited unexpectedly" and both are constants with nothing
// interpolated into them, so an exact match is both safe to disclose and
// independent of the process locale.
//
// The distinction between these and the connect failures above is real but not
// one a caller can act on differently: killing a server and reading from it
// immediately produces either, depending on whether the client had connected
// before it went. Classifying only one of them would make the answer to "is
// anything running" depend on that race.
var commandServerGoneMessages = [...]string{
	// CLIENT_EXIT_LOST_SERVER: the connection went while the command was in
	// flight.
	"server exited unexpectedly",
	// CLIENT_EXIT_SERVER_EXITED: the server shut down and said so.
	"server exited",
}

// commandTargetNotFoundObjects are the object words tmux names when target
// resolution fails. tmux exits 1 for every command error and exposes no
// distinct status for a missing target, so its message is the only available
// signal.
var commandTargetNotFoundObjects = [...]string{"session", "window", "pane", "client"}

// commandTargetNotFound reports whether tmux failed because a target did not
// resolve. tmux's cmd_find_target has emitted "can't find <object>: <target>"
// for exactly these four objects since 2015, and all four sites are unchanged
// across every supported version from 3.2a through 3.7b, so the message is
// stable even though matching it is not a typed signal.
//
// Matching the object word rather than the bare "can't find " prefix keeps
// unrelated failures out of the classification: tmux reports a missing
// terminfo database with the same prefix, and reports an exhausted session
// list as "can't find next session", neither of which means the caller's
// target was absent.
func commandTargetNotFound(stderr []string) bool {
	for _, line := range stderr {
		for _, object := range commandTargetNotFoundObjects {
			if strings.Contains(line, "can't find "+object+":") ||
				strings.HasSuffix(line, "can't find "+object) {
				return true
			}
		}
	}
	return false
}

func cloneCommandResult(result CommandResult) CommandResult {
	result.Command = slices.Clone(result.Command)
	result.Stdout = slices.Clone(result.Stdout)
	result.RawStdout = bytes.Clone(result.RawStdout)
	result.Stderr = slices.Clone(result.Stderr)
	return result
}

// validateLiteralCommandArguments rejects what cannot cross a process argv.
// It is separate from escaping because the two answer to different layers: NUL
// is impossible for either transport, while the escape below describes only
// tmux's outer command parser.
func validateLiteralCommandArguments(arguments []string) error {
	for _, argument := range arguments {
		if strings.ContainsRune(argument, '\x00') {
			return invalidServerCommandRequest(
				"command",
				"Arguments",
				"[redacted]",
				"contains NUL, which cannot cross process argv",
			)
		}
	}
	return nil
}

// escapeCommandListSeparators prepares literal arguments for tmux's outer
// command parser. One added backslash protects a final semicolon; tmux removes
// that backslash before passing nested command or shell strings to the
// subcommand that owns them.
//
// It applies to a tmux process and to nothing else. A control connection quotes
// every argument it sends, so the semicolon is already protected there and the
// added backslash would survive into the value tmux stores.
func escapeCommandListSeparators(arguments []string) []string {
	escaped := make([]string, len(arguments))
	for index, argument := range arguments {
		escaped[index] = escapeTrailingTmuxCommandSeparator(argument)
	}
	return escaped
}

// escapeTrailingTmuxCommandSeparator preserves a literal final semicolon in
// one already-split argv value. tmux otherwise consumes it as a command-list
// separator even when no shell is involved.
func escapeTrailingTmuxCommandSeparator(value string) string {
	if !strings.HasSuffix(value, ";") {
		return value
	}
	return value[:len(value)-1] + `\;`
}

type serverCommandArgument struct {
	field string
	value string
}

func validateServerCommandArguments(
	subcommand string,
	arguments ...serverCommandArgument,
) error {
	for _, argument := range arguments {
		if err := validateServerCommandArgument(
			subcommand,
			argument.field,
			argument.value,
			true,
		); err != nil {
			return err
		}
	}
	return nil
}
