package integration

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// One command earns one trace on every route. A connection-bound server sends
// its commands down a control lane, which is itself a place a command can be
// observed, so the two have to agree on which of them reports.
//
//libtmux:real-tmux
func TestOneCommandIsObservedOnceOverEitherTransport(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	traces := []tmux.CommandTrace{}
	observe := func(trace tmux.CommandTrace) {
		mutex.Lock()
		defer mutex.Unlock()
		traces = append(traces, trace)
	}

	initial := tmux.NewSessionRequest{Name: "observed"}
	base := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		InitialSession: &initial,
	})
	// The harness owns the daemon and its cleanup; this handle only adds the
	// observer, on the same socket.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath:      base.SocketPath(),
		ConfigFile:      os.DevNull,
		CommandObserver: observe,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one session", len(sessions), err)
	}

	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	defer func() { _ = connection.Close() }()

	for _, route := range []struct {
		name   string
		server tmux.Server
		want   tmux.CommandTransport
	}{
		{name: "process", server: server, want: tmux.CommandTransportProcess},
		{
			name:   "connection",
			server: connection.Server(),
			want:   tmux.CommandTransportConnection,
		},
	} {
		t.Run(route.name, func(t *testing.T) {
			mutex.Lock()
			traces = traces[:0]
			mutex.Unlock()

			if _, err := route.server.Cmd(ctx, "list-sessions"); err != nil {
				t.Fatalf("Cmd() error = %v", err)
			}

			mutex.Lock()
			defer mutex.Unlock()
			if len(traces) != 1 {
				t.Fatalf("one list-sessions produced %d traces, want 1: %v",
					len(traces), traces)
			}
			if traces[0].Subcommand != "list-sessions" || traces[0].Transport != route.want {
				t.Errorf("trace = %v, want list-sessions over %v", traces[0], route.want)
			}
		})
	}
	// Connection.Call is a route of its own into the same lane, and reports
	// its own trace because the lane reports none.
	t.Run("connection call", func(t *testing.T) {
		mutex.Lock()
		traces = traces[:0]
		mutex.Unlock()

		if _, err := connection.Call(ctx, "list-sessions"); err != nil {
			t.Fatalf("Call() error = %v", err)
		}

		mutex.Lock()
		defer mutex.Unlock()
		if len(traces) != 1 {
			t.Fatalf("one Call produced %d traces, want 1: %v", len(traces), traces)
		}
		if traces[0].Subcommand != "list-sessions" ||
			traces[0].Transport != tmux.CommandTransportConnection {
			t.Errorf("trace = %v, want list-sessions over a connection", traces[0])
		}
	})
}

// Reaching a session's or window's current pane is the first thing almost
// every program does, and it must cost one listing, not four - each of
// which asks tmux for every format field.
//
//libtmux:real-tmux
func TestResolvingAnActivePaneListsOnlyItsOwnScope(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var commands []string
	observe := func(trace tmux.CommandTrace) {
		mutex.Lock()
		defer mutex.Unlock()
		commands = append(commands, trace.Subcommand)
	}
	taken := func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		seen := slices.Clone(commands)
		commands = commands[:0]
		return seen
	}

	initial := tmux.NewSessionRequest{Name: "scoped"}
	base := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		InitialSession: &initial,
	})
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath:      base.SocketPath(),
		ConfigFile:      os.DevNull,
		CommandObserver: observe,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one session", len(sessions), err)
	}
	session := sessions[0]
	taken()

	pane, err := session.ResolveActivePane(ctx)
	if err != nil {
		t.Fatalf("Session.ResolveActivePane() error = %v", err)
	}
	if got := listings(taken()); !slices.Equal(got, []string{"list-panes"}) {
		t.Errorf("Session.ResolveActivePane() listed %v, want only list-panes", got)
	}

	window, err := pane.ResolveWindow(ctx)
	if err != nil {
		t.Fatalf("ResolveWindow() error = %v", err)
	}
	taken()
	if _, err := window.ResolveActivePane(ctx); err != nil {
		t.Fatalf("Window.ResolveActivePane() error = %v", err)
	}
	if got := listings(taken()); !slices.Equal(got, []string{"list-panes"}) {
		t.Errorf("Window.ResolveActivePane() listed %v, want only list-panes", got)
	}
}

// listings keeps the commands that read tmux state, dropping the identity
// probes every snapshot brackets itself with.
func listings(commands []string) []string {
	kept := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command, "list-") {
			kept = append(kept, command)
		}
	}
	return kept
}

// Typed text reaches the pane as itself. A semicolon separates tmux commands
// and a backslash escapes in them, so both are ways a send that reshapes its
// arguments corrupts what a caller typed.
//
//libtmux:real-tmux
func TestTypedTextReachesThePaneAsItself(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "cat")
	observation, err := pane.OpenObservation(ctx)
	if err != nil {
		t.Fatalf("OpenObservation() error = %v", err)
	}
	defer func() { _ = observation.Close() }()

	for _, typed := range []string{";", "echo one;", "a;b", `printf 'mark\n'`, `tab\tsep`} {
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &typed}); err != nil {
			t.Fatalf("SendKeys(%q) error = %v", typed, err)
		}
		// cat echoes the submitted line back, so what arrives is what the
		// pane received rather than what tmux was asked to send. The whole
		// line is compared: a substring search for ";" would be satisfied by
		// the backslash-escaped form a mis-escaped send delivers.
		if _, err := observation.WaitFor(ctx, func(text string) bool {
			return slices.Contains(strings.Split(text, "\n"), typed)
		}); err != nil {
			t.Fatalf("WaitFor(%q) error = %v, pane never showed that line", typed, err)
		}
	}
}
