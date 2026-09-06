//go:build linux

package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestRunCommandPreservesAnOpaqueExecutableRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	executable, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	opaque := filepath.Join(t.TempDir(), "tmux'"+string([]byte{0xff}))
	if err := os.Symlink(executable, opaque); err != nil {
		t.Fatal(err)
	}
	request := tmux.NewSessionRequest{Name: "work"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Binary: opaque, FixedShell: true, InitialSession: &request,
	})
	panes, err := target.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%v, %v)", panes, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, panes[0])
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	_, output, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err != nil || output.ExitStatus == nil || *output.ExitStatus != 0 {
		t.Fatalf("opaque-route run = (%+v, %v)", output, err)
	}
}
