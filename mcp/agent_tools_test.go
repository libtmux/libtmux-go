package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tools an agent reaches for repeatedly, tested against real tmux.
//
// The in-memory transport covers the protocol; what these cover is the tmux
// behaviour underneath it, which is where the interesting failures are: a
// cursor that survives the grid being renumbered, a bound that keeps the end
// rather than the beginning, a pattern that stops a wait before its deadline.

// workspace builds a session whose panes outlive the assertions made about
// them. A pane running a command that exits takes its window, its session, and
// then the tmux server with it, and a test asserting what survived would race
// that teardown.
func workspace(ctx context.Context, t *testing.T, session *sdk.ClientSession, document string) {
	t.Helper()
	result := call(ctx, t, session, "build_workspace", map[string]any{"document": document}, nil)
	if result.IsError {
		t.Fatalf("build_workspace: %#v", result.Content)
	}
}

// firstPane reports the pane a freshly built workspace put a shell in.
func firstPane(ctx context.Context, t *testing.T, session *sdk.ClientSession) string {
	t.Helper()
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	return panes[0]
}

//libtmux:real-tmux
func TestCaptureKeepsTheEndAndSaysWhatItDropped(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: bounded\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// A hundred numbered lines, so which end came back is visible rather than
	// inferred.
	run(ctx, t, session, pane, "for i in $(seq 1 100); do echo line-$i; done")

	var whole struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "includeHistory": true,
	}, &whole)
	if !strings.Contains(strings.Join(whole.Lines, "\n"), "line-100") {
		t.Fatalf("the pane never showed the output: %q", strings.Join(whole.Lines, "\n"))
	}

	var bounded struct {
		Lines          []string `json:"lines"`
		Truncated      bool     `json:"truncated"`
		TruncatedLines int      `json:"truncatedLines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "includeHistory": true, "maxLines": 5,
	}, &bounded)
	if len(bounded.Lines) != 5 {
		t.Errorf("asked for 5 lines, got %d", len(bounded.Lines))
	}
	if !bounded.Truncated || bounded.TruncatedLines == 0 {
		t.Error("a shortened reply did not say it was shortened")
	}
	joined := strings.Join(bounded.Lines, "\n")
	if strings.Contains(joined, "line-1\n") {
		t.Errorf("the bound kept the beginning rather than the end: %q", joined)
	}

	// The lines also reach a model as terminal text rather than as a quoted
	// JSON array, which is what the content block is for.
	result := call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "maxLines": 5,
	}, nil)
	if len(result.Content) == 0 {
		t.Fatal("capture_pane returned no content block")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || strings.HasPrefix(strings.TrimSpace(text.Text), "{") {
		t.Errorf("pane text arrived as JSON rather than as text: %#v", result.Content[0])
	}
}

//libtmux:real-tmux
func TestCaptureSinceReturnsOnlyWhatIsNew(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: tailing\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	type reading struct {
		PaneID      string   `json:"paneId"`
		Cursor      string   `json:"cursor"`
		Lines       []string `json:"lines"`
		LinesMissed bool     `json:"linesMissed"`
	}

	var first reading
	result := call(ctx, t, session, "capture_since", map[string]any{"paneId": pane}, &first)
	if result.IsError {
		t.Fatalf("capture_since: %#v", result.Content)
	}
	if first.Cursor == "" {
		t.Fatal("the first reading returned no cursor to continue from")
	}
	if first.PaneID != pane {
		t.Errorf("read pane %q, want %q", first.PaneID, pane)
	}

	run(ctx, t, session, pane, "echo BEFORE-THE-CURSOR")

	var marked reading
	call(ctx, t, session, "capture_since", map[string]any{"cursor": first.Cursor}, &marked)
	if !strings.Contains(strings.Join(marked.Lines, "\n"), "BEFORE-THE-CURSOR") {
		t.Errorf("output written after the cursor was not reported: %q", marked.Lines)
	}

	// Nothing has happened since, so what came before is not sent again. A
	// prompt that repaints itself is new text on the row the cursor sits on and
	// is reported, which is why this is about what is absent rather than about
	// the reply being empty.
	var quiet reading
	call(ctx, t, session, "capture_since", map[string]any{"cursor": marked.Cursor}, &quiet)
	if strings.Contains(strings.Join(quiet.Lines, "\n"), "BEFORE-THE-CURSOR") {
		t.Errorf("a second reading repeated what the first already reported: %q", quiet.Lines)
	}
	if quiet.LinesMissed {
		t.Error("an unchanged pane reported that lines were missed")
	}

	run(ctx, t, session, pane, "echo AFTER-THE-CURSOR")

	var next reading
	call(ctx, t, session, "capture_since", map[string]any{"cursor": quiet.Cursor}, &next)
	joined := strings.Join(next.Lines, "\n")
	if !strings.Contains(joined, "AFTER-THE-CURSOR") {
		t.Errorf("the new output was not reported: %q", joined)
	}
	if next.Cursor == quiet.Cursor {
		t.Error("the cursor did not move after the pane wrote something")
	}
}

//libtmux:real-tmux
func TestACursorForAnotherPaneIsRefused(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: cursors\nwindows:\n  - panes:\n      - {}\n      - {}\n")
	panes := paneIDs(ctx, t, session)
	if len(panes) < 2 {
		t.Fatalf("wanted two panes, got %d", len(panes))
	}

	var first struct {
		Cursor string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"paneId": panes[0]}, &first)

	result := call(ctx, t, session, "capture_since", map[string]any{
		"paneId": panes[1], "cursor": first.Cursor,
	}, nil)
	if !result.IsError {
		t.Error("a cursor from another pane was accepted")
	}

	damaged := call(ctx, t, session, "capture_since", map[string]any{
		"paneId": panes[0], "cursor": "capture-since-v2:not-base64!!",
	}, nil)
	if !damaged.IsError {
		t.Error("a damaged cursor was accepted rather than refused")
	}

	// A cursor from a format this server has stopped issuing. Reading it as a
	// fresh start would send the whole screen as though it were new.
	stale := call(ctx, t, session, "capture_since", map[string]any{
		"paneId": panes[0], "cursor": "capture-since-v1:e30",
	}, nil)
	if !stale.IsError {
		t.Error("a cursor from an older format was accepted")
	}

	foreign := call(ctx, t, session, "capture_since", map[string]any{
		"paneId": panes[0], "cursor": "something-else-entirely",
	}, nil)
	if !foreign.IsError {
		t.Error("a cursor this server never issued was accepted")
	}
}

//libtmux:real-tmux
func TestClearedHistoryIsReportedAsLinesMissed(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: cleared\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	run(ctx, t, session, pane, "for i in $(seq 1 60); do echo before-$i; done")

	var first struct {
		Cursor string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"paneId": pane}, &first)

	result := call(ctx, t, session, "clear_pane", map[string]any{
		"paneId": pane, "history": true,
	}, nil)
	if result.IsError {
		t.Fatalf("clear_pane: %#v", result.Content)
	}

	var after struct {
		LinesMissed bool   `json:"linesMissed"`
		Cursor      string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"cursor": first.Cursor}, &after)
	if !after.LinesMissed {
		t.Error("history was cleared under the cursor and the reply did not say so")
	}
	if after.Cursor == "" {
		t.Error("a reading that lost its place returned no cursor to start again from")
	}
}

//libtmux:real-tmux
func TestAStopPatternEndsAWaitBeforeItsDeadline(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: stopping\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	send(ctx, t, session, pane, "sleep 1; echo 'error: it did not work'; sleep 60")

	var ended struct {
		Outcome string `json:"outcome"`
		Found   bool   `json:"found"`
		Matched string `json:"matched"`
	}
	started := time.Now()
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         pane,
		"patterns":       []string{"READY"},
		"stop":           []string{"error:"},
		"sinceEntry":     true,
		"timeoutSeconds": 30,
	}, &ended)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if ended.Outcome != "stopped" || ended.Found {
		t.Errorf("outcome = %q found = %v, want stopped and not found",
			ended.Outcome, ended.Found)
	}
	if ended.Matched != "error:" {
		t.Errorf("matched = %q, want the stop pattern that ended it", ended.Matched)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the stop pattern took %v to end the wait, so it waited the deadline out", elapsed)
	}
}

//libtmux:real-tmux
func TestSearchReportsTheLinesThatMatched(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: searching\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	run(ctx, t, session, pane, "echo 'FAILED test_the_thing'")

	type search struct {
		Panes []struct {
			Pane struct {
				ID string `json:"id"`
			} `json:"pane"`
			Matches []struct {
				Text string `json:"text"`
			} `json:"matches"`
		} `json:"panes"`
	}

	// Matching ignores case unless asked, because a caller repeating a word it
	// read in prose rarely reproduces the terminal's capitalisation.
	var folded search
	call(ctx, t, session, "search_panes", map[string]any{"text": "failed test_the_thing"}, &folded)
	if len(folded.Panes) != 1 {
		t.Fatalf("case-insensitive search found %d panes, want 1", len(folded.Panes))
	}
	if len(folded.Panes[0].Matches) == 0 {
		t.Fatal("a matching pane was reported without the line that matched")
	}
	if !strings.Contains(folded.Panes[0].Matches[0].Text, "FAILED") {
		t.Errorf("the reported line is not the one that matched: %q",
			folded.Panes[0].Matches[0].Text)
	}
	if folded.Panes[0].Pane.ID != pane {
		t.Errorf("search found %q, want %q", folded.Panes[0].Pane.ID, pane)
	}

	var cased search
	call(ctx, t, session, "search_panes", map[string]any{
		"text": "failed test_the_thing", "matchCase": true,
	}, &cased)
	if len(cased.Panes) != 0 {
		t.Error("matchCase matched text whose capitalisation differed")
	}

	var pattern search
	call(ctx, t, session, "search_panes", map[string]any{
		"text": `FAILED\s+test_\w+`, "regex": true, "matchCase": true,
	}, &pattern)
	if len(pattern.Panes) != 1 {
		t.Errorf("regex search found %d panes, want 1", len(pattern.Panes))
	}

	broken := call(ctx, t, session, "search_panes", map[string]any{
		"text": "([unclosed", "regex": true,
	}, nil)
	if !broken.IsError {
		t.Error("an invalid regular expression was accepted")
	}
}

//libtmux:real-tmux
func TestPasteTextDeliversWhatSendKeysWouldEat(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: pasting\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// A word tmux claims as a key, in the middle of text that is not keys.
	result := call(ctx, t, session, "paste_text", map[string]any{
		"paneId": pane, "text": "echo one Escape two",
	}, nil)
	if result.IsError {
		t.Fatalf("paste_text: %#v", result.Content)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
		if strings.Contains(strings.Join(shown.Lines, "\n"), "Escape two") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the pasted text did not arrive intact")
}

//libtmux:real-tmux
func TestKillingTheLastPaneSaysTheWindowWentWithIt(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: ending\nwindows:\n"+
			"  - window_name: first\n    panes:\n      - {}\n      - {}\n"+
			"  - window_name: second\n    panes:\n      - {}\n")

	var listed struct {
		Panes []struct {
			ID       string `json:"id"`
			Window   string `json:"window"`
			WindowID string `json:"windowId"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)

	var lone string
	var loneWindow string
	for _, pane := range listed.Panes {
		if pane.Window == "second" {
			lone = pane.ID
			loneWindow = pane.WindowID
		}
	}
	if lone == "" {
		t.Fatalf("the workspace has no lone pane: %+v", listed.Panes)
	}

	var killed struct {
		Killed      string `json:"killed"`
		WindowEnded bool   `json:"windowEnded"`
	}
	result := call(ctx, t, session, "kill_pane", map[string]any{"paneId": lone}, &killed)
	if result.IsError {
		t.Fatalf("kill_pane: %#v", result.Content)
	}
	if !killed.WindowEnded {
		t.Errorf("killing window %s's only pane did not report the window ending", loneWindow)
	}
}

