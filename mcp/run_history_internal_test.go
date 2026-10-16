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

// fullHistoryPane returns a pane whose history has filled, so that tmux has
// started discarding and renumbering it.
func fullHistoryPane(
	ctx context.Context,
	t *testing.T,
	target tmux.Server,
	instance *Instance,
) tmux.Pane {
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	t.Helper()
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
	for attempt := range 6 {
		_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: pane.ID().String(), Command: "seq 1 50", TimeoutSeconds: 10,
		})
		if err != nil || len(ran.Output) != 50 {
			t.Fatalf("filling run %d = (%d lines, %v)", attempt, len(ran.Output), err)
		}
	}
	return pane
}

// A full history must not change what a run returns. Blank rows the command
// printed are its output, whichever way the output was read back.
//
//libtmux:real-tmux
func TestRunShellCommandKeepsBlankRowsOnceTheHistoryFills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, _ := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	pane := fullHistoryPane(ctx, t, target, instance)

	// Repeated, so that tmux discards history under some of the runs and not
	// others.
	for round := range 4 {
		for _, test := range []struct {
			command string
			want    []string
		}{
			{`printf '\n\nlead\n'`, []string{"", "", "lead"}},
			{`printf '\n'`, []string{""}},
			{`printf 'a\n\nb\n'`, []string{"a", "", "b"}},
			{`printf '\n\n\n\n\n\n\n\n\n\n\n\n'`, make([]string, 12)},
			{`true`, nil},
		} {
			_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
				PaneID: pane.ID().String(), Command: test.command, TimeoutSeconds: 10,
			})
			if err != nil || !slices.Equal(ran.Output, test.want) || ran.LinesMissed {
				t.Errorf("round %d run of %s = (output %q, missed %t, %v), want %q",
					round, test.command, ran.Output, ran.LinesMissed, err, test.want)
			}
		}
	}
}

// A run that times out in a pane whose history is full reports what the
// command printed so far, as a run that finished would, or says it cannot.
//
//libtmux:real-tmux
func TestRunShellCommandReturnsPartialOutputOnceTheHistoryFills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, _ := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	pane := fullHistoryPane(ctx, t, target, instance)

	want := make([]string, 0, 50)
	for line := 1; line <= 50; line++ {
		want = append(want, strconv.Itoa(line))
	}
	for attempt := range 3 {
		_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: pane.ID().String(), Command: "seq 1 50; sleep 20", TimeoutSeconds: 2,
		})
		if err != nil || !ran.TimedOut || !slices.Equal(ran.Output, want) || ran.LinesMissed {
			t.Fatalf("timed-out run %d = (output %q, timed out %t, missed %t, %v), want seq 1 50",
				attempt, ran.Output, ran.TimedOut, ran.LinesMissed, err)
		}
		interruptRun(ctx, t, instance, target, pane)
		_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: pane.ID().String(), Command: "seq 1 50", TimeoutSeconds: 10,
		})
		if err != nil || !slices.Equal(ran.Output, want) || ran.LinesMissed {
			t.Fatalf("run %d of seq 1 50 = (output %q, missed %t, %v)",
				attempt, ran.Output, ran.LinesMissed, err)
		}
	}

	// More than the history and the screen hold together: the start is gone,
	// the reply says so, and the end is what the command printed last.
	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: pane.ID().String(), Command: "seq 1 300; echo MID; sleep 20", TimeoutSeconds: 2,
	})
	if err != nil || !ran.TimedOut || !ran.LinesMissed || len(ran.Output) < 2 ||
		ran.Output[len(ran.Output)-1] != "MID" || ran.Output[len(ran.Output)-2] != "300" {
		t.Fatalf("timed-out run of seq 1 300 = (last %q, timed out %t, missed %t, %v), "+
			"want the end of it with lines missed", lastLine(ran.Output), ran.TimedOut, ran.LinesMissed, err)
	}
}

// interruptRun ends a run that timed out and waits for the pane to take
// another.
func interruptRun(
	ctx context.Context,
	t *testing.T,
	instance *Instance,
	target tmux.Server,
	pane tmux.Pane,
) {
	t.Helper()
	if err := pane.SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-c"}}); err != nil {
		t.Fatal(err)
	}
	requireRunCommandAvailable(ctx, t, instance, target, pane)
}
