package tmux_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// oneSession returns the harness's single "work" session.
func oneSession(ctx context.Context, t *testing.T, server tmux.Server) tmux.Session {
	t.Helper()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	return sessions[0]
}

//libtmux:real-tmux
func TestCommandStartWaitReportsStatus(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "sh -c 'sleep 1; exit 7'", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// The command has not had time to exit yet: prove it is still running
	// before Wait is ever called.
	pane := running.Pane()
	if pane.ID() == "" {
		t.Fatal("Pane() returned a zero pane")
	}
	live, err := pane.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if dead, _ := live.Dead(); dead {
		t.Fatal("pane reported dead immediately after Start")
	}
	if _, hasStatus := live.DeadStatus(); hasStatus {
		t.Fatal("pane reported a dead status immediately after Start")
	}

	result, err := running.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.Status != 7 {
		t.Errorf("Wait() status = %d, want 7", result.Status)
	}
}

// matchingLines returns, in order, the entries of lines that also appear in
// want. Used to tolerate output tmux already folded into a pane observation's
// baseline before the stream could be opened.
func matchingLines(lines, want []string) []string {
	var matched []string
	for _, line := range lines {
		if slices.Contains(want, line) {
			matched = append(matched, line)
		}
	}
	return matched
}

//libtmux:real-tmux
func TestCommandStreamsWhileRunning(t *testing.T) {
	// This test requires a control client. Pane.OpenObservation opens one
	// (through Server.OpenControl) regardless of how the pane's Session was
	// reached; a plain subprocess Server needs no connection for Start
	// itself. Without a control client -- for example, polling Pane.Capture
	// on a timer -- only the screen at each poll is observable: a poll could
	// land between two of the three lines, could repeat a line already seen,
	// or could simply catch the final screen after the command exited, none
	// of which proves output arrived while the command was still running.
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx,
		"sh -c 'sleep 1; echo line1; sleep 0.3; echo line2; sleep 0.3; echo line3; sleep 5'",
		tmux.RunOptions{},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pane := running.Pane()

	observation, err := pane.OpenObservation(ctx)
	if err != nil {
		t.Fatalf("OpenObservation() error = %v", err)
	}
	defer func() { _ = observation.Close() }()

	want := []string{"line1", "line2", "line3"}
	seen := matchingLines(observation.Baseline(), want)
	scanner := bufio.NewScanner(observation.Reader(ctx))
	for len(seen) < len(want) && scanner.Scan() {
		line := scanner.Text()
		if slices.Contains(want, line) {
			seen = append(seen, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Reader() error = %v", err)
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("streamed lines = %q, want %q", seen, want)
	}

	// The command's trailing sleep has not elapsed: the three lines above
	// were necessarily observed while it was still running.
	live, err := pane.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead, _ := live.Dead(); dead {
		t.Fatal("pane reported dead before its trailing sleep finished")
	}

	if err := running.Kill(ctx); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	if _, err := running.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

// Collect only the failed run's state before the fixture removes its server.
func logCommandKillFailure(ctx context.Context, t *testing.T, server tmux.Server, pane tmux.Pane, started time.Time) {
	t.Helper()
	daemonPID, _ := pane.Formats().PID()
	panePID, _ := pane.ProcessPID()
	t.Logf("Kill diagnostics: elapsed=%s context=%v daemon=%d pane=%s pid=%d",
		time.Since(started), ctx.Err(), daemonPID, pane.ID(), panePID)
	if info, err := os.Stat(server.SocketPath()); err != nil {
		t.Logf("Kill socket stat: %v", err)
	} else {
		t.Logf("Kill socket mode: %s", info.Mode())
	}
	for _, arguments := range [][]string{
		{
			"display-message", "-p", "-t", pane.ID().String(),
			"#{pid}:#{session_id}:#{window_id}:#{pane_id}:#{pane_pid}:#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}",
		},
		{"show-messages", "-J"},
	} {
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := server.Cmd(probeCtx, arguments...)
		cancel()
		t.Logf("Kill probe %s: exit=%d stdout=%q stderr=%q error=%v",
			arguments[0], result.ExitCode, result.Stdout, result.Stderr, err)
	}
	if daemonPID > 0 && panePID > 0 {
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		arguments := []string{"-p", fmt.Sprintf("%d,%d", daemonPID, panePID)}
		if runtime.GOOS == "linux" {
			arguments = append(arguments, "--ppid", strconv.Itoa(daemonPID))
		}
		arguments = append(arguments, "-o", "pid,ppid,pgid,sid,stat,wchan,comm")
		output, err := exec.CommandContext(probeCtx, "ps", arguments...).CombinedOutput()
		t.Logf("Kill owned process state: %s error=%v", output, err)
	}
}

// streamUntilWaitCompletes drains observation's Reader concurrently with
// Wait, since PaneObservation.Reader has no end-of-stream of its own: a
// remain-on-exit pane keeps producing no notifications forever once its
// command exits, rather than closing anything. Canceling the read's own
// context when Wait returns is what a caller must add to stop reading.
func streamUntilWaitCompletes(
	ctx context.Context,
	t *testing.T,
	running *tmux.Running,
	observation *tmux.PaneObservation,
) (tmux.RunResult, []string, error) {
	t.Helper()
	streamCtx, stopStreaming := context.WithCancel(ctx)
	defer stopStreaming()

	waited := make(chan struct{})
	var result tmux.RunResult
	var waitErr error
	go func() {
		defer close(waited)
		defer stopStreaming()
		result, waitErr = running.Wait(ctx)
	}()

	// Opening a control-mode observation is itself measurably slow (a new
	// tmux -C process and handshake), so a short-lived command can finish
	// and print all of its output before the observation's baseline is even
	// captured. Non-blank baseline lines are output the stream will never
	// repeat, so they are collected the same as streamed lines.
	var lines []string
	for _, line := range observation.Baseline() {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	scanner := bufio.NewScanner(observation.Reader(streamCtx))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	<-waited
	// Whichever fires first, the caller must treat both as expected: Kill or
	// a natural exit removes the window (unless Keep), and the observation
	// then reports ErrPaneObservationLost before or around the same time as
	// the explicit cancel above.
	if scanErr := scanner.Err(); scanErr != nil &&
		!errors.Is(scanErr, context.Canceled) &&
		!errors.Is(scanErr, tmux.ErrPaneObservationLost) {
		t.Errorf("Reader() ended with %v, want context.Canceled or ErrPaneObservationLost", scanErr)
	}
	return result, lines, waitErr
}

//libtmux:real-tmux
func TestCommandStreamUntilWaitCompletes(t *testing.T) {
	// Both commands below sleep briefly after their last echo instead of
	// exiting right on top of it: a line printed with nothing separating it
	// from the process's own exit races the pane's transition to dead, and
	// the observation's %output for that line is dropped, not delayed,
	// roughly half the time in this environment. Wait's own RunResult.Lines
	// (from a plain screen capture, not the notification stream) always has
	// it regardless.
	server := tmuxtest.NewServer(context.Background(), t)
	server = tmux.RecordCommandFailuresForTest(server, func(arguments, stderr []string, exitCode int, err error) {
		if slices.ContainsFunc(arguments, func(argument string) bool {
			return strings.Contains(argument, "respawn-pane")
		}) {
			t.Logf("native respawn-pane failure: exit=%d stderr=%q transport=%v", exitCode, stderr, err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	t.Run("default removes the window and ends the stream with ErrPaneObservationLost", func(t *testing.T) {
		running, err := session.Start(ctx,
			"sh -c 'sleep 0.5; echo line1; sleep 0.3; echo line2; sleep 0.3; exit 3'",
			tmux.RunOptions{},
		)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		observation, err := running.Pane().OpenObservation(ctx)
		if err != nil {
			t.Fatalf("OpenObservation() error = %v", err)
		}
		defer func() { _ = observation.Close() }()

		result, lines, err := streamUntilWaitCompletes(ctx, t, running, observation)
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if result.Status != 3 {
			t.Errorf("Wait() status = %d, want 3", result.Status)
		}
		if want := []string{"line1", "line2"}; !slices.Equal(lines, want) {
			t.Errorf("streamed lines = %q, want %q", lines, want)
		}
	})

	t.Run("Keep leaves the window and only the canceled context ends the stream", func(t *testing.T) {
		running, err := session.Start(ctx,
			"sh -c 'sleep 0.5; echo line1; sleep 0.3; exit 0'",
			tmux.RunOptions{Keep: true, WindowName: "kept-stream"},
		)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		observation, err := running.Pane().OpenObservation(ctx)
		if err != nil {
			t.Fatalf("OpenObservation() error = %v", err)
		}
		defer func() { _ = observation.Close() }()

		result, lines, err := streamUntilWaitCompletes(ctx, t, running, observation)
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if result.Status != 0 {
			t.Errorf("Wait() status = %d, want 0", result.Status)
		}
		if want := []string{"line1"}; !slices.Equal(lines, want) {
			t.Errorf("streamed lines = %q, want %q", lines, want)
		}
	})
}

// reportsDeadSignal is false before tmux 3.3, which populates no
// pane_dead_signal: a signal-killed command is then indistinguishable from one
// that exited zero through the formats alone.
func reportsDeadSignal(ctx context.Context, t *testing.T, server tmux.Server) bool {
	t.Helper()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatalf("ParseVersion() error = %v", err)
	}
	return version.AtLeast(minimum)
}

//libtmux:real-tmux
func TestCommandKillStopsIt(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "sleep 30", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	started := time.Now()
	if err := running.Kill(ctx); err != nil {
		logCommandKillFailure(ctx, t, server, running.Pane(), started)
		t.Fatalf("Kill() error = %v", err)
	}
	result, err := running.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("Wait() after Kill() took %s, want a prompt return", elapsed)
	}
	if reportsDeadSignal(ctx, t, server) && result.Signal == "" {
		t.Errorf("Wait() after Kill() reported no signal, result = %+v", result)
	}
}

//libtmux:real-tmux
func TestCommandKillOnFinishedCommandIsANoOp(t *testing.T) {
	// Kill resolves its target through the pane, at the moment tmux runs the
	// signal, rather than trusting a process id read earlier: a pane already
	// removed makes that id resolve empty and the wrapped kill a harmless,
	// reported-as-successful no-op instead of a stale-pid guess.
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "true", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := running.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := running.Kill(ctx); err != nil {
		t.Fatalf("Kill() after the command's window is gone: error = %v, want nil", err)
	}
}

//libtmux:real-tmux
func TestCommandWaitIsSafeRepeatedAndConcurrent(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "sh -c 'sleep 0.5; exit 5'", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup
	var first, second tmux.RunResult
	var firstErr, secondErr error
	wg.Add(2)
	go func() { defer wg.Done(); first, firstErr = running.Wait(ctx) }()
	go func() { defer wg.Done(); second, secondErr = running.Wait(ctx) }()
	wg.Wait()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent Wait() errors = %v, %v", firstErr, secondErr)
	}
	if first.Status != 5 {
		t.Errorf("Wait() status = %d, want 5", first.Status)
	}
	if first.Status != second.Status || first.Signal != second.Signal ||
		first.Pane != second.Pane || !slices.Equal(first.Lines, second.Lines) {
		t.Fatalf("concurrent Wait() results differ: %+v vs %+v", first, second)
	}

	third, err := running.Wait(ctx)
	if err != nil {
		t.Fatalf("later Wait() error = %v", err)
	}
	if third.Status != first.Status || !slices.Equal(third.Lines, first.Lines) {
		t.Errorf("later Wait() = %+v, want the same completion as %+v", third, first)
	}
}

//libtmux:real-tmux
func TestCommandWaitRetriesAfterContextCancellation(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "sh -c 'sleep 1.5; exit 0'", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	shortCtx, shortCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer shortCancel()
	if _, err := running.Wait(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() with a short context error = %v, want context.DeadlineExceeded", err)
	}

	// tmux's one-shot wait-for was never delivered to the canceled call above
	// (killing that call's local waiter does not consume it), so a fresh
	// call still observes the command's real completion.
	result, err := running.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() after a canceled call: error = %v, want nil", err)
	}
	if result.Status != 0 {
		t.Errorf("Wait() after a canceled call: status = %d, want 0", result.Status)
	}
}

//libtmux:real-tmux
func TestSessionRunUnchanged(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	result, err := session.Run(ctx, "printf 'one\\ntwo\\n'; exit 7", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != 7 {
		t.Errorf("Run() status = %d, want 7", result.Status)
	}
	if want := []string{"one", "two"}; !slices.Equal(result.Lines, want) {
		t.Errorf("Run() lines = %q, want %q", result.Lines, want)
	}
	refreshed, err := session.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := refreshed.WindowCount(); count != 1 {
		t.Errorf("window count after Run() = %d, want the run's window removed", count)
	}

	kept, err := session.Run(ctx, "echo kept", tmux.RunOptions{Keep: true, WindowName: "kept"})
	if err != nil {
		t.Fatalf("Run(keep) error = %v", err)
	}
	if kept.Pane == "" {
		t.Fatal("Run(keep) reported no pane")
	}
	if _, err := server.Pane(ctx, kept.Pane); err != nil {
		t.Errorf("kept pane %s is gone: %v", kept.Pane, err)
	}

	// A canceled Run must not orphan its window: Run gives the caller no
	// other chance to retry or clean up, unlike a bare Start followed by
	// Wait.
	beforeCancel, err := session.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	countBeforeCancel, _ := beforeCancel.WindowCount()

	shortCtx, shortCancel := context.WithTimeout(ctx, time.Second)
	defer shortCancel()
	if _, err := session.Run(shortCtx, "sleep 30", tmux.RunOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() with a short context error = %v, want context.DeadlineExceeded", err)
	}
	refreshedAfterCancel, err := session.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := refreshedAfterCancel.WindowCount(); count != countBeforeCancel {
		t.Errorf(
			"window count after a canceled Run() = %d, want %d (the run's window removed)",
			count, countBeforeCancel,
		)
	}
}

// settleGoroutines polls runtime.NumGoroutine until it reaches baseline or a
// short deadline passes, returning whatever it last observed. Background
// runtime work (GC, finalizers) can transiently hold the count above
// baseline; this settles past that instead of asserting on a single sample.
func settleGoroutines(baseline int) int {
	deadline := time.Now().Add(2 * time.Second)
	last := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		if last <= baseline {
			return last
		}
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		last = runtime.NumGoroutine()
	}
	return last
}

//libtmux:real-tmux
func TestCommandLeavesNoGoroutines(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	waited, err := session.Start(ctx, "true", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := waited.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	afterWait := settleGoroutines(baseline)
	t.Logf("goroutines: baseline %d, after Start/Wait %d", baseline, afterWait)
	if afterWait > baseline {
		t.Errorf("goroutines after Start/Wait = %d, want <= baseline %d", afterWait, baseline)
	}

	killed, err := session.Start(ctx, "sleep 30", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	started := time.Now()
	if err := killed.Kill(ctx); err != nil {
		logCommandKillFailure(ctx, t, server, killed.Pane(), started)
		t.Fatalf("Kill() error = %v", err)
	}
	if _, err := killed.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	afterKill := settleGoroutines(baseline)
	t.Logf("goroutines: baseline %d, after Start/Kill %d", baseline, afterKill)
	if afterKill > baseline {
		t.Errorf("goroutines after Start/Kill = %d, want <= baseline %d", afterKill, baseline)
	}
}

// syncedBuffer records what a stream wrote while a test reads it from another
// goroutine, which an unguarded bytes.Buffer cannot be asked to do.
type syncedBuffer struct {
	mu    sync.Mutex
	lines []string
	rest  []byte
}

func (b *syncedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rest = append(b.rest, p...)
	for {
		at := bytes.IndexByte(b.rest, '\n')
		if at < 0 {
			return len(p), nil
		}
		b.lines = append(b.lines, strings.TrimRight(string(b.rest[:at]), "\r"))
		b.rest = b.rest[at+1:]
	}
}

func (b *syncedBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.lines)
}

//libtmux:real-tmux
func TestCommandStreamToCopiesOutputAndTheOutcome(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx,
		"sh -c 'sleep 0.4; seq 1 2000; sleep 0.3; echo done; sleep 0.2; exit 5'",
		tmux.RunOptions{},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	destination := &syncedBuffer{}
	result, err := running.StreamTo(ctx, destination)
	if err != nil {
		t.Fatalf("StreamTo() error = %v", err)
	}
	if result.Status != 5 {
		t.Fatalf("StreamTo() status = %d, want 5", result.Status)
	}
	// The screen is authoritative and holds the command's last line.
	if !slices.ContainsFunc(result.Lines, func(line string) bool {
		return strings.Contains(line, "done")
	}) {
		t.Fatalf("RunResult.Lines = %q, want a line containing \"done\"", result.Lines)
	}
	// The stream carries what tmux pushed while the command ran. The command
	// sleeps before its first write, so every one of these is after the
	// observation opened and none of them is the last line before it exits.
	streamed := destination.Lines()
	for _, want := range []string{"1", "1000", "2000"} {
		if !slices.ContainsFunc(streamed, func(line string) bool {
			return strings.TrimSpace(line) == want
		}) {
			t.Fatalf("streamed %d lines, want one equal to %q", len(streamed), want)
		}
	}
}

//libtmux:real-tmux
func TestCommandStreamToKillsWhileStreaming(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx,
		"sh -c 'echo watching; while :; do sleep 0.1; done'", tmux.RunOptions{},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Kill from another goroutine while StreamTo blocks, which is the shape
	// StreamTo exists to make possible.
	killed := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		killed <- running.Kill(ctx)
	}()

	destination := &syncedBuffer{}
	result, err := running.StreamTo(ctx, destination)
	if err != nil {
		t.Fatalf("StreamTo() error = %v", err)
	}
	if killErr := <-killed; killErr != nil {
		t.Fatalf("Kill() error = %v", killErr)
	}
	if reportsDeadSignal(ctx, t, server) && result.Signal == "" {
		t.Fatalf("StreamTo() = %+v, want the signal that killed the command", result)
	}
}

//libtmux:real-tmux
func TestCommandStreamToRejectsANilDestination(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "true", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _, _ = running.Wait(ctx) }()

	if _, err := running.StreamTo(ctx, nil); !errors.Is(
		err, tmux.ErrInvalidServerCommandRequest,
	) {
		t.Fatalf("StreamTo(nil) error = %v, want ErrInvalidServerCommandRequest", err)
	}
}

//libtmux:real-tmux
func TestCommandWaitEndsWhenTheSignalIsLost(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := oneSession(ctx, t, server)

	running, err := session.Start(ctx, "sleep 0.2", tmux.RunOptions{Keep: true})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Take the pane's hook away before the command exits, so tmux signals
	// nothing and only the liveness check can end the wait.
	if err := running.Pane().SetHook(ctx, "pane-died", ""); err != nil {
		t.Fatalf("SetHook() error = %v", err)
	}

	started := time.Now()
	result, err := running.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("Wait() took %s with no signal, want the liveness check to end it", elapsed)
	}
	if result.Pane == "" {
		t.Errorf("Wait() = %+v, want the command's pane", result)
	}
}
