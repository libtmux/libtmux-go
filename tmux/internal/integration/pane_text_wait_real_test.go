//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// textWaitPane starts a server whose first pane runs command at the given
// size, and returns the server and that pane. Extra config lines are loaded
// before the pane starts.
func textWaitPane(
	ctx context.Context,
	t *testing.T,
	command string,
	width, height int,
	config string,
) (tmux.Server, tmux.Pane) {
	t.Helper()
	session := tmux.NewSessionRequest{
		Name: "work", Command: command, Width: width, Height: height,
	}
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Config:         []byte(config),
		InitialSession: &session,
	})
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = %v, %v, want one pane", panes, err)
	}
	return server, panes[0]
}

// keepServerAlive adds a second session, so killing the pane under test
// leaves the server answering.
func keepServerAlive(ctx context.Context, t *testing.T, server tmux.Server) {
	t.Helper()
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "keeper"}); err != nil {
		t.Fatalf("NewSession(keeper) error = %v", err)
	}
}

// screenHolds polls the pane until its visible screen holds text. It is the
// test's own wait; the code under test is never used to set up its own cases.
func screenHolds(ctx context.Context, t *testing.T, pane tmux.Pane, text string) {
	t.Helper()
	for ctx.Err() == nil {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err == nil && strings.Contains(strings.Join(lines, "\n"), text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("screen never held %q", text)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type textWaitOutcome struct {
	result tmux.PaneTextWait
	err    error
	took   time.Duration
}

func startTextWait(
	ctx context.Context,
	pane tmux.Pane,
	request tmux.WaitForTextRequest,
) <-chan textWaitOutcome {
	done := make(chan textWaitOutcome, 1)
	go func() {
		start := time.Now()
		result, err := pane.WaitForText(ctx, request)
		done <- textWaitOutcome{result, err, time.Since(start)}
	}()
	return done
}

func awaitOutcome(t *testing.T, done <-chan textWaitOutcome) textWaitOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(20 * time.Second):
		t.Fatal("WaitForText did not return")
		return textWaitOutcome{}
	}
}

//libtmux:real-tmux
func TestWaitForTextMatchesOutputAfterEntry(t *testing.T) {
	ctx := testContext(t)
	_, pane := textWaitPane(ctx, t, "sleep 1; printf 'late-NEEDLE\\n'; exec sleep 60", 0, 0, "")

	result, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "late-NEEDLE", Interval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitForText() error = %v", err)
	}
	if result.MatchedAtEntry {
		t.Fatal("MatchedAtEntry = true, want false: the text printed after entry")
	}
	if !strings.Contains(result.Text, "late-NEEDLE") {
		t.Fatalf("Text = %q, want the matched screen", result.Text)
	}
}

//libtmux:real-tmux
func TestWaitForTextReadsBeforeFirstSleep(t *testing.T) {
	ctx := testContext(t)
	_, pane := textWaitPane(ctx, t, "printf 'ready-NEEDLE\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "ready-NEEDLE")

	start := time.Now()
	result, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "ready-NEEDLE", Interval: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitForText() error = %v", err)
	}
	if !result.MatchedAtEntry {
		t.Fatal("MatchedAtEntry = false, want true")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("WaitForText() took %s, want well inside the 10s interval", took)
	}
}

//libtmux:real-tmux
func TestWaitForTextJoinsWrappedRows(t *testing.T) {
	ctx := testContext(t)
	line := strings.Repeat("a", 77) + "NEEDLE" + strings.Repeat("b", 67)
	_, pane := textWaitPane(ctx, t, "printf '%s\\n' "+line+"; exec sleep 60", 80, 24, "")
	screenHolds(ctx, t, pane, strings.Repeat("b", 10))

	result, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "NEEDLE", Interval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitForText() error = %v", err)
	}
	if !strings.Contains(result.Text, line) {
		t.Fatalf("Text = %q, want the joined %d-character line", result.Text, len(line))
	}
}