//libtmux:real-tmux
func TestABatchIsCheckedWholeBeforeAnythingRuns(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: batched\nwindows:\n  - panes:\n      - {}\n")
	before := paneIDs(ctx, t, session)

	// A split that would have worked, followed by a kill the tier forbids.
	result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{"paneId": before[0]}},
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "batched"}},
		},
	}, nil)
	if !result.IsError {
		t.Fatal("a mutating batch containing a kill was allowed")
	}
	if after := paneIDs(ctx, t, session); len(after) != len(before) {
		t.Errorf("the refused batch still ran its first call: %d panes became %d",
			len(before), len(after))
	}

	unknown := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{{"tool": "no_such_tool"}},
	}, nil)
	if !unknown.IsError {
		t.Error("a batch naming a tool this server does not serve was accepted")
	}

	// The same calls at the destructive tier are allowed.
	allowed := call(ctx, t, session, "call_destructive_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "batched"}},
		},
	}, nil)
	if allowed.IsError {
		t.Errorf("a destructive batch was refused: %#v", allowed.Content)
	}
}

//libtmux:real-tmux
func TestOptionsAreReadAndWrittenAtTheScopeAsked(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: settings\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	result := call(ctx, t, session, "set_option", map[string]any{
		"paneId": pane, "name": "remain-on-exit", "value": "on",
	}, nil)
	if result.IsError {
		t.Fatalf("set_option: %#v", result.Content)
	}

	var read struct {
		Value string `json:"value"`
		Set   bool   `json:"set"`
		Scope string `json:"scope"`
	}
	call(ctx, t, session, "show_option", map[string]any{
		"paneId": pane, "name": "remain-on-exit",
	}, &read)
	if read.Value != "on" || !read.Set {
		t.Errorf("read back %q set=%v, want on and set", read.Value, read.Set)
	}
	if read.Scope != "pane" {
		t.Errorf("scope = %q, want pane by default", read.Scope)
	}

	bad := call(ctx, t, session, "show_option", map[string]any{
		"name": "remain-on-exit", "scope": "galaxy",
	}, nil)
	if !bad.IsError {
		t.Error("an unknown scope was accepted")
	}
}

//libtmux:real-tmux
func TestTheServerSaysWhichSocketItAddresses(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: identity\nwindows:\n  - panes:\n      - {}\n")

	var info struct {
		SocketPath       string `json:"socketPath"`
		Alive            bool   `json:"alive"`
		Sessions         int    `json:"sessions"`
		SafetyLevel      string `json:"safetyLevel"`
		InsideThisServer bool   `json:"insideThisServer"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if !info.Alive || info.Sessions == 0 {
		t.Fatalf("the server reports itself as %+v", info)
	}
	if info.SocketPath == "" {
		t.Error("the server did not say which socket it addresses")
	}
	if info.SafetyLevel == "" {
		t.Error("the server did not say what the operator allowed")
	}
	// The test's tmux server is not the one this process runs in, whether or
	// not the process is inside tmux at all.
	if info.InsideThisServer {
		t.Errorf("the server claims to be running inside its own target %q", info.SocketPath)
	}

	var servers struct {
		Servers []struct {
			SocketPath string `json:"socketPath"`
			IsTarget   bool   `json:"isTarget"`
			Alive      bool   `json:"alive"`
		} `json:"servers"`
		SearchedIn string `json:"searchedIn"`
	}
	call(ctx, t, session, "list_servers", map[string]any{}, &servers)
	if servers.SearchedIn == "" {
		t.Error("list_servers did not say where it looked")
	}
	// The test server's socket is in the test's own directory rather than
	// tmux's, so it is not expected in the listing; what must hold is that
	// nothing else is marked as the target.
	for _, found := range servers.Servers {
		if found.IsTarget && found.SocketPath != target.SocketPath() {
			t.Errorf("%q is marked as the target, which is %q",
				found.SocketPath, target.SocketPath())
		}
	}
}

//libtmux:real-tmux
func TestASubscriberIsToldWhenAPaneChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	updated := make(chan string, 16)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "subscriber"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			updated <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	workspace(ctx, t, session, "session_name: watched\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	uri := "tmux://panes/" + strings.TrimPrefix(pane, "%") + "/content"
	if err := session.Subscribe(ctx, &sdk.SubscribeParams{URI: uri}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// The watcher opens its tmux connection when the first subscription
	// arrives, and output written before it is attached is not seen by it.
	time.Sleep(500 * time.Millisecond)
	send(ctx, t, session, pane, "echo SUBSCRIBED-OUTPUT")

	select {
	case got := <-updated:
		if got != uri {
			t.Errorf("notified about %q, want %q", got, uri)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a subscriber heard nothing when the pane wrote")
	}

	if err := session.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: uri}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
}

//libtmux:real-tmux
func TestRunCommandReportsWhatTheCommandPrinted(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: running\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var ran struct {
		ExitStatus *int     `json:"exitStatus"`
		TimedOut   bool     `json:"timedOut"`
		Output     []string `json:"output"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo PRINTED-BY-THE-COMMAND", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command: %#v", result.Content)
	}
	if ran.TimedOut || ran.ExitStatus == nil || *ran.ExitStatus != 0 {
		t.Fatalf("run_command reported %+v", ran)
	}
	joined := strings.Join(ran.Output, "\n")
	if !strings.Contains(joined, "PRINTED-BY-THE-COMMAND") {
		t.Errorf("the command's output was not reported: %q", joined)
	}
	// The wrapper this tool runs the command inside is its own bookkeeping and
	// has no business in the answer.
	if strings.Contains(joined, "wait-for") {
		t.Errorf("the wrapper's echo reached the caller: %q", joined)
	}

	var failed struct {
		ExitStatus *int `json:"exitStatus"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "exit 3", "timeoutSeconds": 30,
	}, &failed)
	if failed.ExitStatus == nil || *failed.ExitStatus != 3 {
		t.Errorf("exit status = %v, want 3", failed.ExitStatus)
	}
}

// run runs a command in a pane and waits for it, so what follows can assume it
// finished rather than sleeping and hoping.
func run(ctx context.Context, t *testing.T, session *sdk.ClientSession, pane, command string) {
	t.Helper()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": command, "timeoutSeconds": 30,
	}, nil)
	if result.IsError {
		t.Fatalf("run %q: %#v", command, result.Content)
	}
}

// send types a command without waiting for it, for the cases that are about
// what happens while it runs.
func send(ctx context.Context, t *testing.T, session *sdk.ClientSession, pane, command string) {
	t.Helper()
	result := call(ctx, t, session, "send_keys", map[string]any{
		"paneId": pane, "command": command,
	}, nil)
	if result.IsError {
		t.Fatalf("send %q: %s", command, resultText(result))
	}
}

//libtmux:real-tmux
func TestTheEnvironmentIsWhatANewPaneWouldGet(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: environ\nwindows:\n  - panes:\n      - {}\n")

	result := call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE", "value": "set-by-the-test",
	}, nil)
	if result.IsError {
		t.Fatalf("set_environment: %#v", result.Content)
	}

	var read struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE",
	}, &read)
	if len(read.Variables) != 1 || read.Variables[0].Value != "set-by-the-test" {
		t.Fatalf("read back %+v", read.Variables)
	}

	call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE", "unset": true,
	}, nil)
	var gone struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE",
	}, &gone)
	if len(gone.Variables) != 0 {
		t.Errorf("an unset variable was still reported: %+v", gone.Variables)
	}

	// tmux keeps two layers and a new pane inherits both, the session's
	// overriding the server's. Reading only one of them answers a question
	// nobody asked: PATH lives in the server's, so a caller asking what a pane
	// will get was told it gets no PATH.
	if _, err := target.Cmd(ctx, "set-environment", "-g",
		"LIBTMUX_MCP_GLOBAL", "from-the-server"); err != nil {
		t.Fatalf("set a server-wide variable: %v", err)
	}
	if _, err := target.Cmd(ctx, "set-environment", "-g",
		"LIBTMUX_MCP_BOTH", "from-the-server"); err != nil {
		t.Fatalf("set a server-wide variable: %v", err)
	}
	call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_BOTH", "value": "from-the-session",
	}, nil)

	var inherited struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Scope string `json:"scope"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_GLOBAL",
	}, &inherited)
	if len(inherited.Variables) != 1 ||
		inherited.Variables[0].Value != "from-the-server" {
		t.Errorf("a variable a new pane inherits from the server was not "+
			"reported: %+v", inherited.Variables)
	} else if inherited.Variables[0].Scope != "server" {
		t.Errorf("scope = %q, want server", inherited.Variables[0].Scope)
	}

	// The session's value is the one a pane gets, so it is the one reported.
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_BOTH",
	}, &inherited)
	if len(inherited.Variables) != 1 ||
		inherited.Variables[0].Value != "from-the-session" {
		t.Errorf("the session's value does not override the server's: %+v",
			inherited.Variables)
	} else if inherited.Variables[0].Scope != "session" {
		t.Errorf("scope = %q, want session", inherited.Variables[0].Scope)
	}

	// Listing everything covers both layers too.
	var all struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{}, &all)
	named := map[string]bool{}
	for _, variable := range all.Variables {
		named[variable.Name] = true
	}
	if !named["LIBTMUX_MCP_GLOBAL"] {
		t.Errorf("listing everything omits what the server holds: %d variables",
			len(all.Variables))
	}
}

// Buffer tools normalize names into the libtmux-mcp- namespace.
//
//libtmux:real-tmux
func TestBuffersStayInTheMCPNamespace(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: buffers\nwindows:\n  - panes:\n      - {}\n")

	// An unprefixed buffer stands in for a person's copy.
	if err := target.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "a private copy",
		Name: new("someones-own"),
	}); err != nil {
		t.Fatalf("stage a foreign buffer: %v", err)
	}

	var staged struct {
		Name  string `json:"name"`
		Bytes int    `json:"bytes"`
	}
	result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "staged by the test", "name": "notes",
	}, &staged)
	if result.IsError {
		t.Fatalf("load_buffer: %#v", result.Content)
	}

	var read struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "show_buffer", map[string]any{"name": staged.Name}, &read)
	if strings.Join(read.Lines, "\n") != "staged by the test" {
		t.Errorf("read back %q", strings.Join(read.Lines, "\n"))
	}

	// The foreign buffer is not reachable because its name is prefixed on lookup.
	foreign := call(ctx, t, session, "show_buffer", map[string]any{"name": "someones-own"}, nil)
	if !foreign.IsError {
		t.Error("an unprefixed buffer was readable")
	}

	if result := call(ctx, t, session, "delete_buffer", map[string]any{
		"name": staged.Name,
	}, nil); result.IsError {
		t.Errorf("delete_buffer: %#v", result.Content)
	}
}

