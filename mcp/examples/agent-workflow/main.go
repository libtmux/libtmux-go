// Command agent-workflow demonstrates caller-pane discovery, commandless pane
// creation, visible long-running work, bounded command execution, waiting, and
// topology inspection through the startup-frozen MCP surface.
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
	"io"
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
	socketName := flag.String("socket-name", "libtmux-agent-workflow",
		`tmux socket name; "default" is the one your tmux command uses`)
	traceCalls := flag.Bool("trace", false, "print the work calls and what came back, to standard error")
	flag.Parse()
	if *traceCalls {
		traced = os.Stderr
	}
	if _, set := os.LookupEnv(tmuxmcp.ToolsetsEnvironmentVariable); !set {
		if err := os.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "inspect,manage,execute"); err != nil {
			fmt.Fprintln(os.Stderr, "agent-workflow:", err)
			os.Exit(1)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := run(ctx, *socketName)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-workflow:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, socketName string) error {
	// An empty config, so what it prints does not depend on yours.
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: socketName, ConfigFile: os.DevNull})
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

	// A pane id says nothing about its window on its own. Resolve the relation
	// once instead of guessing, then use it to keep later inspection local to
	// the affected window.
	windowID, err := window(ctx, session, split.PaneID)
	if err != nil {
		return err
	}

	// Start an observation before starting long-lived work. The cursor, pane,
	// and tmux scrollback are the durable state; there is no process-local job
	// handle to lose when an MCP server restarts.
	var baseline struct {
		Cursor string `json:"cursor"`
	}
	if err := call(ctx, session, "capture_since", map[string]any{
		"pane_id": split.PaneID,
	}, &baseline); err != nil {
		return err
	}

	// paste_text with Enter starts visible work and returns as soon as the one
	// private buffer is delivered. The marker is assembled at execution time so
	// the shell's echoed command cannot satisfy wait_for_text prematurely.
	if err := call(ctx, session, "paste_text", map[string]any{
		"pane_id": split.PaneID,
		"text": "if sleep 1; then printf 'agent-workflow-%s\\n' ready; " +
			"else printf 'agent-workflow-%s\\n' failed; fi",
		"enter": true,
	}, nil); err != nil {
		return err
	}

	// Meanwhile, inspect every pane in the window without capturing its screen.
	// This is one topology snapshot while the long-lived work is still running.
	var working struct {
		Panes []paneSummary `json:"panes"`
		Total int           `json:"total"`
	}
	if err := call(ctx, session, "list_panes", nil, &working); err != nil {
		return err
	}
	fmt.Printf("%d panes in this window while the work runs\n",
		len(panesInWindow(working.Panes, windowID)))

	// Wait for output rather than sleeping in the client. Known failure text
	// stops the wait early, and the cursor makes pre-existing screen text
	// ineligible to satisfy this observation.
	var waited struct {
		Found   bool   `json:"found"`
		Outcome string `json:"outcome"`
		Matched string `json:"matched"`
	}
	if err := call(ctx, session, "wait_for_text", map[string]any{
		"pane_id": split.PaneID,
		"cursor":  baseline.Cursor,
		"patterns": []string{
			"agent-workflow-ready",
		},
		"stop":    []string{"agent-workflow-failed"},
		"timeout": 30,
	}, &waited); err != nil {
		return err
	}
	if !waited.Found {
		return fmt.Errorf("visible work ended with %s (%q)", waited.Outcome, waited.Matched)
	}

	// Collect only output written after the baseline. linesMissed would mean
	// tmux discarded part of the scrollback and the record is incomplete.
	var observed struct {
		Cursor      string   `json:"cursor"`
		Lines       []string `json:"lines"`
		LinesMissed bool     `json:"linesMissed"`
	}
	if err := call(ctx, session, "capture_since", map[string]any{
		"pane_id": split.PaneID,
		"cursor":  baseline.Cursor,
	}, &observed); err != nil {
		return err
	}
	if observed.LinesMissed {
		return fmt.Errorf("tmux discarded output before it could be collected")
	}
	fmt.Printf("the work printed %s\n", waited.Matched)

	// Bounded work is a separate workflow. run_shell_command waits with a fixed
	// ceiling and returns a framed exit status and bounded output.
	var ran struct {
		ExitStatus *int     `json:"exit_status"`
		TimedOut   bool     `json:"timed_out"`
		Output     []string `json:"output"`
	}
	if err := call(ctx, session, "run_shell_command", map[string]any{
		"pane_id": split.PaneID,
		"command": "printf 'bounded work\\n'",
		"timeout": 30,
	}, &ran); err != nil {
		return err
	}
	if ran.TimedOut || ran.ExitStatus == nil {
		return fmt.Errorf("the command did not finish in time")
	}
	fmt.Printf("the command exited %d and printed:\n", *ran.ExitStatus)
	for _, line := range ran.Output {
		fmt.Println("  |", line)
	}

	// Report what was built. get_window_info reads the window back rather
	// than assuming the split produced what was asked for, and its layout
	// string is tmux's own, which select_layout takes back.
	var built struct {
		Layout string        `json:"layout"`
		Panes  []paneSummary `json:"panes"`
	}
	if err := call(ctx, session, "get_window_info", map[string]any{
		"window_id": windowID,
	}, &built); err != nil {
		return err
	}
	if built.Layout == "" {
		return fmt.Errorf("tmux reported no layout for window %s", windowID)
	}
	fmt.Printf("the window now holds %d panes:", len(built.Panes))
	for _, pane := range built.Panes {
		fmt.Printf(" %s", pane.ID)
	}
	fmt.Println()
	return nil
}