//libtmux:real-tmux
func TestWaitForTextReadsOnlyTheVisibleScreen(t *testing.T) {
	ctx := testContext(t)
	_, pane := textWaitPane(ctx, t,
		"printf 'scrolled-NEEDLE\\n'; seq 1 3000; printf 'screen-end\\n'; exec sleep 60",
		80, 24, "set -g history-limit 50000\n")
	screenHolds(ctx, t, pane, "screen-end")

	// The control: history holds the needle, so only the visible-screen read
	// keeps the wait from seeing it.
	history, err := pane.Capture(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary,
	})
	if err != nil || !strings.Contains(strings.Join(history, "\n"), "scrolled-NEEDLE") {
		t.Fatalf("history capture = %v, %v, want the scrolled needle", history, err)
	}

	short, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	result, err := pane.WaitForText(short, tmux.WaitForTextRequest{
		Text: "scrolled-NEEDLE", Interval: 50 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForText() error = %v, want the deadline", err)
	}
	if !strings.Contains(result.Text, "screen-end") {
		t.Fatalf("Text = %q, want the last screen read", result.Text)
	}

	// The control: text on the visible screen matches.
	if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{Text: "screen-end"}); err != nil {
		t.Fatalf("WaitForText(visible) error = %v", err)
	}
}

//libtmux:real-tmux
func TestWaitForTextReportsAKilledPane(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'before-kill\\n'; exec sleep 60", 0, 0, "")
	keepServerAlive(ctx, t, server)
	screenHolds(ctx, t, pane, "before-kill")

	done := startTextWait(ctx, pane, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 50 * time.Millisecond,
	})
	time.Sleep(300 * time.Millisecond)
	if err := pane.Kill(ctx); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	outcome := awaitOutcome(t, done)
	ended, ok := errors.AsType[*tmux.PaneEndedError](outcome.err)
	if !ok || ended.Reason != tmux.PaneGone {
		t.Fatalf("error = %v, want PaneEndedError with PaneGone", outcome.err)
	}
	if !errors.Is(outcome.err, tmux.ErrPaneEnded) || !errors.Is(outcome.err, tmux.ErrNotFound) {
		t.Fatalf("error = %v, want ErrPaneEnded and ErrNotFound", outcome.err)
	}
	if !strings.Contains(outcome.result.Text, "before-kill") {
		t.Fatalf("Text = %q, want the output read before the kill", outcome.result.Text)
	}
	if outcome.took > 5*time.Second {
		t.Fatalf("wait took %s, want a few intervals", outcome.took)
	}
}

//libtmux:real-tmux
func TestWaitForTextReportsAnExitedPaneKeptOnScreen(t *testing.T) {
	ctx := testContext(t)
	_, pane := textWaitPane(ctx, t, "printf '\\n\\nexited-output\\n'; sleep 1", 0, 0, "set -g remain-on-exit on\n")

	result, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 50 * time.Millisecond,
	})
	ended, ok := errors.AsType[*tmux.PaneEndedError](err)
	if !ok || ended.Reason != tmux.PaneExited {
		t.Fatalf("error = %v, want PaneEndedError with PaneExited", err)
	}
	if !strings.Contains(result.Text, "exited-output") {
		t.Fatalf("Text = %q, want the dead pane's screen", result.Text)
	}
}

//libtmux:real-tmux
func TestWaitForTextMatchesBeforeDeath(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf '\\n\\nlast-words\\n'; sleep 1", 0, 0, "set -g remain-on-exit on\n")
	for {
		if ctx.Err() != nil {
			t.Fatal("pane never died")
		}
		result, err := server.Cmd(ctx, "display-message", "-p", "-t", pane.ID().String(), "#{pane_dead}")
		if err == nil && len(result.Stdout) == 1 && result.Stdout[0] == "1" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	screenHolds(ctx, t, pane, "last-words")

	result, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{Text: "last-words"})
	if err != nil {
		t.Fatalf("WaitForText() error = %v, want a match from the dead pane's screen", err)
	}
	if !result.MatchedAtEntry {
		t.Fatal("MatchedAtEntry = false, want true")
	}
}

//libtmux:real-tmux
func TestWaitForTextReportsAKilledServer(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'before-kill\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "before-kill")

	done := startTextWait(ctx, pane, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 50 * time.Millisecond,
	})
	time.Sleep(300 * time.Millisecond)
	_, _ = server.Cmd(ctx, "kill-server")

	outcome := awaitOutcome(t, done)
	ended, ok := errors.AsType[*tmux.PaneEndedError](outcome.err)
	if !ok || ended.Reason != tmux.ServerGone {
		t.Fatalf("error = %v, want PaneEndedError with ServerGone", outcome.err)
	}
	if !errors.Is(outcome.err, tmux.ErrNoServer) || !errors.Is(outcome.err, tmux.ErrPaneEnded) {
		t.Fatalf("error = %v, want ErrNoServer and ErrPaneEnded", outcome.err)
	}
	if !strings.Contains(outcome.result.Text, "before-kill") {
		t.Fatalf("Text = %q, want the output read before the kill", outcome.result.Text)
	}
}

