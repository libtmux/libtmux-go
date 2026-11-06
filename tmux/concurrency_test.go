package tmux

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
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
	dependencies.executor = &qualifiedCommandRunner{inner: tmuxcmd.Runner{WaitDelay: drainStandInOutput}}
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

// statelessRunner answers the version probe and succeeds on everything else, so
// the calls under test race only on the Server's own shared state.
type statelessRunner struct{ calls atomic.Int64 }

func (r *statelessRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.calls.Add(1)
	if slices.Contains(request.Arguments, "-V") {
		return tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}, nil
	}
	return tmuxcmd.Result{}, nil
}

// One Server, and the values derived from it, called from many goroutines
// through the fake runner: the first calls race to fill the version cache and
// record the checked daemon identity, which is the shared state the package
// documents as mutex-guarded. Run with -race.
func TestOneServerSupportsConcurrentMethodCalls(t *testing.T) {
	t.Parallel()

	const workers, rounds = 16, 25
	var warnings atomic.Int64
	runner := &statelessRunner{}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported:    DegradeUnsupported,
		WarningHandler: func(Warning) { warnings.Add(1) },
	}, runner)
	pane := paneWithExactTestTarget(server)

	var group sync.WaitGroup
	failures := make(chan error, workers*rounds*4)
	for range workers {
		group.Go(func() {
			ctx := context.Background()
			for round := range rounds {
				if _, err := server.Version(ctx); err != nil {
					failures <- err
				}
				if err := pane.SendKeys(ctx, SendKeysRequest{KeyName: true, Command: Ptr("C-c"), SkipEnter: true}); err != nil {
					failures <- err
				}
				if _, err := pane.Capture(ctx, CapturePaneRequest{}); err != nil {
					failures <- err
				}
				plan := NewPlan()
				plan.SetEnvironment(ServerRef(), "N", strconv.Itoa(round))
				plan.SendKeys(pane.Ref(), SendKeysRequest{KeyName: true, Command: Ptr("C-c"), SkipEnter: true})
				if _, err := plan.Run(ctx, server); err != nil {
					failures <- err
				}
			}
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if want := int64(workers * rounds * 2); warnings.Load() != want {
		t.Errorf("warnings = %d, want %d (one per KeyName request, immediate and planned)", warnings.Load(), want)
	}
}

// The checked-daemon identity is the one piece of shared Server state written
// on the dispatch path. Many goroutines remember, forget and read it through
// the same coordination value copies of a Server share. Run with -race: the
// test is there so that removing checkedMu is detected.
func TestCheckedDaemonSupportsConcurrentAccess(t *testing.T) {
	t.Parallel()

	server := serverWithRunner(&statelessRunner{})
	shared := server.state.coordination()
	identity := &snapshotServerIdentity{}

	var group sync.WaitGroup
	for worker := range 16 {
		group.Go(func() {
			for round := range 200 {
				if (worker+round)%3 == 0 {
					shared.rememberChecked(nil)
				} else {
					shared.rememberChecked(identity)
				}
				if got := shared.checkedDaemon(); got != nil && got != identity {
					t.Errorf("checkedDaemon() = %p, want nil or the remembered identity", got)
				}
			}
		})
	}
	group.Wait()
}
