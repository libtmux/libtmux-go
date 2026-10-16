//go:build unix

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// ignoringHangups respawns pane as a shell that ignores SIGHUP, which every
// command it runs inherits. Closing the pane's terminal then ends nothing it
// ran, as on macOS, where a respawned pane's last command can outlive it.
func ignoringHangups(ctx context.Context, t *testing.T, pane tmux.Pane) {
	t.Helper()
	command := "trap '' HUP; exec env ENV= PS1='" + tmuxtest.ShellPrompt + "' /bin/sh -i"
	if _, err := pane.Respawn(ctx, tmux.RespawnRequest{Command: &command, Kill: true}); err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
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
	for _, hangups := range []string{"signaled", "ignored"} {
		t.Run(hangups, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			withoutCallerEnvironment(t)
			target, _, panes := threePaneInputFixture(ctx, t)
			if hangups == "ignored" {
				ignoringHangups(ctx, t, panes[0])
			}
			instance := mustInternalMCPServer(t, target)
			timedOutRun(ctx, t, instance, target, panes[0])

			// A respawned pane has a new shell, and the wrapper that would
			// have recorded the run's status is no longer in it, whether or
			// not it is still running somewhere.
			if _, err := panes[0].Respawn(ctx, tmux.RespawnRequest{Kill: true}); err != nil {
				t.Fatal(err)
			}
			tmuxtest.WaitForShellReady(ctx, t, panes[0])
			requireRunCommandAvailable(ctx, t, instance, target, panes[0])
		})
	}
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
	for _, hangups := range []string{"signaled", "ignored"} {
		t.Run(hangups, func(t *testing.T) {
			testRunShellCommandReportsAShellThatWentAway(t, hangups == "ignored")
		})
	}
}

func testRunShellCommandReportsAShellThatWentAway(t *testing.T, ignoreHangups bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	if ignoreHangups {
		ignoringHangups(ctx, t, panes[0])
	}
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

// A run that times out still says what the command printed so far, which is
// what a caller needs to decide whether to wait for it.
//
//libtmux:real-tmux
func TestATimedOutRunReturnsWhatItPrintedSoFar(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "echo started; sleep 30", TimeoutSeconds: 1,
	})
	if err != nil || !ran.TimedOut || !slices.Equal(ran.Output, []string{"started"}) ||
		ran.OutputUnavailable != "" {
		t.Fatalf("timed-out run = (output %q, unavailable %q, timed out %t, %v), want [started]",
			ran.Output, ran.OutputUnavailable, ran.TimedOut, err)
	}
}
