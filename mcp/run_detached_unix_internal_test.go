//go:build unix

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// A run whose call has returned may still be running. These hold the pane to
// what the tool reference tells a caller to do then: steer or interrupt the
// command, and run the next one once it has ended, however it ended.
//
// The timed-out cases each take a second, the shortest timeout a run accepts.

func timedOutRun(ctx context.Context, t *testing.T, instance *Instance, target tmux.Server, pane tmux.Pane) {
	t.Helper()
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	_, timedOut, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: pane.ID().String(), Command: "sleep 30", TimeoutSeconds: 1,
	})
	if err != nil || !timedOut.TimedOut {
		t.Fatalf("run of sleep 30 = (%+v, %v), want a timeout", timedOut, err)
	}
	_, _, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: pane.ID().String(), Command: "true", TimeoutSeconds: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "earlier run_shell_command") {
		t.Fatalf("a second run while the first runs = %v, want it refused", err)
	}
}

//libtmux:real-tmux
func TestATimedOutRunTakesAnInterruptFromSendKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	timedOutRun(ctx, t, instance, target, panes[0])

	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	_, sent, err := instance.tools.sendKeysBatch(callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-c"}},
		"send_keys",
	)
	if err != nil || sent.Sent != 1 {
		t.Fatalf("send_keys C-c to a timed-out run = (%+v, %v), want it sent", sent, err)
	}
	requireRunCommandAvailable(ctx, t, instance, target, panes[0])
}

//libtmux:real-tmux
func TestRespawnEndsATimedOutRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	timedOutRun(ctx, t, instance, target, panes[0])

	// A respawned pane has a new shell, and the wrapper that would have
	// recorded the run's status is gone with the old one.
	if _, err := panes[0].Respawn(ctx, tmux.RespawnRequest{Kill: true}); err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, panes[0])
	requireRunCommandAvailable(ctx, t, instance, target, panes[0])
}

//libtmux:real-tmux
func TestRunShellCommandReportsAnInterrupt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	started := filepath.Join(t.TempDir(), "started")
	type outcome struct {
		output runCommandOutput
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		_, output, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID:         panes[0].ID().String(),
			Command:        ": > " + shellQuote(started) + "; sleep 30",
			TimeoutSeconds: 15,
		})
		done <- outcome{output, err}
	}()
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, error) {
		_, err := os.Stat(started)
		return err == nil, nil
	}); err != nil {
		t.Fatalf("the command never started: %v", err)
	}

	// The interrupt a person sends from the keyboard.
	if err := panes[0].SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-c"}}); err != nil {
		t.Fatal(err)
	}
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("the interrupted run never returned")
	}
	if result.err != nil || result.output.TimedOut || result.output.ExitStatus == nil ||
		*result.output.ExitStatus != 130 {
		t.Fatalf("interrupted run = (%+v, %v), want exit status 130 before the timeout",
			result.output, result.err)
	}
}

// A run whose shell goes away records no status. The call waiting on it
// must say so, rather than wait out its timeout and report the command as
// still running.
//
//libtmux:real-tmux
func TestRunShellCommandReportsAShellThatWentAway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	started := filepath.Join(t.TempDir(), "started")
	type outcome struct {
		output runCommandOutput
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		_, output, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID:         panes[0].ID().String(),
			Command:        ": > " + shellQuote(started) + "; sleep 30",
			TimeoutSeconds: 15,
		})
		done <- outcome{output, err}
	}()
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, error) {
		_, err := os.Stat(started)
		return err == nil, nil
	}); err != nil {
		t.Fatalf("the command never started: %v", err)
	}
	if _, err := panes[0].Respawn(ctx, tmux.RespawnRequest{Kill: true}); err != nil {
		t.Fatal(err)
	}
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("the run never returned")
	}
	if !errors.Is(result.err, errCommandWrapperGone) || result.output.TimedOut {
		t.Fatalf("run whose pane was respawned = (%+v, %v), want errCommandWrapperGone before the timeout",
			result.output, result.err)
	}
}
