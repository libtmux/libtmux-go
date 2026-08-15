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

	const workers = 24

	// This test stands in for tmux with this test binary, which is a far heavier
	// program than tmux: it starts the testing framework before it echoes
	// anything. Twenty-four of those at once on a small machine can exit before
	// the parent has finished draining their pipes, and os/exec then reports
	// "WaitDelay expired before I/O complete" -- refused rather than normalised,
	// because a read that stopped early must never be reported as a command that
	// said nothing. The delay is raised here to fit the stand-in rather than
	// lowered in the transport, where it protects real callers from a tmux that
	// exits holding a pipe open.
	const drainStandInOutput = 30 * time.Second

	server := NewServer(ServerOptions{
		Binary: os.Args[0],
		Runner: subprocessRunner(drainStandInOutput),
	})

	// Generous, and not part of what is being tested: what is asserted below is
	// that concurrent commands each get their own answer back, not how quickly
	// the machine can start them. It stays bounded so a command that never
	// returns still fails here rather than hanging until the test binary is
	// killed.
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
