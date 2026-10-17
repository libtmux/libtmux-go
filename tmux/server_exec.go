package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var (
	// ErrInvalidServerCommandRequest identifies a server command request that
	// cannot be represented safely as tmux arguments.
	ErrInvalidServerCommandRequest = errors.New("tmux: invalid server command request")

	serverExecVersion34 = Version{raw: "3.4", major: 3, minor: 4}
	serverExecVersion36 = Version{raw: "3.6", major: 3, minor: 6}
	serverExecVersion37 = Version{raw: "3.7", major: 3, minor: 7}
)

// ServerCommandRequestError reports a locally rejected server command request
// field. Callers can use [errors.Is] with [ErrInvalidServerCommandRequest] or
// [errors.As] to inspect it. Value is redacted only when that field's
// validator requests redaction.
type ServerCommandRequestError struct {
	// Subcommand is the tmux command whose request was rejected.
	Subcommand string
	// Field is the request field that was rejected.
	Field string
	// Value is the rejected value, or "[redacted]" for a secret-sensitive field.
	Value string
	// Reason describes the validation failure without changing the sentinel error.
	Reason string
}

// Error implements error.
func (e *ServerCommandRequestError) Error() string {
	return fmt.Sprintf(
		"%v: %s %s %q %s",
		ErrInvalidServerCommandRequest,
		e.Subcommand,
		e.Field,
		e.Value,
		e.Reason,
	)
}

// Unwrap makes ServerCommandRequestError compatible with
// ErrInvalidServerCommandRequest.
func (e *ServerCommandRequestError) Unwrap() error {
	return ErrInvalidServerCommandRequest
}

// RunShellRequest configures a command executed by tmux's run-shell command.
// Its zero value is invalid because Command is required. Pointer fields
// distinguish omitted flags from explicit empty arguments; RunShell copies
// Delay, StartDirectory, and Args before invoking tmux.
type RunShellRequest struct {
	// Command is the required shell or tmux command to run.
	Command string
	// Background returns after tmux accepts the job instead of waiting for stdout.
	Background bool
	// Delay supplies run-shell's optional delay; nil omits it and an empty value is explicit.
	Delay *string
	// AsTmuxCommand asks tmux to interpret Command as tmux command syntax.
	AsTmuxCommand bool
	// TargetPane selects a stable pane target; its zero value leaves target selection to tmux.
	TargetPane PaneID
	// StartDirectory selects the job directory; nil omits it. It requires tmux
	// 3.4; see UnsupportedPolicy.
	// A directory that does not exist is not an error: tmux starts the
	// process in the home directory instead, or in / without one.
	StartDirectory *string
	// ShowStderr requests job stderr and requires tmux 3.6; see
	// UnsupportedPolicy.
	ShowStderr bool
	// Args are extra job arguments copied before tmux runs. They require tmux
	// 3.7; see UnsupportedPolicy.
	Args []string
}

// WaitForMode selects one wait-for operation. The zero value waits until the
// channel is signaled.
type WaitForMode uint8

// Supported wait-for operations.
const (
	// WaitForModeWait waits until another client signals Channel.
	WaitForModeWait WaitForMode = iota
	// WaitForModeSignal signals Channel and wakes waiters.
	WaitForModeSignal
	// WaitForModeLock acquires Channel's tmux mutex, queuing behind another
	// locker if it is already held.
	//
	// Before tmux 3.8, a cancelled or timed-out ctx while queued costs Channel
	// one unlock: tmux hands the mutex to the next queued client whether or
	// not it is still there, and the one that has gone cannot pass it on
	// (cmd-wait-for.c cmd_wait_for_unlock). Unlocking again hands it to the
	// next real locker, so a channel is not lost, but a program that abandons
	// lock waits and counts its unlocks will deadlock on its own arithmetic.
	// tmux 3.8 drops the wait of a client that has gone (tmux issue 5614), so
	// there an abandoned wait costs nothing and an extra unlock fails.
	WaitForModeLock
	// WaitForModeUnlock releases Channel's tmux mutex.
	WaitForModeUnlock
)

// WaitForRequest configures a wait, signal, lock, or unlock operation. Its
// zero value waits on the required nonempty Channel.
type WaitForRequest struct {
	// Channel names the tmux wait-for channel or mutex.
	Channel string
	// Mode selects the channel action; zero waits.
	Mode WaitForMode
}

