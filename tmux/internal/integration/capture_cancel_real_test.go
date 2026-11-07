package integration

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// capturePanes returns the server, its pid, and a pane on each transport.
func capturePanes(
	ctx context.Context,
	t *testing.T,
) (tmux.Server, int, map[string]tmux.Pane) {
	t.Helper()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	_, connection, err := server.NewSessionConnection(
		ctx, tmux.NewSessionRequest{Name: "cancel"}, tmux.ConnectionOptions{Lanes: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	loose, err := sessions[0].SearchPanes(ctx, nil)
	if err != nil || len(loose) == 0 {
		t.Fatalf("panes = (%v, %v)", loose, err)
	}
	bound, err := connection.Session().SearchPanes(ctx, nil)
	if err != nil || len(bound) == 0 {
		t.Fatalf("connection panes = (%v, %v)", bound, err)
	}
	result, err := server.Cmd(ctx, "display-message", "-p", "#{pid}")
	if err != nil || len(result.Stdout) != 1 {
		t.Fatalf("pid = (%+v, %v)", result, err)
	}
	pid, err := strconv.Atoi(result.Stdout[0])
	if err != nil {
		t.Fatal(err)
	}
	return server, pid, map[string]tmux.Pane{"subprocess": loose[0], "connection": bound[0]}
}

// A capture cancelled while the tmux server is stopped, so its command list is
// in flight, still ends with no scratch buffer once the server runs again, and
// the cleanup that could not reach a stopped server within its bound is said
// in the returned error instead of hanging or vanishing.
//
//libtmux:real-tmux
func TestCaptureToFileCancelledWhileServerStoppedLeavesNoBuffer(t *testing.T) {
	for _, transport := range []string{"subprocess", "connection"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			server, pid, panes := capturePanes(ctx, t)
			pane := panes[transport]
			path := t.TempDir() + "/out"
			if _, err := pane.CaptureToFile(ctx, path, tmux.CapturePaneRequest{}); err != nil {
				t.Fatalf("warm-up capture: %v", err)
			}
			if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
				t.Fatal(err)
			}
			resumed := false
			resume := func() {
				if !resumed {
					resumed = true
					_ = syscall.Kill(pid, syscall.SIGCONT)
				}
			}
			defer resume()

			callCtx, stop := context.WithCancel(ctx)
			type outcome struct {
				lines   []string
				err     error
				elapsed time.Duration
			}
			done := make(chan outcome, 1)
			started := time.Now()
			go func() {
				lines, err := pane.CaptureToFile(callCtx, path, tmux.CapturePaneRequest{})
				done <- outcome{lines, err, time.Since(started)}
			}()
			time.Sleep(300 * time.Millisecond)
			stop()
			var got outcome
			select {
			case got = <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("CaptureToFile still blocked 8s after cancellation")
			}
			t.Logf("returned after %v: err = %v, lines = %d", got.elapsed, got.err, len(got.lines))
			if got.err == nil {
				t.Fatalf("a call cancelled against a stopped server returned nil")
			}
			if !errors.Is(got.err, context.Canceled) {
				t.Errorf("error %v does not wrap context.Canceled", got.err)
			}
			if !strings.Contains(got.err.Error(), "clean scratch buffer") {
				t.Errorf("error %q does not say the cleanup failed", got.err)
			}
			if got.elapsed > 2500*time.Millisecond+300*time.Millisecond {
				t.Errorf("returned after %v, want within the 2s cleanup bound of the cancel", got.elapsed)
			}
			resume()
			deadline := time.Now().Add(5 * time.Second)
			for {
				left := scratchBuffersOn(ctx, t, server)
				if len(left) == 0 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("scratch buffers still on the server after SIGCONT: %q", left)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

// Session.Run on a connection-bound session reads its final screen on a context
// its caller's cancellation does not reach, so only that read's own limit ends
// it. When the server stops right after the final capture-pane (a hook stops it
// from inside tmux) and the limit passes, no scratch buffer is left once the
// server answers again.
//
//libtmux:real-tmux
func TestSessionRunCancelledAfterCaptureOnConnectionLeavesNoScratchBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	server, pid, _ := capturePanes(ctx, t)
	_, connection, err := server.NewSessionConnection(
		ctx, tmux.NewSessionRequest{Name: "run"}, tmux.ConnectionOptions{Lanes: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	session := connection.Session()
	marker := t.TempDir() + "/captured"
	if result, err := server.Cmd(ctx, "set-hook", "-g", "after-capture-pane",
		"run-shell 'tmux -S #{socket_path} list-buffers | grep -q libtmux-go-capture && "+
			"{ touch "+marker+"; kill -STOP #{pid}; }'"); err != nil || result.ExitCode != 0 {
		t.Fatalf("set-hook = (%+v, %v)", result, err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGCONT) }()
	callCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for callCtx.Err() == nil {
			if _, err := os.Stat(marker); err == nil {
				stop()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	started := time.Now()
	_, err = session.Run(callCtx, "echo hi", tmux.RunOptions{})
	t.Logf("Run returned after %v: %v", time.Since(started), err)
	if err == nil {
		t.Fatal("Run completed although its server was stopped after the capture")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the capture hook never ran: %v", statErr)
	}
	// Run reads the final screen on a context its caller's cancellation does not
	// reach, so only that read's own limit ends it. Hold the server stopped past
	// that limit, then let it answer.
	time.Sleep(11*time.Second - time.Since(started))
	if err := syscall.Kill(pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		left := scratchBuffersOn(ctx, t, server)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("scratch buffers still on the server after SIGCONT: %q", left)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
