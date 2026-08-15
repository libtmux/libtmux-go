package integration

// A truncated read must never look like an empty server.
//
// os/exec stops waiting for the output of a command that has already exited
// once cmd.WaitDelay passes: it closes the pipe and keeps whatever arrived.
// That delay is not optional here, because tmux commands such as new-session -d
// leave a daemon holding the write end, and without it a read would wait for
// that daemon rather than for tmux's answer.
//
// So the truncation is possible by construction, and the only question is what
// a caller is told. Collection reads are lenient by default and report a server
// they could not reach as no rows; reporting a partial answer the same way would
// hand back a short listing as a confident complete one.

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

// starvedRunner runs tmux with a wait delay far too short to drain a listing,
// which makes an intermittent truncation a reliable one.
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