//libtmux:real-tmux
func TestWaitForTextReportsAKilledServerWithAStoppedClient(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'before-kill\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "before-kill")

	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	client := exec.Command(binary, "-S", server.SocketPath(), "-C", "attach")
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("starting the control client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Process.Signal(syscall.SIGCONT)
		_ = stdin.Close()
		_ = client.Process.Kill()
		_ = client.Wait()
	})
	for {
		if ctx.Err() != nil {
			t.Fatal("control client never attached")
		}
		result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_control_mode}")
		if err == nil && len(result.Stdout) == 1 && result.Stdout[0] == "1" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := client.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}

	done := startTextWait(ctx, pane, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 50 * time.Millisecond,
	})
	time.Sleep(300 * time.Millisecond)
	_, _ = server.Cmd(ctx, "kill-server")

	outcome := awaitOutcome(t, done)
	ended, ok := errors.AsType[*tmux.PaneEndedError](outcome.err)
	if !ok || ended.Reason != tmux.ServerGone {
		t.Fatalf("error = %v, want PaneEndedError with ServerGone", outcome.err)
	}
	if !errors.Is(outcome.err, tmux.ErrNoServer) {
		t.Fatalf("error = %v, want ErrNoServer", outcome.err)
	}
	if outcome.took > 5*time.Second {
		t.Fatalf("wait took %s, want a few intervals", outcome.took)
	}
}

//libtmux:real-tmux
func TestWaitForTextCancelDoesNotWaitOutTheInterval(t *testing.T) {
	ctx := testContext(t)
	_, pane := textWaitPane(ctx, t, "printf 'quiet\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "quiet")

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startTextWait(cancelCtx, pane, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 10 * time.Second,
	})
	time.Sleep(250 * time.Millisecond)
	cancel()

	outcome := awaitOutcome(t, done)
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", outcome.err)
	}
	if outcome.took > 2*time.Second {
		t.Fatalf("wait took %s after a cancel, want well inside the 10s interval", outcome.took)
	}
	if !strings.Contains(outcome.result.Text, "quiet") {
		t.Fatalf("Text = %q, want the screen read before the cancel", outcome.result.Text)
	}
}

// hookCounts installs global hooks that each append a line to a file under a
// private directory, and returns a function reading the lines per hook.
func hookCounts(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	hooks ...string,
) func(string) int {
	t.Helper()
	dir := t.TempDir()
	for _, hook := range hooks {
		script := `run-shell "echo x >> ` + filepath.Join(dir, hook) + `"`
		if result, err := server.Cmd(ctx, "set-hook", "-g", "after-"+hook, script); err != nil || result.ExitCode != 0 {
			t.Fatalf("set-hook %s = %+v, %v", hook, result, err)
		}
	}
	t.Cleanup(func() {
		for _, hook := range hooks {
			_, _ = server.Cmd(context.Background(), "set-hook", "-gu", "after-"+hook)
		}
	})
	return func(hook string) int {
		data, err := os.ReadFile(filepath.Join(dir, hook))
		if err != nil {
			return 0
		}
		return strings.Count(string(data), "\n")
	}
}

