package tmux

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestServerHandleSupportsConcurrentCommands(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Binary: os.Args[0]})
	const workers = 24

	// Generous, because this deadline is not part of what is being tested. What
	// is asserted below is that concurrent commands each get their own answer
	// back; how long the machine takes to start 24 processes is not a property
	// of the code under test. A short deadline instead asserts that the machine
	// is fast, and on a two-core runner starting 24 copies of this test binary
	// it fails: the context ends the process, its pipes do not drain inside the
	// runner's WaitDelay, and the error is "WaitDelay expired before I/O
	// complete" rather than anything about concurrency. It stays bounded so a
	// command that never returns still fails here rather than hanging until the
	// whole test binary is killed.
	const perCommand = 60 * time.Second

	errors := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
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
		}()
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
		group.Add(1)
		go func() {
			defer group.Done()
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
				_ = snapshot.WindowsByID("@0")[0].LinkedSessions()
				client, err := snapshot.ClientByName("/dev/pts/9")
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = client.AttachedPane()
			}
		}()
	}
	group.Wait()
	if got := snapshot.Sessions()[0].sessionID; got != "$0" {
		t.Fatalf("shared snapshot mutated to %q", got)
	}
}
