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

// TestTheTwoListsThatAnswerWithoutAServer covers the exception to the rule that
// a list against an empty socket reports no server.
//
// tmux marks list-keys and list-commands as starting a server, so a client runs
// them against one it starts for the purpose. The server holds no sessions and
// exits immediately, leaving its socket file behind. Every other list this
// package wraps reports [tmux.ErrNoServer] instead, and a caller reaching for
// one of these two to ask whether a server is up gets an answer to a different
// question.
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
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName:         "empty",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "PATH=" + os.Getenv("PATH")},
	})

	if alive, err := server.IsAlive(ctx); err != nil || alive {
		t.Fatalf("IsAlive() = (%t, %v), want (false, nil) before anything runs", alive, err)
	}

	keys, err := server.ListKeys(ctx, tmux.ListKeysRequest{})
	if err != nil {
		t.Fatalf("ListKeys() = %v, want the bindings of a server tmux starts", err)
	}
	if len(keys) == 0 {
		t.Error("ListKeys() returned no rows")
	}

	commands, err := server.ListCommands(ctx, tmux.ListCommandsRequest{})
	if err != nil {
		t.Fatalf("ListCommands() = %v", err)
	}
	if len(commands) == 0 {
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
