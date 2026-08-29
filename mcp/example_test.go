package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"time"

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
// [tmuxmcp.NewServer] returns an instance embedding the SDK server, so its
// methods remain available and Close releases resources owned by the tools.
func ExampleNewServer() {
	// The work is in a function returning an error so that the session is
	// closed on every path out of it. An example that logs and exits with a
	// close deferred above would be teaching a leak.
	if err := whichPaneAmIIn(context.Background()); err != nil {
		log.Fatal(err)
	}
	// Output: false
}

func whichPaneAmIIn(ctx context.Context) error {
	target := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-which-pane",
	})
	defer killExampleServer(target)
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "work"}); err != nil {
		return err
	}

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := tmuxmcp.NewServer(target)
	defer func() { _ = instance.Close() }()
	if _, err := instance.Connect(ctx, serverTransport, nil); err != nil {
		return err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "example", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	// Which pane is this program running in? A pane the server reports as this
	// one is the terminal the conversation is happening through. A program
	// outside the tmux server it drives is told so rather than left to guess.
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
		return nil
	}
	fmt.Println(info.InsideThisServer)
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
	session, done := connectedClient(ctx) // your MCP client session
	defer done()

	// Put something in the pane to be read back. run_command waits for it, so
	// the first reading below is taken after it has already been printed.
	if _, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"command": "printf 'deploy finished\\n'", "timeoutSeconds": 30,
		},
	}); err != nil {
		fmt.Println("run the command:", err)
		return
	}

	cursor, missed, seen := "", false, false
	for range 3 {
		arguments := map[string]any{}
		if cursor != "" {
			// The cursor names the pane, so paneId is not needed once you
			// have one.
			arguments = map[string]any{"cursor": cursor}
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "capture_since", Arguments: arguments,
		})
		if err != nil {
			fmt.Println("read the pane:", err)
			return
		}
		var reading struct {
			Cursor      string   `json:"cursor"`
			Lines       []string `json:"lines"`
			LinesMissed bool     `json:"linesMissed"`
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			fmt.Println("encode the reading:", err)
			return
		}
		if err := json.Unmarshal(encoded, &reading); err != nil {
			fmt.Println("decode the reading:", err)
			return
		}
		missed = missed || reading.LinesMissed
		seen = seen || slices.Contains(reading.Lines, "deploy finished")
		cursor = reading.Cursor
	}

	fmt.Println(missed, seen)
	// Output: false true
}

// Run a command and get its exit status, rather than reading the screen and
// guessing whether it finished.
//
// A shell echoes the command it was given, so a capture taken straight after
// send_keys finds the request rather than the result. run_command waits for
// the command itself and reports what it printed alongside how it ended.
func Example_runningACommand() {
	ctx := context.Background()
	session, done := connectedClient(ctx) // your MCP client session
	defer done()

	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"command":        "go version",
			"timeoutSeconds": 300,
			// A courtesy to whoever's terminal this is: a shell told to ignore
			// space-prefixed lines keeps it out of their history.
			"suppressHistory": true,
		},
	})
	if err != nil {
		fmt.Println("run the command:", err)
		return
	}
	var ran struct {
		ExitStatus *int     `json:"exitStatus"`
		TimedOut   bool     `json:"timedOut"`
		Running    string   `json:"running"`
		Output     []string `json:"output"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		fmt.Println("encode the result:", err)
		return
	}
	if err := json.Unmarshal(encoded, &ran); err != nil {
		fmt.Println("decode the result:", err)
		return
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
	// Output: passed
}

// connectedClient stands in for however your program got a client session; see
// [ExampleNewServer] for one way.
//
// It serves a tmux server of its own so that the examples above run against a
// real one. Setup failures panic rather than returning: nothing here is part of
// what the examples demonstrate, and a reader is not shown this function.
func connectedClient(ctx context.Context) (*sdk.ClientSession, func()) {
	target := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-client",
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "work"}); err != nil {
		panic(err)
	}
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		panic(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "example", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		panic(err)
	}
	return session, func() {
		_ = session.Close()
		_ = serverSession.Close()
		killExampleServer(target)
	}
}

// killExampleServer stops an example's server on a context of its own, since
// an example's own context may already be spent by the time it returns.
func killExampleServer(server tmux.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Kill(ctx)
}
