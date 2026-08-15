package mcp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
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

// TestCaptureKeepsTheEndAndSaysWhatItDropped covers the bound every tool that
// returns pane text shares. A pane holding more than the caller asked for must
// answer with the most recent of it, because the end is what a question about
// a terminal is about, and must say that it did.
//
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

// TestCaptureSinceReturnsOnlyWhatIsNew covers the cursor: the second reading of
// a pane must cost what changed rather than what is there.
//
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

// TestACursorForAnotherPaneIsRefused covers the mistake that would be silent:
// reading on from the wrong pane's cursor would report that pane's history as
// this one's, and a client has no way to notice.
//
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
		"paneId": panes[0], "cursor": "capture-since-v1:not-base64!!",
	}, nil)
	if !damaged.IsError {
		t.Error("a damaged cursor was accepted rather than refused")
	}

	foreign := call(ctx, t, session, "capture_since", map[string]any{
		"paneId": panes[0], "cursor": "something-else-entirely",
	}, nil)
	if !foreign.IsError {
		t.Error("a cursor this server never issued was accepted")
	}
}

// TestClearedHistoryIsReportedAsLinesMissed covers the honest failure. tmux
// discards scrollback, and a client whose record of a pane now has a gap in it
// has to be told rather than handed the current screen as though it followed
// on from the last reading.
//
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

// TestAStopPatternEndsAWaitBeforeItsDeadline covers what makes waiting cheap:
// a run that has already failed should return when it fails, not when the
// caller's patience runs out.
//
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

// TestSearchReportsTheLinesThatMatched covers the second call this tool exists
// to avoid: a client told only which pane matched has to capture it to find
// out what it found.
//
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

// TestPasteTextDeliversWhatSendKeysWouldEat covers the difference between the
// two, which is the one an agent gets wrong: tmux reads key names out of
// send_keys, so text containing one is not delivered as itself.
//
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

// TestKillingTheLastPaneSaysTheWindowWentWithIt covers what tmux does without
// saying: a client that killed one pane of what it believed were several has
// just ended the window it was working in.
//
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

// TestABatchIsCheckedWholeBeforeAnythingRuns covers the difference between a
// client learning it asked for the wrong thing and learning it after three
// panes were gone.
//
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

// TestOptionsAreReadAndWrittenAtTheScopeAsked covers the settings that explain
// a pane's behaviour when the pane itself does not.
//
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

// TestTheServerSaysWhichSocketItAddresses covers the first question a client
// handed a tmux server it did not choose has to ask.
//
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

// TestASubscriberIsToldWhenAPaneChanges covers the push side of observation: a
// client that subscribed should not have to ask whether anything happened.
//
//libtmux:real-tmux
func TestASubscriberIsToldWhenAPaneChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.WithStrictErrors().Kill(killCtx)
	})

	updated := make(chan string, 16)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
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

// TestRunCommandReportsWhatTheCommandPrinted covers the half a caller would
// otherwise have to go looking for: an exit status with no output leaves it
// capturing the pane and guessing where its command began.
//
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
		t.Fatalf("send %q: %#v", command, result.Content)
	}
}

// TestTheEnvironmentIsWhatANewPaneWouldGet covers the distinction the tool's
// description makes and a caller will otherwise trip over.
//
//libtmux:real-tmux
func TestTheEnvironmentIsWhatANewPaneWouldGet(t *testing.T) {
	session, _, ctx := connect(t)
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
}

// TestBuffersAreThisServersOwn covers the boundary: tmux's buffer list is where
// a person's copies land, so a tool that read any buffer by name would be a
// tool for reading what someone copied.
//
//libtmux:real-tmux
func TestBuffersAreThisServersOwn(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: buffers\nwindows:\n  - panes:\n      - {}\n")

	// A buffer this server did not create, standing in for a person's copy.
	if err := target.WithStrictErrors().SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "a private copy",
		Name: stringPointer("someones-own"),
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

	// The foreign buffer is not reachable: its name is prefixed on the way in,
	// so it addresses a buffer this server would have made rather than that one.
	foreign := call(ctx, t, session, "show_buffer", map[string]any{"name": "someones-own"}, nil)
	if !foreign.IsError {
		t.Error("a buffer this server did not create was readable")
	}

	if result := call(ctx, t, session, "delete_buffer", map[string]any{
		"name": staged.Name,
	}, nil); result.IsError {
		t.Errorf("delete_buffer: %#v", result.Content)
	}
}

// stringPointer is the address of a value, which several tmux requests take to
// tell an absent field from an empty one.
func stringPointer(value string) *string { return &value }

// TestTheInstructionsNameTheToolsTheyRecommend guards the text a client reads
// before it calls anything. A recommendation for a tool that is not advertised
// sends a model looking for something it cannot find.
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

// TestPaneTextNeverExceedsTheCeiling guards the promise the bounds make. A
// caller asking for everything is the case the ceiling exists for.
//
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

// TestDisplayMessageAnswersInTmuxsOwnLanguage covers the escape hatch: tmux
// knows hundreds of things no tool here has an answer for.
//
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

// TestSendKeysBatchDrivesAProgramThatReadsKeys covers what send_keys cannot
// express: a sequence with no Enter appended.
//
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

// TestASafetyLevelWithholdsTheToolsItSays guards the guarantee the level
// makes: a withheld tool is not advertised, and a batch cannot reach around
// that to call it.
//
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

// TestAnEmptyPaneIsStillAReadableResource covers the shape MCP requires. A
// text resource has to carry a text field; the SDK omits an empty one, and a
// client then rejects contents that are neither text nor binary. A pane
// showing nothing is ordinary, so it has to survive the round trip.
//
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

// TestTheServerFindsItsOwnPaneWithoutTheEnvironment covers self-detection the
// way a client actually arranges it.
//
// tmux tells a process in a pane which pane it is through TMUX and TMUX_PANE,
// and an MCP client starts its servers with the environment it chooses — most
// choose a curated one that carries neither. A server that only read the
// environment would be inside a pane and unable to tell, and would report its
// own terminal as somebody else's pane.
//
//libtmux:real-tmux
func TestTheServerFindsItsOwnPaneWithoutTheEnvironment(t *testing.T) {
	if socket := os.Getenv(insidePaneSocket); socket != "" {
		reportOwnPaneFromInside(t, socket, os.Getenv(insidePaneReport))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket}).WithStrictErrors()
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

	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
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
