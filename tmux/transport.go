package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

type commandKind uint8

const (
	commandServer commandKind = iota
	commandProcess
)

// requireProcess marks exact-output and interactive operations. A plain
// Server starts its frozen executable; a connection-bound Server refuses.
func (s Server) requireProcess() Server {
	s.requiresProcess = true
	return s
}

func (s Server) boundToInstance() bool { return s.connection != nil }

// runCommand adds client-global selectors unless an owned connection already
// addresses the daemon. commandList controls subprocess escaping of literal
// trailing semicolons.
func (s Server) runCommand(
	ctx context.Context,
	kind commandKind,
	args []string,
	stdio *tmuxcmd.Stdio,
	commandList bool,
) (tmuxcmd.Result, error) {
	state, err := s.stateForUse()
	if err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	guarded, guard, err := s.guardCommand(args, commandList)
	if err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	if guard != nil {
		commandList = false
	}
	routeKind := kind
	if s.requiresProcess {
		routeKind = commandProcess
	}

	var result tmuxcmd.Result
	if s.connection != nil {
		connectedResult, connectionErr := s.connection.run(
			ctx,
			routeKind,
			guarded,
			commandList,
		)
		result = internalCommandResult(connectedResult)
		err = connectionErr
	} else {
		arguments := guarded
		if !commandList {
			arguments = escapeCommandListSeparators(arguments)
		}
		result, err = state.executor.Run(ctx, tmuxcmd.Request{
			Binary:      state.config.executable,
			Arguments:   s.commandArguments(arguments),
			Environment: slices.Clone(state.config.processEnvironment),
			Directory:   state.config.directory,
			Stdio:       stdio,
		})
	}
	if guard == nil {
		return result, err
	}
	result.Command = s.originalCommand(kind, args, s.connection != nil)
	if err == nil && guard.rejected(result.ExitCode, result.Stderr) {
		return tmuxcmd.Result{Command: result.Command, ExitCode: -1}, ErrDaemonReplaced
	}
	if err == nil && stdio != nil && result.ExitCode != 0 &&
		s.daemonNoLongerAtSocket(ctx) {
		return tmuxcmd.Result{Command: result.Command, ExitCode: -1}, ErrDaemonReplaced
	}
	return result, err
}

// Streaming commands hand stderr to the caller, so the guard's private
// failure marker is unavailable. Probe only after a nonzero process exit.
func (s Server) daemonNoLongerAtSocket(ctx context.Context) bool {
	if s.daemon == nil {
		return false
	}
	current, err := s.withoutDaemon().probeSnapshotIdentity(ctx)
	if err != nil {
		return errors.Is(err, ErrNoServer)
	}
	return !sameSnapshotIdentity(*s.daemon, current)
}

func (s Server) originalCommand(
	kind commandKind,
	arguments []string,
	connected bool,
) []string {
	if connected {
		if kind == commandServer {
			return slices.Clone(arguments)
		}
		return s.commandArguments(arguments)
	}
	state, err := s.stateForUse()
	if err != nil {
		return nil
	}
	return append([]string{state.config.executable}, s.commandArguments(arguments)...)
}

func internalCommandResult(result CommandResult) tmuxcmd.Result {
	return tmuxcmd.Result{
		Command:   slices.Clone(result.Command),
		Stdout:    slices.Clone(result.Stdout),
		RawStdout: bytes.Clone(result.RawStdout),
		Stderr:    slices.Clone(result.Stderr),
		ExitCode:  result.ExitCode,
	}
}

// runExactArgv routes a complete tmux argv that carries its own client-global
// options, such as the tmux -V version probe.
func (s Server) runExactArgv(
	ctx context.Context,
	arguments []string,
) (tmuxcmd.Result, error) {
	state, err := s.stateForUse()
	if err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	if s.connection != nil {
		return tmuxcmd.Result{ExitCode: -1},
			s.connection.routeError(ctx, commandProcess)
	}
	return state.executor.Run(ctx, tmuxcmd.Request{
		Binary:      state.config.executable,
		Arguments:   arguments,
		Environment: slices.Clone(state.config.processEnvironment),
		Directory:   state.config.directory,
	})
}

func importCommandStdio(stdio *CommandStdio) *tmuxcmd.Stdio {
	if stdio == nil {
		return nil
	}
	return &tmuxcmd.Stdio{
		Stdin:  stdio.Stdin,
		Stdout: stdio.Stdout,
		Stderr: stdio.Stderr,
	}
}