// IfShellRequest configures conditional execution of a tmux command. Its zero
// value is invalid because ShellCommand and ThenCommand are required; nil
// ElseCommand omits the failure branch while a pointer to an empty string is
// explicit.
type IfShellRequest struct {
	// ShellCommand is the required shell condition executed by tmux.
	ShellCommand string
	// ThenCommand is the required tmux command executed when ShellCommand succeeds.
	ThenCommand string
	// ElseCommand is the optional tmux command executed when ShellCommand fails.
	ElseCommand *string
	// Background makes tmux schedule the conditional command without waiting.
	Background bool
	// TargetPane selects a stable pane target; its zero value omits -t.
	TargetPane PaneID
}

// SourceFileRequest configures loading or parsing a tmux configuration file.
// Its zero value is invalid because Path is required; SourceFile expands only
// the current user's ~ forms and copies no caller-owned mutable input.
type SourceFileRequest struct {
	// Path is the required configuration path; exact "-" is rejected because no stdin is provided.
	Path string
	// Quiet suppresses tmux diagnostics for missing source files.
	Quiet bool
	// ParseOnly validates the file without applying its commands.
	ParseOnly bool
	// Verbose requests tmux parsing diagnostics.
	Verbose bool
}

// RunShell executes a shell command through tmux and returns an owned stdout
// slice. Captured foreground shell jobs require tmux 3.5 before execution;
// older releases deliver that output to no client. A background request
// returns nil after tmux accepts the job. TargetPane delivers output to that
// pane on older supported daemons. AsTmuxCommand uses native tmux command
// semantics rather than promising captured shell-job output.
// Unsupported optional flags follow [UnsupportedPolicy]. A completed stderr
// result is an error; context cancellation can be observed with [errors.Is] but
// does not establish that tmux did not start the job.
//
// On a [Connection]-bound server a foreground request runs on a control client
// opened for that call and closed when it ends, as [Server.WaitFor] does, so a
// slow or canceled command leaves every lane free. tmux sends that client the
// command's output as lines outside any reply frame, so the client takes every
// line before its own fence as output, whatever the line looks like. The one
// exception is a line that is a well-formed control notification, such as
// "%window-add @1", which a connection cannot tell from tmux's own and drops.
//
// A foreground command that tmux ends with success is checked against the
// server, as in [Server.WaitFor], and returns an error matching [ErrNoServer]
// when the server exited under it.
func (s Server) RunShell(ctx context.Context, request RunShellRequest) ([]string, error) {
	request.Delay = copyOptionalString(request.Delay)
	request.StartDirectory = copyOptionalString(request.StartDirectory)
	request.Args = slices.Clone(request.Args)
	if err := validateRunShellArguments(request); err != nil {
		return nil, err
	}
	if request.Command == "" {
		return nil, invalidServerCommandRequest("run-shell", "Command", "", "must not be empty")
	}
	targetPane := request.TargetPane.String()
	if targetPane != "" {
		if err := validateTypedTarget(
			"run-shell", "TargetPane", "pane", targetPane,
		); err != nil {
			return nil, err
		}
	}

	if !request.Background && !request.AsTmuxCommand && request.TargetPane == "" {
		s.requiredVersion = capturedShellMinimumVersion
	}
	checked, err := s.checkCore(ctx, false)
	if err != nil {
		return nil, err
	}
	s = checked
	extraArguments := request.Args
	var current Version
	if request.StartDirectory != nil || request.ShowStderr || len(extraArguments) != 0 {
		var err error
		current, err = s.Version(ctx)
		if err != nil {
			return nil, err
		}
	}

	arguments := make([]string, 0, 13+len(extraArguments))
	arguments = append(arguments, "run-shell")
	if request.Background {
		arguments = append(arguments, "-b")
	}
	if request.Delay != nil {
		arguments = append(arguments, "-d", *request.Delay)
	}
	if request.AsTmuxCommand {
		arguments = append(arguments, "-C")
	}
	if targetPane != "" {
		arguments = append(arguments, "-t", targetPane)
	}
	if request.StartDirectory != nil {
		if current.AtLeast(serverExecVersion34) {
			arguments = append(arguments, "-c", *request.StartDirectory)
		} else if err := s.unsupportedFeature(
			"run-shell",
			"start_directory",
			current,
			serverExecVersion34,
		); err != nil {
			return nil, err
		}
	}
	if request.ShowStderr {
		if current.AtLeast(serverExecVersion36) {
			arguments = append(arguments, "-E")
		} else if err := s.unsupportedFeature(
			"run-shell",
			"show_stderr",
			current,
			serverExecVersion36,
		); err != nil {
			return nil, err
		}
	}
	arguments = append(arguments, "--", request.Command)
	if len(extraArguments) != 0 {
		if current.AtLeast(serverExecVersion37) {
			arguments = append(arguments, extraArguments...)
		} else if err := s.unsupportedFeature(
			"run-shell",
			"args",
			current,
			serverExecVersion37,
		); err != nil {
			return nil, err
		}
	}

	// Without -C, tmux prints the shell command's stdout; with it, tmux runs
	// that output as commands, which answer in frames of their own.
	result, err := s.literalCmdAs(
		ctx, shellCommandKind(request.Background, !request.AsTmuxCommand), arguments...,
	)
	err = s.blockingFailure(ctx, "run-shell", err)
	if err := requireRedactedServerCommandNoStderr("run-shell", result, err); err != nil {
		return nil, err
	}
	if request.Background {
		return nil, nil
	}
	if err := s.requireServerSurvived(ctx, "run-shell"); err != nil {
		return nil, err
	}
	return result.Stdout, nil
}

