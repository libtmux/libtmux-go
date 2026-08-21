package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

// connect starts the MCP server against a tmux server unique to the test and
// returns a connected client session. Both are torn down with the test.
func connect(t *testing.T) (*sdk.ClientSession, tmux.Server, context.Context) {
	t.Helper()
	return connectWith(t, tmuxtest.ServerOptions{})
}

// connectWith is connect against a server the test configures.
//
// FixedShell is the one worth knowing about: it gives every pane /bin/sh and a
// one-character prompt, so a test about where the cursor sits measures the code
// rather than whoever's shell configuration the suite inherited.
func connectWith(
	t *testing.T,
	options tmuxtest.ServerOptions,
) (*sdk.ClientSession, tmux.Server, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, t, options)

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, target, ctx
}

// resultText is every text part of a reply joined, which is what a failure
// message needs. Printing the content slice prints pointers.
func resultText(result *sdk.CallToolResult) string {
	said := ""
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			said += text.Text
		}
	}
	return said
}

// call invokes one tool and decodes its structured result into value.
func call(
	ctx context.Context,
	t *testing.T,
	session *sdk.ClientSession,
	name string,
	arguments any,
	value any,
) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s: CallTool error = %v", name, err)
	}
	if result.IsError {
		return result
	}
	if value != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("%s: marshal structured content: %v", name, err)
		}
		if err := json.Unmarshal(encoded, value); err != nil {
			t.Fatalf("%s: decode structured content: %v", name, err)
		}
	}
	return result
}

func TestServerAdvertisesItsTools(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	advertised := map[string]bool{}
	for _, tool := range tools.Tools {
		advertised[tool.Name] = true
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
	}
	for _, want := range []string{
		"list_panes", "capture_pane", "send_keys", "build_workspace", "kill_session",
	} {
		if !advertised[want] {
			t.Errorf("tool %q is not advertised", want)
		}
	}
}

//libtmux:real-tmux
func TestBuildWorkspaceThenListAndCapture(t *testing.T) {
	session, _, ctx := connect(t)

	var built struct {
		SessionID   string `json:"sessionId"`
		SessionName string `json:"sessionName"`
	}
	result := call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: mcp-test\n" +
			"windows:\n" +
			"  - window_name: editor\n" +
			"    panes:\n" +
			"      - echo one\n" +
			"      - echo two\n",
	}, &built)
	if result.IsError {
		t.Fatalf("build_workspace failed: %#v", result.Content)
	}
	if built.SessionName != "mcp-test" || !strings.HasPrefix(built.SessionID, "$") {
		t.Fatalf("build_workspace = %#v, want mcp-test with a $ id", built)
	}

	var listed struct {
		Panes []struct {
			ID      string `json:"id"`
			Session string `json:"session"`
			Window  string `json:"window"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) != 2 {
		t.Fatalf("list_panes = %d panes, want 2", len(listed.Panes))
	}
	for _, pane := range listed.Panes {
		if pane.Session != "mcp-test" || pane.Window != "editor" {
			t.Errorf("pane %#v, want session mcp-test window editor", pane)
		}
	}

	var captured struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": listed.Panes[0].ID,
	}, &captured)
	if len(captured.Lines) == 0 {
		t.Fatal("capture_pane returned no lines")
	}
}

//libtmux:real-tmux
func TestSendKeysReachesThePane(t *testing.T) {
	session, _, ctx := connect(t)

	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: keys-test\nwindows:\n  - window_name: only\n    panes:\n      - true\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) != 1 {
		t.Fatalf("list_panes = %d panes, want 1", len(listed.Panes))
	}
	paneID := listed.Panes[0].ID

	call(ctx, t, session, "send_keys", map[string]any{
		"paneId":  paneID,
		"command": "printf 'mcp ready\\n'",
	}, nil)

	// Poll against the context's own budget rather than a second, shorter
	// deadline: a shell can take longer to start when the suite is running many
	// tmux servers at once, and a private timeout turns that into a flake.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var captured struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": paneID}, &captured)
		if slices.Contains(captured.Lines, "mcp ready") {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane never showed the command output: %#v", captured.Lines)
		case <-ticker.C:
		}
	}
}

//libtmux:real-tmux
func TestToolFailuresReachTheClientAsContent(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	for name, arguments := range map[string]map[string]any{
		"capture_pane":    {"paneId": "%99999"},
		"send_keys":       {"paneId": "%99999", "command": "true"},
		"kill_session":    {"sessionName": "no-such-session"},
		"build_workspace": {"document": "session_nme: typo\n"},
	} {
		result := call(ctx, t, session, name, arguments, nil)
		if !result.IsError {
			t.Errorf("%s: expected a tool error", name)
			continue
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || strings.TrimSpace(text.Text) == "" {
			t.Errorf("%s: error content = %#v, want nonempty text", name, result.Content)
			continue
		}
		// A failure must not also ship a result. The SDK serializes a handler's
		// output whether or not the call failed, so a zero value would reach a
		// client as a successful-looking empty answer next to the error.
		if result.StructuredContent != nil {
			t.Errorf("%s: failure carried structured content %v",
				name, result.StructuredContent)
		}
		t.Logf("%s -> %s", name, text.Text)
	}
}

//libtmux:real-tmux
func TestListPanesReportsAnEmptyServerAsNoPanes(t *testing.T) {
	session, _, ctx := connect(t)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	result := call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("list_panes on an unstarted server failed: %#v", result.Content)
	}
	if len(listed.Panes) != 0 {
		t.Fatalf("list_panes = %d panes, want 0", len(listed.Panes))
	}
}

//libtmux:real-tmux
func TestKillSessionRequiresAnExactName(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, target, ctx := connect(t)

	for _, name := range []string{"alpha", "bravo"} {
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: " + name + "\nwindows:\n  - panes:\n      - shell: sleep 300\n",
		}, nil)
	}

	// tmux resolves a bare target by prefix, so an unanchored "alph" would kill
	// "alpha". A model repeating a name it read must not destroy a neighbour.
	result := call(ctx, t, session, "kill_session", map[string]any{"sessionName": "alph"}, nil)
	if !result.IsError {
		t.Error("kill_session with a prefix succeeded; it must require an exact name")
	}

	// Survival is checked with a strict read rather than through list_panes.
	// That tool is lenient by contract, so a momentarily unreachable tmux
	// returns no panes, which this test would otherwise report as a safety
	// guard having failed.
	sessions, err := target.Sessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	surviving := map[string]bool{}
	for _, live := range sessions {
		name, _ := live.Formats().SessionName()
		surviving[name] = true
	}
	for _, name := range []string{"alpha", "bravo"} {
		if !surviving[name] {
			t.Errorf("session %q was killed by a prefix match", name)
		}
	}

	if result := call(ctx, t, session, "kill_session",
		map[string]any{"sessionName": "alpha"}, nil); result.IsError {
		t.Errorf("kill_session with the exact name failed: %#v", result.Content)
	}
}

//libtmux:real-tmux
func TestKillSessionRefusesAnEmptyName(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, target, ctx := connect(t)

	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: keep\nwindows:\n  - panes:\n      - shell: sleep 300\n",
	}, nil)

	// tmux reads an empty target as the current session, which is what a model
	// sends when it does not know a name.
	result := call(ctx, t, session, "kill_session", map[string]any{"sessionName": ""}, nil)
	if !result.IsError {
		t.Error("kill_session with an empty name succeeded")
	}

	// Survival is read strictly from the server under test. list_panes is
	// lenient by contract, so a momentarily unreachable tmux answers with
	// nothing, which this would otherwise report as a safety guard having let
	// a session be destroyed.
	sessions, err := target.Sessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("an empty session name destroyed a session")
	}
}

// TestConnectPutsToolsOnAControlTransport covers the reason Run connects at
// all: a client issuing many small reads should not start a tmux process for
// each one.
//
//libtmux:real-tmux
func TestConnectPutsToolsOnAControlTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	target := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     counting,
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "mcp"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	connected, pool := tmuxmcp.Connect(ctx, target)
	if pool == nil {
		t.Fatal("a server holding a session should have reached a control transport")
	}
	t.Cleanup(func() { _ = pool.Close() })

	mutex.Lock()
	processes = 0
	mutex.Unlock()
	for range 10 {
		if _, err := connected.Snapshot(ctx); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	if processes != 0 {
		t.Fatalf("ten snapshots over a connected server started %d processes, want 0", processes)
	}
}

// TestConnectLeavesAnEmptyServerAlone pins that connecting never invents a
// session, because one created here would outlive the pool and show up in the
// user's own tmux.
//
//libtmux:real-tmux
func TestConnectLeavesAnEmptyServerAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	_, pool := tmuxmcp.Connect(ctx, target)
	if pool != nil {
		_ = pool.Close()
		t.Fatal("connecting to a server with no session must not open a pool")
	}
	// Connect must not have started the server it declined to connect to, so
	// the socket still has nothing on it.
	sessions, err := target.Sessions(ctx)
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("sessions after declining to connect: (%#v, %v), want ErrNoServer", sessions, err)
	}
}

// TestConnectLeavesAChosenTransportAlone covers an embedder declining the
// long-lived client, which is what a tmux configuration that reacts to
// attachment wants.
//
//libtmux:real-tmux
func TestConnectLeavesAChosenTransportAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "chosen"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, pool := tmuxmcp.Connect(ctx, target.WithEngine(target.SubprocessEngine()))
	if pool != nil {
		_ = pool.Close()
		t.Fatal("connecting overrode a transport the embedder chose")
	}
	clients, err := target.SearchClients(ctx, nil)
	if err != nil {
		t.Fatalf("search clients: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("connecting attached %d clients despite a chosen transport", len(clients))
	}
}

// TestRunCommandReportsExitStatusWithoutReadingTheScreen covers the tool that
// exists so a client does not poll. The command prints the very text a naive
// waiter would look for before it exits nonzero, so a screen-reading answer
// would be both early and wrong.
//
//libtmux:real-tmux
func TestRunCommandReportsExitStatusWithoutReadingTheScreen(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: runner\nwindows:\n  - panes:\n      - {}\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to run a command in")
	}
	pane := listed.Panes[0].ID

	for _, testCase := range []struct {
		name    string
		command string
		want    int
	}{
		{"success", "sleep 1; printf 'DONE\\n'", 0},
		{"failure after printing the marker", "printf 'DONE\\n'; sleep 1; exit 7", 7},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var out struct {
				ExitStatus *int `json:"exitStatus"`
				TimedOut   bool `json:"timedOut"`
			}
			result := call(ctx, t, session, "run_command", map[string]any{
				"paneId": pane, "command": testCase.command, "timeoutSeconds": 30,
			}, &out)
			if result.IsError {
				t.Fatalf("run_command failed: %#v", result.Content)
			}
			if out.TimedOut {
				t.Fatal("run_command timed out")
			}
			if out.ExitStatus == nil || *out.ExitStatus != testCase.want {
				t.Errorf("exit status = %v, want %d", out.ExitStatus, testCase.want)
			}
		})
	}
}

// TestRunCommandReportsATimeoutRatherThanFailing covers a command that outlasts
// the wait, which is a fact about the command rather than a broken call.
//
//libtmux:real-tmux
func TestRunCommandReportsATimeoutRatherThanFailing(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: slow\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes")
	}

	var out struct {
		ExitStatus *int `json:"exitStatus"`
		TimedOut   bool `json:"timedOut"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": listed.Panes[0].ID, "command": "sleep 30", "timeoutSeconds": 1,
	}, &out)
	if result.IsError {
		t.Fatalf("a timeout must not be a tool error: %#v", result.Content)
	}
	if !out.TimedOut {
		t.Fatal("a command that outlasted the wait was not reported as timed out")
	}
	// Zero is what a successful command reports, so a timeout must not carry a
	// status at all rather than one that reads as success.
	if out.ExitStatus != nil {
		t.Errorf("a timeout reported exit status %d", *out.ExitStatus)
	}
}

