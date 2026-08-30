package tmuxtest_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// This test is the generated README quickstart and must remain runnable.
func TestRunInPaneReachesAnAssertionInFourLines(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// docs:tmuxtest-quickstart
	pane := tmuxtest.RunInPane(ctx, t, "printf 'ready\\n'; cat")

	tmuxtest.WaitForText(ctx, t, pane, "ready")
	tmuxtest.Type(ctx, t, pane, "a line for the program")
	tmuxtest.WaitForLine(ctx, t, pane, "a line for the program")
	// docs:end
}

// tmux pads captures to pane height; Screen removes that padding.
func TestScreenDropsTmuxPadding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "printf 'first\\nsecond\\n'")
	tmuxtest.WaitForLine(ctx, t, pane, "second")

	screen := tmuxtest.Screen(ctx, t, pane)
	if len(screen) == 0 {
		t.Fatal("Screen() = no lines, want the pane's output")
	}
	if last := screen[len(screen)-1]; strings.TrimSpace(last) == "" {
		t.Fatalf("Screen() ends with a blank line: %#v", screen)
	}
	height, _ := pane.Height()
	if len(screen) >= height && height > 0 {
		t.Fatalf("Screen() = %d lines on a pane %d high, want only what was drawn",
			len(screen), height)
	}
}

// Type must send tmux syntax literally.
func TestTypeSendsCharactersTmuxWouldOtherwiseRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "true")
	tmuxtest.Type(ctx, t, pane, `printf 'semi ; dollar $HOME done\n'`)
	tmuxtest.WaitForText(ctx, t, pane, "semi ; dollar")
}

// TestWaitForScreenTakesAConditionOfItsOwn covers the general form, and that a
// caller's want string is what a failure would report.
func TestWaitForScreenTakesAConditionOfItsOwn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "printf 'one\\ntwo\\nthree\\n'")
	tmuxtest.WaitForScreen(ctx, t, pane, "three counted lines", func(screen []string) bool {
		counted := 0
		for _, line := range screen {
			if line == "one" || line == "two" || line == "three" {
				counted++
			}
		}
		return counted == 3
	})
}

// Timeout diagnostics must include the pane, wanted condition, and captured
// screen.
func TestWaitFailureReportsTheScreen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "printf 'what the pane really shows\\n'")
	tmuxtest.WaitForText(ctx, t, pane, "what the pane really shows")

	deadline, cancelDeadline := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancelDeadline()
	message, failed := captureFatal(t, func(stand testing.TB) {
		tmuxtest.WaitForText(deadline, stand, pane, "text the pane never shows")
	})

	if !failed {
		t.Fatal("WaitForText() on absent text did not fail the test")
	}
	for _, want := range []string{
		"never showed",
		`"text the pane never shows"`,
		pane.ID().String(),
		"what the pane really shows",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("failure message = %q, want it to contain %q", message, want)
		}
	}
}

// TestWaitForScreenRejectsANilCondition keeps a caller's mistake a reported one
// rather than a wait that can never hold.
func TestWaitForScreenRejectsANilCondition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "true")
	if _, failed := captureFatal(t, func(stand testing.TB) {
		tmuxtest.WaitForScreen(ctx, stand, pane, "anything", nil)
	}); !failed {
		t.Fatal("WaitForScreen(nil) did not fail the test")
	}
}

// A command sent before the shell is ready may be dropped without an error.
func TestWaitForShellReadyPrecedesTheFirstCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{})
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%t, %v), want a pane", ok, err)
	}

	tmuxtest.WaitForShellReady(ctx, t, pane)
	tmuxtest.Type(ctx, t, pane, "printf 'accepted\\n'")
	tmuxtest.WaitForLine(ctx, t, pane, "accepted")
}

// captureFatal runs body against a stand-in for t and returns the first fatal
// it reported, rather than letting that fatal end this test.
//
// It runs on a goroutine of its own because a real Fatal does not return: the
// stand-in ends its goroutine the way testing does, so a helper's code after
// its Fatal stays unreached here exactly as it would in a failing test.
func captureFatal(t *testing.T, body func(testing.TB)) (string, bool) {
	t.Helper()
	stand := &recordingTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		body(stand)
	}()
	<-done
	return stand.message, stand.failed
}

// recordingTB records the first fatal reported to it instead of failing.
type recordingTB struct {
	testing.TB

	failed  bool
	message string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatal(args ...any) {
	r.record(fmt.Sprintln(args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.record(fmt.Sprintf(format, args...))
}

func (r *recordingTB) record(message string) {
	if !r.failed {
		r.failed = true
		r.message = message
	}
	runtime.Goexit()
}

// TypeAndWait returns after the command exits and its final output is visible.
func TestTypeAndWaitReturnsAfterTheCommandIsOver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "true")
	tmuxtest.TypeAndWait(ctx, t, pane, "printf 'slow start\\n'; sleep 0.4; printf 'slow finish\\n'")

	screen := tmuxtest.Screen(ctx, t, pane)
	var finished bool
	for _, line := range screen {
		finished = finished || line == "slow finish"
	}
	if !finished {
		t.Fatalf("TypeAndWait() returned before the command's last line: %#v", screen)
	}
}

// TypeAndWait waits for completion, not success.
func TestTypeAndWaitReturnsForACommandThatFailed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pane := tmuxtest.RunInPane(ctx, t, "true")
	tmuxtest.TypeAndWait(ctx, t, pane, "printf 'before failing\\n'; false")
	tmuxtest.WaitForLine(ctx, t, pane, "before failing")
}