// WaitFor waits for, signals, locks, or unlocks a named tmux channel. It
// changes only that server-side channel state; cancellation can interrupt the
// client wait but cannot prove a preceding signal or lock did not take
// effect. See [WaitForModeLock]: before tmux 3.8, abandoning a lock wait costs
// that channel one unlock.
//
// On a [Connection]-bound server a wait or lock runs on a control client opened
// for that call and closed when it ends, so canceling it, or a peer never
// signaling, leaves every lane free. Closing the connection ends a parked call
// with [ErrControlClosed].
//
// tmux ends a waiting client with success when the server exits, which reads
// as a signal. A wait or lock that tmux ends that way is checked against the
// server and returns an error matching [ErrNoServer] when the server is gone.
// A signal followed at once by the server exiting reads the same way.
func (s Server) WaitFor(ctx context.Context, request WaitForRequest) error {
	if err := validateServerCommandArgument(
		"wait-for", "Channel", request.Channel, true,
	); err != nil {
		return err
	}
	if request.Channel == "" {
		return invalidServerCommandRequest("wait-for", "Channel", "", "must not be empty")
	}
	arguments := []string{"wait-for"}
	switch request.Mode {
	case WaitForModeWait:
	case WaitForModeSignal:
		arguments = append(arguments, "-S")
	case WaitForModeLock:
		arguments = append(arguments, "-L")
	case WaitForModeUnlock:
		arguments = append(arguments, "-U")
	default:
		return invalidServerCommandRequest(
			"wait-for",
			"Mode",
			strconv.FormatUint(uint64(request.Mode), 10),
			"is unsupported",
		)
	}
	arguments = append(arguments, "--", request.Channel)
	// A wait or lock can hold its client until another client acts, and tmux
	// cannot cancel a command already sent, so a connection runs it on a
	// client of its own rather than on a shared lane.
	kind := commandServer
	if request.Mode == WaitForModeWait || request.Mode == WaitForModeLock {
		kind = commandBlocking
	}
	result, err := s.literalCmdAs(ctx, kind, arguments...)
	err = s.blockingFailure(ctx, "wait-for", err)
	if err := requireRedactedServerCommandNoStderr("wait-for", result, err); err != nil {
		return err
	}
	if kind == commandBlocking {
		return s.requireServerSurvived(ctx, "wait-for")
	}
	return nil
}

// requireServerSurvived follows a blocking command tmux ended with success.
// tmux's client exits 0 when the server exits under it, with no output that
// tells that from a real completion, so only the server can say. It returns an
// error matching [ErrNoServer] when the server is gone; a failed look is no
// evidence either way, and the command itself did complete.
func (s Server) requireServerSurvived(ctx context.Context, subcommand string) error {
	if s.serverGone(ctx) {
		return fmt.Errorf("%s: the tmux server exited during the command: %w", subcommand, ErrNoServer)
	}
	return nil
}

// blockingFailure follows a blocking command that failed on a connection's
// client. The server exiting ends that client's stream, which reads as a lost
// client rather than a gone server, so a lost client is checked against the
// server and reports [ErrNoServer] when it is gone. Any other failure,
// including the caller's own cancellation, passes through.
func (s Server) blockingFailure(ctx context.Context, subcommand string, err error) error {
	if err == nil || s.connection == nil || ctx.Err() != nil ||
		(!errors.Is(err, ErrControlClosed) && !errors.Is(err, ErrOutcomeUnknown)) {
		return err
	}
	if !s.serverGone(ctx) {
		return err
	}
	return fmt.Errorf("%s: the tmux server exited during the command: %w (%w)", subcommand, ErrNoServer, err)
}

