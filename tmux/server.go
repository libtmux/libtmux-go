package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// ServerOptions configures a [Server] without executing tmux. [NewServer]
// copies ProcessEnvironment; callers retain ownership of the supplied slice.
type ServerOptions struct {
	// Binary is the tmux executable name or path. Empty resolves tmux through
	// PATH for each invocation. A binary that cannot be resolved fails every
	// operation, as an os/exec.Error. Match it
	// with errors.As rather than errors.Is(err, exec.ErrNotFound), which holds
	// only for a bare name missing from PATH and not for an absent explicit
	// path.
	Binary string
	// SocketName selects tmux's named socket. SocketPath takes precedence.
	SocketName string
	// SocketPath selects an explicit tmux socket path.
	SocketPath string
	// ConfigFile selects an exact tmux configuration file. Empty lets tmux read
	// its default configuration, so a program inherits whatever the user
	// running it has configured: a base-index other than zero, a prompt that
	// appears in captured pane text, hooks that fire on the sessions this
	// program creates. Point it at a file the program owns, which may be an
	// empty one, when the tmux it drives should not depend on that.
	ConfigFile string
	// Colors overrides tmux's terminal color capability.
	Colors ColorMode
	// ProcessEnvironment replaces the child process environment. Nil inherits
	// the current process environment; a non-nil empty slice supplies no
	// caller-provided variables, subject to additions required by Go or the
	// target platform. NewServer clones the slice.
	ProcessEnvironment []string
	// WarningHandler receives nonfatal compatibility warnings. Nil discards
	// warnings. See WarningHandler for delivery and concurrency semantics.
	WarningHandler WarningHandler
	// Runner replaces process execution for tests and alternate transports. Nil
	// uses the local tmux subprocess runner. The server retains Runner, which
	// must support concurrent calls when the server is used concurrently.
	//
	// It covers requests that run to completion and return output. It does not
	// cover the tmux -C process Server.OpenControl starts, which is a
	// long-lived bidirectional stream that a request-and-result interface
	// cannot carry; that process is started directly, while its registration
	// and version probes do pass through Runner. Substituting Runner therefore
	// does not stop OpenControl from starting a real tmux.
	//
	// Because Runner sees every request an engine does not carry, it is also
	// how a caller confirms an engine is being used: count the requests that
	// reach it before and after Server.WithEngine. Wrap SubprocessRunner to do
	// that rather than reimplementing execution, whose result shape the rest
	// of the package reads.
	Runner CommandRunner
}

// Server is an immutable handle to one tmux configuration. Its zero value
// targets tmux's default binary, socket, configuration, and environment.
// Copying a Server preserves its configuration and shares version-cache
// coordination; only the documented concurrent operations are safe to share.
type Server struct {
	state  *serverState
	engine Engine
}

type serverState struct {
	options ServerOptions
	runner  commandRunner
	version versionCache
}

type commandRunner interface {
	Run(context.Context, tmuxcmd.Request) (tmuxcmd.Result, error)
}

var defaultServerState = &serverState{runner: tmuxcmd.Runner{}}

// NewServer returns a configured server handle without executing tmux. Empty
// socket selectors retain tmux's default configuration.
func NewServer(options ServerOptions) Server {
	options.ProcessEnvironment = slices.Clone(options.ProcessEnvironment)
	return Server{state: &serverState{
		options: options,
		runner:  configuredCommandRunner(options.Runner),
	}}
}

type commandRunnerAdapter struct {
	runner CommandRunner
}

func configuredCommandRunner(runner CommandRunner) commandRunner {
	if runner == nil {
		return tmuxcmd.Runner{}
	}
	return commandRunnerAdapter{runner: runner}
}

func (adapter commandRunnerAdapter) Run(
	ctx context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	result, err := adapter.runner.Run(ctx, CommandRequest{
		Binary:      request.Binary,
		Arguments:   slices.Clone(request.Arguments),
		Environment: slices.Clone(request.Environment),
		Directory:   request.Directory,
		Stdio:       exportCommandStdio(request.Stdio),
	})
	return tmuxcmd.Result{
		Command:   slices.Clone(result.Command),
		Stdout:    slices.Clone(result.Stdout),
		RawStdout: bytes.Clone(result.RawStdout),
		Stderr:    slices.Clone(result.Stderr),
		ExitCode:  result.ExitCode,
	}, err
}

