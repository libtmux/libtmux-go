package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// Server is an immutable handle to one tmux configuration. Its zero value
// is invalid. Copying a configured Server preserves its configuration and
// shares version-cache coordination; only the documented concurrent operations
// are safe to share.
type Server struct {
	state      *serverState
	connection *Connection
	daemon     *snapshotServerIdentity
	// requiresProcess marks exact-output and interactive operations that cannot
	// cross a persistent control connection.
	requiresProcess bool
}

type serverState struct {
	config   serverConfig
	executor commandRunner
	shared   *serverShared
}

// serverShared coordinates version caching across handles that address the
// same daemon with different process environments.
type serverShared struct {
	version versionCache
}

// coordination returns the daemon-scoped shared state.
func (s *serverState) coordination() *serverShared {
	return s.shared
}

type commandRunner interface {
	Run(context.Context, tmuxcmd.Request) (tmuxcmd.Result, error)
}

// SocketPath returns the absolute socket path selected when the server was
// constructed. An invalid server returns an empty string.
func (s Server) SocketPath() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.socketSelection.Path
}

// ConfigFile returns the configured tmux configuration path.
func (s Server) ConfigFile() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.configFile
}

// ProcessEnvironment returns the normalized environment explicitly supplied
// through [ServerOptions]. Nil means the server inherited a private process
// snapshot. The returned slice is owned by the caller; an invalid server
// returns nil.
func (s Server) ProcessEnvironment() []string {
	if s.state == nil {
		return nil
	}
	return slices.Clone(s.state.config.configuredProcessEnvironment)
}

// Executable returns the absolute tmux executable path resolved when the
// server was constructed. An invalid server returns an empty string.
func (s Server) Executable() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.executable
}

// Cmd executes raw tmux arguments and returns caller-owned result slices. A
// completed nonzero exit remains a result; validation and transport failures
// return errors. Cancellation cannot prove whether a mutation reached tmux.
//
// An argument that is exactly ";" separates two tmux commands, so one call can
// submit a command list. tmux runs a list until a command fails and drops the
// rest, and answers with one merged stdout, so a caller that needs to know
// which command produced which line submits them separately. A list means the
// same thing through either execution binding.
//
// Only a standalone ";" separates commands. Other semicolons follow the selected
// binding's parsing; typed operations keep values literal across bindings.
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
	result, err := s.runCommand(ctx, commandServer, args, nil, commandList)
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
	return s.state
}

func (s Server) stateForUse() (*serverState, error) {
	if s.state == nil || s.state.executor == nil || s.state.shared == nil ||
		s.state.config.executable == "" || s.state.config.directory == "" {
		return nil, ErrInvalidServer
	}
	return s.state, nil
}

func (s Server) commandArguments(args []string) []string {
	if s.state == nil {
		return slices.Clone(args)
	}
	config := s.state.config
	selectorFlag, selectorValue := effectiveSocketSelectorValues(
		config.socketPath,
		config.socketName,
	)
	globalCount := 0
	if config.colors != ColorDefault {
		globalCount++
	}
	if config.configFile != "" {
		globalCount++
	}
	if selectorValue != "" {
		globalCount++
	}

	command := make([]string, 0, globalCount+len(args))
	switch config.colors {
	case ColorDefault:
	case Color88:
		command = append(command, "-8")
	case Color256:
		command = append(command, "-2")
	}
	// Global arguments are consumed before tmux's command-list parser. Their
	// values are already literal; a separator escape would become part of the
	// configured path or socket name.
	if config.configFile != "" {
		command = append(command, "-f"+config.configFile)
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
	return effectiveSocketSelectorValues(options.SocketPath, options.SocketName)
}

func effectiveSocketSelectorValues(socketPath, socketName string) (flag string, value string) {
	if socketPath != "" {
		return "-S", socketPath
	}
	if socketName != "" {
		return "-L", socketName
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