// serverGone reports whether the server answers that it is gone. A bound
// server asks through its connection first; once the server has exited that
// connection fails with something other than [ErrNoServer], so a failed look
// asks again through a process of its own. A look that fails both ways is no
// evidence, which keeps a Connection closed while the server is healthy from
// reading as a gone server.
func (s Server) serverGone(ctx context.Context) bool {
	alive, err := s.IsAlive(ctx)
	if (err == nil && !alive) || errors.Is(err, ErrNoServer) {
		return true
	}
	if err == nil || s.connection == nil || ctx.Err() != nil {
		return false
	}
	process := s
	process.connection = nil
	alive, err = process.IsAlive(ctx)
	return (err == nil && !alive) || errors.Is(err, ErrNoServer) || errors.Is(err, ErrDaemonReplaced)
}

// IfShell executes ThenCommand when ShellCommand succeeds and ElseCommand, if
// present, when it fails. A completed stderr result is an error; cancellation
// can interrupt delivery without proving that tmux did not execute a branch.
//
// On a [Connection]-bound server a foreground request runs on a control client
// of its own, as [Server.RunShell] does, and a server that exited under it is
// reported the same way.
func (s Server) IfShell(ctx context.Context, request IfShellRequest) error {
	if err := validateIfShellArguments(request); err != nil {
		return err
	}
	if request.ShellCommand == "" {
		return invalidServerCommandRequest("if-shell", "ShellCommand", "", "must not be empty")
	}
	if request.ThenCommand == "" {
		return invalidServerCommandRequest("if-shell", "ThenCommand", "", "must not be empty")
	}
	targetPane := request.TargetPane.String()
	if targetPane != "" {
		if err := validateTypedTarget(
			"if-shell", "TargetPane", "pane", targetPane,
		); err != nil {
			return err
		}
	}
	arguments := []string{"if-shell"}
	if request.Background {
		arguments = append(arguments, "-b")
	}
	if targetPane != "" {
		arguments = append(arguments, "-t", targetPane)
	}
	arguments = append(arguments, "--", request.ShellCommand, request.ThenCommand)
	if request.ElseCommand != nil {
		arguments = append(arguments, *request.ElseCommand)
	}
	result, err := s.literalCmdAs(ctx, shellCommandKind(request.Background, false), arguments...)
	err = s.blockingFailure(ctx, "if-shell", err)
	if err := requireRedactedServerCommandNoStderr("if-shell", result, err); err != nil {
		return err
	}
	if !request.Background {
		return s.requireServerSurvived(ctx, "if-shell")
	}
	return nil
}

// shellCommandKind says how run-shell and if-shell occupy a client. tmux
// holds a client's queue through a foreground one until its shell command has
// finished, so it can hold a client as long as that command runs; a background
// one is answered at once. prints says the command's stdout reaches a control
// client as lines after the reply.
func shellCommandKind(background, prints bool) commandKind {
	switch {
	case background:
		return commandServer
	case prints:
		return commandBlockingOutput
	}
	return commandBlocking
}

// SourceFile loads or parses one tmux configuration file. The path expands ~
// and ~/... for the current user and normalizes redundant separators and dot
// components without collapsing parent components. Named-user forms are
// rejected. Exact "-" is rejected because it requires process stdin; a
// relative path naming a file called "-" remains file data. It can mutate
// tmux configuration; completed stderr is an error, and cancellation does not
// prove that parsing or sourcing did not occur.
func (s Server) SourceFile(ctx context.Context, request SourceFileRequest) error {
	arguments, err := sourceFileArguments(request)
	if err != nil {
		return err
	}
	result, err := s.literalCmd(ctx, arguments...)
	return requireRedactedServerCommandNoStderr("source-file", result, err)
}

// sourceFileArguments renders one source-file argument vector. It performs no
// tmux I/O, so a [Plan] can render a load it has not run. It does resolve the
// path against the filesystem, which is what expanding ~ means.
func sourceFileArguments(request SourceFileRequest) ([]string, error) {
	if request.Path == "" {
		return nil, invalidServerCommandRequest(
			"source-file", "Path", "", "must not be empty")
	}
	if request.Path == "-" {
		return nil, invalidServerCommandRequest(
			"source-file",
			"Path",
			request.Path,
			"requires stdin, which this runner does not provide",
		)
	}
	path, err := expandServerCommandPath(request.Path)
	if err != nil {
		return nil, err
	}
	arguments := []string{"source-file"}
	if request.Quiet {
		arguments = append(arguments, "-q")
	}
	if request.ParseOnly {
		arguments = append(arguments, "-n")
	}
	if request.Verbose {
		arguments = append(arguments, "-v")
	}
	return append(arguments, "--", path), nil
}

