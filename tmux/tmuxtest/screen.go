package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// A tmux pane settles after the program in it is asked to do something, not
// while it is being asked, and there is no signal that says it has. Every wait
// below therefore polls, and the interesting part of each is not the polling
// but what it prints when the deadline arrives: the screen it last read, which
// is the thing a caller would otherwise add a print statement to see.
const (
	// pollInterval is how often a wait re-reads the pane. It is short because
	// the read is one tmux command against a local socket, and a wait that
	// ends as soon as its condition holds costs nothing for being eager.
	pollInterval = 10 * time.Millisecond
	// waitBudget bounds a wait whose context carries no deadline of its own.
	// Without it a condition that never holds hangs until go test's own
	// timeout, which reports every goroutine in the process and not the screen
	// that would say why.
	waitBudget = 30 * time.Second
)

// Screen returns the pane's visible lines, top to bottom, with tmux's trailing
// blank lines removed.
//
// It is what a person watching the pane would see, and nothing more: output
// that has scrolled out of view is not returned, and neither are the blank
// lines tmux pads a short screen with. Every wait here reads the same thing, so
// what they search is what a failure prints.
//
// Reach past it with [tmux.Pane.Capture] and [tmux.CaptureBoundary] for a test
// whose subject is the scrollback rather than the screen.
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
// It is the wait to reach for first: a program announcing itself, a prompt
// returning, a value appearing in a status line. [WaitForLine] is the stricter
// one, and [WaitForScreen] takes a condition of your own.
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
// It is [WaitForText] for output a test controls the whole of, such as a marker
// a command was asked to print. Prefer it there: a substring of a prompt that
// still shows the command that produced it will match text it was not meant to.
//
// A pane that never shows line fails the test with the screen it last read.
func WaitForLine(ctx context.Context, t testing.TB, pane tmux.Pane, line string) {
	t.Helper()
	WaitForScreen(ctx, t, pane, fmt.Sprintf("the line %q", line),
		func(screen []string) bool {
			for _, candidate := range screen {
				if candidate == line {
					return true
				}
			}
			return false
		})
}

// WaitForScreen blocks until the pane's screen satisfies match.
//
// It is the general form the other waits are written in, for a condition they
// do not cover: two values in order, a table with the right number of rows, a
// spinner that has stopped. The want string is what the failure reports the
// pane never showed, so it reads best as a noun phrase completing "the pane
// never showed": "three rows of results", "a prompt ending in $".
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

// WaitForShellReady blocks until the pane's shell has drawn its prompt and is
// ready to be typed at.
//
// A pane exists before its shell is reading from it, and keys sent in between
// are dropped without a word: the test that follows then waits for output from
// a command that never ran. This waits for [ShellPrompt], which the shell draws
// once it is ready, so the first thing a test sends is the first thing to
// arrive.
//
// It reads the pane rather than typing at it. A wait that probes by sending
// keys leaves every probe it sent on the screen, and the screen is what every
// other assertion here is about.
//
// [RunInPane] calls it, and a test building its own pane should too.
func WaitForShellReady(ctx context.Context, t testing.TB, pane tmux.Pane) {
	t.Helper()
	WaitForScreen(ctx, t, pane, "a shell prompt", func(screen []string) bool {
		for _, line := range screen {
			if strings.Contains(line, strings.TrimSpace(ShellPrompt)) {
				return true
			}
		}
		return false
	})
}

// Type sends command to the pane and submits it, as a person at the keyboard
// would.
//
// tmux does not interpret command, so a value holding a semicolon or a dollar
// sign arrives as written. It returns as soon as tmux has the keys, which is
// before the shell has run them: follow it with [WaitForText] or one of its
// neighbours rather than assuming the command is done.
func Type(ctx context.Context, t testing.TB, pane tmux.Pane, command string) {
	t.Helper()
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		t.Fatal(harnessFailure("send pane command", err))
	}
}

// RunInPane returns a pane running command on a tmux server of its own.
//
// It is the short way into everything else here, for a test whose subject is a
// program rather than tmux:
//
//	pane := tmuxtest.RunInPane(ctx, t, "./mytui --watch")
//	tmuxtest.WaitForText(ctx, t, pane, "ready")
//	tmuxtest.Type(ctx, t, pane, "q")
//
// The server, its session, and its window belong to t and end with it. The pane
// runs a shell with command typed into it rather than running command as the
// pane's own process, because a pane whose process exits takes its window, its
// session, and then the server with it, and a test asserting on what it left
// behind would race that teardown.
//
// Setup failures call [testing.TB.Fatal]. Use [NewServerWithOptions] and the
// resource helpers when a test needs more than one pane, a particular tmux
// binary, or a configuration of its own.
func RunInPane(ctx context.Context, t testing.TB, command string) tmux.Pane {
	t.Helper()
	server := NewServer(ctx, t)
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

// waitContext bounds a wait that would otherwise run until go test gives up.
// A caller's own deadline is always the tighter one and is left alone.
func waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, waitBudget)
}

// readScreen returns the pane's visible lines with tmux's trailing blank lines
// removed.
//
// The zero request is tmux's visible-screen default. Capturing from
// [tmux.CaptureBoundary] instead would return the scrollback, which for a pane
// that has been running a while is thousands of lines: a wait would search
// output long gone from view, and its failure would print all of it.
//
// tmux pads the visible capture to the pane's height, so a two-line program on
// a twenty-line pane reports eighteen empty lines that no assertion wants.
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
