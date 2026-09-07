package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve one pinned tmux server over stdin and stdout, which is what an agent
// CLI launches. [tmuxmcp.Run] holds the process until the client goes away.
func ExampleRun() {
	ctx := context.Background()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})
	if err != nil {
		log.Fatal(err)
	}

	// Run validates the startup-frozen tool selection before it probes the
	// pinned socket. A server that is not running yet is not an error.
	if err := tmuxmcp.Run(ctx, target); err != nil {
		log.Fatal(err)
	}
}

func ExampleAdvertisedTools() {
	tools, err := tmuxmcp.AdvertisedTools(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(len(tools) > 0)
	// Output: true
}

// Put the server on a transport of your own rather than stdin and stdout. Its
// Connection must commit each response independently before Write returns nil.
//
// [tmuxmcp.NewServer] owns the SDK server behind its tracked Connect and Run
// methods. Close releases resources owned by the tools.
func ExampleNewServer() {
	if err := whichPaneAmIIn(context.Background()); err != nil {
		log.Fatal(err)
	}
	// Output: false
}

func whichPaneAmIIn(ctx context.Context) error {
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-which-pane",
	})
	if err != nil {
		return err
	}
	defer killExampleServer(target)
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "work"}); err != nil {
		return err
	}

	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		return err
	}
	defer closeSession()

	// A matching pane and socket identify the terminal carrying this process.
	// A program outside the selected tmux server is told so rather than left to
	// guess from a pane id alone.
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_server_info"})
	if err != nil {
		return err
	}
	var info struct {
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	if err := decodeStructured(result, &info); err != nil {
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
// The first call has no cursor and returns what the pane shows now. Later calls
// pass the cursor back and receive only new output. linesMissed means tmux
// discarded scrollback between reads, leaving a hole in the caller's record.
func Example_watchingAPane() {
	ctx := context.Background()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-watch-pane",
	})
	if err != nil {
		log.Fatal(err)
	}
	session, paneID, done := connectedExampleClient(ctx, target)
	defer done()

	if _, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_shell_command",
		Arguments: map[string]any{
			"pane_id": paneID, "command": "printf 'deploy finished\\n'", "timeout": 30,
		},
	}); err != nil {
		fmt.Println("run the command:", err)
		return
	}

	cursor, missed, seen := "", false, false
	for range 3 {
		result, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "capture_since",
			Arguments: map[string]any{
				"pane_id": paneID, "cursor": cursor,
			},
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
		if err := decodeStructured(result, &reading); err != nil {
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

// Keep long-lived work visible in its pane and carry only an observation
// cursor across turns. There is no detached server-side job handle to recover.
func Example_watchingAVisibleCommandAcrossTurns() {
	ctx := context.Background()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-visible-command",
	})
	if err != nil {
		log.Fatal(err)
	}
	session, paneID, done := connectedExampleClient(ctx, target)
	defer done()

	first, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "capture_since",
		Arguments: map[string]any{
			"pane_id": paneID,
		},
	})
	if err != nil {
		fmt.Println("start observing the pane:", err)
		return
	}
	var baseline struct {
		Cursor string `json:"cursor"`
	}
	if err := decodeStructured(first, &baseline); err != nil {
		fmt.Println("decode the first cursor:", err)
		return
	}

	if _, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "paste_text",
		Arguments: map[string]any{
			"pane_id": paneID,
			"text":    "sleep 0.1; printf 'ready\\n'",
			"enter":   true,
		},
	}); err != nil {
		fmt.Println("start the visible command:", err)
		return
	}

	waited, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "wait_for_text",
		Arguments: map[string]any{
			"pane_id": paneID,
			"cursor":  baseline.Cursor,
			"patterns": []string{
				"^ready$",
			},
			"regex":   true,
			"timeout": 5,
		},
	})
	if err != nil {
		fmt.Println("wait for the visible command:", err)
		return
	}
	var wait struct {
		Found bool `json:"found"`
	}
	if err := decodeStructured(waited, &wait); err != nil {
		fmt.Println("decode the wait:", err)
		return
	}

	next, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "capture_since",
		Arguments: map[string]any{
			"pane_id": paneID,
			"cursor":  baseline.Cursor,
		},
	})
	if err != nil {
		fmt.Println("read the visible command:", err)
		return
	}
	var reading struct {
		Lines []string `json:"lines"`
	}
	if err := decodeStructured(next, &reading); err != nil {
		fmt.Println("decode the new output:", err)
		return
	}

	fmt.Println(wait.Found, slices.Contains(reading.Lines, "ready"))
	// Output: true true
}