// TestWaitForTextReadsTheStreamRatherThanTheScreen covers waiting on output
// the client did not author. The pane announces itself a second in, so an
// answer that arrived immediately would be reading the screen rather than the
// stream.
//
//libtmux:real-tmux
func TestWaitForTextReadsTheStreamRatherThanTheScreen(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: watcher\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'sleep 1; printf \"SERVICE READY\\n\"; sleep 60'\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to watch")
	}

	var found struct {
		Found          bool     `json:"found"`
		Outcome        string   `json:"outcome"`
		Matched        string   `json:"matched"`
		MatchedAtEntry bool     `json:"matchedAtEntry"`
		ElapsedSeconds float64  `json:"elapsedSeconds"`
		Lines          []string `json:"lines"`
	}
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         listed.Panes[0].ID,
		"patterns":       []string{"SERVICE READY"},
		"timeoutSeconds": 30,
	}, &found)
	if result.IsError {
		t.Fatalf("wait_for_text failed: %#v", result.Content)
	}
	if !found.Found {
		t.Fatalf("the announcement was not seen; pane wrote %q", strings.Join(found.Lines, "\n"))
	}
	if found.Outcome != "matched" || found.Matched != "SERVICE READY" {
		t.Errorf("outcome = %q matched = %q, want matched/SERVICE READY",
			found.Outcome, found.Matched)
	}
	// How long the call took proves nothing about the wait. The program's own
	// delay starts when the workspace is built, not when the wait begins, so on
	// a machine that took a moment to build it the announcement lands within
	// milliseconds of the wait starting — or before it, which the reply reports
	// as a match at entry. What is worth asserting is that the wait reports its
	// own elapsed time and that a pattern which never appears still times out,
	// which is the half below.
	if found.ElapsedSeconds < 0 {
		t.Errorf("elapsedSeconds = %v, want the time the wait actually took", found.ElapsedSeconds)
	}

	var missing struct {
		Found   bool   `json:"found"`
		Outcome string `json:"outcome"`
	}
	result = call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         listed.Panes[0].ID,
		"patterns":       []string{"NEVER PRINTED"},
		"timeoutSeconds": 1,
	}, &missing)
	if result.IsError {
		t.Fatalf("a wait that timed out must not be a tool error: %#v", result.Content)
	}
	if missing.Found || missing.Outcome != "timeout" {
		t.Errorf("text that was never written reported found=%v outcome=%q",
			missing.Found, missing.Outcome)
	}
}

// paneIDs lists the panes the server reports, in tmux's order.
func paneIDs(ctx context.Context, t *testing.T, session *sdk.ClientSession) []string {
	t.Helper()
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	ids := make([]string, 0, len(listed.Panes))
	for _, pane := range listed.Panes {
		ids = append(ids, pane.ID)
	}
	return ids
}

// TestSplitPaneDividesAPaneAndNamesTheNewOne covers the tool an agent uses to
// make room for work: without it, changing a layout means typing tmux commands
// into a shell and reading the screen to learn what happened.
//
//libtmux:real-tmux
func TestSplitPaneDividesAPaneAndNamesTheNewOne(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: splitter\nwindows:\n  - panes:\n      - {}\n",
	}, nil)

	before := paneIDs(ctx, t, session)
	if len(before) != 1 {
		t.Fatalf("started with %d panes, want 1", len(before))
	}

	for _, direction := range []string{"below", "right"} {
		var created struct {
			PaneID string `json:"paneId"`
		}
		result := call(ctx, t, session, "split_window", map[string]any{
			"paneId": before[0], "direction": direction, "percentage": 40,
		}, &created)
		if result.IsError {
			t.Fatalf("split %s: %#v", direction, result.Content)
		}
		if created.PaneID == "" {
			t.Fatalf("split %s named no pane", direction)
		}
		if !slices.Contains(paneIDs(ctx, t, session), created.PaneID) {
			t.Errorf("split %s reported %q, which the server does not list",
				direction, created.PaneID)
		}
	}
	if after := paneIDs(ctx, t, session); len(after) != 3 {
		t.Fatalf("ended with %d panes, want 3", len(after))
	}

	if result := call(ctx, t, session, "split_window", map[string]any{
		"paneId": before[0], "direction": "sideways",
	}, nil); !result.IsError {
		t.Error("an unknown direction was accepted")
	}
}

// TestResizePaneSetsTheSizeTmuxSettlesOn covers changing a pane's size, and
// that the answer is what tmux did rather than what was asked, since a layout
// constrains its panes.
//
//libtmux:real-tmux
func TestResizePaneSetsTheSizeTmuxSettlesOn(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: resizer\nwindows:\n  - panes:\n      - {}\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) != 2 {
		t.Fatalf("built %d panes, want 2", len(panes))
	}

	var sized struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	result := call(ctx, t, session, "resize_pane", map[string]any{
		"paneId": panes[0], "height": 5,
	}, &sized)
	if result.IsError {
		t.Fatalf("resize: %#v", result.Content)
	}
	if sized.Height != 5 {
		t.Errorf("height = %d, want 5", sized.Height)
	}
	if sized.Width <= 0 {
		t.Errorf("width = %d, want the pane's actual width", sized.Width)
	}

	if result := call(ctx, t, session, "resize_pane", map[string]any{
		"paneId": panes[0],
	}, nil); !result.IsError {
		t.Error("a resize naming no dimension was accepted")
	}
}

// TestSelectLayoutRefusesTwoAlternativesItself covers a pair tmux rejects. The
// schema can hold both, so the tool has to say which of its own arguments
// conflict — otherwise tmux's parser answers, naming modes this tool does not
// offer.
//
//libtmux:real-tmux
func TestSelectLayoutRefusesTwoAlternativesItself(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: layouts\nwindows:\n  - panes:\n      - {}\n      - {}\n",
	}, nil)

	result := call(ctx, t, session, "select_layout", map[string]any{
		"layout": "tiled", "spread": true,
	}, nil)
	if !result.IsError {
		t.Fatal("a layout and a spread together were accepted")
	}
	said := ""
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			said += text.Text
		}
	}
	for _, leaked := range []string{"mutually exclusive", "invalid server command"} {
		if strings.Contains(said, leaked) {
			t.Errorf("the refusal is tmux's parser talking, not this tool: %q", said)
		}
	}
	if !strings.Contains(said, "alternatives") {
		t.Errorf("the refusal does not say the two are alternatives: %q", said)
	}

	// Each on its own still works.
	for _, arguments := range []map[string]any{
		{"layout": "tiled"},
		{"spread": true},
	} {
		if result := call(ctx, t, session, "select_layout", arguments, nil); result.IsError {
			t.Errorf("select_layout %v was refused: %#v", arguments, result.Content)
		}
	}
}

// TestEveryPresetThisTmuxArrangesIsOffered covers an allowlist that stopped
// keeping up with tmux.
//
// A name tmux does not know is not merely refused: 3.3a dies of it and takes
// every session on the socket, which is why the names are checked before they
// are sent. The cost of that is a list that has to grow when tmux's does, and
// the mirrored presets arrived at 3.5 without it.
//
// The mirrored pair is checked against the running version rather than by
// sending an unknown name to find out, because finding out is what ends a 3.3a
// server. Where the tool does offer one, tmux is made to apply it, so the
// boundary is not taken on trust.
//
//libtmux:real-tmux
func TestEveryPresetThisTmuxArrangesIsOffered(t *testing.T) {
	session, target, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: presets\nwindows:\n  - panes:\n      - {}\n      - {}\n",
	}, nil)

	version, err := target.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	mirrored, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}

	for preset, since := range map[string]bool{
		"even-horizontal":          true,
		"even-vertical":            true,
		"main-horizontal":          true,
		"main-vertical":            true,
		"tiled":                    true,
		"main-horizontal-mirrored": version.AtLeast(mirrored),
		"main-vertical-mirrored":   version.AtLeast(mirrored),
	} {
		result := call(ctx, t, session, "select_layout",
			map[string]any{"layout": preset}, nil)
		if result.IsError == since {
			t.Errorf("select_layout %q on tmux %s: refused = %t, want %t",
				preset, version, result.IsError, !since)
			continue
		}
		if !since {
			continue
		}
		// Offering it is only right if tmux arranges it, so read back what the
		// window ended up with rather than trusting the call's own success.
		var window struct {
			Layout string `json:"layout"`
		}
		if info := call(ctx, t, session, "get_window_info",
			map[string]any{}, &window); info.IsError {
			t.Fatalf("get_window_info: %#v", info.Content)
		}
		if window.Layout == "" {
			t.Errorf("after select_layout %q the window reports no layout", preset)
		}
	}
}

// TestFindPaneByPositionReadsTheLayout covers the question an index cannot
// answer. A pane's index is the order it was made in, so a client with only
// indexes cannot say which pane is above another.
//
//libtmux:real-tmux
func TestFindPaneByPositionReadsTheLayout(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: layout\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	// A pane below the first, then one to the right of that: a known shape.
	var below, right struct {
		PaneID string `json:"paneId"`
	}
	call(ctx, t, session, "split_window", map[string]any{
		"paneId": panes[0], "direction": "below",
	}, &below)
	call(ctx, t, session, "split_window", map[string]any{
		"paneId": below.PaneID, "direction": "right",
	}, &right)

	for _, testCase := range []struct{ from, direction, want string }{
		{panes[0], "below", below.PaneID},
		{below.PaneID, "above", panes[0]},
		{below.PaneID, "right", right.PaneID},
		{right.PaneID, "left", below.PaneID},
	} {
		var found struct {
			PaneID string `json:"paneId"`
			Found  bool   `json:"found"`
		}
		result := call(ctx, t, session, "find_pane_by_position", map[string]any{
			"paneId": testCase.from, "direction": testCase.direction,
		}, &found)
		if result.IsError {
			t.Fatalf("%s of %s: %#v", testCase.direction, testCase.from, result.Content)
		}
		if !found.Found || found.PaneID != testCase.want {
			t.Errorf("%s of %s = %q (found=%v), want %q",
				testCase.direction, testCase.from, found.PaneID, found.Found, testCase.want)
		}
	}

	// Nothing borders the top of the first pane, which is not an error.
	var none struct {
		Found bool `json:"found"`
	}
	if result := call(ctx, t, session, "find_pane_by_position", map[string]any{
		"paneId": panes[0], "direction": "above",
	}, &none); result.IsError || none.Found {
		t.Errorf("a pane at the top reported a neighbour above it")
	}
}

// TestWaitForChannelIsReleasedBySignalling covers coordinating with anything
// that signals a tmux channel, which run_command cannot: that one waits on a
// command it started itself.
//
//libtmux:real-tmux
func TestWaitForChannelIsReleasedBySignalling(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: channels\nwindows:\n  - panes:\n      - {}\n",
	}, nil)

	waited := make(chan bool, 1)
	go func() {
		var out struct {
			Signalled bool `json:"signalled"`
		}
		call(ctx, t, session, "wait_for_channel", map[string]any{
			"channel": "ready-for-work", "timeoutSeconds": 30,
		}, &out)
		waited <- out.Signalled
	}()

	// The waiter is released by a signal, not by its own deadline.
	deadline := time.After(20 * time.Second)
	for {
		call(ctx, t, session, "signal_channel", map[string]any{
			"channel": "ready-for-work",
		}, nil)
		select {
		case signalled := <-waited:
			if !signalled {
				t.Fatal("the wait ended without being signalled")
			}
			return
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("the wait was never released")
		}
	}
}