// SubprocessRunner returns the [CommandRunner] a server uses when
// [ServerOptions.Runner] is nil: it runs each request as its own tmux process.
//
// It exists so a Runner can wrap the default rather than replace it. Counting,
// logging, or failing requests needs only the wrapper, while running them
// produces the result the rest of the package reads: a nonzero exit is a
// completed result rather than an error, Stdout holds one decoded line per
// element with no trailing empty line, and RawStdout holds the exact bytes
// that the captures promising them return. Delegating keeps all of that
// correct in a wrapper that does not care about any of it.
func SubprocessRunner() CommandRunner {
	return subprocessRunner(0)
}

// subprocessRunner is SubprocessRunner with the transport's wait delay exposed.
// Zero keeps the transport's own default, which is what every caller outside
// this package gets. It is separate only because a test standing in for tmux
// with something far heavier than tmux needs longer than that default allows.
func subprocessRunner(waitDelay time.Duration) CommandRunner {
	return CommandRunnerFunc(func(
		ctx context.Context,
		request CommandRequest,
	) (CommandResult, error) {
		result, err := tmuxcmd.Runner{WaitDelay: waitDelay}.Run(ctx, tmuxcmd.Request{
			Binary:      request.Binary,
			Arguments:   slices.Clone(request.Arguments),
			Environment: slices.Clone(request.Environment),
			Directory:   request.Directory,
			Stdio:       importCommandStdio(request.Stdio),
		})
		return CommandResult{
			Command:   slices.Clone(result.Command),
			Stdout:    slices.Clone(result.Stdout),
			RawStdout: bytes.Clone(result.RawStdout),
			Stderr:    slices.Clone(result.Stderr),
			ExitCode:  result.ExitCode,
		}, err
	})
}

func exportCommandStdio(stdio *tmuxcmd.Stdio) *CommandStdio {
	if stdio == nil {
		return nil
	}
	return &CommandStdio{
		Stdin:  stdio.Stdin,
		Stdout: stdio.Stdout,
		Stderr: stdio.Stderr,
	}
}

// SocketPath returns the configured explicit tmux socket path.
func (s Server) SocketPath() string {
	return s.connectionState().options.SocketPath
}

// ConfigFile returns the configured tmux configuration path.
func (s Server) ConfigFile() string {
	return s.connectionState().options.ConfigFile
}

// ProcessEnvironment returns the configured child-process environment. Nil
// means commands inherit the current process environment. The returned slice
// is owned by the caller.
func (s Server) ProcessEnvironment() []string {
	return slices.Clone(s.connectionState().options.ProcessEnvironment)
}

// Cmd executes raw tmux arguments. A completed nonzero tmux exit remains in
// the returned [CommandResult]; validation and transport failures return an
// error. Cmd clones result slices, so the caller owns Command, decoded Stdout,
// exact RawStdout, and decoded Stderr. Canceling ctx stops the wait for the
// command but cannot determine whether a mutating command reached tmux.
//
// An argument that is exactly ";" separates two tmux commands, so one call can
// submit a command list. tmux runs a list until a command fails and drops the
// rest, and answers with one merged stdout, so a caller that needs to know
// which command produced which line submits them separately. A list means the
// same thing through every transport.
//
// Only a standalone ";" is a separator. A semicolon inside a larger argument is
// left to tmux's own parsing, which differs by transport: a tmux process hands
// the argument to tmux's outer command parser, which consumes a trailing
// semicolon, while a control connection quotes the argument and keeps it. Pass
// values through the typed operations rather than here when that matters; they
// are literal through either transport.
func (s Server) Cmd(ctx context.Context, args ...string) (CommandResult, error) {
	result, _, err := s.dispatch(ctx, true, args...)
	return result, err
}

func (s Server) literalCmd(ctx context.Context, args ...string) (CommandResult, error) {
	result, _, err := s.literalCmdWithRaw(ctx, args...)
	return result, err
}

