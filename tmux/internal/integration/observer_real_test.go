package integration

import (
	"context"
	"os"
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
