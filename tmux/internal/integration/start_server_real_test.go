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
//
//libtmux:real-tmux
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

			root := t.TempDir()
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
