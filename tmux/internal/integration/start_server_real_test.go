//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// list-keys and list-commands start a transient server on an empty socket, so
// their success cannot establish that a daemon was already alive.
func TestTheTwoListsThatAnswerWithoutAServer(t *testing.T) {
	t.Parallel()

	for _, one := range []struct {
		name string
		list func(context.Context, tmux.Server) (int, error)
	}{
		{name: "ListKeys", list: func(ctx context.Context, server tmux.Server) (int, error) {
			rows, err := server.ListKeys(ctx, tmux.ListKeysRequest{})
			return len(rows), err
		}},
		{name: "ListCommands", list: func(ctx context.Context, server tmux.Server) (int, error) {
			rows, err := server.ListCommands(ctx, tmux.ListCommandsRequest{})
			return len(rows), err
		}},
	} {
		t.Run(one.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
			root, err := os.MkdirTemp("", "ltg-cold-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			if err := os.Mkdir(filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())), 0o700); err != nil {
				t.Fatalf("Mkdir() = %v", err)
			}
			server, err := tmux.NewServer(tmux.ServerOptions{
				SocketName:         "empty",
				ConfigFile:         os.DevNull,
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "PATH=" + os.Getenv("PATH")},
			})
			if err != nil {
				t.Fatalf("NewServer() error = %v", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cleanupCancel()
				_ = server.Kill(cleanupCtx)
			})

			if alive, err := server.IsAlive(ctx); err != nil || alive {
				t.Fatalf("IsAlive() = (%t, %v), want (false, nil) before anything runs", alive, err)
			}
			// Separate fresh endpoints keep the next list from racing the previous
			// daemon's shutdown. A stale socket file is allowed after it exits.
			if rows, err := one.list(ctx, server); err != nil || rows == 0 {
				t.Fatalf("%s() = %d rows, %v", one.name, rows, err)
			}
			if err := tmux.Poll(ctx, 5*time.Millisecond, func(ctx context.Context) (bool, error) {
				alive, err := server.IsAlive(ctx)
				return !alive, err
			}); err != nil {
				t.Fatalf("transient daemon did not exit: %v", err)
			}
			if _, err := server.Sessions(ctx); !errors.Is(err, tmux.ErrNoServer) {
				t.Fatalf("Sessions() = %v, want ErrNoServer", err)
			}
		})
	}
}

// kill-server is answered before the daemon exits, and the daemon drops every
// connection it accepts until then, so a replacement started at once read
// "server exited unexpectedly" instead of starting. Kill returns after that
// gap. The gap is milliseconds on an idle machine and fails a round rarely, so
// the loop runs on request; pin it to one core beside a busy loop to widen the
// gap:
//
//	LIBTMUX_TEST_SLOW=1 taskset -c 0 go test -run TestKillThenNewSession ./tmux/internal/integration/
func TestKillThenNewSessionNeverMeetsTheDyingDaemon(t *testing.T) {
	if os.Getenv("LIBTMUX_TEST_SLOW") == "" {
		t.Skip("slow: set LIBTMUX_TEST_SLOW=1 to run the kill and replace loop")
	}
	rounds := 200
	if value, err := strconv.Atoi(os.Getenv("LIBTMUX_TEST_KILL_ROUNDS")); err == nil && value > 0 {
		rounds = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	root, err := os.MkdirTemp("", "ltg-kill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(root, "s"), ConfigFile: os.DevNull,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = server.Kill(cleanupCtx)
	})
	for round := range rounds {
		// Cmd, not NewSession: it reuses the identity of the daemon the last
		// command found, so nothing slows it between Kill and the new-session
		// that replaces the daemon.
		result, err := server.Cmd(
			ctx, "new-session", "-d", "-s", "round-"+strconv.Itoa(round), "sleep 300",
		)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("round %d: new-session = %+v, %v", round, result, err)
		}
		if err := server.Kill(ctx); err != nil {
			t.Fatalf("round %d: Kill() = %v", round, err)
		}
	}
}