func (s Server) literalCmdWithRaw(
	ctx context.Context,
	args ...string,
) (CommandResult, []byte, error) {
	if err := validateLiteralCommandArguments(args); err != nil {
		return CommandResult{ExitCode: -1}, nil, err
	}
	return s.dispatch(ctx, false, args...)
}

// dispatch runs one tmux command. commandList reports whether a bare ";" in
// args separates two commands, which decides how each transport renders it.
func (s Server) dispatch(
	ctx context.Context,
	commandList bool,
	args ...string,
) (CommandResult, []byte, error) {
	state := s.connectionState()
	if err := validateColorMode(state.options.Colors); err != nil {
		return CommandResult{ExitCode: -1}, nil, err
	}
	if err := validateConnectionArguments(state.options); err != nil {
		return CommandResult{ExitCode: -1}, nil, err
	}

	result, err := s.runCommand(ctx, CommandServer, args, nil, commandList)
	commandResult := cloneCommandResult(CommandResult{
		Command:   result.Command,
		Stdout:    result.Stdout,
		RawStdout: result.RawStdout,
		Stderr:    result.Stderr,
		ExitCode:  result.ExitCode,
	})
	if err != nil {
		return commandResult, bytes.Clone(result.RawStdout), commandTransportFailure(err)
	}
	return commandResult, bytes.Clone(result.RawStdout), nil
}

// IsAlive reports whether a tmux server answers on the configured socket.
// Only an absent server reports false without an error: a socket that exists
// but cannot be reached, such as one the process may not read, is a question
// that could not be answered and is returned as an error.
func (s Server) IsAlive(ctx context.Context) (bool, error) {
	result, err := s.literalCmd(ctx, "list-sessions")
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}
	commandErr := newCommandError("list-sessions", result)
	if errors.Is(commandErr, ErrNoServer) {
		return false, nil
	}
	return false, commandErr
}

// RaiseIfDead returns a [CommandError] when the configured server is not
// alive. Cancellation and transport failures are returned directly.
func (s Server) RaiseIfDead(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "list-sessions")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newCommandError("list-sessions", result)
	}
	return nil
}

func (s Server) connectionState() *serverState {
	if s.state == nil {
		return defaultServerState
	}
	return s.state
}

func (s Server) commandArguments(args []string) []string {
	options := s.connectionState().options
	selectorFlag, selectorValue := effectiveSocketSelector(options)
	globalCount := 0
	if options.Colors != ColorDefault {
		globalCount++
	}
	if options.ConfigFile != "" {
		globalCount++
	}
	if selectorValue != "" {
		globalCount++
	}

	command := make([]string, 0, globalCount+len(args))
	switch options.Colors {
	case ColorDefault:
	case Color88:
		command = append(command, "-8")
	case Color256:
		command = append(command, "-2")
	}
	// Global arguments are consumed before tmux's command-list parser. Their
	// values are already literal; a separator escape would become part of the
	// configured path or socket name.
	if options.ConfigFile != "" {
		command = append(command, "-f"+options.ConfigFile)
	}
	if selectorValue != "" {
		command = append(command, selectorFlag+selectorValue)
	}
	return append(command, args...)
}

func validateConnectionArguments(options ServerOptions) error {
	if options.ConfigFile != "" {
		if err := validateServerCommandArgument(
			"tmux", "ConfigFile", options.ConfigFile, true,
		); err != nil {
			return err
		}
	}
	selectorFlag, selectorValue := effectiveSocketSelector(options)
	if selectorValue == "" {
		return nil
	}
	field := "SocketName"
	if selectorFlag == "-S" {
		field = "SocketPath"
	}
	return validateServerCommandArgument("tmux", field, selectorValue, true)
}

func effectiveSocketSelector(options ServerOptions) (flag string, value string) {
	if options.SocketPath != "" {
		return "-S", options.SocketPath
	}
	if options.SocketName != "" {
		return "-L", options.SocketName
	}
	return "", ""
}

func validateColorMode(mode ColorMode) error {
	switch mode {
	case ColorDefault, Color88, Color256:
		return nil
	default:
		return &ColorError{Mode: mode}
	}
}
