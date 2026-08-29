package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestConnectionBoundServerCannotEscapeToAProcess(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	pool := &ControlPool{
		stopped: make(chan struct{}),
		drained: make(chan struct{}),
	}
	connection := &Connection{pool: pool}
	server.connection = connection
	connection.server = server

	for name, call := range map[string]func(Server) error{
		"streaming command": func(bound Server) error {
			_, err := bound.runCommand(
				context.Background(),
				CommandProcess,
				[]string{"attach-session"},
				&tmuxcmd.Stdio{},
				false,
			)
			return err
		},
		"exact argv": func(bound Server) error {
			_, err := bound.runExactArgv(context.Background(), []string{"-V"})
			return err
		},
		"engine reset": func(bound Server) error {
			_, err := bound.WithEngine(nil).runExactArgv(
				context.Background(),
				[]string{"-V"},
			)
			return err
		},
		"subprocess engine": func(bound Server) error {
			_, err := bound.SubprocessEngine().Run(
				context.Background(),
				CommandProcess,
				CommandRequest{Arguments: []string{"-V"}},
			)
			return err
		},
		"socket retarget": func(bound Server) error {
			_, err := bound.WithSocketPath("/tmp/other.sock")
			return err
		},
		"nested control client": func(bound Server) error {
			_, err := bound.OpenControl(
				context.Background(),
				Session{server: bound, sessionID: "$0"},
			)
			return err
		},
		"new-session binding": func(bound Server) error {
			derived, err := newSessionCommandServer(bound)
			if err != nil {
				return err
			}
			_, err = derived.runExactArgv(context.Background(), []string{"-V"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(server); !errors.Is(err, ErrConnectionRequiresProcess) {
				t.Fatalf("operation error = %v, want ErrConnectionRequiresProcess", err)
			}
		})
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("process runner calls = %d, want 0", calls)
	}
}

func TestClosedConnectionBoundServerFailsClosed(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	stopped := make(chan struct{})
	close(stopped)
	connection := &Connection{pool: &ControlPool{stopped: stopped}}
	server.connection = connection
	connection.server = server

	if _, err := server.WithEngine(nil).Cmd(
		context.Background(),
		"display-message",
		"late",
	); !errors.Is(err, ErrControlClosed) {
		t.Fatalf("Cmd() error = %v, want ErrControlClosed", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("process runner calls = %d, want 0", calls)
	}
}

func TestControlPoolNeverLeasesAfterStop(t *testing.T) {
	t.Parallel()

	for range 100 {
		stopped := make(chan struct{})
		close(stopped)
		free := make(chan *ControlClient, 1)
		free <- &ControlClient{}
		pool := &ControlPool{
			free:    free,
			stopped: stopped,
			drained: make(chan struct{}),
		}
		client, err := pool.acquire(context.Background())
		if client != nil || !errors.Is(err, ErrControlClosed) {
			t.Fatalf("acquire() = (%p, %v), want (nil, ErrControlClosed)", client, err)
		}
	}
}