//libtmux:real-tmux
func TestBufferNamesPreserveEmbeddedWhitespace(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: spaced-buffer\nwindows:\n  - panes:\n      - {}\n")

	const name = "release notes"
	var staged struct {
		Name string `json:"name"`
	}
	result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "payload", "name": name,
	}, &staged)
	if result.IsError {
		t.Fatalf("load_buffer(%q): %#v", name, result.Content)
	}
	if !strings.HasSuffix(staged.Name, name) {
		t.Errorf("load_buffer(%q) returned name %q", name, staged.Name)
	}

	var read struct {
		Lines []string `json:"lines"`
	}
	result = call(ctx, t, session, "show_buffer", map[string]any{
		"name": staged.Name,
	}, &read)
	if result.IsError {
		t.Fatalf("show_buffer(%q): %#v", staged.Name, result.Content)
	}
	if got := strings.Join(read.Lines, "\n"); got != "payload" {
		t.Errorf("show_buffer(%q) = %q, want payload", staged.Name, got)
	}
}

// tmux 3.7 normalizes displayed buffer names differently from lookup. A
// successful load must return a name that later buffer operations can use.
//
//libtmux:real-tmux
func TestALoadedBufferIsReachableByTheNameItAnswersWith(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: handles\nwindows:\n  - panes:\n      - {}\n")

	for _, name := range []string{`a\b`, `\`, `back\\slash`} {
		var staged struct {
			Name string `json:"name"`
		}
		result := call(ctx, t, session, "load_buffer", map[string]any{
			"text": "payload", "name": name,
		}, &staged)
		if !result.IsError {
			// Accepting it is only sound if the handle works, so hold it to that.
			read := call(ctx, t, session, "show_buffer", map[string]any{
				"name": staged.Name,
			}, nil)
			if read.IsError {
				t.Errorf("load_buffer(%q) answered with %q, which show_buffer cannot "+
					"reach: %#v", name, staged.Name, read.Content)
			}
			call(ctx, t, session, "delete_buffer", map[string]any{"name": staged.Name}, nil)
			continue
		}
		if text, ok := result.Content[0].(*sdk.TextContent); ok &&
			!strings.Contains(text.Text, `\`) {
			t.Errorf("load_buffer(%q) was refused without naming the character: %s",
				name, text.Text)
		}
	}
}

// stringPointer is the address of a value, which several tmux requests take to
// tell an absent field from an empty one.
//

//libtmux:real-tmux
func TestTheInstructionsNameTheToolsTheyRecommend(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	initialized := session.InitializeResult()
	if initialized == nil || strings.TrimSpace(initialized.Instructions) == "" {
		t.Fatal("the server sent no instructions")
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	advertised := map[string]bool{}
	for _, tool := range listed.Tools {
		advertised[tool.Name] = true
	}

	// Every word in the instructions that looks like one of this server's tool
	// names has to be one, which catches a rename that updated the code and
	// not the text.
	for _, word := range strings.FieldsFunc(initialized.Instructions, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z')
	}) {
		if !strings.Contains(word, "_") || advertised[word] {
			continue
		}
		// Words that merely contain an underscore and are not tool names, such
		// as a tmux format, are not this test's business.
		if strings.HasPrefix(word, "pane_") || strings.HasPrefix(word, "window_") ||
			strings.HasPrefix(word, "session_") || strings.HasPrefix(word, "client_") {
			continue
		}
		t.Errorf("the instructions recommend %q, which is not advertised", word)
	}

	for _, prompt := range []string{
		"diagnose_pane", "watch_pane", "recover_pane", "set_up_workspace",
	} {
		result, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: prompt})
		if err != nil {
			t.Errorf("get prompt %s: %v", prompt, err)
			continue
		}
		if len(result.Messages) == 0 {
			t.Errorf("prompt %s produced no message", prompt)
		}
	}
}

//libtmux:real-tmux
func TestPaneTextNeverExceedsTheCeiling(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: ceiling\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	run(ctx, t, session, pane, "for i in $(seq 1 200); do echo line-$i; done")

	var asked struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "includeHistory": true, "maxLines": 1_000_000,
	}, &asked)
	if len(asked.Lines) > 5000 {
		t.Errorf("a request above the ceiling returned %d lines", len(asked.Lines))
	}

	negative := call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "maxLines": -1,
	}, nil)
	if !negative.IsError {
		t.Error("a negative bound was accepted")
	}
}

//libtmux:real-tmux
func TestDisplayMessageAnswersInTmuxsOwnLanguage(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: formats\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var answered struct {
		Value  string `json:"value"`
		PaneID string `json:"paneId"`
	}
	result := call(ctx, t, session, "display_message", map[string]any{
		"paneId": pane, "format": "#{session_name}/#{pane_id}",
	}, &answered)
	if result.IsError {
		t.Fatalf("display_message: %#v", result.Content)
	}
	if answered.Value != "formats/"+pane {
		t.Errorf("tmux answered %q, want %q", answered.Value, "formats/"+pane)
	}
	if answered.PaneID != pane {
		t.Errorf("evaluated against %q, want %q", answered.PaneID, pane)
	}

	empty := call(ctx, t, session, "display_message", map[string]any{"format": "  "}, nil)
	if !empty.IsError {
		t.Error("an empty format was accepted")
	}
}

//libtmux:real-tmux
func TestSendKeysBatchDrivesAProgramThatReadsKeys(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: keys\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var sent struct {
		Sent   int    `json:"sent"`
		PaneID string `json:"paneId"`
	}
	result := call(ctx, t, session, "send_keys_batch", map[string]any{
		"paneId": pane, "keys": []string{"e", "c", "h", "o", "Space", "K", "E", "Y", "S"},
	}, &sent)
	if result.IsError {
		t.Fatalf("send_keys_batch: %#v", result.Content)
	}
	if sent.Sent != 9 {
		t.Errorf("sent %d keys, want 9", sent.Sent)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
		if strings.Contains(strings.Join(shown.Lines, "\n"), "echo KEYS") {
			// Nothing ran, which is the point: no Enter was appended.
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the keys did not reach the pane")
}

//libtmux:real-tmux
func TestASafetyLevelWithholdsTheToolsItSays(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is advertised at the readonly level", tool.Name)
		}
	}

	refused := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "anything"}},
		},
	}, nil)
	if !refused.IsError {
		t.Error("a batch reached a tool the safety level withheld")
	}

	if _, err := os.Stat("."); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

//libtmux:real-tmux
func TestAnEmptyPaneIsStillAReadableResource(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: blank\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	if result := call(ctx, t, session, "clear_pane", map[string]any{
		"paneId": pane, "history": true,
	}, nil); result.IsError {
		t.Fatalf("clear_pane: %#v", result.Content)
	}

	uri := "tmux://panes/" + strings.TrimPrefix(pane, "%") + "/content"
	result, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("read returned %d contents, want 1", len(result.Contents))
	}
	if result.Contents[0].Text == "" {
		t.Error("an empty pane produced contents with no text, which a client rejects")
	}
}

// insidePaneVariables let this test re-run itself inside a tmux pane. The pane
// this process runs in is not something a test can arrange after the fact, so
// the test becomes the program the pane runs.
const (
	insidePaneSocket = "LIBTMUX_MCP_TEST_SOCKET"
	insidePaneReport = "LIBTMUX_MCP_TEST_REPORT"
)

//libtmux:real-tmux
func TestTheServerFindsItsOwnPaneWithoutTheEnvironment(t *testing.T) {
	if socket := os.Getenv(insidePaneSocket); socket != "" {
		reportOwnPaneFromInside(t, socket, os.Getenv(insidePaneReport))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "host"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// The pane runs this test binary again, with TMUX and TMUX_PANE removed so
	// only the process tree can answer, and with the pane held open long enough
	// for the report to be written.
	report := filepath.Join(t.TempDir(), "report")
	command := fmt.Sprintf(
		"env -u TMUX -u TMUX_PANE %s=%s %s=%s %s -test.run='^%s$' >/dev/null 2>&1; sleep 30",
		insidePaneSocket, socket,
		insidePaneReport, report,
		os.Args[0], t.Name(),
	)
	window, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "probe", Command: command,
	})
	if err != nil {
		t.Fatalf("start the probe pane: %v", err)
	}
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("find the probe pane: %v", err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var found string
	for time.Now().Before(deadline) {
		if written, err := os.ReadFile(report); err == nil {
			found = strings.TrimSpace(string(written))
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("the server running inside a pane reported nothing")
	}
	if want := pane.ID().String(); found != want {
		t.Errorf("the server inside pane %s reported %q", want, found)
	}
}

// reportOwnPaneFromInside is the half of the test that runs in the pane. It
// asks the server which pane it is in and writes the answer where the other
// half will read it.
func reportOwnPaneFromInside(t *testing.T, socket, report string) {
	t.Helper()
	if report == "" {
		t.Fatal("no report path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	client := sdk.NewClient(&sdk.Implementation{Name: "inside"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	var info struct {
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	answer := ""
	if info.InsideThisServer {
		answer = info.CallerPaneID
	}
	if err := os.WriteFile(report, []byte(answer), 0o600); err != nil {
		t.Fatalf("write the report: %v", err)
	}
}

// connectAsking is connect with a client that can be asked questions. The
// handler decides what the person says.
func connectAsking(
	t *testing.T,
	answer func(*sdk.ElicitRequest) *sdk.ElicitResult,
) (*sdk.ClientSession, tmux.Server, context.Context, *int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	asked := 0
	options := &sdk.ClientOptions{}
	if answer != nil {
		options.ElicitationHandler = func(
			_ context.Context,
			request *sdk.ElicitRequest,
		) (*sdk.ElicitResult, error) {
			asked++
			return answer(request), nil
		}
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "asking-client"}, options)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, target, ctx, &asked
}

// answering is an elicitation handler that always gives the same action.
func answering(action string) func(*sdk.ElicitRequest) *sdk.ElicitResult {
	return func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: action}
	}
}

// propertiesOf is the form an elicitation asked to have filled in.
func propertiesOf(t *testing.T, request *sdk.ElicitRequest) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(request.Params.RequestedSchema)
	if err != nil {
		t.Fatalf("marshal the requested schema: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("decode the requested schema: %v", err)
	}
	return schema.Properties
}

//libtmux:real-tmux
func TestAYesAboutTheCallerPaneCanBeKept(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")

	asked := 0
	remembering, keeping := true, false
	session, target, ctx, _ := connectAsking(t, func(request *sdk.ElicitRequest) *sdk.ElicitResult {
		asked++
		_, offered := propertiesOf(t, request)["remember"]
		if offered != remembering {
			t.Errorf("the form offered to keep the yes = %v, want %v",
				offered, remembering)
		}
		if !offered {
			return &sdk.ElicitResult{Action: "accept"}
		}
		return &sdk.ElicitResult{
			Action:  "accept",
			Content: map[string]any{"remember": keeping},
		}
	})
	workspace(ctx, t, session, "session_name: kept\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	// Without an answer that keeps it, every write asks.
	for range 2 {
		call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo asked",
		}, nil)
	}
	if asked != 2 {
		t.Fatalf("two writes asked %d times, want one each", asked)
	}

	// Then one that keeps it, and the writes after it go through unasked --
	// including through a different tool, because the yes is about the pane.
	keeping = true
	asked = 0
	call(ctx, t, session, "send_keys", map[string]any{
		"paneId": ownPane, "command": "echo keeping",
	}, nil)
	if asked != 1 {
		t.Fatalf("keeping the yes asked %d times, want once", asked)
	}
	for _, kept := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"send_keys", map[string]any{"paneId": ownPane, "command": "echo after"}},
		{"paste_text", map[string]any{"paneId": ownPane, "text": "echo pasted"}},
		{"clear_pane", map[string]any{"paneId": ownPane}},
	} {
		if result := call(ctx, t, session, kept.tool, kept.arguments, nil); result.IsError {
			t.Errorf("%s was refused after the yes was kept: %s", kept.tool, resultText(result))
		}
	}
	if asked != 1 {
		t.Errorf("writes after the kept yes asked %d more times", asked-1)
	}

	// Ending the pane is a different yes, so it asks whatever was said about
	// typing there, and its form never offers to keep it.
	remembering = false
	if result := call(ctx, t, session, "kill_pane",
		map[string]any{"paneId": ownPane}, nil); result.IsError {
		t.Errorf("kill_pane was refused by an accepting client: %s", resultText(result))
	}
	if asked != 2 {
		t.Errorf("ending the pane asked %d times in total, want one of its own", asked)
	}
}

//libtmux:real-tmux
func TestOneSessionsYesDoesNotSilenceAnother(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	server := mustMCPServer(t, target)

	// Two clients of one server, as an embedder holds them.
	join := func(name string, answer func(*sdk.ElicitRequest) *sdk.ElicitResult) (*sdk.ClientSession, *int) {
		clientTransport, serverTransport := sdk.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = serverSession.Close() })
		asked := 0
		client := sdk.NewClient(&sdk.Implementation{Name: name}, &sdk.ClientOptions{
			ElicitationHandler: func(
				_ context.Context, request *sdk.ElicitRequest,
			) (*sdk.ElicitResult, error) {
				asked++
				return answer(request), nil
			},
		})
		session, err := client.Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session, &asked
	}

	first, askedFirst := join("first", func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"remember": true}}
	})
	second, askedSecond := join("second", func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: "decline"}
	})

	workspace(ctx, t, first, "session_name: shared\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	// The first client says yes and keeps it, then writes again unasked.
	for range 2 {
		if result := call(ctx, t, first, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo first",
		}, nil); result.IsError {
			t.Fatalf("the first client was refused: %s", resultText(result))
		}
	}
	if *askedFirst != 1 {
		t.Errorf("the first client was asked %d times, want once", *askedFirst)
	}

	// The second client never saw that question, so it must be asked its own
	// -- and its answer must be the one that governs its own call.
	result := call(ctx, t, second, "send_keys", map[string]any{
		"paneId": ownPane, "command": "echo second",
	}, nil)
	if *askedSecond != 1 {
		t.Errorf("the second client was asked %d times, want once of its own", *askedSecond)
	}
	if !result.IsError {
		t.Error("the second client wrote to the caller pane on another client's yes")
	}
}

//libtmux:real-tmux
func TestEndingWhatHoldsTheCallerPaneIsAskedAbout(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: holding\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	var listed struct {
		Panes []struct {
			WindowID string `json:"windowId"`
			Session  string `json:"session"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to be held by anything")
	}
	holder := listed.Panes[0]

	for _, ending := range []struct {
		tool      string
		arguments map[string]any
		names     string
	}{
		{"kill_window", map[string]any{"windowId": holder.WindowID}, holder.WindowID},
		{"kill_session", map[string]any{"sessionName": holder.Session}, holder.Session},
		{"kill_server", map[string]any{"confirm": true}, "tmux server"},
	} {
		t.Run(ending.tool, func(t *testing.T) {
			result := call(ctx, t, session, ending.tool, ending.arguments, nil)
			if !result.IsError {
				t.Fatalf("%s ended what holds the caller pane", ending.tool)
			}
			said := resultText(result)
			if !strings.Contains(said, ending.names) {
				t.Errorf("the refusal does not name %s: %q", ending.names, said)
			}
			if !strings.Contains(said, "holds the pane this server is running in") {
				t.Errorf("the refusal does not say why: %q", said)
			}
		})
	}

	// The same tools still work on something that does not hold it, which is
	// what keeps this a guard rather than a ban.
	var made struct {
		WindowID string `json:"windowId"`
	}
	call(ctx, t, session, "create_window", map[string]any{
		"sessionName": holder.Session, "command": "sleep 300",
	}, &made)
	if made.WindowID == "" {
		t.Fatal("no second window to end")
	}
	if result := call(ctx, t, session, "kill_window",
		map[string]any{"windowId": made.WindowID}, nil); result.IsError {
		t.Errorf("a window holding no caller pane was refused: %s", resultText(result))
	}
}

