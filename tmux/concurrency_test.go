package tmux

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestServerHandleSupportsConcurrentCommands(t *testing.T) {
	t.Parallel()

	const workers = 24

	// The test binary starts and drains more slowly than tmux. Give the stand-in
	// more time without weakening the transport's truncated-read guard.
	const drainStandInOutput = 30 * time.Second

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dependencies := defaultServerDependencies()
	dependencies.executor = tmuxcmd.Runner{WaitDelay: drainStandInOutput}
	server, err := newServer(ServerOptions{Binary: executable}, dependencies)
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	// Command startup speed is not under test, but a hung command must stay bounded.
	const perCommand = 60 * time.Second

	errors := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), perCommand)
			defer cancel()
			want := strconv.Itoa(worker)
			result, err := server.Cmd(
				ctx,
				"-test.run=^TestServerCommandHelperProcess$",
				"--",
				"echo",
				want,
			)
			if err != nil {
				errors <- err
				return
			}
			if len(result.Stdout) != 1 || result.Stdout[0] != want {
				errors <- fmt.Errorf("concurrent command returned %#v, want stdout %q", result, want)
			}
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestSnapshotSupportsConcurrentReadAndFreshSliceMutation(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	const workers = 24
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for range 100 {
				sessions := snapshot.Sessions()
				windows := snapshot.Windows()
				panes := snapshot.Panes()
				clients := snapshot.Clients()
				sessions[0].sessionID = "$99"
				windows[0].windowIndex = 99
				panes[0].paneIndex = 99
				clients[0].clientName = "mutated"

				beta, err := snapshot.SessionByID("$1")
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = beta.ActiveWindow()
				_, _ = beta.ActivePane()
				_, _ = snapshot.WindowsByID("@0")[0].LinkedSessions()
				client, err := snapshot.ClientByName("/dev/pts/9")
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = client.AttachedPane()
			}
		})
	}
	group.Wait()
	if got := snapshot.Sessions()[0].sessionID; got != "$0" {
		t.Fatalf("shared snapshot mutated to %q", got)
	}
}