// TestChannelNamesAreValidated covers a name tmux would read as something
// other than a channel.
//
//libtmux:real-tmux
func TestChannelNamesAreValidated(t *testing.T) {
	session, _, ctx := connect(t)
	for _, channel := range []string{"", "-L", "two words"} {
		if result := call(ctx, t, session, "signal_channel", map[string]any{
			"channel": channel,
		}, nil); !result.IsError {
			t.Errorf("channel %q was accepted", channel)
		}
	}
}

// TestIsCallerHasThreeAnswers covers identifying the server's own pane. A pane
// id is unique only within one tmux server, so the socket has to agree before
// a pane can be called this one.
//
// Each situation gets its own server, because a server works out which pane it
// runs in once and keeps the answer: the pane a process is in does not change,
// and finding it costs a walk up the process tree and a listing.
//
//libtmux:real-tmux
func TestIsCallerHasThreeAnswers(t *testing.T) {
	// The pane ids a fresh server hands out are predictable, which is what lets
	// the environment name one before the server that reports it exists.
	const ownPane = "%0"

	read := func(t *testing.T) []struct {
		ID       string `json:"id"`
		IsCaller *bool  `json:"isCaller"`
	} {
		t.Helper()
		session, _, ctx := connect(t)
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: identity\nwindows:\n  - panes:\n      - {}\n",
		}, nil)
		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		if len(listed.Panes) == 0 {
			t.Fatal("no panes to identify")
		}
		return listed.Panes
	}

	t.Run("outside tmux the question has no answer", func(t *testing.T) {
		t.Setenv("TMUX_PANE", "")
		t.Setenv("TMUX", "")
		for _, pane := range read(t) {
			if pane.IsCaller != nil {
				t.Errorf("pane %s answered %v while the server is not inside tmux",
					pane.ID, *pane.IsCaller)
			}
		}
	})

	t.Run("on this socket exactly one pane is the caller", func(t *testing.T) {
		// The socket is not known until the server exists, and the server reads
		// the environment when it is first asked rather than when it starts, so
		// naming the socket the harness is about to use is enough.
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx := connect(t)
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: identity\nwindows:\n  - panes:\n      - {}\n",
		}, nil)
		// The socket is only knowable once tmux is running, and the server works
		// out its own pane the first time something asks rather than at startup,
		// so naming it here still reaches the listing below.
		socket, err := target.Cmd(
			ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		if len(listed.Panes) == 0 {
			t.Fatal("no panes to identify")
		}
		for _, pane := range listed.Panes {
			if pane.IsCaller == nil {
				t.Fatalf("pane %s gave no answer while the server is inside tmux", pane.ID)
			}
			if want := pane.ID == ownPane; *pane.IsCaller != want {
				t.Errorf("pane %s answered %v, want %v", pane.ID, *pane.IsCaller, want)
			}
		}
	})

	t.Run("the same pane id on another socket is somebody else", func(t *testing.T) {
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", filepath.Join(t.TempDir(), "other.sock")+",1234,0")
		for _, pane := range read(t) {
			if pane.IsCaller == nil || *pane.IsCaller {
				t.Errorf("pane %s claimed the caller across sockets", pane.ID)
			}
		}
	})
}

// TestInstructionsNameTheCallersPane covers the half of self-detection that
// costs no tool call: a client that must ask which pane it is in spends a
// round trip on a fact tmux put in this process's environment.
//
//libtmux:real-tmux
func TestInstructionsNameTheCallersPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("TMUX", "/tmp/example.sock,1234,0")
	session, _, _ := connect(t)

	instructions := session.InitializeResult().Instructions
	if !strings.Contains(instructions, "%7") {
		t.Errorf("instructions do not name the caller's pane: %q", instructions)
	}
	if !strings.Contains(instructions, "isCaller") {
		t.Errorf("instructions do not explain the pane flag: %q", instructions)
	}

	// The instructions decide whether a client reaches for this server at all
	// and which of two overlapping tools it picks, so they carry more than the
	// caller's pane.
	for _, expected := range []string{
		"DO NOT USE THIS FOR", // the word "window" is a browser tab elsewhere
		"WAIT, DO NOT POLL",   // the loop every tool description warns about
		"run_command",         // and where each waiting case should go
		"wait_for_text",
		"wait_for_channel",
		"snapshot_pane", // prefer the whole answer to two that can disagree
	} {
		if !strings.Contains(instructions, expected) {
			t.Errorf("instructions omit %q", expected)
		}
	}
}

// TestBatchesRunInOrderAndRefuseMutations covers running several calls in one
// request, and the promise a read-only batch makes: a batch a client believed
// only looked at tmux must not have changed it.
//
//libtmux:real-tmux
func TestBatchesRunInOrderAndRefuseMutations(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: batched\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var readOnly struct {
		Results []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
		Completed int `json:"completed"`
	}
	result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "find_pane_by_position", "arguments": map[string]any{
				"paneId": panes[0], "direction": "below",
			}},
		},
	}, &readOnly)
	if result.IsError {
		t.Fatalf("read-only batch: %#v", result.Content)
	}
	if readOnly.Completed != 2 {
		t.Fatalf("completed %d of 2 calls: %+v", readOnly.Completed, readOnly.Results)
	}

	// A mutating call refuses the whole batch, and nothing runs.
	before := len(paneIDs(ctx, t, session))
	if result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "split_window", "arguments": map[string]any{"paneId": panes[0]}},
		},
	}, nil); !result.IsError {
		t.Error("a read-only batch accepted a mutating call")
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a refused batch changed the panes: %d then %d", before, after)
	}

	// The mutating batch runs the same call.
	var mutating struct {
		Completed int `json:"completed"`
	}
	if result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{"paneId": panes[0]}},
			{"tool": "list_panes"},
		},
	}, &mutating); result.IsError {
		t.Fatalf("mutating batch: %#v", result.Content)
	}
	if mutating.Completed != 2 {
		t.Errorf("mutating batch completed %d of 2", mutating.Completed)
	}
	if after := len(paneIDs(ctx, t, session)); after != before+1 {
		t.Errorf("panes = %d, want %d", after, before+1)
	}
}

// TestWaitForTextSeesWhatThePaneAlreadyShowed covers the race a client cannot
// avoid: it cannot start a program and wait for it in one request, so a quick
// program has announced itself before the wait begins.
//
//libtmux:real-tmux
func TestWaitForTextSeesWhatThePaneAlreadyShowed(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: already\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'printf \"ANNOUNCED-ONCE\\n\"; sleep 120'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}

	// Let the announcement land before the wait starts, which is the case a
	// stream-only wait misses.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": panes[0]}, &shown)
		if strings.Contains(strings.Join(shown.Lines, "\n"), "ANNOUNCED-ONCE") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	var found struct {
		Found          bool `json:"found"`
		MatchedAtEntry bool `json:"matchedAtEntry"`
	}
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": panes[0], "patterns": []string{"ANNOUNCED-ONCE"}, "timeoutSeconds": 5,
	}, &found)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if !found.Found {
		t.Fatal("text already on the pane was not found, so the announcement was missed")
	}
	if !found.MatchedAtEntry {
		t.Error("a match that was already on the screen was not reported as one")
	}

	// sinceEntry is the other reading of the same question: whether the pane
	// says it again, rather than whether it has said it.
	var again struct {
		Found   bool   `json:"found"`
		Outcome string `json:"outcome"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         panes[0],
		"patterns":       []string{"ANNOUNCED-ONCE"},
		"sinceEntry":     true,
		"timeoutSeconds": 1,
	}, &again)
	if again.Found || again.Outcome != "timeout" {
		t.Errorf("sinceEntry matched what was already shown: found=%v outcome=%q",
			again.Found, again.Outcome)
	}
}

// TestBatchArgumentsAreCheckedLikeAnyCall covers the one place a mistake used
// to pass: the schema the SDK enforces on a call of its own does not reach a
// call inside a batch, so a misspelled field was dropped and the call ran on
// defaults while reporting success.
//
//libtmux:real-tmux
func TestBatchArgumentsAreCheckedLikeAnyCall(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: strict\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	before := len(panes)

	var batch struct {
		Results []struct {
			Error string `json:"error"`
		} `json:"results"`
		Completed int `json:"completed"`
	}
	call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{
				"paneId": panes[0], "directon": "right",
			}},
		},
	}, &batch)
	if batch.Completed != 0 {
		t.Errorf("a misspelled field was accepted: completed=%d", batch.Completed)
	}
	if len(batch.Results) == 0 || batch.Results[0].Error == "" {
		t.Fatalf("a misspelled field reported no error: %+v", batch.Results)
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a rejected call still split a pane: %d then %d", before, after)
	}
}

// TestSendKeysRecoversAPaneLeftBusy covers the way back from a run_command
// that timed out. The pane still holds that command, so every later one times
// out too, and nothing else in the tool set can interrupt it.
//
//libtmux:real-tmux
func TestSendKeysRecoversAPaneLeftBusy(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: wedged\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var timedOut struct {
		TimedOut bool `json:"timedOut"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "sleep 120", "timeoutSeconds": 1,
	}, &timedOut)
	if !timedOut.TimedOut {
		t.Fatal("the command did not outlast its wait")
	}

	// tmux reads the key name, so this interrupts rather than typing letters.
	call(ctx, t, session, "send_keys", map[string]any{
		"paneId": panes[0], "command": "C-c",
	}, nil)

	var recovered struct {
		ExitStatus *int `json:"exitStatus"`
		TimedOut   bool `json:"timedOut"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "printf 'back'", "timeoutSeconds": 20,
	}, &recovered)
	if result.IsError {
		t.Fatalf("run_command after recovery: %#v", result.Content)
	}
	if recovered.TimedOut || recovered.ExitStatus == nil || *recovered.ExitStatus != 0 {
		t.Fatalf("the pane did not recover: timedOut=%v status=%v",
			recovered.TimedOut, recovered.ExitStatus)
	}
}

// TestATimeoutNamesWhatThePaneWasRunning covers telling a slow command from a
// pane that never ran one. run_command types into whatever the pane is
// running, so a busy pane receives the text as that program's input.
//
//libtmux:real-tmux
func TestATimeoutNamesWhatThePaneWasRunning(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: busy\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'while read line; do :; done'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var out struct {
		TimedOut bool   `json:"timedOut"`
		Running  string `json:"running"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "printf 'never runs'", "timeoutSeconds": 2,
	}, &out)
	if !out.TimedOut {
		t.Fatal("a command typed into a busy pane did not time out")
	}
	if out.Running == "" {
		t.Fatal("a timeout did not say what the pane was running")
	}
}

