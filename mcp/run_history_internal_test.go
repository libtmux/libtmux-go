package mcp

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Each time a pane's history fills, tmux discards its oldest tenth and
// renumbers what is left. A run must still return what it printed once that
// starts, and say when tmux discarded the start of it.
//
//libtmux:real-tmux
func TestRunShellCommandReadsOutputOnceTheHistoryFills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, _ := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	// A pane takes its history limit when it is created.
	if _, err := target.Cmd(ctx, "set-option", "-g", "history-limit", "100"); err != nil {
		t.Fatal(err)
	}
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window := tmuxtest.NewWindow(ctx, t, sessions[0], tmux.NewWindowRequest{})
	pane, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)

	want := make([]string, 0, 50)
	for line := 1; line <= 50; line++ {
		want = append(want, strconv.Itoa(line))
	}
	// Enough runs for the history to fill, and to keep filling.
	for attempt := range 6 {
		_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: pane.ID().String(), Command: "seq 1 50", TimeoutSeconds: 10,
		})
		if err != nil || !slices.Equal(ran.Output, want) || ran.LinesMissed || ran.Truncated {
			t.Fatalf("run %d of seq 1 50 = (output %q, missed %t, truncated %t, %v)",
				attempt, ran.Output, ran.LinesMissed, ran.Truncated, err)
		}
	}

	// More than the history and the screen hold together: tmux discards the
	// start, and the reply says so.
	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: pane.ID().String(), Command: "seq 1 300", TimeoutSeconds: 10,
	})
	if err != nil || !ran.LinesMissed || len(ran.Output) == 0 || ran.Output[len(ran.Output)-1] != "300" {
		t.Fatalf("run of seq 1 300 = (last %q, missed %t, %v), want the end of it with lines missed",
			lastLine(ran.Output), ran.LinesMissed, err)
	}
}

func lastLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// A terminal that does not echo shows no wrapper line to find the output
// after. Once the history is nearly full, a short command must still come
// back as only what it printed.
//
//libtmux:real-tmux
func TestRunShellCommandReadsOutputWithoutEchoOnceTheHistoryFills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, _ := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	if _, err := target.Cmd(ctx, "set-option", "-g", "history-limit", "100"); err != nil {
		t.Fatal(err)
	}
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window := tmuxtest.NewWindow(ctx, t, sessions[0], tmux.NewWindowRequest{})
	pane, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	// Fill the history, then stop the terminal echoing what is typed.
	tmuxtest.TypeAndWait(ctx, t, pane, "seq 1 150; stty -echo")

	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo hi", TimeoutSeconds: 10,
	})
	if err != nil || !slices.Equal(ran.Output, []string{"hi"}) || ran.LinesMissed {
		t.Fatalf("run of echo hi = (output %q, missed %t, %v), want [hi]",
			ran.Output, ran.LinesMissed, err)
	}
}
