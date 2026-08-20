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
func TestServerIsAliveReturnsProcessStartError(t *testing.T) {
	server := tmux.NewServer(tmux.ServerOptions{
		Binary: filepath.Join(t.TempDir(), "missing-tmux"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	alive, err := server.IsAlive(ctx)
	if err == nil || alive {
		t.Fatalf("IsAlive() = (%v, %v), want (false, process-start error)", alive, err)
	}
}

// TestServerSessionsReportsAnUnresolvableBinaryInBothModes pins the boundary
// collection leniency draws. A binary that cannot be resolved is caller
// configuration rather than server state, so no mode normalizes it. os/exec
// reports the two spellings differently: a name absent from PATH wraps
// exec.ErrNotFound, while an explicit path that does not exist does not, so
// both are covered here and the library classifies on *exec.Error.
func TestServerSessionsReportsAnUnresolvableBinaryInBothModes(t *testing.T) {
	for _, binary := range []string{
		filepath.Join(t.TempDir(), "missing-tmux"),
		"libtmux-go-definitely-not-on-path",
	} {
		for _, mode := range []struct {
			name   string
			server tmux.Server
		}{
			{name: "lenient", server: tmux.NewServer(tmux.ServerOptions{Binary: binary})},
			{
				name:   "strict",
				server: tmux.NewServer(tmux.ServerOptions{Binary: binary}),
			},
		} {
			sessions, err := mode.server.Sessions(context.Background())
			if _, ok := errors.AsType[*exec.Error](err); !ok {
				t.Errorf("%s Sessions() with %q error = %v, want an *exec.Error",
					mode.name, binary, err)
			}
			if len(sessions) != 0 {
				t.Errorf("%s Sessions() with %q = %#v, want no rows", mode.name, binary, sessions)
			}
		}
	}
}

// TestServerSessionsReportsEveryUnusableSocket is the gate on list accessors
// never answering a failure with an empty collection.
//
// Three of these four sockets are a misconfiguration rather than a server
// holding nothing, and none of them can be told apart from an empty server by
// its result alone. Answering any of them with no sessions would send a program
// that builds an environment when it finds none on to build a second one beside
// the environment it was given a wrong path to.
//
// [tmux.ErrNoServer] classifies all four, because tmux reaches no server in all
// four and does not say which kind of nothing it found. The classification is
// safe to act on because creating what was not found reports tmux's own refusal
// rather than succeeding somewhere unintended, which the final case proves.
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
			server := tmux.NewServer(tmux.ServerOptions{SocketPath: test.socketPath})

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
	unusable := tmux.NewServer(tmux.ServerOptions{SocketPath: unreadable})
	session, err := unusable.NewSession(context.Background(), tmux.NewSessionRequest{Name: "probe"})
	if err == nil {
		_ = unusable.Kill(context.Background())
		t.Fatalf("NewSession() on an unreadable socket = (%#v, nil), want tmux's refusal", session)
	}
}

// TestErrNoServerCoversEveryWayTmuxSaysItIsGone pins the classification that a
// real-tmux test can only reach by winning a race.
//
// Killing a server and reading from it immediately produces a connect failure
// or a lost-connection message depending on whether the client had connected
// first, and tmux 3.6 on a loaded machine produces the second. Classifying only
// one of them would make "is anything running" depend on that timing, so both
// are covered and this test states them without needing the race to land.
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
			server := tmux.NewServer(tmux.ServerOptions{Runner: runner})

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
	server := tmux.NewServer(tmux.ServerOptions{Binary: proxy})
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
