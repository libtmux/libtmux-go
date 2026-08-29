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

// ErrNoServer identifies a command that no tmux server answered. A
// [CommandError] may match both ErrNoServer and [ErrCommand]. tmux cannot
// reliably distinguish an absent or unreachable server from one that exited
// during the command.
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
	// Binary is the executable. A Server supplies the absolute path resolved at
	// construction. A request passed directly to SubprocessRunner may leave it
	// empty to resolve "tmux" through PATH.
	Binary string
	// Arguments contains the complete tmux argv after global socket, config,
	// and color arguments are applied.
	Arguments []string
	// Environment is the child environment. A Server supplies its private
	// constructor snapshot. A request passed directly to SubprocessRunner may
	// leave it nil to inherit the current process environment.
	Environment []string
	// Directory is the child working directory. A Server supplies the absolute
	// directory frozen at construction. A request passed directly to
	// SubprocessRunner may leave it empty to inherit the current directory.
	Directory string
	// Stdio selects direct streaming. Nil requests captured output; nil files
	// within a non-nil value inherit the corresponding process stream.
	Stdio *CommandStdio
	// CommandList reports that Arguments contains tmux command-list syntax, where
	// a bare ";" separates commands. Zero means one command of literal values.
	//
	// Engines must preserve literal trailing semicolons when false and leave bare
	// separators unquoted when true; subprocess and control transports parse
	// these forms differently.
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

// commandTransportFailure leaves executable-resolution and truncated-read
// failures strict so configuration errors or partial output cannot become empty rows.
func commandTransportFailure(err error) error {
	if _, ok := errors.AsType[*exec.Error](err); ok {
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

// These pre-command diagnostics contain only socket paths and errno text, so
// secret-redacting errors may disclose them. tmux 3.2a and 3.3a use different
// socket-path wording, and tmux may exit zero after failing to create a socket.
var commandServerUnreachablePrefixes = [...]string{
	"no server running on ",
	"error connecting to ",
	"error creating ",
	"couldn't create directory ",
	"couldn't read directory ",
	"no suitable socket path",
}

// These socket-path failures put the variable path before the fixed diagnostic.
var commandServerUnreachableSuffixes = [...]string{
	" is not a directory",
	" has unsafe permissions",
}

// Fixed refusals contain no caller values and remain safe in redacted errors.
var commandFixedRefusals = [...]string{
	"no space for a new pane",
}

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

// commandServerUnreachable detects safe-to-disclose failures before a command ran.
func commandServerUnreachable(stderr []string) bool {
	for _, line := range stderr {
		for _, prefix := range commandServerUnreachablePrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		for _, suffix := range commandServerUnreachableSuffixes {
			if strings.HasSuffix(line, suffix) {
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

// These exact, locale-independent messages cover a server lost after connect.
// Treating them like connect failures avoids a race-dependent ErrNoServer result.
var commandServerGoneMessages = [...]string{
	// CLIENT_EXIT_LOST_SERVER: the connection went while the command was in
	// flight.
	"server exited unexpectedly",
	// CLIENT_EXIT_SERVER_EXITED: the server shut down and said so.
	"server exited",
}

// tmux exposes missing targets only through these words in its diagnostics.
var commandTargetNotFoundObjects = [...]string{"session", "window", "pane", "client"}

// Match the object word because terminfo and "next session" failures also begin
// with "can't find" but do not mean the requested target was absent.
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

// validateLiteralCommandArguments rejects NUL, which no process argv can carry.
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

// escapeCommandListSeparators protects final semicolons from a subprocess's
// outer tmux parser. Control mode quotes arguments and must not add this escape.
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
