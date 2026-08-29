package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestConnectionBoundServerCannotEscapeToAProcess(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	pool := &controlLanePool{
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
				commandProcess,
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

func TestServerReportsConnectionBinding(t *testing.T) {
	t.Parallel()

	plain := serverWithRunner(&versionQueueRunner{})
	if plain.ConnectionBound() {
		t.Fatal("plain server reports a terminal connection")
	}
	connection := &Connection{}
	bound := plain
	bound.connection = connection
	connection.server = bound
	if !bound.ConnectionBound() {
		t.Fatal("connection server does not report its terminal binding")
	}
}

func TestControlLaneCountValidationNamesTheCallerField(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	session := Session{server: server, sessionID: "$1"}
	connection, err := session.OpenControl(
		context.Background(),
		ConnectionOptions{Lanes: -1},
	)
	if connection != nil {
		_ = connection.Close()
		t.Fatal("OpenControl() returned a connection for negative lanes")
	}
	var requestError *ServerCommandRequestError
	if !errors.As(err, &requestError) || requestError.Field != "Lanes" {
		t.Fatalf("OpenControl() error = %#v, want rejected Lanes field", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want validation before tmux I/O", calls)
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

func TestControlDialectAdaptsClientFlags(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version string
		mode    controlNotificationMode
		want    []string
	}{
		{name: "3.2a notifications", version: "3.2a", mode: controlNotificationsRetained},
		{name: "3.2a commands", version: "3.2a", mode: controlNotificationsDiscarded, want: []string{"no-output"}},
		{name: "3.5 commands", version: "3.5", mode: controlNotificationsDiscarded, want: []string{"no-output"}},
		{name: "3.6 notifications", version: "3.6", mode: controlNotificationsRetained, want: []string{"no-detach-on-destroy"}},
		{name: "3.6 commands", version: "3.6", mode: controlNotificationsDiscarded, want: []string{"no-output", "no-detach-on-destroy"}},
		{name: "3.7 commands", version: "3.7", mode: controlNotificationsDiscarded, want: []string{"no-output", "no-detach-on-destroy"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dialect := controlDialect{version: mustParseVersion(t, test.version)}
			if got := dialect.clientFlags(test.mode); !slices.Equal(got, test.want) {
				t.Fatalf("client flags = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNewSessionConnectionArgumentsUseControlDialect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version string
		want    string
	}{
		{version: "3.2a", want: "-fno-output"},
		{version: "3.5", want: "-fno-output"},
		{version: "3.6", want: "-fno-output,no-detach-on-destroy"},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			arguments, _, err := newSessionConnectionArguments(
				NewSessionRequest{},
				controlDialect{version: mustParseVersion(t, test.version)},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(arguments, test.want) {
				t.Fatalf("new-session arguments = %#v, want %q", arguments, test.want)
			}
		})
	}
}

func TestConnectionRejectsTmuxBeforeSupportedFloor(t *testing.T) {
	t.Parallel()

	response := versionResponse{result: tmuxcmd.Result{
		Stdout: []string{"tmux 3.1"}, ExitCode: 0,
	}}
	assertFloor := func(t *testing.T, err error, calls int) {
		t.Helper()
		var tooLow *VersionTooLowError
		if !errors.Is(err, ErrVersionTooLow) || !errors.As(err, &tooLow) {
			t.Fatalf("connection error = %v, want VersionTooLowError", err)
		}
		if tooLow.Current.String() != "3.1" ||
			tooLow.Minimum.String() != MinimumConnectionVersion {
			t.Fatalf(
				"version floor = %s -> %s, want 3.1 -> %s",
				tooLow.Current,
				tooLow.Minimum,
				MinimumConnectionVersion,
			)
		}
		if calls != 1 {
			t.Fatalf("runner calls = %d, want only tmux -V", calls)
		}
	}

	t.Run("existing session", func(t *testing.T) {
		runner := &versionQueueRunner{responses: []versionResponse{response}}
		server := serverWithRunner(runner)
		connection, err := (Session{server: server, sessionID: "$1"}).OpenControl(
			context.Background(),
			ConnectionOptions{},
		)
		if connection != nil {
			_ = connection.Close()
			t.Fatal("OpenControl() returned a connection below the supported floor")
		}
		assertFloor(t, err, runner.callCount())
	})

	t.Run("new session", func(t *testing.T) {
		runner := &versionQueueRunner{responses: []versionResponse{response}}
		server := serverWithRunner(runner)
		created, connection, err := server.NewSessionConnection(
			context.Background(),
			NewSessionRequest{},
			ConnectionOptions{},
		)
		if created.ID() != "" || connection != nil {
			if connection != nil {
				_ = connection.Close()
			}
			t.Fatalf("NewSessionConnection() = (%#v, %#v), want zero and nil", created, connection)
		}
		assertFloor(t, err, runner.callCount())
	})
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
	arguments, fields, err := newSessionConnectionArguments(
		NewSessionRequest{},
		controlDialect{version: mustParseVersion(t, "3.5")},
	)
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
	connection := &Connection{pool: &controlLanePool{stopped: stopped}}
	server.connection = connection
	connection.server = server

	if _, err := server.Cmd(
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

func TestControlLanePoolNeverLeasesAfterStop(t *testing.T) {
	t.Parallel()

	for range 100 {
		stopped := make(chan struct{})
		close(stopped)
		free := make(chan *ControlClient, 1)
		free <- &ControlClient{}
		pool := &controlLanePool{
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