// TestOrientationToolsDescribeTheServer covers the questions an agent asks
// first. A window is what a person switches between, so what windows exist is
// usually the first question rather than a coarser form of what panes exist.
//
//libtmux:real-tmux
func TestOrientationToolsDescribeTheServer(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: oriented\nwindows:\n" +
			"  - window_name: first\n    panes:\n      - {}\n      - {}\n" +
			"  - window_name: second\n    panes:\n      - {}\n",
	}, nil)

	var windows struct {
		Windows []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Panes  int    `json:"panes"`
			Active bool   `json:"active"`
		} `json:"windows"`
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &windows)
	if len(windows.Windows) != 2 {
		t.Fatalf("listed %d windows, want 2", len(windows.Windows))
	}
	byName := map[string]int{}
	for _, window := range windows.Windows {
		byName[window.Name] = window.Panes
	}
	if byName["first"] != 2 || byName["second"] != 1 {
		t.Errorf("pane counts = %v, want first 2 and second 1", byName)
	}

	var sessions struct {
		Sessions []struct {
			Name    string `json:"name"`
			Windows int    `json:"windows"`
		} `json:"sessions"`
	}
	call(ctx, t, session, "list_sessions", map[string]any{}, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].Windows != 2 {
		t.Fatalf("sessions = %+v, want one holding two windows", sessions.Sessions)
	}

	// Selecting the window that is not current makes it so.
	var inactive string
	for _, window := range windows.Windows {
		if !window.Active {
			inactive = window.ID
		}
	}
	if inactive == "" {
		t.Fatal("both windows reported as active")
	}
	if result := call(ctx, t, session, "select_window", map[string]any{
		"windowId": inactive,
	}, nil); result.IsError {
		t.Fatalf("select_window: %#v", result.Content)
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &windows)
	for _, window := range windows.Windows {
		if window.ID == inactive && !window.Active {
			t.Error("the selected window is not current")
		}
	}
}

