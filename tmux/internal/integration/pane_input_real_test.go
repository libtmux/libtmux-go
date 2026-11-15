//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestPaneCaptureHasLineRequiresExactLine(t *testing.T) {
	t.Parallel()

	lines := []string{"$ printf 'go-pane-input\\n'", "go-pane-input"}
	if paneCaptureHasLine(lines[:1], "go-pane-input") {
		t.Fatal("paneCaptureHasLine() matched the echoed command, want false")
	}
	if !paneCaptureHasLine(lines, "go-pane-input") {
		t.Fatal("paneCaptureHasLine() did not match the output line")
	}
}

func TestSendKeysLiteralCaptureWorkflowAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	readyPath := filepath.Join(t.TempDir(), "ready")
	shellCommand := "printf ready > " + strconv.Quote(readyPath) + "; exec /bin/sh"
	pane := paneFromInputCommand(
		ctx,
		t,
		server,
		"split-window",
		"-h",
		"-d",
		"-t",
		panes[0].ID().String(),
		"-P",
		"-F",
		"#{pane_id}",
		shellCommand,
	)
	if got := waitForProcessFile(ctx, t, readyPath); got != "ready" {
		t.Fatalf("shell readiness marker = %q, want ready", got)
	}

	command := "printf '\\ngo-send-capture-workflow\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	waitForPaneCapture(ctx, t, pane, "go-send-capture-workflow")

	leadingDashCommand := "-N"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command:   &leadingDashCommand,
		Literal:   true,
		SkipEnter: true,
	}); err != nil {
		t.Fatalf("SendKeys(%q) error = %v", leadingDashCommand, err)
	}
	waitForPaneCaptureSuffix(ctx, t, pane, leadingDashCommand)
}

func TestPaneInputAndHistoryAgainstRealTmux(t *testing.T) {
	baseServer := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 1)
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         baseServer.SocketPath(),
		ConfigFile:         baseServer.ConfigFile(),
		ProcessEnvironment: baseServer.ProcessEnvironment(),
		Unsupported:        tmux.DegradeUnsupported,
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	base := panes[0]
	target := paneFromInputCommand(
		ctx,
		t,
		server,
		"split-window",
		"-h",
		"-d",
		"-t",
		base.ID().String(),
		"-P",
		"-F",
		"#{pane_id}",
		"/bin/sh",
	)
	readyCtx, cancelReady := context.WithTimeout(ctx, 5*time.Second)
	waitForPaneShellReady(readyCtx, t, target)
	cancelReady()

	sendRealPaneCommand(ctx, t, target, "printf 'go-pane-input\\n'")
	waitForPaneCapture(ctx, t, target, "go-pane-input")
	if output := captureRealPane(ctx, t, base); slices.Contains(output, "go-pane-input") {
		t.Fatalf("base pane capture = %#v, inactive target received input", output)
	}

	copyResult, copyErr := target.Cmd(ctx, "copy-mode")
	requirePaneInputCommandSuccess(t, "copy-mode", copyResult, copyErr)
	copyCommand := "cancel"
	if err := target.SendKeys(ctx, tmux.SendKeysRequest{CopyModeCommand: &copyCommand}); err != nil {
		t.Fatalf("SendKeys(CopyModeCommand) error = %v", err)
	}
	modeResult, modeErr := server.Cmd(
		ctx, "display-message", "-p", "-t", target.ID().String(), "#{pane_in_mode}",
	)
	requirePaneInputCommandSuccess(t, "display-message pane_in_mode", modeResult, modeErr)
	if !slices.Equal(modeResult.Stdout, []string{"0"}) {
		t.Fatalf("pane_in_mode = %#v, want 0 after copy-mode cancel", modeResult.Stdout)
	}

	sendRealPaneCommand(ctx, t, target, strings.Join([]string{
		"i=1",
		"while [ $i -le 80 ]",
		"do printf 'go-history-%03d\\n' \"$i\"",
		"i=$((i+1))",
		"done",
	}, "; "))
	waitForPaneCapture(ctx, t, target, "go-history-080")
	if output := captureRealPane(ctx, t, target); !slices.Contains(output, "go-history-001") {
		t.Fatalf("history before ClearHistory() = %#v, want first seeded line", output)
	}

	warnings = nil
	if err := target.ClearHistory(ctx, tmux.ClearHistoryRequest{ResetHyperlinks: true}); err != nil {
		t.Fatalf("ClearHistory() error = %v", err)
	}
	if output := captureRealPane(ctx, t, target); slices.Contains(output, "go-history-001") {
		t.Fatalf("history after ClearHistory() still contains first seeded line: %#v", output)
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version34, err := tmux.ParseVersion("3.4")
	if err != nil {
		t.Fatal(err)
	}
	wantWarning := !version.AtLeast(version34)
	if (len(warnings) != 0) != wantWarning {
		t.Fatalf("ClearHistory warnings = %#v, want warning %t on tmux %s", warnings, wantWarning, version)
	}
	if wantWarning && (warnings[0].Kind != tmux.WarningUnsupportedFeature ||
		warnings[0].Subcommand != "clear-history" ||
		warnings[0].Feature != "reset_hyperlinks") {
		t.Fatalf("ClearHistory warning = %#v", warnings[0])
	}

	sendRealPaneCommand(ctx, t, target, strings.Join([]string{
		"printf 'go-reset-old\\n'",
		"i=1",
		"while [ $i -le 80 ]",
		"do printf 'go-reset-fill-%03d\\n' \"$i\"",
		"i=$((i+1))",
		"done",
	}, "; "))
	waitForPaneCapture(ctx, t, target, "go-reset-fill-080")
	if output := captureRealPane(ctx, t, target); !slices.Contains(output, "go-reset-old") {
		t.Fatalf("history before Reset() = %#v, want old marker", output)
	}
	if err := target.Reset(ctx); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if output := captureRealPane(ctx, t, target); slices.Contains(output, "go-reset-old") {
		t.Fatalf("history after Reset() still contains old marker: %#v", output)
	}

	if err := target.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if err := target.SendPrefix(ctx, tmux.PrefixPrimary); err != nil {
		t.Fatalf("SendPrefix() error = %v", err)
	}
}