//libtmux:real-tmux
func TestWritingToTheCallerPaneIsAskedAbout(t *testing.T) {
	// A fresh server hands out predictable pane ids, which is what lets the
	// environment name one before the server reporting it exists.
	const ownPane = "%0"

	arrange := func(
		t *testing.T,
		answer func(*sdk.ElicitRequest) *sdk.ElicitResult,
	) (*sdk.ClientSession, context.Context, *int) {
		t.Helper()
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx, asked := connectAsking(t, answer)
		workspace(ctx, t, session, "session_name: guarded\nwindows:\n  - panes:\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")
		return session, ctx, asked
	}

	t.Run("declining stops the write", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("decline"))
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo into-my-own-terminal",
		}, nil)
		if !result.IsError {
			t.Error("a declined write to the caller pane went through anyway")
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times, want once", *asked)
		}
	})

	t.Run("a client that cannot be asked is refused", func(t *testing.T) {
		// Elicitation is optional, and letting the write through when nobody
		// can be asked makes the guard advisory: the client least able to warn
		// its person is the one that types into their terminal unannounced. It
		// happened -- a peer session identifying its own server ran a command
		// against the caller pane and put the text in its user's prompt box.
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx := connect(t)
		workspace(ctx, t, session, "session_name: unasked\nwindows:\n  - panes:\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		for _, tool := range []string{"send_keys", "run_command"} {
			arguments := map[string]any{"paneId": ownPane, "command": "echo into-my-own-terminal"}
			if tool == "run_command" {
				arguments["timeoutSeconds"] = 5
			}
			result := call(ctx, t, session, tool, arguments, nil)
			if !result.IsError {
				t.Errorf("%s reached the caller pane with nobody able to be asked", tool)
				continue
			}
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			if !strings.Contains(said, ownPane) {
				t.Errorf("the %s refusal does not name the pane: %q", tool, said)
			}
			// The caller whose only pane is this one has nowhere to be sent,
			// so making a pane is named beside finding one.
			for _, way := range []string{"split_window", "create_session", "isCaller"} {
				if !strings.Contains(said, way) {
					t.Errorf("the %s refusal does not offer %s: %q", tool, way, said)
				}
			}
		}
	})

	t.Run("accepting lets it through", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("accept"))
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "true",
		}, nil)
		if result.IsError {
			t.Errorf("an accepted write was refused: %#v", result.Content)
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times, want once", *asked)
		}
	})

	t.Run("a batch is asked about too", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("decline"))
		var out struct {
			Completed int `json:"completed"`
			Results   []struct {
				Tool  string `json:"tool"`
				Error string `json:"error"`
			} `json:"results"`
		}
		call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
			"calls": []map[string]any{{
				"tool": "send_keys",
				"arguments": map[string]any{
					"paneId": ownPane, "command": "echo into-my-own-terminal",
				},
			}},
		}, &out)
		if out.Completed != 0 {
			t.Error("a batch typed into the caller pane a direct call was refused")
		}
		if len(out.Results) == 0 || !strings.Contains(out.Results[0].Error, "declined") {
			t.Errorf("the batch does not report the decline: %+v", out.Results)
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times for a batched write, want once", *asked)
		}
	})

	t.Run("another pane is not asked about", func(t *testing.T) {
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx, asked := connectAsking(t,
			answering("decline"))
		workspace(ctx, t, session,
			"session_name: elsewhere\nwindows:\n  - panes:\n      - {}\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		panes := paneIDs(ctx, t, session)
		other := ""
		for _, pane := range panes {
			if pane != ownPane {
				other = pane
				break
			}
		}
		if other == "" {
			t.Fatal("the workspace built no pane other than the caller's")
		}
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": other, "command": "true",
		}, nil)
		if result.IsError {
			t.Errorf("writing to somebody else's pane was refused: %#v", result.Content)
		}
		if *asked != 0 {
			t.Errorf("the person was asked about a pane that is not the caller's")
		}
	})

	t.Run("a client that cannot be asked is still stopped", func(t *testing.T) {
		// This reverses what this test asserted before. Letting the write
		// through when nobody could be asked was chosen so a client without
		// elicitation kept the behaviour it had; what that bought in practice
		// was a guard that is absent from the clients most likely to need it.
		// Writing to somebody's own terminal unannounced is the harm, and it
		// does not become acceptable because their client cannot show a prompt.
		session, ctx, _ := arrange(t, nil)
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "true",
		}, nil)
		if !result.IsError {
			t.Error("a client that cannot be asked typed into the caller pane")
		}

		// Every other pane is unaffected, which is what keeps this a guard on
		// one pane rather than a restriction on the tool.
		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		for _, pane := range listed.Panes {
			if pane.IsCaller != nil && *pane.IsCaller {
				continue
			}
			if result := call(ctx, t, session, "send_keys", map[string]any{
				"paneId": pane.ID, "command": "true",
			}, nil); result.IsError {
				t.Errorf("writing to %s was refused: %#v", pane.ID, result.Content)
			}
		}
	})
}

//libtmux:real-tmux
func TestEnteringCopyModeOnTheCallerPaneIsAskedAbout(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")
	session, target, ctx, asked := connectAsking(t,
		answering("decline"))
	workspace(ctx, t, session, "session_name: copymode\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	if result := call(ctx, t, session, "enter_copy_mode",
		map[string]any{"paneId": ownPane}, nil); !result.IsError {
		t.Error("entering copy mode on the caller pane was not asked about")
	}
	if *asked != 1 {
		t.Errorf("the person was asked %d times, want once", *asked)
	}

	// The way out is never blocked, or a declined entry would be unrecoverable
	// through this server.
	if result := call(ctx, t, session, "exit_copy_mode",
		map[string]any{"paneId": ownPane}, nil); result.IsError {
		t.Errorf("leaving copy mode was refused: %#v", result.Content)
	}
	if *asked != 1 {
		t.Errorf("leaving copy mode asked the person as well")
	}
}

//libtmux:real-tmux
func TestAWaitIsClampedRatherThanRefused(t *testing.T) {
	t.Setenv(tmuxmcp.WaitCeilingEnvironmentVariable, "2")
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: clamped\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var ended struct {
		Outcome                 string `json:"outcome"`
		EffectiveTimeoutSeconds int    `json:"effectiveTimeoutSeconds"`
		TimeoutClamped          bool   `json:"timeoutClamped"`
	}
	started := time.Now()
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "patterns": []string{"NEVER-APPEARS"},
		"sinceEntry": true, "timeoutSeconds": 3600,
	}, &ended)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if !ended.TimeoutClamped {
		t.Error("a wait past the ceiling did not report that it was clamped")
	}
	if ended.EffectiveTimeoutSeconds != 2 {
		t.Errorf("effectiveTimeoutSeconds = %d, want the ceiling of 2",
			ended.EffectiveTimeoutSeconds)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the wait ran %v, so the ceiling did not bound it", elapsed)
	}
}

