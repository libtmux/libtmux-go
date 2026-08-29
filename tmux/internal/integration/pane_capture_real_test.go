//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestCaptureBytesPreservesRealTmuxFraming(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	panes, err := server.Panes(ctx)
	if err != nil {
		t.Fatalf("Panes() error = %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("Panes() returned %d panes, want 1", len(panes))
	}
	pane := panes[0]
	seedCapturePane(ctx, t, server, pane)

	got, err := pane.CaptureBytes(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureLine(0),
		End:   tmux.CaptureLine(1),
	})
	if err != nil {
		t.Fatalf("CaptureBytes() error = %v", err)
	}
	want := []byte("go-capture-one\ngo-capture-two\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("CaptureBytes() = %q, want %q", got, want)
	}
}

// libtmux:parity libtmux.pane.Pane.capture_pane
// libtmux:parity libtmux.pane.Pane.capture_pane#overload:0653b86df0b2
// libtmux:parity libtmux.pane.Pane.capture_pane#overload:32a22433af94
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:alternate_screen:e91ecf2dceb1
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:end:303ee00bfa76
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:escape_non_printable:02b7db7578ad
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:escape_sequences:d40b84a4c7b9
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:hyperlinks:55a95c0433ba
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:join_wrapped:c9ba0c8a5e01
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:line_flags:06dd6ad5ce57
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:line_numbers:f20035bc8efc
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:mode_screen:b2ec82b89b59
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:pending:c991f82d2f68
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:preserve_trailing:f6e6a717b592
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:quiet:8573bc8befe4
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:start:e400519f5cf8
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:to_buffer:23593cb2f5d5
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:to_buffer:23593cb2f5d5:2
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:trim_trailing:5505ae431ffc
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027:2
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027:3
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:03c1f413095c
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:0c19eb697b50
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:2922a48ac870
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:2ec07ea00289
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:4aee8ceaa034
//
//libtmux:real-tmux
func TestPaneCaptureAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	panes, err := server.Panes(ctx)
	if err != nil {
		t.Fatalf("Panes() error = %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("Panes() returned %d panes, want 1", len(panes))
	}
	pane := panes[0]
	seedCapturePane(ctx, t, server, pane)

	output, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !slices.Contains(output, "go-capture-one") || !slices.Contains(output, "go-capture-two") {
		t.Fatalf("Capture() = %#v, want both seeded lines", output)
	}

	ranged, err := pane.Capture(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureLine(0),
		End:   tmux.CaptureLine(1),
	})
	if err != nil {
		t.Fatalf("CapturePane(range) error = %v", err)
	}
	wantLines := []string{"go-capture-one", "go-capture-two"}
	if !slices.Equal(ranged, wantLines) {
		t.Fatalf("CapturePane(range) = %#v, want %#v", ranged, wantLines)
	}

	bounded, err := pane.Capture(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary,
		End:   tmux.CaptureBoundary,
	})
	if err != nil {
		t.Fatalf("CapturePane(boundaries) error = %v", err)
	}
	if !slices.Contains(bounded, "go-capture-one") || !slices.Contains(bounded, "go-capture-two") {
		t.Fatalf("CapturePane(boundaries) = %#v, want both seeded lines", bounded)
	}

	const buffer = "go-capture-buffer"
	if err := pane.CaptureToBuffer(ctx, buffer, tmux.CapturePaneRequest{
		Start: tmux.CaptureLine(0),
		End:   tmux.CaptureLine(1),
	}); err != nil {
		t.Fatalf("CaptureToBuffer() error = %v", err)
	}
	bufferResult, err := server.Cmd(ctx, "show-buffer", "-b", buffer)
	requireCaptureCommandSuccess(t, "show-buffer", bufferResult, err)
	if !slices.Equal(bufferResult.Stdout, wantLines) {
		t.Fatalf("show-buffer stdout = %#v, want %#v", bufferResult.Stdout, wantLines)
	}
	deleteResult, err := server.Cmd(ctx, "delete-buffer", "-b", buffer)
	requireCaptureCommandSuccess(t, "delete-buffer", deleteResult, err)

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version34, err := tmux.ParseVersion("3.4")
	if err != nil {
		t.Fatal(err)
	}
	if version.AtLeast(version34) {
		trimmed, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start:        tmux.CaptureLine(0),
			End:          tmux.CaptureLine(0),
			TrimTrailing: true,
		})
		if captureErr != nil {
			t.Fatalf("CapturePane(TrimTrailing) error = %v", captureErr)
		}
		if !slices.Equal(trimmed, []string{"go-capture-one"}) {
			t.Fatalf("CapturePane(TrimTrailing) = %#v, want seeded first line", trimmed)
		}
	}

	version36, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	if version.AtLeast(version36) {
		copyResult, copyErr := pane.Cmd(ctx, "copy-mode")
		requireCaptureCommandSuccess(t, "copy-mode", copyResult, copyErr)
		mode, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start:      tmux.CaptureLine(0),
			End:        tmux.CaptureLine(1),
			ModeScreen: true,
		})
		if captureErr != nil {
			t.Fatalf("CapturePane(ModeScreen) error = %v", captureErr)
		}
		if !slices.Equal(mode, wantLines) {
			t.Fatalf("CapturePane(ModeScreen) = %#v, want %#v", mode, wantLines)
		}
		cancelResult, cancelErr := pane.Cmd(ctx, "send-keys", "-X", "cancel")
		requireCaptureCommandSuccess(t, "send-keys cancel", cancelResult, cancelErr)
	}

	version37, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}
	if version.AtLeast(version37) {
		numbered, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start:        tmux.CaptureLine(0),
			End:          tmux.CaptureLine(0),
			LineNumbers:  true,
			TrimTrailing: true,
		})
		if captureErr != nil {
			t.Fatalf("CapturePane(LineNumbers) error = %v", captureErr)
		}
		if !slices.Equal(numbered, []string{"0 go-capture-one"}) {
			t.Fatalf("CapturePane(LineNumbers) = %#v, want numbered first line", numbered)
		}
		flagged, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start:     tmux.CaptureLine(0),
			End:       tmux.CaptureLine(0),
			LineFlags: true,
		})
		if captureErr != nil {
			t.Fatalf("CapturePane(LineFlags) error = %v", captureErr)
		}
		if !slices.Equal(flagged, []string{"- go-capture-one"}) {
			t.Fatalf("CapturePane(LineFlags) = %#v, want flagged first line", flagged)
		}
	}
}