// window reports which window a pane is in, so later listings can be narrowed
// to it. One call is better than a guess: a pane id carries no window relation.
func window(ctx context.Context, session *sdk.ClientSession, paneID string) (string, error) {
	var info struct {
		Pane struct {
			WindowID string `json:"windowId"`
		} `json:"pane"`
	}
	if err := call(ctx, session, "get_pane_info", map[string]any{
		"pane_id": paneID,
	}, &info); err != nil {
		return "", err
	}
	return info.Pane.WindowID, nil
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

// connect joins a client to the server in memory.
//
// Over stdio this is the client's job and the server is a subprocess. Tool
// names, arguments, metadata, and result shapes on either side are identical,
// which lets this example run the same protocol without installing a client.
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

// call runs one tool and decodes its structured result.
//
// A failed tool reports through an MCP result rather than a transport error so
// a model can read the reason and choose another call. A Go program wants that
// failure as an error, which is what this adapter provides.
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
	if err := traceCall(name, arguments, result.StructuredContent); err != nil {
		return fmt.Errorf("%s: trace: %w", name, err)
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

// traced receives the work calls and what came back, when set.
var traced io.Writer

// tracedReplies names the work calls traced, and the fields of each reply
// shown. A cursor is left out wherever it appears: it is a token that differs
// on every run.
var tracedReplies = map[string][]string{
	"split_window":      {"paneId"},
	"paste_text":        {"bytes"},
	"wait_for_text":     {"outcome", "found", "matched"},
	"run_shell_command": {"exit_status", "output", "timed_out"},
}

func traceCall(name string, arguments, reply any) error {
	fields, shown := tracedReplies[name]
	if traced == nil || !shown {
		return nil
	}
	sent := map[string]any{}
	if given, ok := arguments.(map[string]any); ok {
		for key, value := range given {
			if key != "cursor" {
				sent[key] = value
			}
		}
	}
	kept := map[string]any{}
	if replied, ok := reply.(map[string]any); ok {
		for _, field := range fields {
			if value, present := replied[field]; present {
				kept[field] = value
			}
		}
	}
	request, err := json.Marshal(sent)
	if err != nil {
		return err
	}
	answer, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(traced, "→ %s %s\n← %s\n", name, request, answer)
	return err
}

func contentText(result *sdk.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			return text.Text
		}
	}
	return "the call failed without a message"
}