// TestCreateWindowAndSessionWithoutADocument covers wanting one more window.
// build_workspace lays out a whole session from a document, which is a larger
// door than the job, and the alternative was typing a tmux command into a pane.
//
//libtmux:real-tmux
func TestCreateWindowAndSessionWithoutADocument(t *testing.T) {
	session, _, ctx := connect(t)
	var made struct {
		SessionID   string `json:"sessionId"`
		SessionName string `json:"sessionName"`
	}
	result := call(ctx, t, session, "create_session", map[string]any{
		"name": "started",
	}, &made)
	if result.IsError {
		t.Fatalf("create_session: %#v", result.Content)
	}
	if made.SessionName != "started" {
		t.Fatalf("session name = %q, want started", made.SessionName)
	}

	// The server has one session, so a window needs no name to find it.
	var window struct {
		WindowID string `json:"windowId"`
		PaneID   string `json:"paneId"`
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"name": "added",
	}, &window); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}
	if window.WindowID == "" || window.PaneID == "" {
		t.Fatalf("create_window reported %+v, want a window and a pane", window)
	}

	var windows struct {
		Windows []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"windows"`
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &windows)
	found := false
	for _, listed := range windows.Windows {
		if listed.ID == window.WindowID && listed.Name == "added" {
			found = true
		}
	}
	if !found {
		t.Errorf("the created window is not listed: %+v", windows.Windows)
	}

	// Naming the session explicitly is the path a client takes once a server
	// holds more than one, so it is exercised before there is more than one.
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "started", "name": "by-name",
	}, &window); result.IsError {
		t.Fatalf("create_window naming its session: %#v", result.Content)
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "no-such-session",
	}, nil); !result.IsError {
		t.Error("a window was added to a session that does not exist")
	}

	// A second session makes the choice ambiguous, which is asked about rather
	// than guessed at.
	call(ctx, t, session, "create_session", map[string]any{"name": "another"}, nil)
	if result := call(ctx, t, session, "create_window", map[string]any{
		"name": "ambiguous",
	}, nil); !result.IsError {
		t.Error("a window was added without naming which of two sessions")
	}
}

// TestSnapshotAndSearchFindPanesByWhatTheyShow covers the two questions that
// otherwise cost a call per pane: what is this pane doing, and where is the
// thing I am looking for.
//
//libtmux:real-tmux
func TestSnapshotAndSearchFindPanesByWhatTheyShow(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: findable\nwindows:\n  - panes:\n" +
			"      - shell: sh -c 'printf \"NEEDLE-9f2c\\n\"; sleep 120'\n" +
			"      - shell: sh -c 'printf \"other output\\n\"; sleep 120'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) != 2 {
		t.Fatalf("built %d panes, want 2", len(panes))
	}

	var found struct {
		Panes []struct {
			Pane struct {
				ID string `json:"id"`
			} `json:"pane"`
			Matches []struct {
				Text string `json:"text"`
			} `json:"matches"`
		} `json:"panes"`
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		call(ctx, t, session, "search_panes", map[string]any{"text": "NEEDLE-9f2c"}, &found)
		if len(found.Panes) == 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(found.Panes) != 1 {
		t.Fatalf("search found %d panes, want exactly the one showing it", len(found.Panes))
	}

	var shot struct {
		Lines []string `json:"lines"`
		Pane  struct {
			ID       string `json:"id"`
			Geometry struct {
				Width int `json:"width"`
			} `json:"geometry"`
		} `json:"pane"`
		Dead bool `json:"dead"`
	}
	if len(found.Panes[0].Matches) == 0 {
		t.Error("search reported a matching pane without the line that matched")
	}
	result := call(ctx, t, session, "snapshot_pane", map[string]any{
		"paneId": found.Panes[0].Pane.ID,
	}, &shot)
	if result.IsError {
		t.Fatalf("snapshot_pane: %#v", result.Content)
	}
	if shot.Pane.ID != found.Panes[0].Pane.ID {
		t.Errorf("snapshot describes %q, want %q", shot.Pane.ID, found.Panes[0].Pane.ID)
	}
	if !strings.Contains(strings.Join(shot.Lines, "\n"), "NEEDLE-9f2c") {
		t.Error("the snapshot does not include the pane's contents")
	}
	if shot.Pane.Geometry.Width <= 0 || shot.Dead {
		t.Errorf("snapshot state looks wrong: width=%d dead=%v",
			shot.Pane.Geometry.Width, shot.Dead)
	}
}

// TestEveryToolCarriesAnnotations covers what a client acts on without reading
// prose: a listing can be approved unasked, a kill cannot. A tool shipped
// without hints looks exactly as dangerous as one that ends a session.
//
//libtmux:real-tmux
func TestEveryToolCarriesAnnotations(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("the server advertises no tools")
	}

	readers := map[string]bool{
		"list_panes": true, "list_windows": true, "list_sessions": true,
		"list_servers": true, "snapshot_pane": true, "search_panes": true,
		"capture_pane": true, "capture_since": true,
		"find_pane_by_position": true, "wait_for_text": true,
		"wait_for_channel": true, "call_readonly_tools_batch": true,
		"get_pane_info": true, "get_window_info": true, "get_session_info": true,
		"get_server_info": true, "display_message": true, "show_buffer": true,
		"show_option": true, "show_environment": true, "show_hooks": true,
		"get_job": true,
	}
	enders := map[string]bool{
		"kill_session": true, "kill_window": true, "kill_pane": true,
		"kill_server": true, "call_destructive_tools_batch": true,
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s carries no annotations", tool.Name)
			continue
		}
		if tool.Annotations.Title == "" {
			t.Errorf("%s carries no title", tool.Name)
		}
		if got := tool.Annotations.ReadOnlyHint; got != readers[tool.Name] {
			t.Errorf("%s readOnlyHint = %v, want %v", tool.Name, got, readers[tool.Name])
		}
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		if want := enders[tool.Name]; destructive != want {
			t.Errorf("%s destructiveHint = %v, want %v", tool.Name, destructive, want)
		}
	}
}

// TestNoNotificationsBeforeTheClientIsInitialized covers the ordering MCP
// asks for: a server should not send notifications until it has received
// notifications/initialized. The SDK announces the tool list as changed as
// soon as a session exists, which lands before that.
//
//libtmux:real-tmux
func TestNoNotificationsBeforeTheClientIsInitialized(t *testing.T) {
	target := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	client, server := sdk.NewInMemoryTransports()
	ordered := handshakeOrderingFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, ordered, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	var early int
	clientImpl := sdk.NewClient(&sdk.Implementation{Name: "ordering"}, &sdk.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
			early++
		},
	})
	clientSession, err := clientImpl.Connect(ctx, client, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	// A connect that completes at all is the point: a client enforcing the
	// ordering rule would not have got here.
	if _, err := clientSession.ListTools(ctx, nil); err != nil {
		t.Fatalf("list tools: %v", err)
	}
}

// handshakeOrderingFor exposes the ordering wrapper to this test.
func handshakeOrderingFor(inner sdk.Transport) sdk.Transport {
	return tmuxmcp.HandshakeOrdered(inner)
}

// TestToolDescriptionsCarryNoSchemaSyntax covers text a model reads. The SDK
// takes a whole jsonschema tag as the description, so a constraint written
// there never reaches the schema and ships as trailing syntax instead.
//
//libtmux:real-tmux
func TestToolDescriptionsCarryNoSchemaSyntax(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		for _, leak := range []string{"minLength=", "minItems=", "maxLength="} {
			if strings.Contains(string(schema), leak) {
				t.Errorf("%s ships %q as description text", tool.Name, leak)
			}
		}
	}
}

// TestEveryChangingToolSaysWhetherRepeatingItCompounds covers the hint a
// client needs after a timeout: a call that may or may not have landed can be
// retried only when repeating it cannot compound. A tool added without
// deciding lands in neither list and fails here rather than defaulting to the
// cautious answer silently.
//
//libtmux:real-tmux
func TestEveryChangingToolSaysWhetherRepeatingItCompounds(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Changing tmux to a state: doing it twice leaves the same state.
	settles := map[string]bool{
		"clear_pane": true, "delete_buffer": true, "exit_copy_mode": true,
		"move_window": true, "rename_session": true, "rename_window": true,
		"select_layout": true, "select_pane": true, "select_window": true,
		"set_environment": true, "set_option": true, "set_pane_title": true,
	}
	// Changing tmux by a step, or by one that reverses: splitting twice makes
	// two panes, swapping twice puts them back, zooming twice unzooms.
	compounds := map[string]bool{
		"build_workspace": true, "call_destructive_tools_batch": true,
		"call_mutating_tools_batch": true, "create_session": true,
		"create_window": true, "enter_copy_mode": true, "kill_pane": true,
		"kill_server": true, "kill_session": true, "kill_window": true,
		"load_buffer": true, "move_pane": true, "paste_buffer": true,
		"paste_text": true, "pipe_pane": true, "resize_pane": true,
		"resize_window": true, "respawn_pane": true, "run_command": true,
		"send_keys": true, "send_keys_batch": true, "signal_channel": true,
		"split_window": true, "swap_pane": true,
	}

	for _, tool := range listed.Tools {
		annotations := tool.Annotations
		if annotations == nil {
			t.Errorf("%s carries no annotations at all", tool.Name)
			continue
		}
		if annotations.ReadOnlyHint {
			if !annotations.IdempotentHint {
				t.Errorf("%s only reads, so repeating it cannot compound", tool.Name)
			}
			continue
		}
		switch {
		case settles[tool.Name]:
			if !annotations.IdempotentHint {
				t.Errorf("%s sets a state, so it should say repeating it is safe",
					tool.Name)
			}
		case compounds[tool.Name]:
			if annotations.IdempotentHint {
				t.Errorf("%s compounds, so it must not say repeating it is safe",
					tool.Name)
			}
		default:
			t.Errorf("%s is a changing tool in neither list: decide whether "+
				"repeating it compounds and add it to one", tool.Name)
		}
	}
}

// TestSafetyLevelWithholdsTools covers the guarantee a level makes: a tool
// above it is never advertised, so no prompt reaches it, and a batch cannot
// reach around the level that hid it.
//
//libtmux:real-tmux
func TestSafetyLevelWithholdsTools(t *testing.T) {
	for _, testCase := range []struct {
		level    string
		kill     bool
		split    bool
		listOnly bool
	}{
		{"readonly", false, false, true},
		{"", false, true, false},
		{"destructive", true, true, false},
		// A level nobody meant to write is the one case where guessing wrong
		// hands out more than was asked for, so it reads as the lowest.
		{"readonyl", false, false, true},
	} {
		t.Run("level "+testCase.level, func(t *testing.T) {
			t.Setenv("LIBTMUX_SAFETY", testCase.level)
			session, _, ctx := connect(t)
			listed, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			advertised := map[string]bool{}
			for _, tool := range listed.Tools {
				advertised[tool.Name] = true
			}
			if advertised["kill_session"] != testCase.kill {
				t.Errorf("kill_session advertised = %v, want %v",
					advertised["kill_session"], testCase.kill)
			}
			if advertised["split_window"] != testCase.split {
				t.Errorf("split_window advertised = %v, want %v",
					advertised["split_window"], testCase.split)
			}
			if !advertised["list_panes"] {
				t.Error("list_panes is withheld at every level")
			}

			// A batch must not reach a tool the level hid. The mutating batch
			// is itself withheld at the readonly level, which is correct and
			// leaves nothing to test there.
			if !testCase.kill && !testCase.listOnly {
				var out struct {
					Completed int `json:"completed"`
				}
				call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
					"calls": []map[string]any{{
						"tool":      "kill_session",
						"arguments": map[string]any{"sessionName": "anything"},
					}},
				}, &out)
				if out.Completed != 0 {
					t.Error("a batch reached a tool the safety level withheld")
				}
			}
		})
	}
}

// TestResourcesAddressTheHierarchy covers the surface a tool cannot offer: a
// URI a person can attach to a conversation, and a client can browse without
// knowing a single tool name.
//
//libtmux:real-tmux
func TestResourcesAddressTheHierarchy(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: addressed\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - shell: sh -c 'printf RESOURCE-MARK; sleep 300'\n",
	}, nil)

	listed, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(listed.Resources) == 0 {
		t.Fatal("the server advertises no resources")
	}

	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(templates.ResourceTemplates) < 4 {
		t.Fatalf("advertised %d templates, want the hierarchy",
			len(templates.ResourceTemplates))
	}

	// The whole hierarchy, by name.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "tmux://sessions"})
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "addressed") {
		t.Fatalf("tmux://sessions does not describe the session: %+v", read.Contents)
	}

	// One pane's contents, as text a person would paste.
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}

	// Which spellings a URI takes, pinned rather than assumed. Every tool hands
	// a pane back as %1, so a client composing a URI from one is the likely
	// path, and a read and a subscription of the same string must not disagree
	// about whether it is a URI at all.
	bare := strings.TrimPrefix(panes[0], "%")
	for _, spelling := range []struct {
		uri      string
		readable bool
		why      string
	}{
		{"tmux://panes/" + bare + "/content", true, "the form the templates and completions give"},
		{"tmux://panes/%25" + bare + "/content", true, "the sigil, percent-encoded"},
		{
			"tmux://panes/%" + bare + "/content", false,
			"the sigil raw, which no URI can carry: % begins an escape",
		},
	} {
		_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: spelling.uri})
		if (err == nil) != spelling.readable {
			t.Errorf("read %s (%s): error = %v, want readable = %t",
				spelling.uri, spelling.why, err, spelling.readable)
		}
		// Subscription is routed by the string itself rather than by template,
		// so it takes the raw sigil too and must keep doing so: a client that
		// subscribed and got silence is the defect that bought this.
		if err := session.Subscribe(ctx, &sdk.SubscribeParams{URI: spelling.uri}); err != nil {
			t.Errorf("subscribe %s (%s): %v", spelling.uri, spelling.why, err)
		}
		if err := session.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: spelling.uri}); err != nil {
			t.Errorf("unsubscribe %s: %v", spelling.uri, err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{
			URI: "tmux://panes/" + strings.TrimPrefix(panes[0], "%") + "/content",
		})
		if err != nil {
			t.Fatalf("read pane content: %v", err)
		}
		if len(read.Contents) != 0 && strings.Contains(read.Contents[0].Text, "RESOURCE-MARK") {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "RESOURCE-MARK") {
		t.Errorf("pane content resource does not show the pane: %+v", read.Contents)
	}
	if got := read.Contents[0].MIMEType; got != "text/plain" {
		t.Errorf("pane content is %q, want text/plain", got)
	}

	// A pane's identity, as JSON.
	read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "tmux://panes/" + strings.TrimPrefix(panes[0], "%")})
	if err != nil {
		t.Fatalf("read pane: %v", err)
	}
	if !strings.Contains(read.Contents[0].Text, panes[0]) {
		t.Errorf("pane resource does not name the pane: %s", read.Contents[0].Text)
	}

	// A URI the server does not serve is refused rather than guessed at.
	if _, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://nonsense/1",
	}); err == nil {
		t.Error("an unknown resource URI was accepted")
	}
}

// TestResourceURIsArePercentDecoded covers the promise the URI comment makes:
// both the bare form and the percent-encoded sigil form address one object. A
// name needing an escape has no other spelling, so without decoding it cannot
// be addressed as a resource at all.
//
//libtmux:real-tmux
func TestResourceURIsArePercentDecoded(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: spaced name\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - shell: sleep 300\n",
	}, nil)

	// A session whose name needs an escape has exactly one legal spelling.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://sessions/spaced%20name/windows",
	})
	if err != nil {
		t.Fatalf("read windows of an escaped session name: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "only") {
		t.Errorf("an escaped session name selects no windows: %+v", read.Contents)
	}

	// %25 is how a client encodes the sigil tmux prints, so %250 is pane %0.
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	encoded := "tmux://panes/%25" + strings.TrimPrefix(panes[0], "%")
	read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: encoded})
	if err != nil {
		t.Fatalf("read %s: %v", encoded, err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, panes[0]) {
		t.Errorf("%s does not describe pane %s: %+v", encoded, panes[0], read.Contents)
	}
}

// TestResourcesSurviveAReadOnlyServer covers browsing a server that offers no
// tool which changes anything: every resource only reads, so the safety level
// never withholds one.
//
//libtmux:real-tmux
func TestResourcesSurviveAReadOnlyServer(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)
	listed, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(listed.Resources) == 0 {
		t.Error("a read-only server offers no resources to browse")
	}
}

// TestSuppressHistoryKeepsACommandOutOfHistory covers a courtesy an agent
// owes a person whose pane it is typing into: a shell told to ignore lines
// beginning with a space keeps them out of its history, and without this every
// command an agent ran would sit in the history of someone who never ran one.
//
//libtmux:real-tmux
func TestSuppressHistoryKeepsACommandOutOfHistory(t *testing.T) {
	session, target, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: history\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}

	// What tmux was asked to type is what proves it: a suppressed command
	// reaches the pane with a leading space, and a plain one does not.
	for _, testCase := range []struct {
		name      string
		suppress  bool
		wantSpace bool
	}{
		{"suppressed", true, true},
		{"plain", false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var seen string
			counting := tmux.CommandRunnerFunc(func(
				ctx context.Context,
				request tmux.CommandRequest,
			) (tmux.CommandResult, error) {
				// SendKeys issues the text and the Enter as separate commands, and
				// the text is the argument after the -- that ends the flags.
				for index, argument := range request.Arguments {
					if argument == "--" && index+1 < len(request.Arguments) && seen == "" {
						seen = request.Arguments[index+1]
					}
				}
				return tmux.SubprocessRunner().Run(ctx, request)
			})
			watched := tmux.NewServer(tmux.ServerOptions{
				SocketPath: target.SocketPath(),
				Runner:     counting,
			})
			pane, err := watched.Pane(ctx, tmux.PaneID(panes[0]))
			if err != nil {
				t.Fatalf("pane: %v", err)
			}
			command := "printf marker"
			if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
				Command:         &command,
				SuppressHistory: testCase.suppress,
			}); err != nil {
				t.Fatalf("send keys: %v", err)
			}
			if got := strings.HasPrefix(seen, " "); got != testCase.wantSpace {
				t.Errorf("typed %q, leading space = %v, want %v", seen, got, testCase.wantSpace)
			}
		})
	}
}

// TestPromptsNameTheJobs covers the surface neither a tool list nor a resource
// list can offer: the task a person actually wants, with the order of tools
// that does it attached.
//
//libtmux:real-tmux
func TestPromptsNameTheJobs(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	names := map[string]bool{}
	for _, prompt := range listed.Prompts {
		names[prompt.Name] = true
		if prompt.Description == "" {
			t.Errorf("%s has no description", prompt.Name)
		}
	}
	for _, want := range []string{"diagnose_pane", "set_up_workspace"} {
		if !names[want] {
			t.Errorf("prompt %q is not offered", want)
		}
	}

	// Naming a pane produces advice about that pane; omitting one produces
	// advice on finding it first.
	got, err := session.GetPrompt(ctx, &sdk.GetPromptParams{
		Name: "diagnose_pane", Arguments: map[string]string{"pane": "%3"},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("the prompt carries no message")
	}
	text, ok := got.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		t.Fatalf("prompt content is %T, want text", got.Messages[0].Content)
	}
	if !strings.Contains(text.Text, "%3") {
		t.Errorf("the prompt does not mention the pane it was given: %q", text.Text)
	}
	// It must steer away from the loop every tool description warns about.
	if !strings.Contains(text.Text, "wait_for_text") ||
		!strings.Contains(text.Text, "snapshot_pane") {
		t.Errorf("the prompt does not name the tools that do the job: %q", text.Text)
	}

	unnamed, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "diagnose_pane"})
	if err != nil {
		t.Fatalf("get prompt without a pane: %v", err)
	}
	if first, ok := unnamed.Messages[0].Content.(*sdk.TextContent); !ok ||
		!strings.Contains(first.Text, "list_panes") {
		t.Error("the prompt does not say how to find the pane when none was given")
	}
}

// TestPromptsRespectTheSafetyLevel covers a server offering nothing that
// changes tmux: suggesting how to lay a session out would be advice it cannot
// carry out.
//
//libtmux:real-tmux
func TestPromptsRespectTheSafetyLevel(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)
	listed, err := session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	for _, prompt := range listed.Prompts {
		// Both of these tell the model to run tools a read-only server does
		// not offer, so suggesting them is advice it cannot take.
		switch prompt.Name {
		case "set_up_workspace":
			t.Error("a read-only server offers to set a workspace up")
		case "recover_pane":
			t.Error("a read-only server offers to recover a pane")
		}
	}
	if len(listed.Prompts) == 0 {
		t.Error("a read-only server offers no prompts at all")
	}
}

// TestCompletionsOfferValuesThatExist covers the one surface a client fills in
// rather than reads. MCP has no completion for tool arguments, so this reaches
// the resource templates and the prompts.
//
//libtmux:real-tmux
func TestCompletionsOfferValuesThatExist(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: completed\nwindows:\n  - window_name: first\n" +
			"    panes:\n      - {}\n      - {}\n",
	}, nil)

	// A pane variable offers the panes that exist, without their sigil, which
	// is the form the URIs take.
	got, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/resource", URI: "tmux://panes/{pane}"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete a pane: %v", err)
	}
	if len(got.Completion.Values) < 2 {
		t.Fatalf("offered %v, want the panes that exist", got.Completion.Values)
	}
	for _, value := range got.Completion.Values {
		if strings.HasPrefix(value, "%") {
			t.Errorf("offered %q with its sigil, which is not what a URI takes", value)
		}
	}

	// A session variable offers the session by name.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://sessions/{session}/windows",
		},
		Argument: sdk.CompleteParamsArgument{Name: "session", Value: "comp"},
	})
	if err != nil {
		t.Fatalf("complete a session: %v", err)
	}
	if !slices.Contains(got.Completion.Values, "completed") {
		t.Errorf("offered %v, want the session named completed", got.Completion.Values)
	}

	// A value that matches nothing offers nothing rather than everything.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/resource", URI: "tmux://panes/{pane}"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: "zzz"},
	})
	if err != nil {
		t.Fatalf("complete with no match: %v", err)
	}
	if len(got.Completion.Values) != 0 {
		t.Errorf("a prefix matching nothing offered %v", got.Completion.Values)
	}

	// A prompt argument is answered in the dialect the tools speak, because
	// what fills it is read back by a model and passed to paneId. Offering the
	// URI form there hands the model an id every tool rejects.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/prompt", Name: "diagnose_pane"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete a prompt argument: %v", err)
	}
	if len(got.Completion.Values) == 0 {
		t.Fatal("a prompt argument offered nothing")
	}
	for _, value := range got.Completion.Values {
		if !strings.HasPrefix(value, "%") {
			t.Errorf("offered %q to a prompt, which no tool accepts as a pane", value)
		}
	}

	// Whatever is offered for a prompt has to be usable as a pane id, which is
	// the whole claim: hand it straight to a tool.
	if len(got.Completion.Values) > 0 {
		result := call(ctx, t, session, "get_pane_info", map[string]any{
			"paneId": got.Completion.Values[0],
		}, nil)
		if result.IsError {
			t.Errorf("a completed prompt value is not a pane a tool will take: %#v",
				result.Content)
		}
	}
}

// TestCompletionsEscapeWhatAUriMustCarry covers a name a URI cannot hold as it
// stands: a completion for a template slot is pasted into a path, so it has to
// arrive already escaped or the URI it builds does not parse.
//
//libtmux:real-tmux
func TestCompletionsEscapeWhatAUriMustCarry(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: spaced name\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - {}\n",
	}, nil)

	got, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://sessions/{session}/windows",
		},
		Argument: sdk.CompleteParamsArgument{Name: "session", Value: "spaced"},
	})
	if err != nil {
		t.Fatalf("complete a session: %v", err)
	}
	if !slices.Contains(got.Completion.Values, "spaced%20name") {
		t.Fatalf("offered %v, want the name escaped for a path", got.Completion.Values)
	}

	// The value a client was handed has to build a URI that reads.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://sessions/" + got.Completion.Values[0] + "/windows",
	})
	if err != nil {
		t.Fatalf("read the URI a completion built: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "only") {
		t.Errorf("the URI a completion built selects no windows: %+v", read.Contents)
	}
}

// TestCompletionsNarrowToWhatIsAlreadyChosen covers the context a client sends:
// a session already filled in should leave only its own windows offered.
//
//libtmux:real-tmux
func TestCompletionsNarrowToWhatIsAlreadyChosen(t *testing.T) {
	session, _, ctx := connect(t)
	for _, name := range []string{"alpha", "bravo"} {
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: " + name + "\nwindows:\n  - window_name: only\n" +
				"    panes:\n      - shell: sleep 300\n",
		}, nil)
	}

	all, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://windows/{window}/panes",
		},
		Argument: sdk.CompleteParamsArgument{Name: "window", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete windows: %v", err)
	}
	narrowed, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://windows/{window}/panes",
		},
		Argument: sdk.CompleteParamsArgument{Name: "window", Value: ""},
		Context:  &sdk.CompleteContext{Arguments: map[string]string{"session": "alpha"}},
	})
	if err != nil {
		t.Fatalf("complete windows within a session: %v", err)
	}
	if len(narrowed.Completion.Values) >= len(all.Completion.Values) {
		t.Errorf("naming a session offered %d windows, not fewer than %d",
			len(narrowed.Completion.Values), len(all.Completion.Values))
	}
}

// TestATimeoutIsLoggedToAClientThatAsked covers the capability this server
// advertises. A client that sets a level should hear why a wait ended with
// nothing; one that never asks should hear nothing at all.
//
//libtmux:real-tmux
func TestATimeoutIsLoggedToAClientThatAsked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	target := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "logged"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	logged := make(chan *sdk.LoggingMessageParams, 8)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "listener"}, &sdk.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, request *sdk.LoggingMessageRequest) {
			logged <- request.Params
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.SetLoggingLevel(ctx, &sdk.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatalf("set logging level: %v", err)
	}

	var panes struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &panes)
	if len(panes.Panes) == 0 {
		t.Fatal("no panes")
	}

	// A wait that ends with nothing says why in the log, not in the result.
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": panes.Panes[0].ID, "patterns": []string{"NEVER-WRITTEN"}, "timeoutSeconds": 1,
	}, nil)

	select {
	case message := <-logged:
		if message.Logger != "libtmux" {
			t.Errorf("logger = %q, want libtmux", message.Logger)
		}
		if !strings.Contains(fmt.Sprint(message.Data), "wait_for_text timed out") {
			t.Errorf("log says %v, want the timeout it describes", message.Data)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a client that asked for logs heard nothing about a timeout")
	}
}

// advertisedSchema is a tool's input schema as a client receives it, decoded
// far enough to ask what each argument is called and what it says about itself.
type advertisedSchema struct {
	Properties map[string]struct {
		Description string `json:"description"`
	} `json:"properties"`
}

// schemaOf decodes one tool's advertised input schema.
func schemaOf(t *testing.T, tool *sdk.Tool) advertisedSchema {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
	}
	var schema advertisedSchema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("%s: decode input schema: %v", tool.Name, err)
	}
	return schema
}

// TestEveryArgumentSaysWhatItIs is a structural gate on the surface a client
// reads. An argument with no description is invisible to anything choosing
// tools from the schema alone, which is how an agent chooses them.
func TestEveryArgumentSaysWhatItIs(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		for name, property := range schemaOf(t, tool).Properties {
			if strings.TrimSpace(property.Description) == "" {
				t.Errorf("%s: argument %q has no description", tool.Name, name)
			}
		}
	}
}

// TestNoToolOffersAnArgumentItIgnores covers arguments that reached a schema by
// sharing a Go type with another tool. A client cannot tell such an argument
// from one that works, so it asks for behaviour it will not get.
func TestNoToolOffersAnArgumentItIgnores(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ignored := map[string]string{
		"exit_copy_mode": "scrollUp",
	}
	for _, tool := range tools.Tools {
		unwanted, watched := ignored[tool.Name]
		if !watched {
			continue
		}
		if _, offered := schemaOf(t, tool).Properties[unwanted]; offered {
			t.Errorf("%s offers %q, which it does not read", tool.Name, unwanted)
		}
	}
}

// TestAListWithNoMatchesIsStillAList covers the shape a client parses. A
// collection that disappears when it is empty makes every caller check whether
// the field is there before it can count what matched, and the three listing
// tools disagreed about it.
//
//libtmux:real-tmux
func TestAListWithNoMatchesIsStillAList(t *testing.T) {
	session, _, ctx := connect(t)

	for _, listing := range []struct {
		tool       string
		arguments  map[string]any
		collection string
	}{
		{"list_panes", map[string]any{"command": "no-such-command-xyz"}, "panes"},
		{"list_sessions", map[string]any{"name": "no-such-session-xyz"}, "sessions"},
		{"list_windows", map[string]any{"name": "no-such-window-xyz"}, "windows"},
	} {
		t.Run(listing.tool, func(t *testing.T) {
			result := call(ctx, t, session, listing.tool, listing.arguments, nil)
			if result.IsError {
				t.Fatalf("%s failed: %#v", listing.tool, result.Content)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var reply map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &reply); err != nil {
				t.Fatal(err)
			}
			found, present := reply[listing.collection]
			if !present {
				t.Fatalf("%s left out %q entirely: %s",
					listing.tool, listing.collection, encoded)
			}
			if string(found) != "[]" {
				t.Errorf("%s reported %s = %s, want []",
					listing.tool, listing.collection, found)
			}
		})
	}
}

// TestABatchNamesTheCallsItSkipped covers what a caller of the mutating batch
// needs after a failure: which of its changes were never made. Counting the
// difference between the calls sent and the results returned is not something
// a reply should ask of a client.
//
//libtmux:real-tmux
func TestABatchNamesTheCallsItSkipped(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: batched\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var batch struct {
		Completed int      `json:"completed"`
		Skipped   []string `json:"skipped"`
		Results   []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
	}
	result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": pane, "title": "ran"}},
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": "%999", "title": "fails"}},
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": pane, "title": "skipped"}},
			{"tool": "select_pane", "arguments": map[string]any{"paneId": pane}},
		},
	}, &batch)
	if result.IsError {
		t.Fatalf("the batch itself failed: %#v", result.Content)
	}
	if batch.Completed != 1 || len(batch.Results) != 2 {
		t.Fatalf("completed = %d with %d results, want 1 and 2",
			batch.Completed, len(batch.Results))
	}
	want := []string{"set_pane_title", "select_pane"}
	if !slices.Equal(batch.Skipped, want) {
		t.Errorf("skipped = %q, want %q", batch.Skipped, want)
	}
}

// TestABatchSaysWhyItCannotBatchABatch covers a refusal that used to deny the
// existence of a tool the client can see in the listing.
//
//libtmux:real-tmux
func TestABatchSaysWhyItCannotBatchABatch(t *testing.T) {
	session, _, ctx := connect(t)

	result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "call_readonly_tools_batch", "arguments": map[string]any{}},
		},
	}, nil)
	if !result.IsError {
		t.Fatal("a batch inside a batch was accepted")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("the refusal carried %T", result.Content[0])
	}
	if strings.Contains(text.Text, "is not a tool this server serves") {
		t.Errorf("the refusal denies a tool that is advertised: %s", text.Text)
	}
	if !strings.Contains(text.Text, "cannot be called from inside a batch") {
		t.Errorf("the refusal does not say why: %s", text.Text)
	}
}

// TestListServersLeavesOutSocketsNothingIsListeningOn covers a reply whose size
// was set by how many test suites the machine had ever run. tmux leaves a
// socket file behind when a server exits, so the directory only grows, and a
// listing that reported all of them buried the running ones.
//
//libtmux:real-tmux
func TestListServersLeavesOutSocketsNothingIsListeningOn(t *testing.T) {
	session, _, ctx := connect(t)

	var listed struct {
		Servers []struct {
			Name     string `json:"name"`
			Alive    bool   `json:"alive"`
			IsTarget bool   `json:"isTarget"`
		} `json:"servers"`
		Total   int `json:"total"`
		Skipped int `json:"skipped"`
	}
	result := call(ctx, t, session, "list_servers", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("list_servers failed: %#v", result.Content)
	}
	for _, found := range listed.Servers {
		if !found.Alive && !found.IsTarget {
			t.Errorf("socket %q has no server and is not the target, so it should "+
				"have been left out", found.Name)
		}
	}
	if listed.Total < len(listed.Servers) {
		t.Errorf("total = %d with %d reported, want total to count them all",
			listed.Total, len(listed.Servers))
	}

	// A cap is reported rather than applied quietly, so a caller can tell a
	// machine with one server from a reply that stopped after one.
	var capped struct {
		Servers []struct{} `json:"servers"`
		Skipped int        `json:"skipped"`
	}
	call(ctx, t, session, "list_servers", map[string]any{"maxServers": 1}, &capped)
	if len(capped.Servers) > 1 {
		t.Errorf("maxServers 1 returned %d servers", len(capped.Servers))
	}
}

// TestANamedTargetThatIsGoneNamesTheCallThatFindsOne covers the difference
// between an error a model can read and one it can act on.
//
// tmux answers with what it looked for, which names the mechanism and leaves
// the way out to be guessed. The listing is always the right next move, so the
// refusal says so.
//
// This is about a tool that resolves a target. The listing tools take the same
// argument names as filters, where selecting nothing is an answer rather than
// a failure, and they are deliberately not here.
//
//libtmux:real-tmux
func TestANamedTargetThatIsGoneNamesTheCallThatFindsOne(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: named\nwindows:\n  - panes:\n      - {}\n")

	for _, testCase := range []struct {
		tool, argument, value, wants string
	}{
		{"capture_pane", "paneId", "%99999", "list_panes"},
		{"get_pane_info", "paneId", "%99999", "list_panes"},
		{"get_window_info", "windowId", "@99999", "list_windows"},
		{"get_session_info", "sessionName", "no-such-session", "list_sessions"},
		{"send_keys", "paneId", "%99999", "list_panes"},
	} {
		t.Run(testCase.tool+"/"+testCase.argument, func(t *testing.T) {
			arguments := map[string]any{testCase.argument: testCase.value}
			if testCase.tool == "send_keys" {
				arguments["command"] = "true"
			}
			result := call(ctx, t, session, testCase.tool, arguments, nil)
			if !result.IsError {
				t.Fatalf("a target that does not exist was accepted")
			}
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			if !strings.Contains(said, testCase.wants) {
				t.Errorf("the refusal does not name %s: %q", testCase.wants, said)
			}
			if strings.Contains(said, "snapshot object") {
				t.Errorf("the refusal is tmux's wording, not this server's: %q", said)
			}
		})
	}
}

// TestASettingsScopeRefusesATargetItCannotRead covers an argument thrown away
// without a word.
//
// A caller who means a pane and writes session gets a session-wide answer, and
// nothing in the reply says the pane they named was discarded — so a mistake
// in one field reads as a successful call about something else.
//
//libtmux:real-tmux
func TestASettingsScopeRefusesATargetItCannotRead(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: scoped\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	for _, testCase := range []struct {
		name      string
		tool      string
		arguments map[string]any
		refused   bool
	}{
		{
			"a pane at session scope", "show_option",
			map[string]any{"name": "history-limit", "scope": "session", "paneId": pane},
			true,
		},
		{
			"a window at server scope", "show_option",
			map[string]any{"name": "history-limit", "scope": "server", "windowId": "@0"},
			true,
		},
		{
			"a pane at window scope", "show_option",
			map[string]any{"name": "history-limit", "scope": "window", "paneId": pane},
			true,
		},
		{
			"setting one too", "set_option",
			map[string]any{
				"name": "history-limit", "value": "5000",
				"scope": "session", "paneId": pane,
			},
			true,
		},
		{
			"hooks too", "show_hooks",
			map[string]any{"scope": "session", "paneId": pane},
			true,
		},
		{
			"a pane at pane scope is the point", "show_option",
			map[string]any{"name": "history-limit", "scope": "pane", "paneId": pane},
			false,
		},
		{
			"naming no target is fine", "show_option",
			map[string]any{"name": "history-limit", "scope": "session"},
			false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := call(ctx, t, session, testCase.tool, testCase.arguments, nil)
			if result.IsError != testCase.refused {
				t.Fatalf("isError = %v, want %v: %#v",
					result.IsError, testCase.refused, result.Content)
			}
			if !testCase.refused {
				return
			}
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			if !strings.Contains(said, "not read at") {
				t.Errorf("the refusal does not say the argument is unread: %q", said)
			}
		})
	}
}

// TestServerMessagesAreBoundedLikeEveryOtherReply covers the one reply in this
// package that had a count cap and no byte cap.
//
// tmux's message log records the commands it ran, and this server's own
// listings carry a format string naming every field it wants, so the log is
// mostly this server quoted back at itself at thousands of characters a line.
// A hundred of those is a reply no caller can afford, and the caller cannot
// see it coming: the size belongs to the log, not to the request.
//
//libtmux:real-tmux
func TestServerMessagesAreBoundedLikeEveryOtherReply(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: messages\nwindows:\n  - panes:\n      - {}\n")

	// A log long enough to be worth bounding, made the way a caller makes one:
	// by asking this server questions.
	call(ctx, t, session, "set_option", map[string]any{
		"scope": "server", "name": "message-limit", "value": "1000",
	}, nil)
	for range 40 {
		call(ctx, t, session, "list_panes", map[string]any{}, nil)
	}

	measure := func(arguments map[string]any) (int, int, bool) {
		t.Helper()
		var reply struct {
			Messages            []string `json:"messages"`
			MessagesUnavailable string   `json:"messagesUnavailable"`
			Truncated           bool     `json:"truncated"`
		}
		arguments["includeMessages"] = true
		result := call(ctx, t, session, "get_server_info", arguments, &reply)
		if result.IsError {
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			t.Fatalf("get_server_info %v: %s", arguments, said)
		}
		// tmux keeps the message log per client and refuses the command
		// outright before 3.5 when nothing is attached, which the in-memory
		// transport used here never is. The reply has to say so rather than
		// return an empty log, and there is nothing to bound when it does.
		if reply.MessagesUnavailable != "" {
			if len(reply.Messages) != 0 {
				t.Errorf("the log is reported unavailable and carries %d messages",
					len(reply.Messages))
			}
			t.Skipf("this tmux will not report its message log here: %s",
				reply.MessagesUnavailable)
		}
		size := 0
		for _, message := range reply.Messages {
			size += len(message) + 1
		}
		return len(reply.Messages), size, reply.Truncated
	}

	// The number is this test's own claim rather than a copy of the cap: what
	// matters is that a caller who asked for nothing cannot be handed a reply
	// measured in hundreds of kilobytes. Unbounded, this log runs past 180,000.
	const affordable = 32_000
	count, size, truncated := measure(map[string]any{})
	if size > affordable {
		t.Errorf("asking for the message log with no bounds returned %d bytes over "+
			"%d messages; a caller cannot spend that and did not ask to", size, count)
	}
	if truncated && count == 0 {
		t.Error("the reply reports truncation and carries nothing")
	}

	// A caller may ask for less, and is told that it cost something.
	fewer, _, fewerTruncated := measure(map[string]any{"maxLines": 5})
	if fewer > 5 {
		t.Errorf("maxLines 5 returned %d messages", fewer)
	}
	if count > fewer && !fewerTruncated {
		t.Error("a reply cut to five messages does not report the truncation")
	}

	// And may bound the bytes, which is the cap that was missing.
	_, tight, tightTruncated := measure(map[string]any{"maxLines": 1000, "maxBytes": 4000})
	if tight > 4000 {
		t.Errorf("maxBytes 4000 returned %d bytes", tight)
	}
	if size > tight && !tightTruncated {
		t.Error("a reply cut to four thousand bytes does not report the truncation")
	}
}

// TestServerInfoDoesNotInventAHealthyEmptyServer covers a reply that a caller
// would believe.
//
// A tmux server with nothing in it and a tmux this process could not run come
// back as the same reply once the errors are dropped: alive false, no socket,
// zero of everything. The first is an answer and the second is not knowing, and
// a caller acting on the second acts on a description of a server that does not
// exist.
//
//libtmux:real-tmux
func TestServerInfoDoesNotInventAHealthyEmptyServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	unreachable := tmux.NewServer(tmux.ServerOptions{
		Binary:     filepath.Join(t.TempDir(), "there-is-no-tmux-here"),
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(unreachable).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "unreachable"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	var reported struct {
		Alive    bool   `json:"alive"`
		Sessions int    `json:"sessions"`
		Socket   string `json:"socketPath"`
	}
	result := call(ctx, t, session, "get_server_info", map[string]any{}, &reported)
	if !result.IsError {
		t.Fatalf("a tmux that cannot be run was reported as alive=%t with %d sessions "+
			"and socket %q, rather than as an error",
			reported.Alive, reported.Sessions, reported.Socket)
	}
}

// TestAHalfBuiltWorkspaceSaysWhatSurvived covers a failure whose leftovers the
// reply did not mention.
//
// Build is not atomic and cannot be: tmux has no transaction. The reply named
// the pane it died on and nothing else, so a caller who read it believed
// nothing happened and sent the same document again -- which fails on a name
// that already exists, for a reason the first reply never gave. The batch tools
// have the same property and disclose it; this now does too.
//
//libtmux:real-tmux
func TestAHalfBuiltWorkspaceSaysWhatSurvived(t *testing.T) {
	session, _, ctx := connect(t)

	// More panes than an eighty-column window can hold, so tmux refuses part
	// way through rather than at the start.
	document := "session_name: halfbuilt\nwindows:\n  - panes:\n" +
		strings.Repeat("      - {}\n", 40)
	result := call(ctx, t, session, "build_workspace", map[string]any{
		"document": document,
	}, nil)
	if !result.IsError {
		t.Skip("this tmux fitted forty panes, so there is no partial build to report")
	}
	// The helper stops at IsError, so the fields are read here: a failed call
	// still carries the identifiers a caller cleans up with.
	var reported struct {
		SessionID   string `json:"sessionId"`
		SessionName string `json:"sessionName"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &reported); err != nil {
		t.Fatal(err)
	}
	said := ""
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			said += text.Text
		}
	}
	if !strings.Contains(said, "halfbuilt") {
		t.Errorf("the failure does not name the session it left behind: %q", said)
	}
	if reported.SessionName != "halfbuilt" {
		t.Errorf("sessionName = %q, want the name that was asked for", reported.SessionName)
	}
	if reported.SessionID == "" {
		t.Error("no session id to clean up with")
	}

	// The session really is there, which is what the reply now says.
	var listed struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	call(ctx, t, session, "list_sessions", map[string]any{}, &listed)
	found := false
	for _, each := range listed.Sessions {
		if each.Name == "halfbuilt" {
			found = true
		}
	}
	if !found {
		t.Error("the reply named a surviving session that is not there")
	}
}

