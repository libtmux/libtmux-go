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
				server: tmux.NewServer(tmux.ServerOptions{Binary: binary}).WithStrictErrors(),
			},
		} {
			sessions, err := mode.server.Sessions(context.Background())
			var executableError *exec.Error
			if !errors.As(err, &executableError) {
				t.Errorf("%s Sessions() with %q error = %v, want an *exec.Error",
					mode.name, binary, err)
			}
			if len(sessions) != 0 {
				t.Errorf("%s Sessions() with %q = %#v, want no rows", mode.name, binary, sessions)
			}
		}
	}
}

// TestServerSessionsNormalizesAnUnreachableServerInLenientMode pins the other
// side of that boundary. A server that is not running is ordinary runtime
// state, so the lenient default still reports it as an empty collection.
func TestServerSessionsNormalizesAnUnreachableServerInLenientMode(t *testing.T) {
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "absent.sock"),
	})

	sessions, err := server.Sessions(context.Background())
	if err != nil || sessions == nil || len(sessions) != 0 {
		t.Fatalf("lenient Sessions() = (%#v, %v), want nonnil empty result", sessions, err)
	}
	if _, err := server.WithStrictErrors().Sessions(context.Background()); err == nil {
		t.Fatal("strict Sessions() error = nil, want a command failure")
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
