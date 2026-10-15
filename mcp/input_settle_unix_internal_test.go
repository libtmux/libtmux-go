//go:build unix

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// A pane that has just started has a foreground command that is still moving:
// tmux's own name, then whatever the shell's start-up files run. These hold
// input to waiting that out rather than refusing a pane nobody changed, which
// is what the first call after split_window used to meet.

// startingPane respawns pane as a shell held in a start-up command, and
// returns once tmux reports that command in the foreground. The shell reaches
// its prompt when release is called.
func startingPane(ctx context.Context, t *testing.T, pane tmux.Pane) (release func()) {
	t.Helper()
	directory := t.TempDir()
	gate := filepath.Join(directory, "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	// An interactive shell runs the file ENV names before its first prompt.
	// bash, which is sh on macOS, turns on job control only after it, so the
	// file does so itself to give cat the foreground. cat blocks opening the
	// gate until something opens it for writing.
	startup := filepath.Join(directory, "startup")
	if err := os.WriteFile(startup, []byte("set -m\ncat "+shellQuote(gate)+" >/dev/null\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := panePID(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	command := "ENV=" + shellQuote(startup) + " exec sh -i"
	if _, err := pane.Respawn(ctx, tmux.RespawnRequest{Command: &command, Kill: true}); err != nil {
		t.Fatal(err)
	}
	if err := waitForPaneReplacement(ctx, pane, before); err != nil {
		t.Fatal(err)
	}
	if err := waitForForeground(ctx, pane, func(name string) bool { return name == "cat" }); err != nil {
		t.Fatalf("the pane never reached its start-up command: %v", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			writer, err := os.OpenFile(gate, os.O_WRONLY, 0)
			if err != nil {
				t.Errorf("open the start-up gate: %v", err)
				return
			}
			_ = writer.Close()
		})
	}
	t.Cleanup(release)
	return release
}

//libtmux:real-tmux
func TestRunShellCommandWaitsForAStartingPanesShell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	release := startingPane(ctx, t, panes[0])

	// The shell is let through to its prompt only after the call has read the
	// pane as still starting, so the call cannot succeed without waiting.
	defaults := instance.runtime.deps
	var read sync.Once
	instance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		snapshot, err := defaults.snapshot(snapshotCtx, server)
		read.Do(release)
		return snapshot, err
	}

	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "exit 3", TimeoutSeconds: 15,
	})
	if err != nil || ran.ExitStatus == nil || *ran.ExitStatus != 3 {
		t.Fatalf("runCommand() on a starting pane = (%+v, %v), want exit status 3", ran, err)
	}
}

//libtmux:real-tmux
func TestRunShellCommandStillRefusesAPaneWithNoShell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	startingPane(ctx, t, panes[0])
	// The wait is bounded, and a pane that stays in something else is refused
	// by the name of what it is running.
	instance.runtime.deps.inputSettleGrace = 50 * time.Millisecond

	_, _, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 15,
	})
	if err == nil || !strings.Contains(err.Error(), "POSIX-compatible pane shell") ||
		!strings.Contains(err.Error(), "is running cat") {
		t.Fatalf("runCommand() on a pane held in cat = %v, want a refusal naming cat", err)
	}
}

//libtmux:real-tmux
func TestPasteTextWaitsOutAForegroundCommandThatChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	release := startingPane(ctx, t, panes[0])

	// The first reading sees the start-up command. Before the second, the
	// shell is let through and seen at its prompt, so the two readings differ
	// by a change the pane made by itself.
	defaults := instance.runtime.deps
	readings := 0
	instance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		readings++
		if readings == 2 {
			release()
			if err := waitForForeground(snapshotCtx, panes[0], func(name string) bool {
				return name != "cat"
			}); err != nil {
				return tmux.Snapshot{}, err
			}
		}
		return defaults.snapshot(snapshotCtx, server)
	}

	_, pasted, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "x",
	})
	if err != nil || pasted.Bytes != 1 {
		t.Fatalf("pasteText() across a pane reaching its prompt = (%+v, %v), want 1 byte pasted",
			pasted, err)
	}
	if readings < 3 {
		t.Fatalf("pasteText() read the pane %d times, want a third reading to confirm the second", readings)
	}
}