//libtmux:real-tmux
func TestWaitForTextRunsOnlyTheCaptureHook(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'hooked\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "hooked")
	count := hookCounts(ctx, t, server,
		"display-message", "list-panes", "show-options", "capture-pane")

	// Let the hooks from the setup above finish, then count from zero.
	time.Sleep(300 * time.Millisecond)
	base := map[string]int{}
	for _, hook := range []string{"display-message", "list-panes", "show-options", "capture-pane"} {
		base[hook] = count(hook)
	}

	if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "hooked", Interval: 10 * time.Second,
	}); err != nil {
		t.Fatalf("WaitForText(entry) error = %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	_, err := pane.WaitForText(short, tmux.WaitForTextRequest{
		Text: "never-printed", Interval: 100 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForText(deadline) error = %v, want the deadline", err)
	}
	time.Sleep(500 * time.Millisecond)

	for _, hook := range []string{"display-message", "list-panes", "show-options"} {
		if got := count(hook) - base[hook]; got != 0 {
			t.Errorf("after-%s ran %d times during the waits, want 0", hook, got)
		}
	}
	// One read for the entry match, one at the start of the deadline wait and
	// one per interval after it, with slack for scheduling.
	got := count("capture-pane") - base["capture-pane"]
	if most := 1 + 1 + int(1500*time.Millisecond/(100*time.Millisecond)) + 3; got < 3 || got > most {
		t.Errorf("after-capture-pane ran %d times, want between 3 and %d reads", got, most)
	}
}

//libtmux:real-tmux
func TestWaitForTextReadsOncePerHookAtEntry(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'hooked\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "hooked")
	count := hookCounts(ctx, t, server, "capture-pane")
	time.Sleep(300 * time.Millisecond)
	base := count("capture-pane")

	if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
		Text: "hooked", Interval: 10 * time.Second,
	}); err != nil {
		t.Fatalf("WaitForText() error = %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := count("capture-pane") - base; got != 1 {
		t.Fatalf("after-capture-pane ran %d times for a match at entry, want 1", got)
	}
}

//libtmux:real-tmux
func TestWaitForTextRejectsEmptyTextBeforeAnyCommand(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "exec sleep 60", 0, 0, "")
	count := hookCounts(ctx, t, server, "capture-pane")
	time.Sleep(300 * time.Millisecond)
	base := count("capture-pane")

	_, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{})
	if !errors.Is(err, tmux.ErrInvalidWaitForText) {
		t.Fatalf("WaitForText(empty) error = %v, want ErrInvalidWaitForText", err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := count("capture-pane") - base; got != 0 {
		t.Fatalf("after-capture-pane ran %d times, want no tmux command", got)
	}
}

//libtmux:real-tmux
func TestWaitForTextRefusesAConnectionBoundServer(t *testing.T) {
	ctx := testContext(t)
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	_, connection, err := server.NewSessionConnection(
		ctx,
		tmux.NewSessionRequest{Name: "bound", Command: "printf 'bound-text\\n'; exec sleep 60"},
		tmux.ConnectionOptions{Lanes: 1},
	)
	if err != nil {
		t.Fatalf("NewSessionConnection() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	panes, err := connection.Server().Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = %v, %v, want one pane", panes, err)
	}

	_, err = panes[0].WaitForText(ctx, tmux.WaitForTextRequest{Text: "bound-text"})
	if !errors.Is(err, tmux.ErrConnectionRequiresProcess) {
		t.Fatalf("WaitForText() error = %v, want ErrConnectionRequiresProcess", err)
	}
	result, err := connection.Server().Cmd(ctx, "display-message", "-p", "still-answering")
	if err != nil || len(result.Stdout) != 1 || result.Stdout[0] != "still-answering" {
		t.Fatalf("display-message after the refusal = %+v, %v, want the connection intact", result, err)
	}
}

//libtmux:real-tmux
func TestWaitForTextReturnsTheDeadlineWhileAMatchingReadIsInFlight(t *testing.T) {
	ctx := testContext(t)
	server, pane := textWaitPane(ctx, t, "printf 'late-match\\n'; exec sleep 60", 0, 0, "")
	screenHolds(ctx, t, pane, "late-match")
	// The hook holds the client after the capture ran, so the read that would
	// match is still in flight when the deadline passes.
	if result, err := server.Cmd(ctx, "set-hook", "-g", "after-capture-pane",
		`run-shell "sleep 1"`); err != nil || result.ExitCode != 0 {
		t.Fatalf("set-hook = %+v, %v", result, err)
	}
	t.Cleanup(func() {
		_, _ = server.Cmd(context.Background(), "set-hook", "-gu", "after-capture-pane")
	})

	short, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	result, err := pane.WaitForText(short, tmux.WaitForTextRequest{Text: "late-match"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForText() error = %v, want the deadline", err)
	}
	if result.MatchedAtEntry || strings.Contains(result.Text, "late-match") {
		t.Fatalf("WaitForText() = %+v, want the deadline and no match", result)
	}
}
