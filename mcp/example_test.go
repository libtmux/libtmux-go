package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve one tmux server over stdin and stdout, which is what an agent CLI
// launches. [tmuxmcp.Run] holds the process until the client goes away.
func ExampleRun() {
	ctx := context.Background()
	target := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	// Fail at startup rather than on the first tool call when tmux cannot be
	// resolved. A tmux server that is not running yet is not an error: tmux
	// starts one when something asks it to.
	if _, err := target.Version(ctx); err != nil {
		log.Fatal(err)
	}
	if err := tmuxmcp.Run(ctx, target); err != nil {
		log.Fatal(err)
	}
}

// Put the server on a transport of your own — an in-memory pair, a pipe, or
// anything else the SDK speaks — rather than stdin and stdout.
//
// [tmuxmcp.NewServer] returns the SDK's own server, so its options, middleware
// and transports all apply.
func ExampleNewServer() {
	// The work is in a function returning an error so that the session is
	// closed on every path out of it. An example that logs and exits with a
	// close deferred above would be teaching a leak.
	if err := whichPaneAmIIn(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func whichPaneAmIIn(ctx context.Context) error {
	target := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil); err != nil {
		return err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "example", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	// Which pane is this program running in? A pane the server reports as this
	// one is the terminal the conversation is happening through.
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_server_info"})
	if err != nil {
		return err
	}
	var info struct {
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &info); err != nil {
		return err
	}
	if info.InsideThisServer {
		fmt.Println("running in pane", info.CallerPaneID)
	}
	return nil
}

// Read a pane repeatedly without paying for its whole screen each time.
//
// The first call has no cursor and returns what the pane shows now. Every call
// after it passes back the cursor from the one before and receives only what
// was written since. Check linesMissed: true means tmux discarded scrollback
// between two reads, so what came back is the current screen rather than
// everything since, and your record of that pane has a hole in it.
func Example_watchingAPane() {
	ctx := context.Background()
	session := connectedClient(ctx) // your MCP client session

	cursor := ""
	for range 3 {
		arguments := map[string]any{"paneId": "%1"}
		if cursor != "" {
			// The cursor names the pane, so paneId is not needed once you
			// have one.
			arguments = map[string]any{"cursor": cursor}
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "capture_since", Arguments: arguments,
		})
		if err != nil {
			log.Fatal(err)
		}
		var reading struct {
			Cursor      string   `json:"cursor"`
			Lines       []string `json:"lines"`
			LinesMissed bool     `json:"linesMissed"`
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &reading); err != nil {
			log.Fatal(err)
		}
		if reading.LinesMissed {
			fmt.Println("tmux discarded scrollback; this reading is not continuous")
		}
		for _, line := range reading.Lines {
			fmt.Println(line)
		}
		cursor = reading.Cursor
	}
}

// Run a command and get its exit status, rather than reading the screen and
// guessing whether it finished.
//
// A shell echoes the command it was given, so a capture taken straight after
// send_keys finds the request rather than the result. run_command waits for
// the command itself and reports what it printed alongside how it ended.
func Example_runningACommand() {
	ctx := context.Background()
	session := connectedClient(ctx) // your MCP client session

	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"paneId":         "%1",
			"command":        "go test ./...",
			"timeoutSeconds": 300,
			// A courtesy to whoever's terminal this is: a shell told to ignore
			// space-prefixed lines keeps it out of their history.
			"suppressHistory": true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	var ran struct {
		ExitStatus *int     `json:"exitStatus"`
		TimedOut   bool     `json:"timedOut"`
		Running    string   `json:"running"`
		Output     []string `json:"output"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &ran); err != nil {
		log.Fatal(err)
	}

	switch {
	case ran.TimedOut && ran.Running != "":
		// Anything but a shell here means the pane was busy and read the
		// command as that program's input. send_keys with "C-c" gets it back.
		fmt.Printf("the pane was running %s, not a shell\n", ran.Running)
	case ran.TimedOut:
		fmt.Println("still running when the wait ended")
	case *ran.ExitStatus == 0:
		fmt.Println("passed")
	default:
		fmt.Printf("failed with %d:\n%v\n", *ran.ExitStatus, ran.Output)
	}
}

// connectedClient stands in for however your program got a client session; see
// [ExampleNewServer] for one way.
func connectedClient(context.Context) *sdk.ClientSession { return nil }
