package tmux_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	for _, argument := range os.Args[1:] {
		if argument == "-test.run=^TestServerCommandHelperProcess$" {
			os.Exit(m.Run())
		}
	}
	os.Exit(tmuxtest.Main(m))
}

// libtmux:parity libtmux.server.Server.is_alive
// libtmux:parity libtmux.server.Server.raise_if_dead
//
//libtmux:real-tmux
func TestServerLivenessDistinguishesDeadFromTransportFailure(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)

	alive, err := server.IsAlive(context.Background())
	if err != nil || !alive {
		t.Fatalf("IsAlive() = (%v, %v), want (true, nil)", alive, err)
	}
	result, err := server.Cmd(context.Background(), "kill-server")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("kill-server = (%#v, %v), want exit 0", result, err)
	}

	alive, err = server.IsAlive(context.Background())
	if err != nil || alive {
		t.Fatalf("IsAlive() = (%v, %v), want (false, nil)", alive, err)
	}
	err = server.RaiseIfDead(context.Background())
	if !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("RaiseIfDead() error = %v, want ErrCommand", err)
	}
	var commandError *tmux.CommandError
	if !errors.As(err, &commandError) || commandError.Result.ExitCode == 0 {
		t.Fatalf("RaiseIfDead() error = %#v, want CommandError with failed result", err)
	}
}

func TestServerIsAliveReturnsContextError(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	alive, err := server.IsAlive(ctx)
	if !errors.Is(err, context.Canceled) || alive {
		t.Fatalf("IsAlive() = (%v, %v), want (false, context.Canceled)", alive, err)
	}
}

// libtmux:parity libtmux.exc.TmuxCommandNotFound
func TestNewServerRejectsMissingExecutableBeforeLiveness(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-tmux")
	_, err := tmux.NewServer(tmux.ServerOptions{Binary: missing})
	var executableError *exec.Error
	if !errors.As(err, &executableError) || executableError.Name != missing {
		t.Fatalf("NewServer() error = %#v, want *exec.Error for %q", err, missing)
	}
}

// TestNewServerReportsEveryUnresolvableExecutableSpelling covers both an
// explicit missing path and a name absent from the frozen PATH. Both are
// constructor failures that retain the requested spelling in an *exec.Error.
func TestNewServerReportsEveryUnresolvableExecutableSpelling(t *testing.T) {
	for _, binary := range []string{
		filepath.Join(t.TempDir(), "missing-tmux"),
		"libtmux-go-definitely-not-on-path",
	} {
		_, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
		var executableError *exec.Error
		if !errors.As(err, &executableError) || executableError.Name != binary {
			t.Errorf("NewServer(%q) error = %#v, want *exec.Error for requested spelling", binary, err)
		}
	}
}

// An unusable socket must return an error, not an empty collection. tmux cannot
// distinguish these cases from an absent daemon, so [tmux.ErrNoServer] covers
// all of them; creation must still fail rather than use another socket.
func TestServerSessionsReportsEveryUnusableSocket(t *testing.T) {
	directory := t.TempDir()

	regular := filepath.Join(directory, "regular-file")
	if err := os.WriteFile(regular, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	unreadable := filepath.Join(directory, "unreadable")
	if err := os.WriteFile(unreadable, []byte("not a socket"), 0o000); err != nil {
		t.Fatalf("write unreadable file: %v", err)
	}
	subdirectory := filepath.Join(directory, "directory")
	if err := os.Mkdir(subdirectory, 0o755); err != nil {
		t.Fatalf("make directory: %v", err)
	}

	for _, test := range []struct {
		name       string
		socketPath string
	}{
		{name: "absent socket", socketPath: filepath.Join(directory, "absent.sock")},
		{name: "regular file", socketPath: regular},
		{name: "unreadable file", socketPath: unreadable},
		{name: "directory", socketPath: subdirectory},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := mustNewServer(t, tmux.ServerOptions{SocketPath: test.socketPath})

			sessions, err := server.Sessions(context.Background())
			if err == nil {
				t.Fatalf("Sessions() = (%#v, nil), want a reported failure", sessions)
			}
			if sessions != nil {
				t.Fatalf("Sessions() = %#v, want no sessions beside an error", sessions)
			}
			if !errors.Is(err, tmux.ErrNoServer) {
				t.Fatalf("Sessions() error = %v, want ErrNoServer", err)
			}
			if alive, err := server.IsAlive(context.Background()); alive || err != nil {
				t.Fatalf("IsAlive() = (%t, %v), want (false, nil)", alive, err)
			}
		})
	}

	// Acting on ErrNoServer is only safe if creating what was not found still
	// fails on a socket that exists and cannot be used.
	unusable := mustNewServer(t, tmux.ServerOptions{SocketPath: unreadable})
	session, err := unusable.NewSession(context.Background(), tmux.NewSessionRequest{Name: "probe"})
	if err == nil {
		_ = unusable.Kill(context.Background())
		t.Fatalf("NewSession() on an unreadable socket = (%#v, nil), want tmux's refusal", session)
	}
}

