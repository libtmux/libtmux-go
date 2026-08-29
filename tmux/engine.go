package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// CommandKind classifies what a request needs from an [Engine]. Unknown kinds
// must be unsupported so later kinds fall back safely.
type CommandKind int

const (
	// CommandServer is a tmux command addressed to the configured running
	// server, with its output captured. A transport that serves this kind is
	// already connected to that server, so the request's Arguments hold the tmux
	// command alone, without the configured color, configuration, and socket
	// selectors, and its Stdio is nil. Nearly every operation in this package
	// issues this kind.
	CommandServer CommandKind = iota
	// CommandProcess is a request that needs a tmux process of its own. Its
	// Arguments hold the complete tmux argv including the configured
	// client-global selectors, and its Stdio may stream to caller-owned files.
	// Interactive attachment needs it because a real terminal is the point of
	// the command, and the version probe needs it because tmux -V is a
	// client-global option rather than a command any connected transport can
	// carry.
	CommandProcess
)

// String implements fmt.Stringer.
func (k CommandKind) String() string {
	switch k {
	case CommandServer:
		return "server"
	case CommandProcess:
		return "process"
	default:
		return "CommandKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Engine executes supported [CommandKind] values for a [Server]. Unsupported
// requests use [ServerOptions.Runner] unless fallback is rejected.
//
// The creator, not the copyable Server, owns the engine's shutdown.
//
// Run may be called concurrently; an engine may serialize internally.
type Engine interface {
	// Supports reports whether the engine can carry requests of kind. It must
	// be deterministic and must not perform I/O: a Server consults it on every
	// command.
	Supports(kind CommandKind) bool
	// Run executes one classified request. A completed tmux command failure
	// belongs in the returned result as a nonzero ExitCode with the tmux
	// message in Stderr, matching the subprocess transport, so that operation
	// error classification is identical through either. Only a transport
	// failure is returned as an error. Returned slices are copied before they
	// reach the caller.
	Run(ctx context.Context, kind CommandKind, request CommandRequest) (CommandResult, error)
}

// EngineFallbackPolicy selects what a handle does when its engine cannot
// carry a command or an operation requires exact subprocess output.
type EngineFallbackPolicy int

const (
	// EngineFallbackAllow runs the command through a tmux process. It is the
	// zero value and preserves the full object API for partial engines.
	EngineFallbackAllow EngineFallbackPolicy = iota
	// EngineFallbackReject returns an EngineFallbackError without starting a
	// process. It lets a caller enforce a transport boundary.
	EngineFallbackReject
)

// ErrEngineFallback matches a command refused because subprocess fallback is
// disabled. Use errors.As with [EngineFallbackError] for the command kind.
var ErrEngineFallback = errors.New("tmux: subprocess fallback is disabled")

// EngineFallbackError reports which command kind would have started a tmux
// process after the selected engine was unable to carry it.
type EngineFallbackError struct {
	// Kind is the command route the engine could not carry.
	Kind CommandKind
}

// Error implements error.
func (e *EngineFallbackError) Error() string {
	return ErrEngineFallback.Error() + " for " + e.Kind.String() + " command"
}

// Unwrap makes errors.Is(err, ErrEngineFallback) true.
func (e *EngineFallbackError) Unwrap() error { return ErrEngineFallback }

// WithEngine returns a handle whose supported commands run through engine. Nil
// restores process execution. Records derived from the result retain the engine.
//
// The caller retains ownership of the engine. Unsupported commands and exact-byte
// reads use the configured fallback policy.
//
// Existing records retain their original handle; look them up through the result
// or use their WithEngine method to change transport.
func (s Server) WithEngine(engine Engine) Server {
	s.engine = engine
	s.engineless = false
	return s
}

// WithEngineFallback returns a handle using policy when its selected engine
// cannot carry a command. The zero policy starts a tmux process, preserving
// access to operations a partial engine does not support. EngineFallbackReject
// returns an [EngineFallbackError] instead.
//
// The policy also covers operations such as [Pane.CaptureBytes] and
// [Server.ShowBufferBytes] that require exact subprocess stdout. It is active
// only while an engine is selected; [Server.WithEngine] with nil restores
// ordinary process execution.
func (s Server) WithEngineFallback(policy EngineFallbackPolicy) Server {
	s.engineFallback = policy
	return s
}

// EngineFallback reports what this handle does when its selected engine
// cannot carry an operation.
func (s Server) EngineFallback() EngineFallbackPolicy { return s.engineFallback }

// Engine returns the selected engine, or nil when commands use subprocesses.
// A non-nil [Server.SubprocessEngine] records an explicit subprocess choice.
func (s Server) Engine() Engine { return s.engine }

// SubprocessEngine returns an [Engine] that runs every request through the
// configured [ServerOptions.Runner]. It expresses an explicit subprocess choice.
func (s Server) SubprocessEngine() Engine {
	return subprocessEngine{server: s.withoutEngine()}
}

type subprocessEngine struct {
	server Server
}

// Supports reports that a tmux process can carry every command kind.
func (e subprocessEngine) Supports(CommandKind) bool { return true }

// Run executes one request as a tmux process.
func (e subprocessEngine) Run(
	ctx context.Context,
	kind CommandKind,
	request CommandRequest,
) (CommandResult, error) {
	arguments := request.Arguments
	if !request.CommandList {
		arguments = escapeCommandListSeparators(arguments)
	}
	if kind == CommandServer {
		arguments = e.server.commandArguments(arguments)
	}
	result, err := e.server.connectionState().runner.Run(ctx, tmuxcmd.Request{
		Binary:      request.Binary,
		Arguments:   arguments,
		Environment: request.Environment,
		Directory:   request.Directory,
		Stdio:       importCommandStdio(request.Stdio),
	})
	return CommandResult{
		Command:   result.Command,
		Stdout:    result.Stdout,
		RawStdout: result.RawStdout,
		Stderr:    result.Stderr,
		ExitCode:  result.ExitCode,
	}, err
}

// String implements fmt.Stringer.
func (e subprocessEngine) String() string { return "subprocess" }

// withoutEngine returns a handle that always runs commands as tmux processes.
// Operations whose documented result is tmux's exact stdout bytes use it: a
// connected transport reports a command reply in its own protocol rendering,
// which this package does not normalize back into tmux's process stdout.
func (s Server) withoutEngine() Server {
	s.engine = nil
	s.engineless = true
	return s
}

// InstanceBoundEngine reports that consecutive commands cannot cross a tmux
// daemon replacement. Snapshots use it to skip a redundant identity probe.
// Wrapping engines should forward the property.
type InstanceBoundEngine interface {
	// InstanceBound reports whether consecutive commands this engine carried
	// provably reached one tmux server instance.
	InstanceBound() bool
}

func (s Server) boundToInstance() bool {
	bound, ok := s.engine.(InstanceBoundEngine)
	return ok && bound.InstanceBound()
}

// engineDecliner reports noteworthy changes in an engine's support.
type engineDecliner interface {
	declined(kind CommandKind) (Warning, bool)
}

func (s Server) commandEngine(kind CommandKind) (Engine, error) {
	if s.engine == nil {
		if s.engineless && s.engineFallback == EngineFallbackReject {
			return nil, &EngineFallbackError{Kind: kind}
		}
		s.warnIfPoolUnused(kind)
		return nil, nil
	}
	if !s.engine.Supports(kind) {
		if s.engineFallback == EngineFallbackReject {
			return nil, &EngineFallbackError{Kind: kind}
		}
		if decliner, ok := s.engine.(engineDecliner); ok {
			if warning, worth := decliner.declined(kind); worth {
				s.warn(warning)
			}
		}
		return nil, nil
	}
	return s.engine, nil
}

// runCommand adds client-global selectors unless the engine is already connected.
// commandList controls subprocess escaping of literal trailing semicolons.
func (s Server) runCommand(
	ctx context.Context,
	kind CommandKind,
	args []string,
	stdio *tmuxcmd.Stdio,
	commandList bool,
) (tmuxcmd.Result, error) {
	state := s.connectionState()
	engine, err := s.commandEngine(kind)
	if err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	if engine != nil {
		arguments := args
		if kind != CommandServer {
			arguments = s.commandArguments(args)
		}
		return runEngine(ctx, engine, kind, state.options, arguments, stdio, commandList)
	}
	arguments := args
	if !commandList {
		arguments = escapeCommandListSeparators(arguments)
	}
	return state.runner.Run(ctx, tmuxcmd.Request{
		Binary:      state.options.Binary,
		Arguments:   s.commandArguments(arguments),
		Environment: state.options.ProcessEnvironment,
		Stdio:       stdio,
	})
}

// runExactArgv routes a complete tmux argv that carries its own client-global
// options, such as the tmux -V version probe. It is always [CommandProcess].
func (s Server) runExactArgv(
	ctx context.Context,
	arguments []string,
) (tmuxcmd.Result, error) {
	state := s.connectionState()
	engine, err := s.commandEngine(CommandProcess)
	if err != nil {
		return tmuxcmd.Result{ExitCode: -1}, err
	}
	if engine != nil {
		// An exact argv is already what tmux should receive, so nothing here
		// re-escapes it.
		return runEngine(ctx, engine, CommandProcess, state.options, arguments, nil, true)
	}
	return state.runner.Run(ctx, tmuxcmd.Request{
		Binary:      state.options.Binary,
		Arguments:   arguments,
		Environment: state.options.ProcessEnvironment,
	})
}

// runEngine copies across the engine boundary in both directions, so caller
// code can retain or modify what it receives without reaching library state.
func runEngine(
	ctx context.Context,
	engine Engine,
	kind CommandKind,
	options ServerOptions,
	arguments []string,
	stdio *tmuxcmd.Stdio,
	commandList bool,
) (tmuxcmd.Result, error) {
	result, err := engine.Run(ctx, kind, CommandRequest{
		Binary:      options.Binary,
		Arguments:   slices.Clone(arguments),
		Environment: slices.Clone(options.ProcessEnvironment),
		Stdio:       exportCommandStdio(stdio),
		CommandList: commandList,
	})
	return tmuxcmd.Result{
		Command:   slices.Clone(result.Command),
		Stdout:    slices.Clone(result.Stdout),
		RawStdout: bytes.Clone(result.RawStdout),
		Stderr:    slices.Clone(result.Stderr),
		ExitCode:  result.ExitCode,
	}, err
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
