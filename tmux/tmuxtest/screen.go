package tmuxtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// Pane waits poll because tmux provides no screen-settled signal. Failures print
// the last screen read.
const (
	// pollInterval is how often a wait re-reads the local pane.
	pollInterval = 10 * time.Millisecond
	// waitBudget bounds a wait whose context has no deadline.
	waitBudget = 30 * time.Second
)

// Screen returns the pane's visible lines, top to bottom, with tmux's trailing
// blank lines removed.
//
// Scrolled-out output is excluded. Use [tmux.Pane.Capture] with
// [tmux.CaptureBoundary] to read scrollback.
//
// Read failures call [testing.TB.Fatal].
func Screen(ctx context.Context, t testing.TB, pane tmux.Pane) []string {
	t.Helper()
	lines, err := readScreen(ctx, pane)
	if err != nil {
		t.Fatal(harnessFailure("read pane screen", err))
	}
	return lines
}

// WaitForText blocks until some line of the pane's screen contains text.
//
// A pane that never shows text fails the test with the screen it last read.
func WaitForText(ctx context.Context, t testing.TB, pane tmux.Pane, text string) {
	t.Helper()
	WaitForScreen(ctx, t, pane, fmt.Sprintf("a line containing %q", text),
		func(screen []string) bool {
			for _, line := range screen {
				if strings.Contains(line, text) {
					return true
				}
			}
			return false
		})
}

// WaitForLine blocks until some line of the pane's screen is exactly line.
//
// A pane that never shows line fails the test with the screen it last read.
func WaitForLine(ctx context.Context, t testing.TB, pane tmux.Pane, line string) {
	t.Helper()
	WaitForScreen(ctx, t, pane, fmt.Sprintf("the line %q", line),
		func(screen []string) bool {
			return slices.Contains(screen, line)
		})
}

// WaitForScreen blocks until the pane's screen satisfies match.
//
// want is the noun phrase used in the failure message.
//
// match receives a newly read screen and must not retain it.
func WaitForScreen(
	ctx context.Context,
	t testing.TB,
	pane tmux.Pane,
	want string,
	match func(screen []string) bool,
) {
	t.Helper()
	if match == nil {
		t.Fatal(harnessFailure("wait for pane screen", errNilScreenMatch))
	}
	ctx, cancel := waitContext(ctx)
	defer cancel()

	var last []string
	err := WaitFor(ctx, pollInterval, func(ctx context.Context) (bool, error) {
		screen, err := readScreen(ctx, pane)
		if err != nil {
			return false, err
		}
		last = screen
		return match(screen), nil
	})
	if err == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("tmuxtest: pane %s never showed %s\n%s", pane.ID(), want, formatScreen(last))
	}
	t.Fatal(harnessFailure("wait for pane screen", err))
}

// WaitForShellReady blocks until the pane first shows output. With
// [ServerOptions.FixedShell], that expected first output is the harness prompt
// and serves as readiness evidence; custom shell startup output may not.
//
// [RunInPane] calls it, and a test building its own pane should too.
func WaitForShellReady(ctx context.Context, t testing.TB, pane tmux.Pane) {
	t.Helper()
	WaitForScreen(ctx, t, pane, "shell output", func(screen []string) bool {
		return len(screen) > 0
	})
}

// Type sends command to the pane and submits it, as a person at the keyboard
// would.
//
// command is sent literally. Type returns when tmux accepts the keys, before the
// shell finishes; follow it with a wait when completion matters.
func Type(ctx context.Context, t testing.TB, pane tmux.Pane, command string) {
	t.Helper()
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		t.Fatal(harnessFailure("send pane command", err))
	}
}

// TypeAndWait sends command to the pane and blocks until it has finished.
//
// It appends and waits for a unique marker whether the command succeeds or
// fails. It does not return the command's exit status.
func TypeAndWait(ctx context.Context, t testing.TB, pane tmux.Pane, command string) {
	t.Helper()
	marker, err := finishedMarker()
	if err != nil {
		t.Fatal(harnessFailure("generate command marker", err))
	}
	// Separated by ";" rather than "&&" so that a command which fails is still
	// a command that finished.
	Type(ctx, t, pane, command+"; printf '\\n"+marker+"\\n'")
	// Matched whole: the shell echoes the line above, and that echo contains
	// the marker as a substring of a longer line.
	WaitForLine(ctx, t, pane, marker)
}

func finishedMarker() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "tmuxtest-finished-" + hex.EncodeToString(raw), nil
}

// RunInPane creates an isolated pane, waits for its startup-file-free POSIX
// shell, and submits command with [Type]. It does not wait for command to start
// or finish. Resources belong to t; setup failures call [testing.TB.Fatal]. Use
// [NewServerWithOptions] for custom topology or configuration.
func RunInPane(ctx context.Context, t testing.TB, command string) tmux.Pane {
	t.Helper()
	initialSession := tmux.NewSessionRequest{}
	server := NewServerWithOptions(ctx, t, ServerOptions{
		FixedShell:     true,
		InitialSession: &initialSession,
	})
	session := NewSession(ctx, t, server, tmux.NewSessionRequest{})
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(harnessFailure("resolve pane for command", err))
	}
	if !ok {
		t.Fatal(harnessFailure("resolve pane for command", errNoActivePane))
	}
	WaitForShellReady(ctx, t, pane)
	Type(ctx, t, pane, command)
	return pane
}

var (
	errNilScreenMatch = errors.New("screen condition is nil")
	errNoActivePane   = errors.New("session reported no active pane")
)

func waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, waitBudget)
}

// readScreen removes the blank lines tmux pads to the pane height.
func readScreen(ctx context.Context, pane tmux.Pane) ([]string, error) {
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		return nil, err
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// formatScreen renders a screen for a failure message, indented and bounded so
// that the pane's content cannot be mistaken for the test's own output.
func formatScreen(screen []string) string {
	if len(screen) == 0 {
		return "the pane was empty"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "the pane showed %d line(s):", len(screen))
	for _, line := range screen {
		builder.WriteString("\n    | ")
		builder.WriteString(line)
	}
	return builder.String()
}
