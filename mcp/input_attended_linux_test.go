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
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestPaneInputRefusesAnAttendedPane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clearEnvironment(t, AllowAttendedInputEnvironmentVariable)
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
	clearEnvironment(t, AllowAttendedInputEnvironmentVariable)
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

// attachViewingClient attaches a client that views the fixture's session, which
// makes every pane of the window it shows attended.
func attachViewingClient(ctx context.Context, t *testing.T, target tmux.Server) {
	t.Helper()
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
}

//libtmux:real-tmux
func TestAttendedInputIsDeliveredOnlyWhenTheOperatorAllowsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	clearEnvironment(t, AllowAttendedInputEnvironmentVariable)
	target, _, panes := threePaneInputFixture(ctx, t)
	attachViewingClient(ctx, t, target)
	pane := panes[0]

	// Each server reads the setting once, when it starts.
	refusing := inputTestClient(ctx, t, target, nil)
	t.Setenv(AllowAttendedInputEnvironmentVariable, "1")
	allowing := inputTestClient(ctx, t, target, nil)

	const (
		refusedMarker   = "libtmux-attended-refused"
		deliveredMarker = "libtmux-attended-delivered"
	)
	send := func(client *sdk.ClientSession, marker string) *sdk.CallToolResult {
		return callInputTool(ctx, t, client, "send_keys", map[string]any{
			"pane_id": pane.ID().String(), "keys": []string{"echo " + marker},
			"literal": true, "enter": true,
		})
	}
	run := func(client *sdk.ClientSession, command string) *sdk.CallToolResult {
		return callInputTool(ctx, t, client, "run_shell_command", map[string]any{
			"pane_id": pane.ID().String(), "command": command, "timeout": 10,
		})
	}

	for name, result := range map[string]*sdk.CallToolResult{
		"send_keys":         send(refusing, refusedMarker),
		"run_shell_command": run(refusing, "echo "+refusedMarker),
	} {
		if !result.IsError || !strings.Contains(callToolResultText(result), "attended") {
			t.Errorf("%s with the setting unset = (%t, %q), want the attended refusal",
				name, result.IsError, callToolResultText(result))
		}
	}

	if result := send(allowing, deliveredMarker); result.IsError {
		t.Fatalf("send_keys with the setting on refused: %q", callToolResultText(result))
	}
	tmuxtest.WaitForLine(ctx, t, pane, deliveredMarker)
	result := run(allowing, "echo "+deliveredMarker+"-run")
	if result.IsError {
		t.Fatalf("run_shell_command with the setting on refused: %q", callToolResultText(result))
	}
	if status := structuredMap(t, result)["exit_status"]; status != float64(0) {
		t.Errorf("run_shell_command exit_status = %v, want 0", status)
	}

	if result := send(refusing, refusedMarker); !result.IsError {
		t.Error("a server started with the setting unset delivered after the environment changed")
	}
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), refusedMarker) {
		t.Error("the refused input reached the viewed pane")
	}
}

// The setting lifts the attended refusal and nothing else: each pane state
// that refuses input still refuses it while a client is viewing the pane.
//
//libtmux:real-tmux
func TestAllowedAttendedInputKeepsTheOtherRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv(AllowAttendedInputEnvironmentVariable, "1")
	target, window, panes := threePaneInputFixture(ctx, t)
	attachViewingClient(ctx, t, target)
	client := inputTestClient(ctx, t, target, nil)
	const marker = "libtmux-attended-must-not-arrive"

	refused := func(t *testing.T, pane tmux.Pane, want string) {
		t.Helper()
		result := callInputTool(ctx, t, client, "send_keys", map[string]any{
			"pane_id": pane.ID().String(), "keys": []string{marker}, "literal": true,
		})
		if text := callToolResultText(result); !result.IsError || !strings.Contains(text, want) {
			t.Errorf("send_keys = (%t, %q), want a refusal containing %q", result.IsError, text, want)
		}
	}
	command := func(t *testing.T, args ...string) {
		t.Helper()
		if result, err := target.Cmd(ctx, args...); err != nil || result.ExitCode != 0 {
			t.Fatalf("tmux %v = (%+v, %v)", args, result, err)
		}
	}

	t.Run("input off", func(t *testing.T) {
		command(t, "select-pane", "-t", panes[0].ID().String(), "-d")
		defer command(t, "select-pane", "-t", panes[0].ID().String(), "-e")
		refused(t, panes[0], "input disabled")
	})
	t.Run("pane mode", func(t *testing.T) {
		if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = panes[0].CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}) }()
		refused(t, panes[0], "mode")
	})
	t.Run("synchronized member", func(t *testing.T) {
		command(t, "select-pane", "-t", panes[1].ID().String(), "-d")
		defer command(t, "select-pane", "-t", panes[1].ID().String(), "-e")
		if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = window.SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{})
		}()
		refused(t, panes[0], panes[1].ID().String())
	})
	t.Run("dead pane", func(t *testing.T) {
		if err := panes[2].SetRemainOnExit(ctx, tmux.RemainOnExitOn); err != nil {
			t.Fatal(err)
		}
		exit := "exit 7"
		if _, err := panes[2].Respawn(ctx, tmux.RespawnRequest{Command: &exit, Kill: true}); err != nil {
			t.Fatal(err)
		}
		for {
			refreshed, err := panes[2].Refresh(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if dead, present := refreshed.Formats().PaneDead(); present && dead {
				break
			}
			select {
			case <-time.After(10 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		refused(t, panes[2], "no process")
	})

	lines, err := panes[0].Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), marker) {
		t.Error("a refused input reached the viewed pane")
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
