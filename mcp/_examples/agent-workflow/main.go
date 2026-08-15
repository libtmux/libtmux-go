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
//	go run ./_examples/agent-workflow -socket-name my-application
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
	tmuxmcp "github.com/tmux-python/libtmux/golang/mcp"
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
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	if err := call(ctx, session, "get_server_info", nil, &server); err != nil {
		return err
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

	// Run something there and wait for it. This is one call: no polling, and
	// no reading the screen to guess whether the command finished.
	var ran struct {
		ExitStatus *int     `json:"exitStatus"`
		TimedOut   bool     `json:"timedOut"`
		Output     []string `json:"output"`
	}
	if err := call(ctx, session, "run_command", map[string]any{
		"paneId":         split.PaneID,
		"command":        "tmux -V && echo ready",
		"timeoutSeconds": 30,
	}, &ran); err != nil {
		return err
	}
	switch {
	case ran.TimedOut:
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
