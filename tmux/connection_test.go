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

func TestNewSessionConnectionRejectsBeforeStartingTmux(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		call func() error
		want error
	}{
		"negative lanes": {
			call: func() error {
				_, _, err := (Server{}).NewSessionConnection(
					context.Background(),
					NewSessionRequest{},
					ConnectionOptions{Lanes: -1},
				)
				return err
			},
			want: ErrInvalidServerCommandRequest,
		},
		"kill existing": {
			call: func() error {
				_, _, err := (Server{}).NewSessionConnection(
					context.Background(),
					NewSessionRequest{Name: "existing", KillExisting: true},
					ConnectionOptions{},
				)
				return err
			},
			want: ErrInvalidRequest,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, test.want) {
				t.Fatalf("NewSessionConnection() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewSessionFrameNormalizesOpenBSDIdentityVersion(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux openbsd-7.8"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{
			"display-popup (popup) [-CE]",
			"confirm-before (confirm) [-by] [-c confirm-key] command",
			"copy-mode [-deHMqSu] [-t target-pane]",
		}, ExitCode: 0}},
	}}
	server := serverWithRunner(runner)
	arguments, fields, err := newSessionConnectionArguments(NewSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	client := &ControlClient{
		frames:  make(chan controlFrame, 1),
		closing: make(chan struct{}),
	}
	client.frames <- controlFrame{rawStdout: framedSnapshotRecord(
		fields,
		snapshotRowValues(mustParseVersion(t, "3.7"), map[string]string{
			"session_id": "$7",
			"version":    "openbsd-7.8",
		}),
	)}

	created, err := server.acceptNewSessionFrame(
		context.Background(),
		client,
		nil,
		arguments,
		fields,
	)
	if err != nil {
		t.Fatalf("acceptNewSessionFrame() error = %v", err)
	}
	want := mustParseVersion(t, "3.5")
	if created.ID() != "$7" || created.server.daemon == nil ||
		created.server.daemon.version.String() != "openbsd-7.8" ||
		created.server.daemon.version.Compare(want) != 0 {
		t.Fatalf(
			"accepted session = %#v, want $7 with raw openbsd-7.8 at %s",
			created,
			want,
		)
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
