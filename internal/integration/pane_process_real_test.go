//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.paste_buffer
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:bracket:f0ca1fd9e751
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:delete_after:c39a94610567
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:linefeed_separator:e71619e032d9
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:no_vis:5a188285cf0c
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:separator:70ecb771763a
// libtmux:parity libtmux.pane.Pane.paste_buffer#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.paste_buffer#warning:7e6d34b02246
// libtmux:parity libtmux.pane.Pane.pipe
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:command:581de879aee9
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:input_only:09d5057c767b
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:output_only:3a41f8d69dcf
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:toggle:fecb07ce0a7c
// libtmux:parity libtmux.pane.Pane.respawn
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:kill:c73eb1e87efe
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:start_directory:d91549582997
//
//libtmux:real-tmux
func TestPaneRespawnPasteAndPipeAgainstRealTmux(t *testing.T) {
	baseServer := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 1)
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         baseServer.SocketPath(),
		ConfigFile:         baseServer.ConfigFile(),
		ProcessEnvironment: baseServer.ProcessEnvironment(),
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	}).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, beta, _, betaWindow := newLinkedProcessWindow(ctx, t, server, "pane-process")
	if err := beta.SetEnvironment(ctx, "RESPAWN_CONTEXT", "beta", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	pane := betaWindow.Panes()[0]
	workDirectory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	respawnOutput := filepath.Join(workDirectory, "pane-respawn-context")
	respawnCommand := "printf '%s|%s' \"$RESPAWN_CONTEXT\" \"$REQUEST_VALUE\" > " +
		strconv.Quote(respawnOutput) + "; sleep 30"
	respawned, err := pane.Respawn(ctx, tmux.RespawnRequest{
		Command:     &respawnCommand,
		Environment: map[string]string{"REQUEST_VALUE": "request"},
		Kill:        true,
	})
	if err != nil {
		t.Fatalf("Respawn() error = %v", err)
	}
	if respawned.ID() != pane.ID() {
		t.Fatalf("Respawn() PaneID = %s, want stable %s", respawned.ID(), pane.ID())
	}
	if got := waitForProcessFile(ctx, t, respawnOutput); got != "beta|request" {
		t.Fatalf("pane respawn context = %q, want beta|request", got)
	}

	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	pane = betaWindow.Panes()[0]
	readyPath := filepath.Join(workDirectory, "paste-ready")
	pasteOutput := filepath.Join(workDirectory, "paste-byte")
	readerCommand := "printf ready > " + strconv.Quote(readyPath) +
		"; stty raw; od -An -tx1 -N1 > " + strconv.Quote(pasteOutput) + "; sleep 30"
	if _, err := pane.Respawn(ctx, tmux.RespawnRequest{
		Command: &readerCommand,
		Kill:    true,
	}); err != nil {
		t.Fatalf("Respawn(reader) error = %v", err)
	}
	if got := waitForProcessFile(ctx, t, readyPath); got != "ready" {
		t.Fatalf("reader ready marker = %q", got)
	}
	nulPath := filepath.Join(workDirectory, "nul-buffer")
	if err := os.WriteFile(nulPath, []byte{0}, 0o600); err != nil {
		t.Fatal(err)
	}
	bufferName := "raw-nul"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{
		Path: nulPath,
		Name: &bufferName,
	}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}
	warnings = nil
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName:  &bufferName,
		DeleteAfter: true,
		NoVis:       true,
	}); err != nil {
		t.Fatalf("PasteBuffer() error = %v", err)
	}
	if got := strings.TrimSpace(waitForProcessFile(ctx, t, pasteOutput)); got != "00" {
		t.Fatalf("raw pasted byte = %q, want 00", got)
	}
	if _, err := server.ShowBuffer(ctx, &bufferName); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("ShowBuffer(deleted) error = %v, want ErrCommand", err)
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version37, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}
	wantWarning := !version.AtLeast(version37)
	if (len(warnings) == 1) != wantWarning {
		t.Fatalf("PasteBuffer() warnings = %#v, want warning %t on %s", warnings, wantWarning, version)
	}
	if wantWarning && (warnings[0].Kind != tmux.WarningUnsupportedFeature ||
		warnings[0].Subcommand != "paste-buffer" || warnings[0].Feature != "no_vis") {
		t.Fatalf("PasteBuffer() warning = %#v", warnings[0])
	}

	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	pane = betaWindow.Panes()[0]
	pipeOutput := filepath.Join(workDirectory, "pipe-context")
	pipeCommand := "echo \"#{session_name}\" > " + strconv.Quote(pipeOutput) + "; sleep 30"
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	if got := strings.TrimSpace(waitForProcessFile(ctx, t, pipeOutput)); got != "pane-process-beta" {
		t.Fatalf("pipe format context = %q, want pane-process-beta", got)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{
		Command: &pipeCommand,
		Toggle:  true,
	}); err != nil {
		t.Fatalf("Pipe(toggle) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe(reopen) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	emptyCommand := ""
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &emptyCommand}); err != nil {
		t.Fatalf("Pipe(empty) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe(second reopen) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{}); err != nil {
		t.Fatalf("Pipe(nil) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	failingCommand := "exit 42"
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &failingCommand}); err != nil {
		t.Fatalf("Pipe(failing child) error = %v, want installation success", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{}); err != nil {
		t.Fatalf("Pipe(clean up failed child) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")
}

func waitForPanePipeState(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	pane tmux.Pane,
	want string,
) {
	t.Helper()
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := server.Cmd(ctx, "display-message", "-p", "-t", target, "#{pane_pipe}")
		if err == nil && result.ExitCode == 0 && len(result.Stderr) == 0 &&
			len(result.Stdout) == 1 && result.Stdout[0] == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane_pipe did not become %s before deadline: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}