//libtmux:real-tmux
func TestEveryWaitReportsTheBudgetItRan(t *testing.T) {
	t.Setenv(tmuxmcp.WaitCeilingEnvironmentVariable, "30")
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: budget\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var ended struct {
		EffectiveTimeoutSeconds int  `json:"effectiveTimeoutSeconds"`
		TimeoutClamped          bool `json:"timeoutClamped"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "idleSeconds": 1, "sinceEntry": true,
	}, &ended)
	if ended.TimeoutClamped {
		t.Error("a wait that asked for nothing was reported as clamped")
	}
	if ended.EffectiveTimeoutSeconds == 0 {
		t.Error("a wait that asked for nothing did not report the budget it ran")
	}
}

//libtmux:real-tmux
func TestAQuietPaneEndsAnIdleWait(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: quiet\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var ended struct {
		Outcome string   `json:"outcome"`
		Found   bool     `json:"found"`
		Lines   []string `json:"lines"`
	}
	started := time.Now()
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "idleSeconds": 2, "sinceEntry": true, "timeoutSeconds": 60,
	}, &ended)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if ended.Outcome != "idle" || ended.Found {
		t.Errorf("outcome = %q found = %v, want idle and not found", ended.Outcome, ended.Found)
	}
	if elapsed := time.Since(started); elapsed > 40*time.Second {
		t.Errorf("a quiet pane took %v to end an idle wait", elapsed)
	}
}

//libtmux:real-tmux
func TestOutputExtendsAnIdleWait(t *testing.T) {
	const (
		lines = 6
		idle  = 3
		// Two lines is where the cases separate. A window that never reset
		// ends at idle seconds flat however much the pane went on to write;
		// one that resets cannot end before idle seconds after the last line
		// it saw, which is a gap later than that.
		enough = 2
	)
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: extending\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	send(ctx, t, session, pane,
		fmt.Sprintf("for i in $(seq 1 %d); do echo LINE$i; sleep 1; done", lines))

	var ended struct {
		Outcome        string   `json:"outcome"`
		ElapsedSeconds float64  `json:"elapsedSeconds"`
		Lines          []string `json:"lines"`
	}
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "idleSeconds": idle, "sinceEntry": true, "timeoutSeconds": 40,
	}, &ended)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if ended.Outcome != "idle" {
		t.Fatalf("outcome = %q, want idle", ended.Outcome)
	}

	seen := 0
	for index := 1; index <= lines; index++ {
		if slices.ContainsFunc(ended.Lines, func(line string) bool {
			return strings.Contains(line, fmt.Sprintf("LINE%d", index))
		}) {
			seen++
		}
	}
	if seen < enough {
		t.Skipf("the pane wrote %d of %d lines inside a %ds window, "+
			"so this machine is too loaded to tell a reset window from a fixed one",
			seen, lines, idle)
	}
	// A fixed window ends at idle seconds exactly. A window that reset ran on
	// past that by however long the pane kept writing, which is at least the
	// gap between the lines it saw. Load only lengthens that, so this bound
	// does not tighten on a busy machine.
	if ended.ElapsedSeconds < float64(idle)+0.5 {
		t.Errorf("the wait saw %d lines but ended after %.1fs, barely past the %ds "+
			"window, so output did not extend it", seen, ended.ElapsedSeconds, idle)
	}
}

//libtmux:real-tmux
func TestADetachedCommandReturnsBeforeItFinishes(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: detached\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started struct {
		JobID    string `json:"jobId"`
		Detached bool   `json:"detached"`
	}
	sent := time.Now()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "sleep 5; echo FINISHED", "detach": true,
	}, &started)
	if result.IsError {
		t.Fatalf("run_command: %#v", result.Content)
	}
	if !started.Detached || started.JobID == "" {
		t.Fatalf("a detached run reported %+v", started)
	}
	if elapsed := time.Since(sent); elapsed > 4*time.Second {
		t.Errorf("detaching took %v, so it waited for the command", elapsed)
	}

	// Unfinished, and honest about it rather than reporting a status.
	var checked struct {
		Finished   bool `json:"finished"`
		ExitStatus *int `json:"exitStatus"`
	}
	call(ctx, t, session, "get_job", map[string]any{"jobId": started.JobID}, &checked)
	if checked.Finished || checked.ExitStatus != nil {
		t.Errorf("a command still running reported %+v", checked)
	}

	var collected struct {
		Finished   bool     `json:"finished"`
		ExitStatus *int     `json:"exitStatus"`
		Output     []string `json:"output"`
	}
	call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &collected)
	if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 {
		t.Fatalf("collecting the job reported %+v", collected)
	}
	if !slices.ContainsFunc(collected.Output, func(line string) bool {
		return strings.Contains(line, "FINISHED")
	}) {
		t.Errorf("the collected output was %q, want the command's own", collected.Output)
	}
}

//libtmux:real-tmux
func TestAJobAnswersTheSameWayTwice(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: onceonly\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started struct {
		JobID string `json:"jobId"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "true", "detach": true,
	}, &started)

	type collection struct {
		Finished   bool     `json:"finished"`
		ExitStatus *int     `json:"exitStatus"`
		Output     []string `json:"output"`
	}
	var first collection
	call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &first)
	if !first.Finished || first.ExitStatus == nil {
		t.Fatalf("the job never finished: %+v", first)
	}

	var again collection
	result := call(ctx, t, session, "get_job", map[string]any{"jobId": started.JobID}, &again)
	if result.IsError {
		t.Fatalf("asking about a collected job failed: %#v", result.Content)
	}
	if !again.Finished || again.ExitStatus == nil || *again.ExitStatus != *first.ExitStatus {
		t.Errorf("the second reading said %+v, want the same as %+v", again, first)
	}
}

//libtmux:real-tmux
func TestAHandleFromAnotherRunSaysSo(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: handles\nwindows:\n  - panes:\n      - {}\n")

	// A handle shaped like this server's, issued by a process that is not it.
	foreign := fmt.Sprintf("libtmux-mcp-%d-1", os.Getpid()+1)
	result := call(ctx, t, session, "get_job", map[string]any{"jobId": foreign}, nil)
	if !result.IsError {
		t.Fatal("a handle from another process was accepted")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || !strings.Contains(text.Text, "different run") {
		t.Errorf("the refusal did not name the reason: %#v", result.Content)
	}

	// One this server could have issued and simply does not hold.
	mine := fmt.Sprintf("libtmux-mcp-%d-99999", os.Getpid())
	result = call(ctx, t, session, "get_job", map[string]any{"jobId": mine}, nil)
	if !result.IsError {
		t.Fatal("an unheld handle was accepted")
	}
	text, ok = result.Content[0].(*sdk.TextContent)
	if !ok || strings.Contains(text.Text, "different run") {
		t.Errorf("a handle this server could have issued was blamed on a restart: %#v",
			result.Content)
	}
}

// listing is the shape the three list tools answer with, as a client reads it.
type listing struct {
	Total int `json:"total"`
	Panes []struct {
		ID       string `json:"id"`
		Session  string `json:"session"`
		WindowID string `json:"windowId"`
		Status   *struct {
			Dead         bool   `json:"dead"`
			Path         string `json:"path"`
			HistoryLines int    `json:"historyLines"`
		} `json:"status"`
	} `json:"panes"`
}

//libtmux:real-tmux
func TestAFilteredListingReturnsOnlyWhatMatched(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: filtering\nwindows:\n"+
		"  - window_name: one\n    panes:\n      - {}\n      - {}\n"+
		"  - window_name: two\n    panes:\n      - {}\n")

	var everything listing
	call(ctx, t, session, "list_panes", map[string]any{}, &everything)
	if everything.Total != 3 || len(everything.Panes) != 3 {
		t.Fatalf("an unfiltered listing gave %d of %d, want 3 of 3",
			len(everything.Panes), everything.Total)
	}

	var narrowed listing
	call(ctx, t, session, "list_panes", map[string]any{
		"windowId": everything.Panes[2].WindowID,
	}, &narrowed)
	if len(narrowed.Panes) != 1 {
		t.Errorf("filtering to one window gave %d panes, want 1", len(narrowed.Panes))
	}
	if narrowed.Total != 3 {
		t.Errorf("total = %d, want the 3 the filter selected from", narrowed.Total)
	}
}

//libtmux:real-tmux
func TestAFullListingCarriesStateWithoutASecondCall(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: stateful\nwindows:\n  - panes:\n      - {}\n")

	var standard listing
	call(ctx, t, session, "list_panes", map[string]any{}, &standard)
	if len(standard.Panes) != 1 || standard.Panes[0].Status != nil {
		t.Fatalf("a standard listing carried status: %+v", standard.Panes)
	}

	var full listing
	call(ctx, t, session, "list_panes", map[string]any{"detail": "full"}, &full)
	if len(full.Panes) != 1 || full.Panes[0].Status == nil {
		t.Fatalf("a full listing carried no status: %+v", full.Panes)
	}
	if full.Panes[0].Status.Path == "" {
		t.Error("a full listing reported no working directory")
	}
	if full.Panes[0].Status.Dead {
		t.Error("a live pane was reported dead")
	}
}

//libtmux:real-tmux
func TestAnUnknownDetailIsRefused(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: unknowable\nwindows:\n  - panes:\n      - {}\n")

	result := call(ctx, t, session, "list_panes", map[string]any{"detail": "verbose"}, nil)
	if !result.IsError {
		t.Fatal("an unknown detail level was accepted")
	}
}

//libtmux:real-tmux
func TestPathUnderDoesNotMatchASiblingPrefix(t *testing.T) {
	session, _, ctx := connect(t)
	root := t.TempDir()
	shortPath := filepath.Join(root, "wo")
	longPath := filepath.Join(root, "work")
	for _, directory := range []string{shortPath, longPath} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workspace(ctx, t, session, "session_name: paths\nwindows:\n  - panes:\n      - {}\n      - {}\n")

	var everything listing
	call(ctx, t, session, "list_panes", map[string]any{}, &everything)
	// send rather than run_command: run_command runs in a subshell, so a cd
	// inside it changes that subshell's directory and never the pane's. The
	// round trip afterwards is what proves the shell has finished the cd.
	send(ctx, t, session, everything.Panes[0].ID, "cd "+shortPath)
	send(ctx, t, session, everything.Panes[1].ID, "cd "+longPath)
	run(ctx, t, session, everything.Panes[0].ID, "true")
	run(ctx, t, session, everything.Panes[1].ID, "true")

	var under listing
	call(ctx, t, session, "list_panes", map[string]any{"pathUnder": shortPath}, &under)
	if len(under.Panes) != 1 {
		t.Fatalf("filtering under %s matched %d panes, want only the one in it",
			shortPath, len(under.Panes))
	}
	if under.Panes[0].ID != everything.Panes[0].ID {
		t.Errorf("matched %s, want %s", under.Panes[0].ID, everything.Panes[0].ID)
	}
}