// Server shutdown races between connect failure and lost-connection output.
// ErrNoServer must classify both so liveness checks are timing-independent.
func TestErrNoServerCoversEveryWayTmuxSaysItIsGone(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stderr string
		want   bool
	}{
		{name: "connection refused", stderr: "no server running on /tmp/s", want: true},
		{name: "socket absent", stderr: "error connecting to /tmp/s (No such file or directory)", want: true},
		{name: "socket unreadable", stderr: "error connecting to /tmp/s (Permission denied)", want: true},
		{name: "socket uncreatable", stderr: "error creating /tmp/s (Permission denied)", want: true},
		{name: "connection lost", stderr: "server exited unexpectedly", want: true},
		{name: "server shut down", stderr: "server exited", want: true},
		{name: "a missing target", stderr: "can't find window: @99", want: false},
		{name: "a client detaching", stderr: "detached (from session $0)", want: false},
		{name: "a lost terminal", stderr: "lost tty", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := tmux.CommandRunnerFunc(func(
				context.Context, tmux.CommandRequest,
			) (tmux.CommandResult, error) {
				return tmux.CommandResult{Stderr: []string{test.stderr}, ExitCode: 1}, nil
			})
			server := mustNewServer(t, tmux.ServerOptions{
				Binary: testExecutable(t),
				Runner: runner,
			})

			_, err := server.Sessions(context.Background())
			if err == nil {
				t.Fatal("Sessions() error = nil, want a reported failure")
			}
			if got := errors.Is(err, tmux.ErrNoServer); got != test.want {
				t.Fatalf("errors.Is(%q, ErrNoServer) = %t, want %t", test.stderr, got, test.want)
			}
		})
	}
}

func TestServerVersionMatchesConfiguredTmuxBinary(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	result, err := server.Cmd(ctx, "-V")
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
		t.Fatalf("tmux -V = (%#v, %v), want one successful line", result, err)
	}
	want := strings.TrimPrefix(result.Stdout[0], "tmux ")
	if version.String() != want {
		t.Fatalf("Version().String() = %q, want %q", version, want)
	}
}

func TestServerVersionRedactsMalformedProxyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the malformed-version proxy is a POSIX shell script")
	}
	const secret = "private-proxy-version"
	proxy := filepath.Join(t.TempDir(), "tmux-version-proxy")
	if err := os.WriteFile(proxy, []byte("#!/bin/sh\nprintf '%s\\n' 'tmux "+secret+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := mustNewServer(t, tmux.ServerOptions{Binary: proxy})
	_, err := server.Version(context.Background())
	if !errors.Is(err, tmux.ErrVersionQuery) || errors.Is(err, tmux.ErrInvalidVersion) {
		t.Fatalf("Server.Version() error = %v, want only ErrVersionQuery", err)
	}
	var queryError *tmux.VersionQueryError
	if !errors.As(err, &queryError) {
		t.Fatalf("Server.Version() error = %#v, want *VersionQueryError", err)
	}
	encoded, marshalErr := json.Marshal(queryError)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, representation := range []string{err.Error(), fmt.Sprintf("%#v", err), string(encoded)} {
		if strings.Contains(representation, secret) {
			t.Fatalf("Server.Version() error retained proxy output: %s", representation)
		}
	}
}