func seedCapturePane(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	pane tmux.Pane,
) {
	t.Helper()
	const lock = "go-capture-ready"
	lockResult, err := server.Cmd(ctx, "wait-for", "-L", lock)
	requireCaptureCommandSuccess(t, "wait-for lock", lockResult, err)

	script := strings.Join([]string{
		"printf 'go-capture-one\\ngo-capture-two\\n'",
		"tmux wait-for -U " + lock,
		"exec sleep 30",
	}, "; ")
	respawnResult, err := pane.Cmd(ctx, "respawn-pane", "-k", script)
	requireCaptureCommandSuccess(t, "respawn-pane", respawnResult, err)

	readyResult, err := server.Cmd(ctx, "wait-for", "-L", lock)
	requireCaptureCommandSuccess(t, "wait-for ready", readyResult, err)
	unlockResult, err := server.Cmd(ctx, "wait-for", "-U", lock)
	requireCaptureCommandSuccess(t, "wait-for unlock", unlockResult, err)
}

func requireCaptureCommandSuccess(
	t *testing.T,
	operation string,
	result tmux.CommandResult,
	err error,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s transport error = %v", operation, err)
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf(
			"%s result = exit %d stderr %q",
			operation,
			result.ExitCode,
			result.Stderr,
		)
	}
}

// The shell echoes a command before it produces output, so substring matches
// can return early. The command delays output long enough to observe this
// deterministically; whole-line matching distinguishes it.
//
//libtmux:real-tmux
func TestCaptureShowsTheShellEchoBeforeTheOutput(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "echo"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve pane: ok=%v err=%v", ok, err)
	}

	command := "sleep 600; printf 'pane ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
		t.Fatalf("send keys: %v", err)
	}

	// Wait for the echo itself rather than for the program, so the assertion
	// below runs once the screen is settled and long before the program ends.
	err = tmux.Poll(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return false, err
		}
		return strings.Contains(strings.Join(lines, "\n"), "pane ready"), nil
	})
	if err != nil {
		t.Fatalf("wait for the echoed command: %v", err)
	}

	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "pane ready") {
		t.Fatal("the echoed command left the pattern off the screen")
	}
	if slices.Contains(lines, "pane ready") {
		t.Fatal("a line equals the output while the program is still sleeping, " +
			"so comparing whole lines no longer distinguishes the echo")
	}
}