//libtmux:real-tmux
func TestWindowsAndSessionsNarrowToo(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: narrowing\nwindows:\n"+
		"  - window_name: alpha\n    panes:\n      - {}\n"+
		"  - window_name: beta\n    panes:\n      - {}\n")
	workspace(ctx, t, session, "session_name: elsewhere\nwindows:\n"+
		"  - window_name: gamma\n    panes:\n      - {}\n")

	type windows struct {
		Total   int `json:"total"`
		Windows []struct {
			Name    string `json:"name"`
			Session string `json:"session"`
		} `json:"windows"`
	}
	var byName windows
	call(ctx, t, session, "list_windows", map[string]any{"name": "alph"}, &byName)
	if len(byName.Windows) != 1 || byName.Windows[0].Name != "alpha" {
		t.Errorf("filtering windows by name gave %+v", byName.Windows)
	}
	if byName.Total < 3 {
		t.Errorf("total = %d, want every window it selected from", byName.Total)
	}

	var bySession windows
	call(ctx, t, session, "list_windows", map[string]any{"sessionName": "narrowing"}, &bySession)
	if len(bySession.Windows) != 2 {
		t.Errorf("filtering windows to one session gave %d, want 2", len(bySession.Windows))
	}

	type sessions struct {
		Total    int `json:"total"`
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	var named sessions
	call(ctx, t, session, "list_sessions", map[string]any{"name": "elsew"}, &named)
	if len(named.Sessions) != 1 || named.Sessions[0].Name != "elsewhere" {
		t.Errorf("filtering sessions by name gave %+v", named.Sessions)
	}
	if named.Total < 2 {
		t.Errorf("total = %d, want every session it selected from", named.Total)
	}
}

//libtmux:real-tmux
func TestServerInfoTellsWhoIsWatching(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: watched\nwindows:\n  - panes:\n      - {}\n")

	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("sessions: %v", err)
	}
	control, err := target.WithEngine(target.SubprocessEngine()).OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("open a control client: %v", err)
	}
	defer func() { _ = control.Close() }()

	var info struct {
		Clients         int `json:"clients"`
		AttachedClients []struct {
			Name        string `json:"name"`
			Session     string `json:"session"`
			ControlMode bool   `json:"controlMode"`
		} `json:"attachedClients"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if info.Clients != len(info.AttachedClients) {
		t.Errorf("counted %d clients but described %d",
			info.Clients, len(info.AttachedClients))
	}
	if len(info.AttachedClients) == 0 {
		t.Fatal("a server with a control client attached described none")
	}
	if !slices.ContainsFunc(info.AttachedClients, func(client struct {
		Name        string `json:"name"`
		Session     string `json:"session"`
		ControlMode bool   `json:"controlMode"`
	},
	) bool {
		return client.ControlMode
	}) {
		t.Errorf("a control-mode client was reported as a person watching: %+v",
			info.AttachedClients)
	}
}

//libtmux:real-tmux
func TestOneHookIsReadableWithoutTheTable(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: hooked\nwindows:\n  - panes:\n      - {}\n")
	// tmux reports every hook it knows, set or not, and show_hooks keeps only
	// the ones with a command. So the hook read back here has to be one this
	// test set, and one every supported tmux knows by that name.
	// Two, so that filtering to one has something to leave out. A single hook
	// would make the comparison below true whether or not the name was read.
	for _, event := range []string{"after-split-window", "after-new-window"} {
		if _, err := target.Cmd(ctx, "set-hook", "-g", event, "display-message hi"); err != nil {
			t.Fatalf("set a hook to read back: %v", err)
		}
	}

	type hooks struct {
		Hooks []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	var everything hooks
	call(ctx, t, session, "show_hooks", map[string]any{"scope": "server"}, &everything)
	if len(everything.Hooks) == 0 {
		t.Fatal("no hooks were reported at all")
	}

	var one hooks
	call(ctx, t, session, "show_hooks", map[string]any{
		"scope": "server", "name": "after-split-window",
	}, &one)
	if len(one.Hooks) == 0 {
		t.Fatal("naming a hook that is set reported none")
	}
	for _, reported := range one.Hooks {
		if !strings.HasPrefix(reported.Name, "after-split-window") {
			t.Errorf("naming after-split-window also reported %q", reported.Name)
		}
	}
	if len(everything.Hooks) < 2 {
		t.Fatalf("only %d hooks were set, so filtering proves nothing", len(everything.Hooks))
	}
	if len(one.Hooks) >= len(everything.Hooks) {
		t.Errorf("naming one hook returned %d of %d, so the name was not read",
			len(one.Hooks), len(everything.Hooks))
	}
}

//libtmux:real-tmux
func TestAPaneKeepsItsIdentityWhenItMoves(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: moving\nwindows:\n"+
		"  - window_name: from\n    panes:\n      - {}\n      - {}\n"+
		"  - window_name: to\n    panes:\n      - {}\n")

	var before listing
	call(ctx, t, session, "list_panes", map[string]any{}, &before)
	mover := before.Panes[1]
	var destination string
	for _, pane := range before.Panes {
		if pane.WindowID != mover.WindowID {
			destination = pane.WindowID
			break
		}
	}
	if destination == "" {
		t.Fatal("the workspace did not build a second window")
	}

	var joined struct {
		PaneID    string `json:"paneId"`
		WindowID  string `json:"windowId"`
		BrokenOut bool   `json:"brokenOut"`
	}
	result := call(ctx, t, session, "move_pane", map[string]any{
		"paneId": mover.ID, "toWindowId": destination,
	}, &joined)
	if result.IsError {
		t.Fatalf("move_pane: %#v", result.Content)
	}
	if joined.PaneID != mover.ID {
		t.Errorf("the pane came back as %s, want its own id %s", joined.PaneID, mover.ID)
	}
	if joined.WindowID != destination {
		t.Errorf("the pane landed in %s, want %s", joined.WindowID, destination)
	}

	var broken struct {
		PaneID    string `json:"paneId"`
		WindowID  string `json:"windowId"`
		BrokenOut bool   `json:"brokenOut"`
	}
	call(ctx, t, session, "move_pane", map[string]any{"paneId": mover.ID}, &broken)
	if !broken.BrokenOut {
		t.Error("breaking a pane out did not report that it did")
	}
	if broken.WindowID == destination || broken.WindowID == "" {
		t.Errorf("the broken-out pane is in %q, want a window of its own", broken.WindowID)
	}
}

//libtmux:real-tmux
func TestStyledCaptureKeepsColour(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: coloured\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	run(ctx, t, session, pane, `printf '\033[31mFAILED\033[0m\n'`)

	var plain struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &plain)
	if strings.Contains(strings.Join(plain.Lines, "\n"), "\x1b[") {
		t.Error("a plain capture carried escape sequences")
	}

	var styled struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "styles": true,
	}, &styled)
	joined := strings.Join(styled.Lines, "\n")
	if !strings.Contains(joined, "FAILED") {
		t.Fatalf("the styled capture missed the output entirely: %q", joined)
	}
	if !strings.Contains(joined, "\x1b[") {
		t.Errorf("a styled capture carried no colour: %q", joined)
	}
}

//libtmux:real-tmux
func TestRecipesAreOffAsAToolUnlessAsked(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		session, _, ctx := connect(t)
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		for _, tool := range listed.Tools {
			if tool.Name == "get_recipe" {
				t.Error("the recipe tool was advertised without being asked for")
			}
		}
	})

	t.Run("on when the operator asks", func(t *testing.T) {
		t.Setenv(tmuxmcp.RecipeToolEnvironmentVariable, "1")
		session, _, ctx := connect(t)

		var recipe struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
			Steps   string `json:"steps"`
		}
		result := call(ctx, t, session, "get_recipe", map[string]any{
			"name": "recover_pane", "argument": "%3",
		}, &recipe)
		if result.IsError {
			t.Fatalf("get_recipe: %#v", result.Content)
		}
		if !strings.Contains(recipe.Steps, "%3") {
			t.Errorf("the recipe did not mention the pane it was asked about: %q", recipe.Steps)
		}
		if !strings.Contains(recipe.Steps, "copy mode") {
			t.Errorf("the recipe was missing its own advice: %q", recipe.Steps)
		}

		// The same text a client reading prompts would get, because a client
		// that cannot read prompts must not be told something different.
		prompt, err := session.GetPrompt(ctx, &sdk.GetPromptParams{
			Name: "recover_pane", Arguments: map[string]string{"pane": "%3"},
		})
		if err != nil {
			t.Fatalf("get prompt: %v", err)
		}
		text, ok := prompt.Messages[0].Content.(*sdk.TextContent)
		if !ok {
			t.Fatalf("the prompt carried %T", prompt.Messages[0].Content)
		}
		if text.Text != recipe.Steps {
			t.Error("the recipe tool and the prompt disagree about the same job")
		}
	})

	t.Run("an unknown recipe names the ones there are", func(t *testing.T) {
		t.Setenv(tmuxmcp.RecipeToolEnvironmentVariable, "1")
		session, _, ctx := connect(t)
		result := call(ctx, t, session, "get_recipe", map[string]any{"name": "make_coffee"}, nil)
		if !result.IsError {
			t.Fatal("an unknown recipe was accepted")
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || !strings.Contains(text.Text, "diagnose_pane") {
			t.Errorf("the refusal did not say what may be asked for: %#v", result.Content)
		}
	})
}

// ranCommand is what the run_command and get_job assertions below read back.
type ranCommand struct {
	ExitStatus *int     `json:"exitStatus"`
	TimedOut   bool     `json:"timedOut"`
	Output     []string `json:"output"`
	JobID      string   `json:"jobId"`
}

//libtmux:real-tmux
func TestRunCommandKeepsATabInItsCommand(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: tabbed\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// The shell counts the bytes it received rather than printing them. A
	// terminal renders a tab by moving to the next tab stop, and whether the
	// captured row holds a tab or the spaces it painted differs by tmux
	// version; how many bytes reached the shell does not.
	var ran ranCommand
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "printf '%s' 'a\tb' | wc -c", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command failed: %#v", result.Content)
	}
	if ran.TimedOut {
		t.Fatal("a command carrying a tab wedged the pane")
	}
	if len(ran.Output) != 1 || strings.TrimSpace(ran.Output[0]) != "3" {
		t.Errorf("output = %q, want one line reading 3, so the shell was given "+
			"a, a tab, and b rather than whatever a completion inserted",
			ran.Output)
	}
}

//libtmux:real-tmux
func TestDetachedRunCommandReportsOnlyTheCommandsOutput(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: detached\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started ranCommand
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo ONLY-THIS", "detach": true,
	}, &started)
	if started.JobID == "" {
		t.Fatal("a detached run returned no handle")
	}

	// Collect only once the shell has certainly drawn its prompt into the row
	// below the output, which is the case a detached run is for. Reading the
	// closing mark's row inclusively returns that prompt as command output.
	time.Sleep(time.Second)

	var collected ranCommand
	result := call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &collected)
	if result.IsError {
		t.Fatalf("get_job failed: %#v", result.Content)
	}
	if len(collected.Output) != 1 || collected.Output[0] != "ONLY-THIS" {
		t.Errorf("output = %q, want exactly [ONLY-THIS] with no prompt", collected.Output)
	}
}

//libtmux:real-tmux
func TestRunCommandRejoinsOutputTheTerminalWrapped(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: wrapped\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	call(ctx, t, session, "resize_window", map[string]any{
		"sessionName": "wrapped", "width": 40, "height": 24,
	}, nil)

	var ran ranCommand
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "printf 'W%.0s' $(seq 1 100); echo", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command failed: %#v", result.Content)
	}
	if len(ran.Output) != 1 || len(ran.Output[0]) != 100 {
		t.Errorf("output = %d lines of %v, want one line of 100 characters",
			len(ran.Output), lineLengths(ran.Output))
	}
}

// lineLengths reports each line's length, for a failure message that says how
// the output was split rather than printing a hundred identical characters.
func lineLengths(lines []string) []int {
	lengths := make([]int, 0, len(lines))
	for _, line := range lines {
		lengths = append(lengths, len(line))
	}
	return lengths
}

//libtmux:real-tmux
func TestRunCommandRefusesAPaneWhoseShellHasExited(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: exited\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	call(ctx, t, session, "set_option", map[string]any{
		"scope": "pane", "paneId": pane, "name": "remain-on-exit", "value": "on",
	}, nil)
	call(ctx, t, session, "send_keys", map[string]any{"paneId": pane, "command": "exit"}, nil)
	waitForDeadPane(ctx, t, session, pane)

	began := time.Now()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo into-the-void", "timeoutSeconds": 20,
	}, nil)
	if !result.IsError {
		t.Fatal("running a command in a pane whose shell has exited was accepted")
	}
	if waited := time.Since(began); waited > 10*time.Second {
		t.Errorf("refusing a dead pane took %s, so it waited rather than checked", waited)
	}
}

// waitForDeadPane blocks until tmux reports the pane's process has exited,
// which happens a moment after the shell is told to leave.
func waitForDeadPane(
	ctx context.Context, t *testing.T, session *sdk.ClientSession, pane string,
) {
	t.Helper()
	for range 100 {
		var info struct {
			Dead bool `json:"dead"`
		}
		call(ctx, t, session, "get_pane_info", map[string]any{"paneId": pane}, &info)
		if info.Dead {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pane's shell never exited")
}

//libtmux:real-tmux
func TestRunCommandSaysWhyThereIsNoOutput(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: busy\nwindows:\n  - panes:\n      - shell: sleep 300\n")
	pane := firstPane(ctx, t, session)

	var ran struct {
		OutputUnavailable string `json:"outputUnavailable"`
		TimedOut          bool   `json:"timedOut"`
		Running           string `json:"running"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo never-runs", "timeoutSeconds": 3,
	}, &ran)

	if ran.OutputUnavailable == "" {
		t.Fatal("a pane that never ran the command reported no reason")
	}
	if strings.Contains(ran.OutputUnavailable, "/tmp/") ||
		strings.Contains(ran.OutputUnavailable, "no such file") {
		t.Errorf("the reason is this server's own bookkeeping, not the cause: %q",
			ran.OutputUnavailable)
	}
	if !strings.Contains(ran.OutputUnavailable, "never ran") {
		t.Errorf("the reason does not say the command never ran: %q",
			ran.OutputUnavailable)
	}
	if !strings.Contains(ran.OutputUnavailable, "respawn_pane") {
		t.Errorf("the reason names no way out: %q", ran.OutputUnavailable)
	}
}

