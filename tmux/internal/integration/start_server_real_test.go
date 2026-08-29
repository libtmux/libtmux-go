//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())), 0o700); err != nil {
		t.Fatalf("Mkdir() = %v", err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName:         "empty",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if alive, err := server.IsAlive(ctx); err != nil || alive {
		t.Fatalf("IsAlive() = (%t, %v), want (false, nil) before anything runs", alive, err)
	}

	// Each of these starts a server that then exits again at once, having no
	// sessions to hold it open. A call landing while the last one is on its
	// way out is answered "server exited unexpectedly", which is the previous
	// server's teardown rather than this call failing to start its own, so it
	// is worth asking a second time before believing it.
	retrying := func(what string, call func() (int, error)) int {
		rows, err := call()
		if err != nil && strings.Contains(err.Error(), "server exited unexpectedly") {
			rows, err = call()
		}
		if err != nil {
			t.Fatalf("%s() = %v", what, err)
		}
		return rows
	}
	if rows := retrying("ListKeys", func() (int, error) {
		keys, err := server.ListKeys(ctx, tmux.ListKeysRequest{})
		return len(keys), err
	}); rows == 0 {
		t.Error("ListKeys() returned no rows")
	}
	if rows := retrying("ListCommands", func() (int, error) {
		commands, err := server.ListCommands(ctx, tmux.ListCommandsRequest{})
		return len(commands), err
	}); rows == 0 {
		t.Error("ListCommands() returned no rows")
	}

	// The server it started held no sessions, so it goes on its own and the
	// socket it leaves behind must not read as one that is up. Going is not
	// gone: the exit is the server's to schedule, so this waits for it rather
	// than assuming it has already happened by the time the next call runs.
	deadline := time.Now().Add(10 * time.Second)
	for {
		alive, err := server.IsAlive(ctx)
		if err == nil && !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("IsAlive() = (%t, %v) after the throwaway server exited, want (false, nil)",
				alive, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := server.Sessions(ctx); !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("Sessions() = %v, want ErrNoServer", err)
	}
}
