package tmux

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// cancelAfterCaptureLive runs real tmux and cancels the caller's context once
// the command carrying a capture-pane has finished, the moment a deadline can
// land between a capture and the commands that follow it.
type cancelAfterCaptureLive struct {
	inner  tmuxcmd.Runner
	cancel context.CancelFunc
}

func (r *cancelAfterCaptureLive) Run(
	ctx context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	result, err := r.inner.Run(ctx, request)
	if slices.Contains(request.Arguments, "capture-pane") {
		r.cancel()
	}
	return result, err
}

func TestCaptureToFileLeavesNoScratchBufferOnRealTmux(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancelAfterCaptureLive{}
	search := "/usr/local/bin:/usr/bin:/bin"
	if override := os.Getenv("LIBTMUX_GO_TEST_TMUX_DIR"); override != "" {
		search = override + ":" + search
	}
	server, err := newServer(ServerOptions{
		SocketPath: directory + "/s",
		ConfigFile: os.DevNull,
	}, serverDependencies{
		environ: func() []string {
			return []string{"PATH=" + search, "HOME=" + directory, "TERM=xterm", "SHELL=/bin/sh"}
		},
		getwd:             os.Getwd,
		resolveExecutable: tmuxcmd.ResolveExecutable,
		executor:          runner,
	})
	if err != nil {
		t.Skipf("no usable tmux: %v", err)
	}
	background := context.Background()
	defer func() { _ = server.Kill(background) }()
	session, err := server.NewSession(background, NewSessionRequest{Name: "scratch"})
	if err != nil {
		t.Skipf("tmux cannot start a session: %v", err)
	}
	panes, err := session.SearchPanes(background, nil)
	if err != nil || len(panes) == 0 {
		t.Fatalf("panes = %v, error = %v", panes, err)
	}
	runner.cancel = cancel
	_, err = panes[0].CaptureToFile(ctx, directory+"/out", CapturePaneRequest{})
	if _, ok := errors.AsType[*VersionTooLowError](err); ok {
		t.Skipf("scratch buffers need a newer tmux: %v", err)
	}
	runner.cancel = func() {}
	buffers, err := server.ListBuffers(background, ListBuffersRequest{Format: new("#{buffer_name}")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range buffers {
		if strings.HasPrefix(name, "libtmux-go-capture-") {
			t.Fatalf("scratch buffer %q is still on the server", name)
		}
	}
}