//libtmux:real-tmux
func TestToolsKeepWorkingAfterTmuxRestarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	// A socket of its own, not the shared harness's: this test kills the tmux
	// server it is using, and the harness owns the cleanup of the one it made.
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	// A pool attaches to a session, so there has to be one before it opens.
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "before", Width: 80, Height: 24,
	}); err != nil {
		t.Fatalf("start the first session: %v", err)
	}

	// Through the pooled control connection, which is what Run serves over and
	// what connect() in these tests does not build. The in-memory path having
	// no pool is why this went unnoticed: the failure needs the connection
	// that only the real server opens.
	connected, pool := tmuxmcp.Connect(ctx, target)
	if pool == nil {
		t.Skip("no control pool on this tmux; the failure needs one")
	}
	t.Cleanup(func() { _ = pool.Close() })

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, connected).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "restart"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if result := call(ctx, t, session, "list_panes", map[string]any{}, nil); result.IsError {
		t.Fatalf("list_panes before the restart: %#v", result.Content)
	}

	if err := target.Kill(ctx); err != nil {
		t.Fatalf("kill the tmux server: %v", err)
	}
	// Bring one back on the same socket, as a person restarting tmux would.
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "after", Width: 80, Height: 24,
	}); err != nil {
		t.Fatalf("start a replacement tmux server: %v", err)
	}

	// The call that finds the connection dead reports it: a wait opens one of
	// its own and ends with the same error, so a retry here cannot tell the
	// two apart. What must not happen is every later call failing too.
	call(ctx, t, session, "list_panes", map[string]any{}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	result := call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("list_panes stayed broken after the restart: %#v", result.Content)
	}
	if len(listed.Panes) == 0 {
		t.Error("the replacement server's pane was not reported")
	}

	// And the server's own account of itself has to agree that tmux is up.
	var info struct {
		Alive bool `json:"alive"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if !info.Alive {
		t.Error("get_server_info reports the tmux server dead after it came back")
	}

	// Watching has to come back too. It holds a connection of its own and
	// retries when one drops, but it was reaching tmux through the handle it
	// was built with, and the pool inside that handle is the one the restart
	// killed. A subscription made afterwards was accepted and never delivered,
	// which is the same silence as having no watcher at all.
	pane := listed.Panes[0].ID
	uri := "tmux://panes/" + strings.TrimPrefix(pane, "%") + "/content"
	updated := make(chan string, 8)
	watchClient := sdk.NewClient(&sdk.Implementation{Name: "restart-watch"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, r *sdk.ResourceUpdatedNotificationRequest) {
			select {
			case updated <- r.Params.URI:
			default:
			}
		},
	})
	watchTransport, watchServer := sdk.NewInMemoryTransports()
	watchSession, err := mustMCPServer(t, connected).Connect(ctx, watchServer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchSession.Close() })
	watching, err := watchClient.Connect(ctx, watchTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watching.Close() })
	subscribeStarted := time.Now()
	if err := watching.Subscribe(ctx, &sdk.SubscribeParams{URI: uri}); err != nil {
		t.Fatalf("subscribe after the restart: %v", err)
	}
	// How long that took says whether the watch ever attached: it answers as
	// soon as a connection is carrying notifications, and only runs out the
	// bound when none arrives.
	subscribeTook := time.Since(subscribeStarted)
	// run_command rather than send_keys, because this has to know the pane
	// actually wrote. Keys are handed to whatever is reading the pane, and a
	// shell that has only just been restarted is not reading yet -- they were
	// swallowed, the pane stayed at its prompt, and the silence read as a
	// watch that had not attached. run_command waits for the command it typed
	// to finish, so reaching the assertion means there was something to be
	// told about.
	write := func() *sdk.CallToolResult {
		return call(ctx, t, watching, "run_command", map[string]any{
			"paneId": pane, "command": "echo watched-after-restart", "timeoutSeconds": 20,
		}, nil)
	}
	// The pool retires a connection the restart killed on the call that finds
	// it, and deliberately does not run that call again -- so the first tool
	// call through this handle reports the restart. Retrying is what a client
	// does, and what the fix above promises will then work.
	written := write()
	if written.IsError {
		written = write()
	}
	if written.IsError {
		t.Fatalf("run_command after the restart, twice: %s", resultText(written))
	}
	select {
	case <-updated:
	case <-time.After(20 * time.Second):
		var shown struct {
			Lines []string `json:"lines"`
		}
		captured := call(ctx, t, watching, "capture_pane", map[string]any{"paneId": pane}, &shown)
		t.Errorf("a pane written after the restart told no subscriber; "+
			"subscribe took %s, the pane shows error=%t lines=%q",
			subscribeTook.Round(time.Millisecond), captured.IsError, shown.Lines)
	}
}

//libtmux:real-tmux
func TestTypingIntoAPaneInAModeIsRefused(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: moded\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	send(ctx, t, session, pane, "seq 1 200")
	if result := call(ctx, t, session, "enter_copy_mode", map[string]any{
		"paneId": pane, "scrollUp": true,
	}, nil); result.IsError {
		t.Fatalf("enter_copy_mode: %#v", result.Content)
	}
	call(ctx, t, session, "load_buffer", map[string]any{
		"text": "staged", "name": "moded",
	}, nil)

	for tool, arguments := range map[string]map[string]any{
		"send_keys":       {"paneId": pane, "command": "echo NOPE"},
		"send_keys_batch": {"paneId": pane, "keys": []string{"h", "i"}},
		"paste_text":      {"paneId": pane, "text": "echo NOPE"},
		"paste_buffer":    {"paneId": pane, "name": "moded"},
		"run_command":     {"paneId": pane, "command": "echo NOPE", "timeoutSeconds": 5},
	} {
		result := call(ctx, t, session, tool, arguments, nil)
		if !result.IsError {
			t.Errorf("%s into a pane in copy mode was accepted", tool)
			continue
		}
		said := ""
		for _, content := range result.Content {
			if text, ok := content.(*sdk.TextContent); ok {
				said += text.Text
			}
		}
		// Both ways on, because a caller who entered the mode deliberately
		// wants to read rather than to undo it.
		for _, wanted := range []string{"capture_pane", "exit_copy_mode", tool} {
			if !strings.Contains(said, wanted) {
				t.Errorf("the %s refusal does not name %s: %q", tool, wanted, said)
			}
		}
		// And the connection is still there, which run_command used to cost.
		if listed := call(ctx, t, session, "list_panes", map[string]any{}, nil); listed.IsError {
			t.Fatalf("the connection did not survive refusing %s: %#v", tool, listed.Content)
		}
	}

	// The way out works, after which typing lands.
	if result := call(ctx, t, session, "exit_copy_mode", map[string]any{
		"paneId": pane,
	}, nil); result.IsError {
		t.Fatalf("exit_copy_mode: %#v", result.Content)
	}
	if result := call(ctx, t, session, "send_keys", map[string]any{
		"paneId": pane, "command": "echo LANDS",
	}, nil); result.IsError {
		t.Fatalf("send_keys after leaving copy mode: %#v", result.Content)
	}
}

// runInPane runs one command and returns its output, failing the test if the
// command did not finish.
func runInPane(
	ctx context.Context,
	t *testing.T,
	session *sdk.ClientSession,
	pane, command string,
) []string {
	t.Helper()
	var reply struct {
		Output     []string `json:"output"`
		ExitStatus *int     `json:"exitStatus"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": command, "timeoutSeconds": 10,
	}, &reply)
	if result.IsError {
		t.Fatalf("run_command %q: %#v", command, result.Content)
	}
	if reply.ExitStatus == nil {
		t.Fatalf("run_command %q recorded no exit status", command)
	}
	return reply.Output
}

