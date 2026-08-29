package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// CommandKind names what one tmux request needs from the transport that runs
// it. A [Server] asks its [Engine] whether it supports a request's kind and
// normally falls back to a tmux process when it does not. A handle using
// [EngineFallbackReject] refuses that fallback instead.
//
// An engine must report a kind it does not recognize as unsupported. Later
// kinds are therefore additive: an engine written before one existed keeps
// routing it to a tmux process under the default fallback policy.
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

// Engine executes tmux commands for a [Server] over one transport, and
// declares which [CommandKind] values that transport can carry. Selecting one
// with [Server.WithEngine] changes how commands reach tmux without changing
// what any operation means. By default, a request the engine does not support
// runs as a tmux process through [ServerOptions.Runner];
// [Server.WithEngineFallback] can reject that route.
//
// An engine does not own its own shutdown. A [Server] is an immutable handle
// that callers copy freely, so it cannot be the value that closes a transport;
// whoever created the transport closes it. [ControlClient.Close] stops the
// process behind [ControlClient.Engine].
//
// Run may be called concurrently when the Server is used concurrently. An
// engine that serializes internally, as the control-mode engine does, bounds
// concurrent callers to one in-flight tmux command; that is an engine property
// rather than an interface one, so a transport that matches replies out of
// order needs no change here.
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

// WithEngine returns a handle whose supported commands run through engine
// rather than through a tmux process. A nil engine restores process execution.
// The returned handle shares immutable configuration and version-cache
// coordination with s; derived sessions, windows, and panes keep the engine.
//
// The engine is not adopted: s keeps forking, and closing the engine's
// transport is the caller's job. Under the default fallback policy, operations
// the engine does not support and reads that promise exact stdout bytes keep
// running as tmux processes on the returned handle.
//
// A record carries the handle that produced it, so sessions, windows, and
// panes obtained before this call keep forking and report no error while doing
// so. Look one up again through the returned handle, with [Server.Session],
// [Server.Window], or [Server.Pane], to move it onto the engine.
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

// Engine returns the engine this handle routes through, or nil when every
// command starts a tmux process.
//
// It is the read half of [Server.WithEngine], and exists so that code handed a
// Server can tell whether its caller already chose a transport. A library that
// opens a connection of its own should not do so on a handle whose owner has
// already decided: passing [Server.SubprocessEngine] is how that owner says to
// stay on processes, and silently overriding it would make the choice
// unobservable.
func (s Server) Engine() Engine { return s.engine }

// SubprocessEngine returns the [Engine] that runs every request as its own
// tmux process, through this server's configured [ServerOptions.Runner]. It is
// what a handle with no engine already does, as a value: passing it to
// [Server.WithEngine] restores process execution on a handle derived from one
// that selected another engine.
//
// It is also how a caller declines a connection a library would otherwise open
// for them. A handle carrying this engine has chosen its transport, and code
// that checks [Server.Engine] leaves that choice alone, so it is the way to say
// no to something that would attach a tmux client.
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

// InstanceBoundEngine is an optional interface an [Engine] may implement to
// report that its transport cannot outlive the tmux server instance it talks
// to. A connection is bound: tmux gives a client no way to survive its server,
// so a client that answers at all answers from the instance it was opened
// against, and a replacement server on the same socket cannot be reached
// through it. A tmux process is not bound, because each one connects afresh
// and two of them may reach two servers that owned the socket in turn.
//
// Snapshot reads use it to skip a second identity probe whose only job is to
// prove what a bound transport already guarantees. An engine that does not
// implement it, or reports false, is read exactly as before.
//
// An engine that wraps another must forward this, or the transport underneath
// silently loses the property and pays for the probe again.
type InstanceBoundEngine interface {
	// InstanceBound reports whether consecutive commands this engine carried
	// provably reached one tmux server instance.
	InstanceBound() bool
}

// boundToInstance reports whether this handle's transport already proves that
// consecutive commands reached one tmux server instance. Snapshot reads use it
// to skip a second identity probe whose only job is to prove exactly that.
func (s Server) boundToInstance() bool {
	bound, ok := s.engine.(InstanceBoundEngine)
	return ok && bound.InstanceBound()
}

// engineDecliner reports why an engine turned a command down, when the reason
// is one a caller would want to hear about. An engine that simply never
// carries a kind is not news; one that stopped carrying it is.
type engineDecliner interface {
	declined(kind CommandKind) (Warning, bool)
}

// commandEngine returns the engine that will carry kind, or nil for a process.
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

// runCommand routes one tmux command. args holds the tmux command; the
// configured client-global selectors are added wherever the transport needs
// them, which is everywhere except an engine already connected to the server.
//
// commandList reports whether a bare ";" in args separates two commands. It
// decides one thing here: a tmux process hands its argv to tmux's outer command
// parser, so a value that ends in a semicolon is escaped on the way to one and
// left alone on the way to an engine, which quotes its arguments instead.
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