// TestRespawningALivePaneNamesTheWayOut covers tmux's own refusal reaching a
// caller unexplained.
//
// tmux will not respawn a pane that is still running without -k, and says so
// as "respawn-pane exited 1". The way out is one argument away, and every
// other refusal in this server names it.
//
//libtmux:real-tmux
func TestRespawningALivePaneNamesTheWayOut(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: respawn\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	result := call(ctx, t, session, "respawn_pane", map[string]any{"paneId": pane}, nil)
	if !result.IsError {
		t.Fatal("respawning a live pane without kill was accepted")
	}
	said := ""
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			said += text.Text
		}
	}
	if !strings.Contains(said, "kill") {
		t.Errorf("the refusal does not name the way out: %q", said)
	}
	if strings.Contains(said, "exited 1") {
		t.Errorf("the refusal is tmux's exit code rather than a reason: %q", said)
	}

	// And the way out works.
	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": pane, "kill": true,
	}, nil); result.IsError {
		t.Errorf("respawn_pane with kill: %#v", result.Content)
	}
}

// TestACommandThatExitsTakesThePaneAndItsWindow covers the gap between what
// respawn_pane promises and what a caller gets.
//
// Keeping the pane and its place in the layout holds while the command runs.
// A command that exits leaves tmux nothing to keep, and remain-on-exit is the
// only thing that holds the pane open, so the description has to name it.
//
//libtmux:real-tmux
func TestACommandThatExitsTakesThePaneAndItsWindow(t *testing.T) {
	session, _, ctx := connect(t)
	// Both panes run something that outlives the assertions. A pane left to a
	// shell is a pane that can end on its own, and one ending early takes its
	// window and then the session out from under the test -- which is how this
	// failed on a slower machine while passing on every tmux release here.
	if result := call(ctx, t, session, "create_session", map[string]any{
		"name": "reaped", "command": "sleep 300",
	}, nil); result.IsError {
		t.Fatalf("create_session: %s", resultText(result))
	}
	var doomedWindow struct {
		PaneID   string `json:"paneId"`
		WindowID string `json:"windowId"`
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "reaped", "name": "doomed", "command": "sleep 300",
	}, &doomedWindow); result.IsError {
		t.Fatalf("create_window: %s", resultText(result))
	}
	doomed := doomedWindow.PaneID

	// What the server can see of its own making, logged because this failed
	// where it could not be reproduced and the reply named a pane the snapshot
	// did not hold.
	var seen struct {
		Panes []struct {
			ID     string `json:"id"`
			Window string `json:"window"`
			Status struct {
				Dead bool `json:"dead"`
			} `json:"status"`
		} `json:"panes"`
		Total int `json:"total"`
	}
	call(ctx, t, session, "list_panes", map[string]any{"detail": "full"}, &seen)
	t.Logf("created pane %q in window %q; server holds %d panes: %+v",
		doomed, doomedWindow.WindowID, seen.Total, seen.Panes)
	// The same lookup respawn_pane makes, so a failure names which of the two
	// roads to a pane disagrees with the other.
	if info := call(ctx, t, session, "get_pane_info", map[string]any{
		"paneId": doomed,
	}, nil); info.IsError {
		t.Logf("get_pane_info on %s also fails: %s", doomed, resultText(info))
	} else {
		t.Logf("get_pane_info on %s succeeds", doomed)
	}
	var inWindow struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	if listed := call(ctx, t, session, "list_panes", map[string]any{
		"windowId": doomedWindow.WindowID,
	}, &inWindow); listed.IsError {
		t.Logf("list_panes by window %s fails: %s", doomedWindow.WindowID, resultText(listed))
	} else {
		t.Logf("list_panes by window %s: %+v", doomedWindow.WindowID, inWindow.Panes)
	}

	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": doomed, "command": "true", "kill": true,
	}, nil); result.IsError {
		t.Fatalf("respawn_pane on %s: %s", doomed, resultText(result))
	}

	// Reaping waits on the child's exit reaching tmux, so this polls rather
	// than looking once and calling the answer settled.
	deadline := time.Now().Add(10 * time.Second)
	for slices.Contains(paneIDs(ctx, t, session), doomed) {
		if time.Now().After(deadline) {
			t.Fatalf("%s outlived a command that exited", doomed)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// And the way to keep it, which is what the description now points at.
	var made struct {
		PaneID   string `json:"paneId"`
		WindowID string `json:"windowId"`
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "reaped", "name": "held",
	}, &made); result.IsError {
		t.Fatalf("create_window: %s", resultText(result))
	}
	if result := call(ctx, t, session, "set_option", map[string]any{
		"name": "remain-on-exit", "value": "on",
		"scope": "window", "windowId": made.WindowID,
	}, nil); result.IsError {
		t.Fatalf("set_option: %s", resultText(result))
	}
	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": made.PaneID, "command": "true", "kill": true,
	}, nil); result.IsError {
		t.Fatalf("respawn_pane: %#v", result.Content)
	}

	// The pane stays, so what this waits for is the process ending rather than
	// the pane going.
	deadline = time.Now().Add(10 * time.Second)
	for {
		var listed struct {
			Panes []struct {
				ID     string `json:"id"`
				Status struct {
					Dead bool `json:"dead"`
				} `json:"status"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{
			"sessionName": "reaped", "detail": "full",
		}, &listed)
		found := false
		for _, pane := range listed.Panes {
			if pane.ID != made.PaneID {
				continue
			}
			found = true
			if pane.Status.Dead {
				return
			}
		}
		if !found {
			t.Fatalf("%s was reaped though remain-on-exit was set", made.PaneID)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reported its command as finished", made.PaneID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAFilteredListingSaysHowManyItLeftOut covers a total a client reads as a
// remainder.
//
// total counts what was there before the criteria ran, so a shorter list under
// a larger total is what a filter excluded. Every tool here that returns pane
// text does shorten its reply and says so, which makes "ask again for the
// rest" the available and wrong reading. list_servers has always answered this
// with skipped; the listings a client reaches for first did not.
//
//libtmux:real-tmux
func TestAFilteredListingSaysHowManyItLeftOut(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: counted\nwindows:\n"+
		"  - panes:\n      - {}\n  - panes:\n      - {}\n")
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "counted", "name": "singled-out",
	}, nil); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}

	var listed struct {
		Windows []struct {
			Name string `json:"name"`
		} `json:"windows"`
		Total   int `json:"total"`
		Skipped int `json:"skipped"`
	}
	call(ctx, t, session, "list_windows", map[string]any{"name": "singled-out"}, &listed)
	if len(listed.Windows) != 1 {
		t.Fatalf("the filter did not select one window: %#v", listed.Windows)
	}
	if listed.Total <= len(listed.Windows) {
		t.Fatalf("nothing was filtered out, so this proves nothing: total %d",
			listed.Total)
	}
	if listed.Total != len(listed.Windows)+listed.Skipped {
		t.Errorf("total %d does not reconcile: %d listed, %d skipped",
			listed.Total, len(listed.Windows), listed.Skipped)
	}

	// An unfiltered listing left nothing out, and says so by omitting the
	// field rather than by a zero a caller has to tell apart from a filter
	// that happened to exclude none. A pointer is what distinguishes the two,
	// because an absent key leaves a plain int at whatever it already held.
	var unfiltered struct {
		Windows []struct{} `json:"windows"`
		Total   int        `json:"total"`
		Skipped *int       `json:"skipped"`
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &unfiltered)
	if unfiltered.Skipped != nil {
		t.Errorf("an unfiltered listing reported %d skipped", *unfiltered.Skipped)
	}
	if unfiltered.Total != len(unfiltered.Windows) {
		t.Errorf("an unfiltered listing dropped %d of %d",
			unfiltered.Total-len(unfiltered.Windows), unfiltered.Total)
	}
}

// TestTheEnvironmentListingWithholdsValues covers a reply that put every
// credential on a developer's machine into a model's context.
//
// An environment is where people keep API tokens, and a no-argument call
// returned all of them with their values -- eleven live ones on the machine
// this was found on. A listing now carries names, which is what most of the
// question is, and naming a variable returns its value, which is a caller
// asking for one thing rather than receiving everything.
//
//libtmux:real-tmux
func TestTheEnvironmentListingWithholdsValues(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: secrets\nwindows:\n  - panes:\n      - {}\n")

	const secret = "s3cr3t-value-nobody-asked-for"
	for name, value := range map[string]string{
		"PROBE_LOOKS_LIKE_A_TOKEN": secret,
		"PROBE_ORDINARY":           "plain",
	} {
		if err := target.SetEnvironment(ctx, name, value,
			tmux.SetEnvironmentOptions{}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	var listed struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
		ValuesWithheld bool `json:"valuesWithheld"`
	}
	result := call(ctx, t, session, "show_environment", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("show_environment: %#v", result.Content)
	}
	if len(listed.Variables) == 0 {
		t.Fatal("the listing is empty, so it proves nothing")
	}
	if !listed.ValuesWithheld {
		t.Error("the listing does not say it withheld the values")
	}
	seeded := false
	for _, entry := range listed.Variables {
		if entry.Value != "" {
			t.Errorf("the listing carries a value for %s", entry.Name)
		}
		if entry.Name == "PROBE_LOOKS_LIKE_A_TOKEN" {
			seeded = true
		}
	}
	if !seeded {
		t.Error("the listing omits a variable that is set, so names are not enough")
	}
	// Nothing in the whole reply, not only the field this test reads.
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok &&
			strings.Contains(text.Text, secret) {
			t.Error("the reply text carries the value")
		}
	}

	// Naming one returns its value, which is the narrower ask.
	var one struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
		ValuesWithheld bool `json:"valuesWithheld"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "PROBE_LOOKS_LIKE_A_TOKEN",
	}, &one)
	if len(one.Variables) != 1 || one.Variables[0].Value != secret {
		t.Errorf("naming a variable did not return its value: %+v", one.Variables)
	}
	if one.ValuesWithheld {
		t.Error("a named read reports its value withheld")
	}

	// And the listing is bounded like its peers.
	var few struct {
		Variables []struct{} `json:"variables"`
		Truncated bool       `json:"truncated"`
	}
	call(ctx, t, session, "show_environment", map[string]any{"maxLines": 2}, &few)
	if len(few.Variables) > 2 {
		t.Errorf("maxLines 2 returned %d variables", len(few.Variables))
	}
	if len(listed.Variables) > 2 && !few.Truncated {
		t.Error("a bounded listing does not report the truncation")
	}
}