// Run a command and use its framed exit status instead of guessing from a
// screen capture whether it finished.
func Example_runningACommand() {
	ctx := context.Background()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-run-command",
	})
	if err != nil {
		log.Fatal(err)
	}
	session, paneID, done := connectedExampleClient(ctx, target)
	defer done()

	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_shell_command",
		Arguments: map[string]any{
			"pane_id": paneID, "command": "printf 'checked\\n'", "timeout": 30,
			"suppress_history": true,
		},
	})
	if err != nil {
		fmt.Println("run the command:", err)
		return
	}
	var ran struct {
		ExitStatus *int     `json:"exit_status"`
		TimedOut   bool     `json:"timed_out"`
		Running    string   `json:"running"`
		Output     []string `json:"output"`
	}
	if err := decodeStructured(result, &ran); err != nil {
		fmt.Println("decode the result:", err)
		return
	}

	switch {
	case ran.TimedOut && ran.Running != "":
		fmt.Printf("the pane was running %s, not a shell\n", ran.Running)
	case ran.TimedOut:
		fmt.Println("still running when the wait ended")
	case ran.ExitStatus != nil && *ran.ExitStatus == 0:
		fmt.Println("passed")
	case ran.ExitStatus != nil:
		fmt.Printf("failed with %d:\n%v\n", *ran.ExitStatus, ran.Output)
	default:
		fmt.Println("command status unavailable")
	}
	// Output: passed
}

func connectedExampleClient(
	ctx context.Context,
	target tmux.Server,
) (*sdk.ClientSession, string, func()) {
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		panic(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		killExampleServer(target)
		if err != nil {
			panic(err)
		}
		panic("new session has no active pane")
	}
	// A pane that has not finished starting its shell drops pasted keys on the
	// floor: nothing is reading them yet. Locally the prompt is up before the
	// first tool call; on a loaded runner it is not, and the example then
	// reports that its command never ran.
	if err := waitForExampleShell(ctx, target, pane.ID().String()); err != nil {
		killExampleServer(target)
		panic(err)
	}
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		killExampleServer(target)
		panic(err)
	}
	return session, pane.ID().String(), func() {
		closeSession()
		killExampleServer(target)
	}
}

func connectExampleClient(
	ctx context.Context,
	target tmux.Server,
) (*sdk.ClientSession, func(), error) {
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance, err := tmuxmcp.NewServer(target)
	if err != nil {
		return nil, nil, err
	}
	serverSession, err := instance.Connect(
		ctx, tmuxmcp.AssumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		_ = instance.Close()
		return nil, nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "example", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		_ = instance.Close()
		return nil, nil, err
	}
	return session, func() {
		_ = session.Close()
		_ = serverSession.Close()
		_ = instance.Close()
	}, nil
}

func decodeStructured(result *sdk.CallToolResult, into any) error {
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}

func killExampleServer(server tmux.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Kill(ctx)
}

// waitForExampleShell blocks until the pane reports a shell as its foreground
// command, which is when it starts reading keys.
func waitForExampleShell(ctx context.Context, target tmux.Server, paneID string) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		pane, found, err := examplePane(ctx, target, paneID)
		if err == nil && found {
			if command, ok := pane.CurrentCommand(); ok && exampleShell(command) {
				return waitForExampleShellReadingInput(ctx, pane, deadline)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s never reported a shell", paneID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// waitForExampleShellReadingInput proves the shell executes what it is sent.
// A forked shell already reports itself as the pane's command, so waiting for
// that alone still races the moment it starts reading: on a loaded runner the
// first keys land before readline does and are dropped, and the example then
// reports that its command never ran. Sending a marker until its output comes
// back is the signal the pane is ready for the keys that follow.
func waitForExampleShellReadingInput(
	ctx context.Context,
	pane tmux.Pane,
	deadline time.Time,
) error {
	const marker = "libtmux-go-example-ready"
	command := "printf '" + marker + "\n'"
	for {
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
			return err
		}
		for attempt := 0; attempt < 20; attempt++ {
			time.Sleep(25 * time.Millisecond)
			lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
			if err != nil {
				return err
			}
			if slices.Contains(lines, marker) {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("pane %s never ran a command", pane.ID())
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s never ran a command", pane.ID())
		}
	}
}

func examplePane(
	ctx context.Context,
	target tmux.Server,
	paneID string,
) (tmux.Pane, bool, error) {
	panes, err := target.Panes(ctx)
	if err != nil {
		return tmux.Pane{}, false, err
	}
	for _, pane := range panes {
		if pane.ID().String() == paneID {
			return pane, true, nil
		}
	}
	return tmux.Pane{}, false, nil
}

// exampleShell mirrors the package's own shell predicate. example_test.go is
// an external test package, so it cannot reach the unexported original.
func exampleShell(running string) bool {
	name := strings.ToLower(strings.TrimPrefix(filepath.Base(running), "-"))
	return slices.Contains([]string{
		"ash", "bash", "csh", "dash", "elvish", "fish", "ksh", "ksh93", "mksh",
		"nu", "nushell", "pdksh", "powershell", "pwsh", "sh", "tcsh", "zsh",
	}, name)
}
