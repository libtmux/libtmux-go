package integration

// tmux daemons may inherit command output pipes, requiring exec.Cmd.WaitDelay.
// If the forced close truncates output, lenient reads must report the error
// rather than return an empty server.

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// starvedRunner makes output truncation deterministic.
func starvedRunner() tmux.CommandRunner {
	return tmux.CommandRunnerFunc(
		func(ctx context.Context, request tmux.CommandRequest) (tmux.CommandResult, error) {
			result, err := tmuxcmd.Runner{WaitDelay: time.Nanosecond}.Run(ctx, tmuxcmd.Request{
				Binary:      request.Binary,
				Arguments:   request.Arguments,
				Environment: request.Environment,
				Directory:   request.Directory,
			})
			return tmux.CommandResult{
				Command:   result.Command,
				Stdout:    result.Stdout,
				RawStdout: result.RawStdout,
				Stderr:    result.Stderr,
				ExitCode:  result.ExitCode,
			}, err
		},
	)
}

//libtmux:real-tmux
func TestTruncatedReadIsNotReportedAsAnEmptyServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	harness := tmuxtest.NewServer(ctx, t)

	sessions, err := harness.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	for range 4 {
		if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
			break
		}
		_ = window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "tiled"})
	}

	// The same server, read through a transport that truncates.
	starved := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         harness.SocketPath(),
		ConfigFile:         harness.ConfigFile(),
		ProcessEnvironment: harness.ProcessEnvironment(),
		Runner:             starvedRunner(),
	})

	// The lenient default is what this is about: a lenient read must still
	// refuse to call a partial answer an empty one.
	silentlyEmpty, reported := 0, 0
	for range 40 {
		panes, err := starved.SearchPanes(ctx, nil)
		switch {
		case err != nil:
			reported++
			if !errors.Is(err, exec.ErrWaitDelay) {
				t.Errorf("truncated read reported %v, want exec.ErrWaitDelay", err)
			}
		case len(panes) == 0:
			silentlyEmpty++
		}
	}
	t.Logf("truncated reads: %d reported, %d silently empty", reported, silentlyEmpty)

	if silentlyEmpty != 0 {
		t.Errorf("%d truncated reads were reported as an empty server", silentlyEmpty)
	}
	if reported == 0 {
		t.Fatal("no read was truncated, so this proved nothing")
	}

	// The untruncated transport still sees the panes, so the starvation above
	// was the cause rather than the server being empty.
	panes, err := harness.SearchPanes(ctx, nil)
	if err != nil || len(panes) < 2 {
		t.Fatalf("SearchPanes() over the normal transport = (%d, %v)", len(panes), err)
	}
}
