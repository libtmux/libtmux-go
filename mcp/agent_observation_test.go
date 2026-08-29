package mcp_test

import (
	"context"
	"fmt"
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
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
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
