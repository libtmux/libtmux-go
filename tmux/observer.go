package tmux

import (
	"fmt"
	"time"
)

// CommandTransport names how one tmux command reached tmux.
type CommandTransport uint8

const (
	// CommandTransportProcess ran the command as its own tmux process.
	CommandTransportProcess CommandTransport = iota
	// CommandTransportConnection carried the command over a control-mode
	// connection opened earlier.
	CommandTransportConnection
)

// String implements fmt.Stringer.
func (t CommandTransport) String() string {
	switch t {
	case CommandTransportProcess:
		return "process"
	case CommandTransportConnection:
		return "connection"
	default:
		return fmt.Sprintf("CommandTransport(%d)", uint8(t))
	}
}

// CommandTrace records one completed attempt to run a tmux command. It carries
// neither the argument vector nor tmux's output, both of which may hold
// passwords, tokens, or pane contents; [CommandError] is where a caller that
// accepts that reads them.
type CommandTrace struct {
	// Subcommand is the tmux subcommand, such as "list-sessions". A raw
	// command list reports its first subcommand.
	Subcommand string
	// Transport names how the command reached tmux.
	Transport CommandTransport
	// Duration is how long the attempt took.
	Duration time.Duration
	// ExitCode is tmux's exit code, or -1 when no tmux command ran.
	ExitCode int
	// Err is the transport or validation failure, if any. A completed tmux
	// command that failed leaves this nil and reports through ExitCode.
	Err error
}

// String implements fmt.Stringer.
func (t CommandTrace) String() string {
	if t.Err != nil {
		return fmt.Sprintf("tmux %s over %s failed after %s: %v",
			t.Subcommand, t.Transport, t.Duration, t.Err)
	}
	return fmt.Sprintf("tmux %s over %s exited %d after %s",
		t.Subcommand, t.Transport, t.ExitCode, t.Duration)
}

// CommandObserver receives one [CommandTrace] per tmux command a [Server] and
// the values derived from it run:
//
//	server, err := tmux.NewServer(tmux.ServerOptions{
//		CommandObserver: func(trace tmux.CommandTrace) {
//			slog.Info("tmux", "command", trace.Subcommand,
//				"transport", trace.Transport.String(),
//				"duration", trace.Duration, "exit", trace.ExitCode)
//		},
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//
// It is called synchronously on the goroutine that ran the command, after the
// command finishes and before its result is returned, so a slow observer slows
// every command. Operations may invoke it concurrently; synchronize shared
// state. A nil observer costs nothing, not even the clock reads.
//
// It sees the version probes this package runs on its own behalf, and the
// identity probe a daemon guard runs to decide whether a failure was a
// replaced daemon, so one call can produce more than one trace.
type CommandObserver func(CommandTrace)

// observeCommand reports one finished command. subcommand is read from the
// arguments the caller asked for rather than the ones sent, so a daemon guard
// wrapping them does not become the reported command.
func observeCommand(
	observer CommandObserver,
	args []string,
	started time.Time,
	transport CommandTransport,
	exitCode int,
	err error,
) {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	observer(CommandTrace{
		Subcommand: subcommand,
		Transport:  transport,
		Duration:   time.Since(started),
		ExitCode:   exitCode,
		Err:        err,
	})
}

// commandObserver returns the observer this server reports to, or nil.
func (s Server) commandObserver() CommandObserver {
	if s.state == nil {
		return nil
	}
	return s.state.config.commandObserver
}

// commandTransport names how this server's own commands reach tmux.
func (s Server) commandTransport() CommandTransport {
	if s.connection != nil {
		return CommandTransportConnection
	}
	return CommandTransportProcess
}