func waitForPaneShellReady(ctx context.Context, t *testing.T, pane tmux.Pane) {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready")
	command := "printf ready > " + strconv.Quote(readyPath)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := os.ReadFile(readyPath)
		if err == nil && string(ready) == "ready" {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read pane shell readiness marker: %v", err)
		}
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
			Command: &command,
			Literal: true,
		}); err != nil {
			t.Fatalf("send pane shell readiness marker: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane shell was not ready before deadline: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func sendRealPaneCommand(ctx context.Context, t *testing.T, pane tmux.Pane, command string) {
	t.Helper()
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command, Literal: true}); err != nil {
		t.Fatalf("SendKeys(%q) error = %v", command, err)
	}
}

func waitForPaneCapture(ctx context.Context, t *testing.T, pane tmux.Pane, marker string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if paneCaptureHasLine(captureRealPane(ctx, t, pane), marker) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane capture did not contain %q before deadline: %v", marker, ctx.Err())
		case <-ticker.C:
		}
	}
}

func paneCaptureHasLine(lines []string, marker string) bool {
	return slices.Contains(lines, marker)
}

func waitForPaneCaptureSuffix(ctx context.Context, t *testing.T, pane tmux.Pane, suffix string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, line := range captureRealPane(ctx, t, pane) {
			if strings.HasSuffix(line, suffix) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane capture did not end with %q before deadline: %v", suffix, ctx.Err())
		case <-ticker.C:
		}
	}
}

func captureRealPane(ctx context.Context, t *testing.T, pane tmux.Pane) []string {
	t.Helper()
	output, err := pane.Capture(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary,
		End:   tmux.CaptureBoundary,
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	return output
}

func paneFromInputCommand(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	arguments ...string,
) tmux.Pane {
	t.Helper()
	result, err := server.Cmd(ctx, arguments...)
	requirePaneInputCommandSuccess(t, arguments[0], result, err)
	if len(result.Stdout) != 1 {
		t.Fatalf("%s stdout = %#v, want one pane id", arguments[0], result.Stdout)
	}
	pane, err := server.Pane(ctx, tmux.PaneID(result.Stdout[0]))
	if err != nil {
		t.Fatalf("Pane(%s) error = %v", result.Stdout[0], err)
	}
	return pane
}

func requirePaneInputCommandSuccess(
	t *testing.T,
	operation string,
	result tmux.CommandResult,
	err error,
) {
	t.Helper()
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf(
			"%s = (exit %d, stderr %q, %v), want success",
			operation,
			result.ExitCode,
			result.Stderr,
			err,
		)
	}
}
