package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

type commandKind uint8

const (
	commandServer commandKind = iota
	commandProcess
	// commandBlocking is a server command that can hold its client until a
	// peer acts, such as wait-for. A connection gives it a client of its own
	// so abandoning it cannot strand a shared lane.
	commandBlocking
	// commandBlockingOutput is commandBlocking for a shell command whose
	// stdout tmux sends a control client as lines after its reply, such as a
	// foreground run-shell.
	commandBlockingOutput
)

// blocks reports whether a connection gives the command a client of its own.
func (k commandKind) blocks() bool {
	return k == commandBlocking || k == commandBlockingOutput
}

// requireProcess marks exact-output and interactive operations. A plain
// Server starts its frozen executable; a connection-bound Server refuses.
func (s Server) requireProcess() Server {
	s.requiresProcess = true
	return s
}

// inheritLocale marks an operation whose tmux output goes to the caller's own
// terminal, where the caller's locale governs rather than this package's.
func (s Server) inheritLocale() Server {
	s.inheritsLocale = true
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
	if _, err := s.stateForUse(); err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	absentCleanup := len(args) == 1 && args[0] == "kill-server"
	// A command with a stream cannot be replayed, so it always probes. Others
	// reuse the endpoint's checked identity: the if-shell fence refuses a
	// replaced daemon before any effect, and a vanished one answers "no server
	// running" to the -N client, so either outcome discards the record and
	// repeats the command once after a fresh probe.
	reuse := stdio == nil
	for attempt := 0; ; attempt++ {
		checked, cached, err := s.checkForDispatch(ctx, absentCleanup, reuse && attempt == 0)
		if err != nil {
			return tmuxcmd.Result{ExitCode: -1}, err
		}
		if absentCleanup && checked.daemon == nil && checked.connection == nil {
			return tmuxcmd.Result{Command: s.originalCommand(kind, args, false), ExitCode: 0}, nil
		}
		result, err := checked.runCheckedCommand(ctx, kind, args, stdio, commandList)
		if cached && (errors.Is(err, ErrDaemonReplaced) ||
			(err == nil && result.ExitCode != 0 && neverReachedServer(result.Stderr))) {
			s.state.coordination().rememberChecked(nil)
			continue
		}
		return result, err
	}
}

// checkForDispatch checks the daemon version for one ordinary dispatch. cached reports
// that the identity came from the endpoint's record instead of a probe.
func (s Server) checkForDispatch(
	ctx context.Context,
	absentCleanup, reuse bool,
) (checked Server, cached bool, err error) {
	if reuse && s.daemon == nil && s.connection == nil && !absentCleanup {
		if remembered := s.state.coordination().checkedDaemon(); remembered != nil {
			if err := ctx.Err(); err != nil {
				return Server{}, false, err
			}
			bound := s.withDaemon(*remembered)
			if err := requireSupportedVersion(bound.daemon.version); err != nil {
				return Server{}, false, err
			}
			return bound, true, nil
		}
	}
	checked, err = s.checkCore(ctx, absentCleanup)
	if err != nil {
		return Server{}, false, err
	}
	if s.daemon == nil && s.connection == nil && checked.daemon != nil {
		s.state.coordination().rememberChecked(checked.daemon)
	}
	return checked, false, nil
}

// runCheckedCommand also serves the private read-only identity bootstrap.
func (s Server) runCheckedCommand(
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
		commandList = true
	}
	routeKind := kind
	if s.requiresProcess {
		routeKind = commandProcess
	}

	// Read once: a nil observer must not even cost a clock read.
	observer := s.commandObserver()
	var started time.Time
	if observer != nil {
		started = time.Now()
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
		arguments = s.commandArguments(arguments)
		if s.daemon != nil || s.probesIdentity {
			arguments = append([]string{"-N"}, arguments...)
		}
		result, err = state.executor.Run(ctx, tmuxcmd.Request{
			Binary:      state.config.executable,
			Arguments:   arguments,
			Environment: slices.Clone(state.config.processEnvironment),
			Directory:   state.config.directory,
			Stdio:       stdio,
		})
	}
	// Observed after the guard has had its say, so a caller reading a trace
	// and a caller reading the error are told the same thing.
	if guard != nil {
		result.Command = s.originalCommand(kind, args, s.connection != nil)
		switch {
		case err == nil && guard.rejected(result.ExitCode, result.Stderr):
			result = tmuxcmd.Result{Command: result.Command, ExitCode: -1}
			err = ErrDaemonReplaced
		case err == nil && stdio != nil && result.ExitCode != 0 &&
			s.daemonNoLongerAtSocket(ctx):
			result = tmuxcmd.Result{Command: result.Command, ExitCode: -1}
			err = ErrDaemonReplaced
		}
	}
	if observer != nil {
		observeCommand(observer, args, started, s.commandTransport(), result.ExitCode, err)
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
		if kind != commandProcess {
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
	observer := s.commandObserver()
	var started time.Time
	if observer != nil {
		started = time.Now()
	}
	result, err := state.executor.Run(ctx, tmuxcmd.Request{
		Binary:      state.config.executable,
		Arguments:   arguments,
		Environment: slices.Clone(state.config.processEnvironment),
		Directory:   state.config.directory,
	})
	if observer != nil {
		// These carry their own client globals, so the subcommand is whatever
		// follows them rather than the first argument.
		observeCommand(observer, exactArgvSubcommand(arguments), started,
			s.commandTransport(), result.ExitCode, err)
	}
	return result, err
}

// exactArgvSubcommand names the command in an argv that carries its own client
// globals, which are the flag-shaped arguments before it. Those globals carry
// any value in the same token, as commandArguments writes them; a flag whose
// value were a separate token would read as the command.
func exactArgvSubcommand(arguments []string) []string {
	for index, argument := range arguments {
		if !strings.HasPrefix(argument, "-") {
			return arguments[index:]
		}
	}
	return arguments
}

// neverReachedServer reports stderr proving the command found no daemon at all
// and so ran nothing; a server that exits during a command is not included.
func neverReachedServer(stderr []string) bool {
	for _, line := range stderr {
		if strings.HasPrefix(line, "no server running on ") ||
			line == "server version is too old for client" ||
			(strings.HasPrefix(line, "error connecting to ") &&
				strings.HasSuffix(line, " (No such file or directory)")) {
			return true
		}
	}
	return false
}