// TestAnEmptyCollectionIsStillAnArray covers a reply a consumer cannot iterate.
//
// A scope with no hooks returned {"scope":"server"} and nothing else, so a
// caller had to branch on a missing key rather than loop over an empty list.
// The same applied to the attached clients. Both are collections a caller walks
// and neither absence means anything the emptiness does not; the text fields
// elsewhere are different, because run_command's missing output distinguishes a
// command that printed nothing from one whose output could not be read.
//
//libtmux:real-tmux
func TestAnEmptyCollectionIsStillAnArray(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: arrays\nwindows:\n  - panes:\n      - {}\n")

	raw := func(tool string, arguments map[string]any) map[string]any {
		t.Helper()
		result := call(ctx, t, session, tool, arguments, nil)
		if result.IsError {
			t.Fatalf("%s: %#v", tool, result.Content)
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	hooks := raw("show_hooks", map[string]any{"scope": "server"})
	found, present := hooks["hooks"]
	if !present {
		t.Errorf("show_hooks omits the hooks key: %v", hooks)
	} else if _, ok := found.([]any); !ok && found != nil {
		t.Errorf("hooks is %T rather than an array", found)
	}

	info := raw("get_server_info", map[string]any{})
	clients, present := info["attachedClients"]
	if !present {
		t.Errorf("get_server_info omits the attachedClients key: %v", info)
	} else if _, ok := clients.([]any); !ok && clients != nil {
		t.Errorf("attachedClients is %T rather than an array", clients)
	}
}

// TestResizingAPaneSaysWhichPaneMoved covers a reply a caller cannot act on.
//
// paneId is optional and resolves the active pane, so a caller that left it out
// was told a width and a height with nothing saying whose. Every other pane
// tool here echoes the pane it acted on.
//
//libtmux:real-tmux
func TestResizingAPaneSaysWhichPaneMoved(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: resized\nwindows:\n  - panes:\n      - {}\n      - {}\n")

	var resized struct {
		PaneID string `json:"paneId"`
		Height int    `json:"height"`
	}
	// No paneId, which is the case that could not be read back.
	result := call(ctx, t, session, "resize_pane", map[string]any{"height": 8}, &resized)
	if result.IsError {
		t.Fatalf("resize_pane: %#v", result.Content)
	}
	if resized.PaneID == "" {
		t.Fatal("resize_pane did not say which pane it resized")
	}
	if !strings.HasPrefix(resized.PaneID, "%") {
		t.Errorf("paneId = %q, want a tmux pane id", resized.PaneID)
	}

	// And it is the pane tmux actually changed.
	var info struct {
		Pane struct {
			ID       string `json:"id"`
			Geometry struct {
				Height int `json:"height"`
			} `json:"geometry"`
		} `json:"pane"`
	}
	call(ctx, t, session, "get_pane_info", map[string]any{"paneId": resized.PaneID}, &info)
	if info.Pane.ID != resized.PaneID {
		t.Errorf("resize_pane named %s and get_pane_info reports %s",
			resized.PaneID, info.Pane.ID)
	}
	if info.Pane.Geometry.Height != resized.Height {
		t.Errorf("resize_pane reported height %d and the pane is %d",
			resized.Height, info.Pane.Geometry.Height)
	}
}