func invalidServerCommandRequest(
	subcommand string,
	field string,
	value string,
	reason string,
) error {
	return &ServerCommandRequestError{
		Subcommand: subcommand,
		Field:      field,
		Value:      value,
		Reason:     reason,
	}
}

func expandServerCommandPath(path string) (string, error) {
	return expandCommandPath("source-file", path)
}

func validateRunShellArguments(request RunShellRequest) error {
	values := []serverCommandArgument{
		{field: "Command", value: request.Command},
		{field: "TargetPane", value: request.TargetPane.String()},
	}
	if request.Delay != nil {
		values = append(values, serverCommandArgument{field: "Delay", value: *request.Delay})
	}
	if request.StartDirectory != nil {
		values = append(
			values,
			serverCommandArgument{field: "StartDirectory", value: *request.StartDirectory},
		)
	}
	for _, argument := range request.Args {
		values = append(values, serverCommandArgument{field: "Args", value: argument})
	}
	return validateServerCommandArguments("run-shell", values...)
}

func validateIfShellArguments(request IfShellRequest) error {
	values := []serverCommandArgument{
		{field: "ShellCommand", value: request.ShellCommand},
		{field: "ThenCommand", value: request.ThenCommand},
		{field: "TargetPane", value: request.TargetPane.String()},
	}
	if request.ElseCommand != nil {
		values = append(
			values,
			serverCommandArgument{field: "ElseCommand", value: *request.ElseCommand},
		)
	}
	return validateServerCommandArguments("if-shell", values...)
}

func expandCommandPath(subcommand string, path string) (string, error) {
	if err := validateServerCommandArgument(subcommand, "Path", path, true); err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "~") {
		return normalizeCommandPath(path), nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", invalidServerCommandRequest(
			subcommand,
			"Path",
			path,
			"does not support named-user expansion",
		)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", invalidServerCommandRequest(
			subcommand,
			"Path",
			path,
			"cannot expand the current user home: "+err.Error(),
		)
	}
	if home == "" {
		return "", invalidServerCommandRequest(
			subcommand,
			"Path",
			path,
			"cannot expand an empty current user home",
		)
	}
	if path == "~" {
		return normalizeCommandPath(home), nil
	}
	expanded := home
	if !os.IsPathSeparator(home[len(home)-1]) {
		expanded += string(os.PathSeparator)
	}
	expanded += filepath.FromSlash(strings.TrimPrefix(path, "~/"))
	return normalizeCommandPath(expanded), nil
}

// normalizeCommandPath mirrors pathlib's harmless lexical normalization while
// retaining parent components whose kernel resolution may cross symlinks.
func normalizeCommandPath(path string) string {
	volume := filepath.VolumeName(path)
	remainder := path[len(volume):]
	leadingSeparators := 0
	for leadingSeparators < len(remainder) && os.IsPathSeparator(remainder[leadingSeparators]) {
		leadingSeparators++
	}

	parts := strings.FieldsFunc(remainder[leadingSeparators:], func(character rune) bool {
		return character == '/' || character == rune(os.PathSeparator)
	})
	normalized := parts[:0]
	for _, part := range parts {
		if part != "." {
			normalized = append(normalized, part)
		}
	}

	separator := string(os.PathSeparator)
	prefix := volume
	if leadingSeparators != 0 {
		prefix += separator
		if volume == "" && os.PathSeparator == '/' && leadingSeparators == 2 {
			prefix += separator
		}
	}
	joined := strings.Join(normalized, separator)
	// Keep a relative file named "-" distinct from tmux's stdio sentinel.
	if prefix == "" && joined == "-" && path != "-" {
		return "." + separator + "-"
	}
	if prefix != "" {
		return prefix + joined
	}
	if joined == "" {
		return "."
	}
	return joined
}

func validateServerCommandArgument(
	subcommand string,
	field string,
	value string,
	redact bool,
) error {
	if !strings.ContainsRune(value, '\x00') {
		return nil
	}
	if redact {
		value = "[redacted]"
	}
	return invalidServerCommandRequest(
		subcommand,
		field,
		value,
		"contains NUL, which cannot cross process argv",
	)
}

func requireServerCommandNoStderr(
	subcommand string,
	result CommandResult,
	err error,
) error {
	if err != nil {
		return err
	}
	if len(result.Stderr) != 0 {
		return newCommandError(subcommand, result)
	}
	return nil
}

func requireRedactedServerCommandNoStderr(
	subcommand string,
	result CommandResult,
	err error,
) error {
	if err != nil {
		return err
	}
	if len(result.Stderr) != 0 {
		return newRedactedCommandError(subcommand, result)
	}
	return nil
}