//libtmux:real-tmux
func TestAnEraseAfterAScreenClearStillReturnsTheOutput(t *testing.T) {
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: summed\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// The screen clear is the precondition rather than scene-setting: without
	// it the two marks differ and the erase is visible in them.
	runInPane(ctx, t, session, pane, `printf '\033[2J\033[H'; echo KEPT`)

	output := runInPane(ctx, t, session, pane, `printf '\033[3J'; echo SURVIVES`)
	if !slices.ContainsFunc(output, func(line string) bool {
		return strings.Contains(line, "SURVIVES")
	}) {
		t.Errorf("the command printed after erasing the scrollback and the "+
			"reply holds none of it: %q", output)
	}
}

//libtmux:real-tmux
func TestRecoveredOutputHoldsNoWrapperEcho(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: echoed\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	runInPane(ctx, t, session, pane, `printf '\033[2J\033[H'; echo KEPT`)
	output := runInPane(ctx, t, session, pane, `printf '\033[3J'; echo SURVIVES`)

	// Without this the check below passes on a reply that recovered nothing,
	// which is the defect this recovery exists to fix.
	if len(output) == 0 {
		t.Fatal("nothing was recovered, so holding no echo proves nothing")
	}
	for _, line := range output {
		if strings.Contains(line, "libtmux-mcp-run") {
			t.Errorf("the reply holds this server's own sourcing line: %q", output)
		}
	}
}

//libtmux:real-tmux
func TestEveryDeliveryRefusesAPaneWithNoProcess(t *testing.T) {
	session, _, ctx := connect(t)
	pane := deadPaneHeldOpen(ctx, t, session, "corpse")

	if result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "x", "name": "corpse",
	}, nil); result.IsError {
		t.Fatalf("load_buffer: %#v", result.Content)
	}

	for _, delivery := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"send_keys", map[string]any{"paneId": pane, "command": "echo LEAK"}},
		{"send_keys_batch", map[string]any{"paneId": pane, "keys": []string{"a"}}},
		{"paste_text", map[string]any{"paneId": pane, "text": "LEAK"}},
		{"paste_buffer", map[string]any{"paneId": pane, "name": "corpse"}},
		{"run_command", map[string]any{"paneId": pane, "command": "echo LEAK", "timeoutSeconds": 5}},
	} {
		t.Run(delivery.tool, func(t *testing.T) {
			result := call(ctx, t, session, delivery.tool, delivery.arguments, nil)
			if !result.IsError {
				t.Fatalf("%s reported success against a pane with no process", delivery.tool)
			}
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			if !strings.Contains(said, "respawn_pane") {
				t.Errorf("the refusal does not name the way out: %q", said)
			}
			if strings.Contains(said, "exited 1") {
				t.Errorf("the refusal is tmux's exit code rather than a reason: %q", said)
			}
		})
	}
}

//libtmux:real-tmux
func TestADeadPaneIsStillReadableAndRestartable(t *testing.T) {
	session, _, ctx := connect(t)
	pane := deadPaneHeldOpen(ctx, t, session, "readable")

	for _, allowed := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"enter_copy_mode", map[string]any{"paneId": pane}},
		{"exit_copy_mode", map[string]any{"paneId": pane}},
		{"clear_pane", map[string]any{"paneId": pane}},
		{"respawn_pane", map[string]any{"paneId": pane, "command": "sleep 60"}},
	} {
		if result := call(ctx, t, session, allowed.tool, allowed.arguments, nil); result.IsError {
			t.Errorf("%s was refused on a dead pane: %#v", allowed.tool, result.Content)
		}
	}
}

// deadPaneHeldOpen makes a pane whose process has exited and which tmux keeps.
func deadPaneHeldOpen(
	ctx context.Context,
	t *testing.T,
	session *sdk.ClientSession,
	name string,
) string {
	t.Helper()
	var made struct {
		PaneID   string `json:"paneId"`
		WindowID string `json:"windowId"`
	}
	if result := call(ctx, t, session, "create_session", map[string]any{
		"name": name, "command": "sleep 300",
	}, nil); result.IsError {
		t.Fatalf("create_session: %#v", result.Content)
	}
	// A second window, so the pane going does not take the session with it
	// while the assertions are still running.
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": name, "name": "held", "command": "sleep 300",
	}, &made); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}
	if result := call(ctx, t, session, "set_option", map[string]any{
		"name": "remain-on-exit", "value": "on",
		"scope": "window", "windowId": made.WindowID,
	}, nil); result.IsError {
		t.Fatalf("set_option: %#v", result.Content)
	}
	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": made.PaneID, "command": "true", "kill": true,
	}, nil); result.IsError {
		t.Fatalf("respawn_pane: %#v", result.Content)
	}
	// The pane stays; what this waits for is the process ending.
	deadline := time.Now().Add(10 * time.Second)
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
			"sessionName": name, "detail": "full",
		}, &listed)
		for _, pane := range listed.Panes {
			if pane.ID == made.PaneID && pane.Status.Dead {
				return made.PaneID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reported its command as finished", made.PaneID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

//libtmux:real-tmux
func TestAWaitThatIgnoredWhatWasAlreadyThereSaysSo(t *testing.T) {
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: already\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	runInPane(ctx, t, session, pane, "echo ALREADY-THERE")

	var reply struct {
		Outcome        string `json:"outcome"`
		MatchedAtEntry bool   `json:"matchedAtEntry"`
		EntryNote      string `json:"entryNote"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "patterns": []string{"ALREADY-THERE"},
		"sinceEntry": true, "timeoutSeconds": 2,
	}, &reply)

	// The precondition, not the finding: without both of these the note has
	// nothing to explain and this asserts against the wrong reply.
	if reply.Outcome != "timeout" || !reply.MatchedAtEntry {
		t.Fatalf("want a timeout with a match at entry, got outcome %q matchedAtEntry %t",
			reply.Outcome, reply.MatchedAtEntry)
	}
	if !strings.Contains(reply.EntryNote, "sinceEntry") {
		t.Errorf("the reply does not say why it waited out its deadline: %q",
			reply.EntryNote)
	}

	// And it stays off the replies that are not puzzling.
	var plain struct {
		Outcome   string `json:"outcome"`
		EntryNote string `json:"entryNote"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "patterns": []string{"ALREADY-THERE"}, "timeoutSeconds": 2,
	}, &plain)
	if plain.Outcome != "matched" {
		t.Fatalf("want a match without sinceEntry, got %q", plain.Outcome)
	}
	if plain.EntryNote != "" {
		t.Errorf("a reply that matched carries a note anyway: %q", plain.EntryNote)
	}
}

//libtmux:real-tmux
func TestClearingTheScrollbackSaysTheOutputIsGone(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: cleared\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	run := func(command string) (output []string, missed bool, status int) {
		t.Helper()
		var reply struct {
			Output      []string `json:"output"`
			LinesMissed bool     `json:"linesMissed"`
			ExitStatus  *int     `json:"exitStatus"`
		}
		result := call(ctx, t, session, "run_command", map[string]any{
			"paneId": pane, "command": command, "timeoutSeconds": 10,
		}, &reply)
		if result.IsError {
			t.Fatalf("run_command %q: %#v", command, result.Content)
		}
		if reply.ExitStatus == nil {
			t.Fatalf("run_command %q recorded no exit status", command)
		}
		return reply.Output, reply.LinesMissed, *reply.ExitStatus
	}

	// Erasing the screen alone keeps the marks valid, so the output arrives
	// and nothing was lost.
	output, missed, status := run(`printf '\033[2J\033[H'; echo KEPT`)
	if status != 0 || missed {
		t.Errorf("erasing the screen: status %d, linesMissed %t", status, missed)
	}
	if !slices.ContainsFunc(output, func(line string) bool {
		return strings.Contains(line, "KEPT")
	}) {
		t.Errorf("erasing the screen lost the output: %q", output)
	}

	// The pad is the precondition, not decoration: an erase with no scrollback
	// to erase renumbers nothing, and the step above left none. Without it this
	// asserts against a grid that never moved, which is how it came to pass on
	// one tmux and fail on another.
	pad := "for i in $(seq 1 40); do echo pad$i; done"
	run(pad)

	// Erasing the scrollback renumbers the grid under the marks. What the
	// command printed afterwards is still on the screen and still has to come
	// back, and the reply has to say that anything before it is gone.
	output, missed, status = run(`printf '\033[3J'; echo SURVIVES`)
	if status != 0 {
		t.Errorf("erasing the scrollback: status %d, want the command's own", status)
	}
	if !slices.ContainsFunc(output, func(line string) bool {
		return strings.Contains(line, "SURVIVES")
	}) {
		t.Errorf("what the command printed after erasing the scrollback was "+
			"dropped: %q", output)
	}
	if !missed {
		t.Error("the scrollback was erased and the reply does not report the loss")
	}

	// clear is the ordinary way to reach it, and the reason this matters:
	// "clear; make test" reported success and returned silence. Returning the
	// output is the contract; whether the erase also renumbers far enough to be
	// reported is not, because tmux releases differ on it and the command's own
	// output was never lost either way.
	run(pad)
	output, _, status = run("clear; echo AFTERCLEAR")
	if status != 0 {
		t.Errorf("clear: status %d, want the command's own", status)
	}
	if !slices.ContainsFunc(output, func(line string) bool {
		return strings.Contains(line, "AFTERCLEAR")
	}) {
		t.Errorf("clear lost the output that followed it: %q", output)
	}

	// A command that printed nothing is still distinguishable from one whose
	// output went missing.
	output, missed, _ = run("true")
	if len(output) != 0 || missed {
		t.Errorf("a command that printed nothing reported %q, linesMissed %t",
			output, missed)
	}
}

//libtmux:real-tmux
func TestJoinWrappedReadsAPaneAsTmuxDoes(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: joined\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	call(ctx, t, session, "resize_pane", map[string]any{"paneId": pane, "width": 40}, nil)
	// Comfortably more than two rows of a forty-column pane, so joined and
	// unjoined cannot come out the same.
	send(ctx, t, session, pane, "printf 'X%.0s' {1..95}; echo")
	time.Sleep(time.Second)

	read := func(join bool) []string {
		t.Helper()
		var reply struct {
			Lines []string `json:"lines"`
		}
		if result := call(ctx, t, session, "capture_pane", map[string]any{
			"paneId": pane, "joinWrapped": join,
		}, &reply); result.IsError {
			t.Fatalf("capture_pane joinWrapped=%t: %#v", join, result.Content)
		}
		return reply.Lines
	}
	joined, unjoined := read(true), read(false)
	if slices.Equal(joined, unjoined) {
		t.Fatal("joining changed nothing, so this pane cannot tell the two apart")
	}

	panes, err := target.Panes(ctx)
	if err != nil {
		t.Fatalf("Panes() = %v", err)
	}
	var found tmux.Pane
	for _, each := range panes {
		if each.ID().String() == pane {
			found = each
		}
	}
	if found.ID().String() != pane {
		t.Fatalf("pane %s is not in the listing", pane)
	}
	theirs, err := found.Capture(ctx, tmux.CapturePaneRequest{JoinWrapped: true})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	trim := func(rows []string) []string {
		kept := make([]string, 0, len(rows))
		for _, row := range rows {
			if row = strings.TrimRight(row, " "); row != "" {
				kept = append(kept, row)
			}
		}
		return kept
	}
	if mine, tmuxs := trim(joined), trim(theirs); !slices.Equal(mine, tmuxs) {
		t.Errorf("joinWrapped diverged from capture-pane -J\n  ours: %q\n  tmux: %q",
			mine, tmuxs)
	}
}
