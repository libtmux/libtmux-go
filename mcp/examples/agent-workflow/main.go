// Command agent-workflow demonstrates caller-pane discovery, commandless pane
// creation, bounded command execution, and topology inspection through the
// startup-frozen MCP surface.
//
// Its in-memory client uses the same tool names, arguments, metadata, and reply
// shapes as a stdio client.
//
//	go run ./examples/agent-workflow -socket-name my-application
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type paneSummary struct {
	ID             string `json:"id"`
	Session        string `json:"session"`
	WindowID       string `json:"windowId"`
	CurrentCommand string `json:"currentCommand"`
	Active         bool   `json:"active"`
	Geometry       struct {
		Left, Top, Width, Height int
	} `json:"geometry"`
	IsCaller *bool `json:"isCaller"`
}

func main() {
	socketName := flag.String("socket-name", "", "tmux socket name; empty uses tmux's default")
	flag.Parse()
	if _, set := os.LookupEnv(tmuxmcp.ToolsetsEnvironmentVariable); !set {
		if err := os.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "inspect,execute"); err != nil {
			fmt.Fprintln(os.Stderr, "agent-workflow:", err)
			os.Exit(1)
		}
	}
	if err := run(*socketName); err != nil {
		fmt.Fprintln(os.Stderr, "agent-workflow:", err)
		os.Exit(1)
	}
}

func run(socketName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: socketName})
	if err != nil {
		return err
	}
	session, closeSession, err := connect(ctx, target)
	if err != nil {
		return err
	}
	defer closeSession()

	// Which tmux is this, and are we inside it? Acting on the caller pane would
	// act on the conversation itself, so the client proves that identity before
	// choosing where to make room.
	var server struct {
		SocketPath       string `json:"socketPath"`
		Version          string `json:"version"`
		Alive            bool   `json:"alive"`
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	if err := call(ctx, session, "get_server_info", nil, &server); err != nil {
		return err
	}

	// A socket nobody has started yet is an ordinary arrival. Creation accepts
	// no command or environment payload; tmux starts only the configured process.
	if !server.Alive {
		fmt.Println("no tmux server on that socket yet; starting one")
		if err := call(ctx, session, "create_session", map[string]any{
			"session_name": "agent-workflow",
		}, nil); err != nil {
			return err
		}
		if err := call(ctx, session, "get_server_info", nil, &server); err != nil {
			return err
		}
	}
	fmt.Printf("tmux %s on %s\n", server.Version, server.SocketPath)

	var before struct {
		Panes []paneSummary `json:"panes"`
		Total int           `json:"total"`
	}
	if err := call(ctx, session, "list_panes", nil, &before); err != nil {
		return err
	}
	origin := server.CallerPaneID
	if server.InsideThisServer {
		fmt.Printf("running in pane %s; leaving it untouched\n", origin)
	} else {
		origin = activePane(before.Panes)
		fmt.Printf("not running inside this tmux server; using active pane %s\n", origin)
	}
	if origin == "" {
		return fmt.Errorf("selected server has no active pane")
	}

	// Make room beside the origin. split_window starts the new pane's configured
	// process and deliberately has no command field.
	var split struct {
		PaneID string `json:"paneId"`
	}
	if err := call(ctx, session, "split_window", map[string]any{
		"pane_id": origin, "direction": "right", "percent": 40,
	}, &split); err != nil {
		return err
	}
	fmt.Printf("split into %s\n", split.PaneID)

	// The executable input is a separate pane-command call. It waits with a
	// fixed ceiling and returns a real exit status and bounded output; there is
	// no detached background handle to leak across requests.
	var ran struct {
		ExitStatus *int     `json:"exit_status"`
		TimedOut   bool     `json:"timed_out"`
		Output     []string `json:"output"`
	}
	if err := call(ctx, session, "run_shell_command", map[string]any{
		"pane_id": split.PaneID,
		"command": "sleep 2 && tmux -V && echo ready",
		"timeout": 30,
	}, &ran); err != nil {
		return err
	}
	if ran.TimedOut {
		fmt.Println("the command did not finish in time")
	} else if ran.ExitStatus != nil {
		fmt.Printf("exit %d, %d lines of output\n", *ran.ExitStatus, len(ran.Output))
		for _, line := range ran.Output {
			fmt.Println("  |", line)
		}
	}

	// Resolve the new pane's window instead of guessing from a pane id, then
	// inspect the whole window. list_panes checks process state without reading
	// terminal content; get_window_info returns tmux's layout and geometry.
	var paneInfo struct {
		Pane paneSummary `json:"pane"`
	}
	if err := call(ctx, session, "get_pane_info", map[string]any{
		"pane_id": split.PaneID,
	}, &paneInfo); err != nil {
		return err
	}
	var after struct {
		Panes []paneSummary `json:"panes"`
		Total int           `json:"total"`
	}
	if err := call(ctx, session, "list_panes", nil, &after); err != nil {
		return err
	}
	windowPanes := panesInWindow(after.Panes, paneInfo.Pane.WindowID)
	fmt.Printf("%d panes in the affected window, %d on the server:\n",
		len(windowPanes), after.Total)
	for _, pane := range windowPanes {
		fmt.Printf("  %s running %s at %d,%d %dx%d\n",
			pane.ID, pane.CurrentCommand,
			pane.Geometry.Left, pane.Geometry.Top,
			pane.Geometry.Width, pane.Geometry.Height)
	}

	var window struct {
		Layout string        `json:"layout"`
		Width  int           `json:"width"`
		Height int           `json:"height"`
		Panes  []paneSummary `json:"panes"`
	}
	if err := call(ctx, session, "get_window_info", map[string]any{
		"window_id": paneInfo.Pane.WindowID,
	}, &window); err != nil {
		return err
	}
	fmt.Printf("window %dx%d, layout %s\n", window.Width, window.Height, window.Layout)
	return nil
}

func activePane(panes []paneSummary) string {
	for _, pane := range panes {
		if pane.Active {
			return pane.ID
		}
	}
	return ""
}

func panesInWindow(panes []paneSummary, windowID string) []paneSummary {
	selected := make([]paneSummary, 0, len(panes))
	for _, pane := range panes {
		if pane.WindowID == windowID {
			selected = append(selected, pane)
		}
	}
	return selected
}

// connect joins a client to the server in memory. A stdio client owns the
// opposite side of a pipe instead; every tool call around it is identical.
func connect(ctx context.Context, target tmux.Server) (*sdk.ClientSession, func(), error) {
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance, err := tmuxmcp.NewServer(target)
	if err != nil {
		return nil, nil, fmt.Errorf("construct the server: %w", err)
	}
	serverSession, err := instance.Connect(
		ctx, tmuxmcp.AssumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		_ = instance.Close()
		return nil, nil, fmt.Errorf("start the server: %w", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "agent-workflow", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		_ = instance.Close()
		return nil, nil, fmt.Errorf("connect a client: %w", err)
	}
	return session, func() {
		_ = session.Close()
		_ = serverSession.Close()
		_ = instance.Close()
	}, nil
}

// call turns an MCP tool error result into an ordinary Go error and decodes a
// successful structured result.
func call(
	ctx context.Context,
	session *sdk.ClientSession,
	name string,
	arguments any,
	into any,
) error {
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if result.IsError {
		return fmt.Errorf("%s: %s", name, contentText(result))
	}
	if into == nil {
		return nil
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func contentText(result *sdk.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			return text.Text
		}
	}
	return "the call failed without a message"
}
