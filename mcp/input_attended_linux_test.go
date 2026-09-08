//go:build linux

package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestPaneInputRefusesAnAttendedPane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v), want one", sessions, err)
	}
	environment := append(os.Environ(),
		"TERM=xterm-256color",
		"LIBTMUX_MCP_ATTACH_HELPER=1",
		"LIBTMUX_MCP_ATTACH_BINARY="+target.Executable(),
		"LIBTMUX_MCP_ATTACH_SOCKET="+target.SocketPath(),
		"LIBTMUX_MCP_ATTACH_CONFIG="+target.ConfigFile(),
		"LIBTMUX_MCP_ATTACH_SESSION="+sessions[0].ID().String(),
	)
	process := tmuxtest.StartPTYProcess(
		ctx, t, os.Args[0],
		[]string{"-test.run=^TestPaneInputAttachHelper$"}, environment,
	)
	waitForMCPAttachedClient(ctx, t, target, process)

	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	sends := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		sends++
		return nil
	}
	_, _, err = instance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if err == nil || !strings.Contains(err.Error(), "attended") || sends != 0 {
		t.Fatalf("attended send = (error %v, sends %d), want refusal before input", err, sends)
	}
}

//libtmux:real-tmux
func TestRunCommandRechecksClientAttentionAtDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v), want one", sessions, err)
	}
	instance := mustInternalMCPServer(t, target)
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		environment := append(os.Environ(),
			"TERM=xterm-256color",
			"LIBTMUX_MCP_ATTACH_HELPER=1",
			"LIBTMUX_MCP_ATTACH_BINARY="+target.Executable(),
			"LIBTMUX_MCP_ATTACH_SOCKET="+target.SocketPath(),
			"LIBTMUX_MCP_ATTACH_CONFIG="+target.ConfigFile(),
			"LIBTMUX_MCP_ATTACH_SESSION="+sessions[0].ID().String(),
		)
		process := tmuxtest.StartPTYProcess(
			barrierCtx, t, os.Args[0],
			[]string{"-test.run=^TestPaneInputAttachHelper$"}, environment,
		)
		waitForMCPAttachedClient(barrierCtx, t, target, process)
		return nil
	}
	dispatches := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		dispatches++
		return nil
	}
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	_, _, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "attended") || dispatches != 0 {
		t.Fatalf("attended transition = (error %v, dispatches %d)", err, dispatches)
	}
}

func TestPaneInputAttachHelper(t *testing.T) {
	if os.Getenv("LIBTMUX_MCP_ATTACH_HELPER") != "1" {
		return
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary:     os.Getenv("LIBTMUX_MCP_ATTACH_BINARY"),
		SocketPath: os.Getenv("LIBTMUX_MCP_ATTACH_SOCKET"),
		ConfigFile: os.Getenv("LIBTMUX_MCP_ATTACH_CONFIG"),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = server.AttachSession(context.Background(), tmux.AttachSessionRequest{
		Target: os.Getenv("LIBTMUX_MCP_ATTACH_SESSION"),
		AttachSessionOptions: tmux.AttachSessionOptions{
			NoUpdateEnvironment: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func waitForMCPAttachedClient(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	process *tmuxtest.PTYProcess,
) {
	t.Helper()
	for {
		clients, err := server.ListClients(ctx)
		if err == nil && len(clients) == 1 {
			return
		}
		select {
		case <-process.Done():
			t.Fatalf("attach helper exited early: %v; output %q", process.Wait(ctx), process.Output())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
