package tmux

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestCommandObserverReportsEveryCommand(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	traces := []CommandTrace{}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"can't find pane: %7"}, ExitCode: 1}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		CommandObserver: func(trace CommandTrace) {
			mutex.Lock()
			defer mutex.Unlock()
			traces = append(traces, trace)
		},
	}, runner)

	if _, err := server.Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if err := paneWithExactTestTarget(server).Kill(context.Background()); !errors.Is(err, ErrCommand) {
		t.Fatalf("Kill() error = %v, want ErrCommand", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(traces) != 2 {
		t.Fatalf("traces = %#v, want one per command", traces)
	}
	if traces[0].Subcommand != "list-sessions" || traces[0].ExitCode != 0 {
		t.Errorf("traces[0] = %v, want list-sessions exiting 0", traces[0])
	}
	// A completed tmux failure is reported through ExitCode, not Err, the way
	// CommandResult reports one.
	if traces[1].Subcommand != "kill-pane" || traces[1].ExitCode != 1 || traces[1].Err != nil {
		t.Errorf("traces[1] = %v, want kill-pane exiting 1 with no transport error", traces[1])
	}
	for index, trace := range traces {
		if trace.Transport != CommandTransportProcess {
			t.Errorf("traces[%d].Transport = %v, want process", index, trace.Transport)
		}
		if trace.Duration < 0 {
			t.Errorf("traces[%d].Duration = %v, want a measured duration", index, trace.Duration)
		}
	}
}

// A trace must not carry what CommandError redacts, so neither the arguments
// nor tmux's output can reach an observer through it.
func TestCommandTraceCarriesNoArgumentsOrOutput(t *testing.T) {
	t.Parallel()

	secret := "credential-material"
	var seen []CommandTrace
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{secret}, ExitCode: 0}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		CommandObserver: func(trace CommandTrace) { seen = append(seen, trace) },
	}, runner)
	if _, err := server.Cmd(context.Background(), "show-buffer", secret); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("traces = %#v, want one", seen)
	}
	if rendered := seen[0].String(); strings.Contains(rendered, secret) {
		t.Fatalf("trace rendered the caller's argument: %s", rendered)
	}
}

// The observer's contract is every tmux command, which includes the version
// probe this package runs on its own behalf. Whether a command carried over a
// control lane is observed once rather than twice needs a real daemon, and
// TestOneCommandIsObservedOnceOverEitherTransport covers it.
func TestCommandObserverSeesTheVersionProbe(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	traces := []CommandTrace{}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		CommandObserver: func(trace CommandTrace) {
			mutex.Lock()
			defer mutex.Unlock()
			traces = append(traces, trace)
		},
	}, runner)

	if _, err := server.Version(context.Background()); err != nil {
		t.Fatalf("Version() error = %v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(traces) != 1 {
		t.Fatalf("traces = %#v, want the version probe", traces)
	}
	if traces[0].Subcommand != "-V" || traces[0].Transport != CommandTransportProcess {
		t.Errorf("traces[0] = %v, want the -V probe over a process", traces[0])
	}
}
