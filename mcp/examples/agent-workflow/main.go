// Command agent-workflow drives the tmux MCP server the way an agent does.
//
// It does the four things an agent needs to do before it can be useful in
// somebody's terminal: work out which pane it is itself running in, make room
// beside it, run something there and wait for the result, and report the shape
// of what it built.
//
// The client and the server are joined in memory rather than over a pipe, so
// this is one program rather than two. Everything else — the tool names, their
// arguments, the shape of what comes back — is exactly what a client speaking
// to libtmux-mcp over stdin and stdout sees.
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

func main() {
	socketName := flag.String("socket-name", "", "tmux socket name; empty uses tmux's default")
	flag.Parse()

	if err := run(*socketName); err != nil {
		fmt.Fprintln(os.Stderr, "agent-workflow:", err)
		os.Exit(1)
	}
}

func run(socketName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	session, err := connect(ctx, tmux.NewServer(tmux.ServerOptions{SocketName: socketName}))
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	// Which tmux is this, and are we inside it? A pane reported with isCaller
	// true is the terminal this program is running in, so acting on it acts on
	// the conversation itself.
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

	// A socket nobody has started yet is the ordinary way to arrive here, and
	// every tool below needs a pane. Asking first is what a client does rather
	// than splitting a window on a server that is not running and reading
	// tmux's connection error.
	if !server.Alive {
		fmt.Println("no tmux server on that socket yet; starting one")
		if err := call(ctx, session, "create_session", map[string]any{
			"name": "agent-workflow", "command": "sleep 3600",
		}, nil); err != nil {
			return err
		}
		if err := call(ctx, session, "get_server_info", nil, &server); err != nil {
			return err
		}
	}
	fmt.Printf("tmux %s on %s\n", server.Version, server.SocketPath)

	// Somewhere to work. Our own pane when we are inside tmux, otherwise the
	// active one, which is what every tool here means by an omitted paneId.
	origin := server.CallerPaneID
	if !server.InsideThisServer {
		origin = ""
		fmt.Println("not running inside this tmux server; using its active pane")
	} else {
		fmt.Printf("running in pane %s\n", origin)
	}

	// Make room beside it.
	var split struct {
		PaneID string `json:"paneId"`
	}
	if err := call(ctx, session, "split_window", map[string]any{
		"paneId":     origin,
		"direction":  "right",
		"percentage": 40,
	}, &split); err != nil {
		return err
	}
	fmt.Printf("split into %s\n", split.PaneID)

	// Start something there without waiting for it. detach returns as soon as
	// the command is typed, so the work below happens while it runs rather
	// than after it.
	var started struct {
		JobID string `json:"jobId"`
	}
	if err := call(ctx, session, "run_command", map[string]any{
		"paneId":  split.PaneID,
		"command": "sleep 2 && tmux -V && echo ready",
		"detach":  true,
	}, &started); err != nil {
		return err
	}
	fmt.Printf("started job %s without waiting for it\n", started.JobID)

	// Meanwhile: check on every pane in this window without capturing any of
	// them. The criteria keep the reply to the panes asked about, and detail
	// full adds each one's state from the snapshot the listing already took,
	// so this is one tmux command rather than one per pane.
	var listed struct {
		Total int `json:"total"`
		Panes []struct {
			ID             string `json:"id"`
			CurrentCommand string `json:"currentCommand"`
			Status         *struct {
				Dead         bool `json:"dead"`
				HistoryLines int  `json:"historyLines"`
			} `json:"status"`
		} `json:"panes"`
	}
	if err := call(ctx, session, "list_panes", map[string]any{
		"windowId": window(ctx, session, split.PaneID),
		"detail":   "full",
	}, &listed); err != nil {
		return err
	}
	fmt.Printf("%d of %d panes in this window:\n", len(listed.Panes), listed.Total)
	for _, pane := range listed.Panes {
		lines := 0
		if pane.Status != nil {
			lines = pane.Status.HistoryLines
		}
		fmt.Printf("  %s running %s, %d lines of scrollback\n",
			pane.ID, pane.CurrentCommand, lines)
	}

	// Now collect the job. Asking again would give the same answer: the first
	// read that finds a status keeps it.
	var ran struct {
		Finished   bool     `json:"finished"`
		ExitStatus *int     `json:"exitStatus"`
		Output     []string `json:"output"`
	}
	if err := call(ctx, session, "get_job", map[string]any{
		"jobId":          started.JobID,
		"timeoutSeconds": 30,
	}, &ran); err != nil {
		return err
	}
	switch {
	case !ran.Finished:
		fmt.Println("the command did not finish in time")
	case ran.ExitStatus != nil:
		fmt.Printf("exit %d, %d lines of output\n", *ran.ExitStatus, len(ran.Output))
		for _, line := range ran.Output {
			fmt.Println("  |", line)
		}
	}

	// Report what we built. The layout string is tmux's own, and
	// select_layout takes it back, so this is also how a layout worth keeping
	// is saved.
	var window struct {
		Layout string `json:"layout"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Panes  []struct {
			ID       string `json:"id"`
			IsCaller *bool  `json:"isCaller"`
			Geometry struct {
				Left, Top, Width, Height int
			} `json:"geometry"`
		} `json:"panes"`
	}
	if err := call(ctx, session, "get_window_info", nil, &window); err != nil {
		return err
	}
	fmt.Printf("window %dx%d, layout %s\n", window.Width, window.Height, window.Layout)
	for _, pane := range window.Panes {
		mine := ""
		if pane.IsCaller != nil && *pane.IsCaller {
			mine = "  <- this program's pane"
		}
		fmt.Printf("  %s at %d,%d %dx%d%s\n", pane.ID,
			pane.Geometry.Left, pane.Geometry.Top,
			pane.Geometry.Width, pane.Geometry.Height, mine)
	}
	return nil
}

// window reports which window a pane is in, so the listing below can be
// narrowed to it. One call rather than a guess: a pane id says nothing about
// its window on its own.
func window(ctx context.Context, session *sdk.ClientSession, paneID string) string {
	var info struct {
		Pane struct {
			WindowID string `json:"windowId"`
		} `json:"pane"`
	}
	if err := call(ctx, session, "get_pane_info",
		map[string]any{"paneId": paneID}, &info); err != nil {
		return ""
	}
	return info.Pane.WindowID
}

// connect joins a client to the server in memory.
//
// Over a pipe this is the client's job and the server is a subprocess; the
// tool calls either side of it are the same, which is why an example can do
// both halves and still show the real thing.
func connect(ctx context.Context, target tmux.Server) (*sdk.ClientSession, error) {
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil); err != nil {
		return nil, fmt.Errorf("start the server: %w", err)
	}
	client := sdk.NewClient(&sdk.Implementation{
		Name: "agent-workflow", Version: "1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect a client: %w", err)
	}
	return session, nil
}

// call runs one tool and decodes its structured result.
//
// A tool that failed reports it in the result rather than as a protocol error,
// so that a model can read what went wrong and choose a different call; a
// program wants it as an error, which is what this turns it into.
func call(
	ctx context.Context,
	session *sdk.ClientSession,
	name string,
	arguments map[string]any,
	into any,
) error {
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
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
	return json.Unmarshal(encoded, into)
}

// contentText reports what a failing call said.
func contentText(result *sdk.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			return text.Text
		}
	}
	return "the call failed without a message"
}
