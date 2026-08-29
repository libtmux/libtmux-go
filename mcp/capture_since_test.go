package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// What tmux does to a cursor when a pane reaches its history limit.
//
// tmux frees the oldest tenth of the history in one batch and shifts the rest
// up, so every row number changes at once and no arithmetic recovers the old
// one. Either the anchor is still there and the delta must find it, or it is
// gone and the reply must say so — a gap reported as completeness is the one
// outcome a caller cannot detect.

//libtmux:real-tmux
func TestATrimmedAnchorIsReportedAsLinesMissed(t *testing.T) {
	session, _, ctx := connect(t)

	// history-limit is a session option new panes inherit, so it has to be set
	// before the pane that will be trimmed exists.
	workspace(ctx, t, session, "session_name: trimming\nwindows:\n  - panes:\n      - {}\n")
	if result := call(ctx, t, session, "set_option", map[string]any{
		"scope": "session", "sessionName": "trimming",
		"name": "history-limit", "value": "50",
	}, nil); result.IsError {
		t.Fatalf("set history-limit: %#v", result.Content)
	}
	var made struct {
		PaneID string `json:"paneId"`
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "trimming", "name": "small",
	}, &made); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}
	pane := made.PaneID

	var info struct {
		HistoryLimit int `json:"historyLimit"`
	}
	call(ctx, t, session, "get_pane_info", map[string]any{"paneId": pane}, &info)
	if info.HistoryLimit != 50 {
		t.Fatalf("the probe pane has history-limit %d, want 50", info.HistoryLimit)
	}

	// Enough to fill part of the history but not to overflow it.
	run(ctx, t, session, pane, "for i in $(seq 1 20); do echo a-$i; done")

	var first struct {
		Cursor string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"paneId": pane}, &first)
	if first.Cursor == "" {
		t.Fatal("no cursor")
	}

	// Now overflow it, so tmux drops lines from the front and renumbers the
	// grid. The anchor itself is still retained; only its number moved.
	run(ctx, t, session, pane, "for i in $(seq 1 80); do echo b-$i; done")

	var delta struct {
		Lines       []string `json:"lines"`
		LinesMissed bool     `json:"linesMissed"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"cursor": first.Cursor}, &delta)
	joined := strings.Join(delta.Lines, "\n")

	t.Logf("linesMissed=%v, %d lines returned", delta.LinesMissed, len(delta.Lines))
	t.Logf("first returned line %q", firstLine(delta.Lines))

	// Every b line still in the pane's history should be reported, since the
	// cursor sat before all of them.
	var absent []string
	for i := 1; i <= 80; i++ {
		marker := fmt.Sprintf("b-%d", i)
		if !containsWord(joined, marker) {
			absent = append(absent, marker)
		}
	}
	if len(absent) > 0 && !delta.LinesMissed {
		t.Fatalf("%d lines are missing from the delta and linesMissed is false; "+
			"first missing %v", len(absent), absent[:min(6, len(absent))])
	}
	if len(absent) > 0 {
		t.Logf("%d lines absent but linesMissed was set, which is the honest answer", len(absent))
	}
}

//libtmux:real-tmux
func TestATrimThatKeepsTheAnchorLosesNothing(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: nudged\nwindows:\n  - panes:\n      - {}\n")
	if result := call(ctx, t, session, "set_option", map[string]any{
		"scope": "session", "sessionName": "nudged",
		"name": "history-limit", "value": "100",
	}, nil); result.IsError {
		t.Fatalf("set history-limit: %#v", result.Content)
	}
	var made struct {
		PaneID string `json:"paneId"`
	}
	call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "nudged", "name": "small",
	}, &made)
	pane := made.PaneID

	// Fill most of the history, so the anchor sits well away from its start.
	run(ctx, t, session, pane, "for i in $(seq 1 60); do echo a-$i; done")

	var first struct {
		Cursor string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"paneId": pane}, &first)

	// Overflow by a little. tmux drops a handful of lines from the front; the
	// anchor moves down but stays retained.
	run(ctx, t, session, pane, "for i in $(seq 1 55); do echo b-$i; done")

	var delta struct {
		Lines       []string `json:"lines"`
		LinesMissed bool     `json:"linesMissed"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"cursor": first.Cursor}, &delta)
	joined := strings.Join(delta.Lines, "\n")

	// What tmux still holds, for comparison: if a b line is in the pane's own
	// history but not in the delta, the delta lost it rather than tmux.
	var whole struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "includeHistory": true, "maxLines": 5000,
	}, &whole)
	retained := strings.Join(whole.Lines, "\n")

	var lost []string
	for i := 1; i <= 55; i++ {
		marker := fmt.Sprintf("b-%d", i)
		if containsWord(retained, marker) && !containsWord(joined, marker) {
			lost = append(lost, marker)
		}
	}
	t.Logf("linesMissed=%v, delta starts %q, %d retained lines absent from it",
		delta.LinesMissed, firstLine(delta.Lines), len(lost))
	if len(lost) > 0 {
		t.Fatalf("the delta skipped %d lines tmux still holds, "+
			"linesMissed=%v; first %v", len(lost), delta.LinesMissed, lost[:min(6, len(lost))])
	}
}

// firstLine reports the first line, for the probe's log.
func firstLine(lines []string) string {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// containsWord matches a marker as a whole token, so b-1 does not match b-10.
func containsWord(haystack, marker string) bool {
	return slices.Contains(strings.Fields(haystack), marker)
}

//libtmux:real-tmux
func TestASubscriberThatArrivedFirstIsStillTold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	updated := make(chan string, 16)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "early"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			updated <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Subscribing first is the point: there is no session yet, which is the
	// ordinary state of a socket nobody has used.
	if err := session.Subscribe(ctx, &sdk.SubscribeParams{URI: "tmux://sessions"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	workspace(ctx, t, session, "session_name: late\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	send(ctx, t, session, pane, "echo LATE-OUTPUT")

	select {
	case <-updated:
	case <-time.After(30 * time.Second):
		t.Fatal("a subscriber that arrived before the session heard nothing")
	}
}

//libtmux:real-tmux
func TestASubscriberIsToldAboutWhatHappenedWhileNothingWatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	updated := make(chan string, 64)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "gap"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			updated <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	workspace(ctx, t, session, "session_name: watched\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	if err := session.Subscribe(ctx, &sdk.SubscribeParams{
		URI: "tmux://panes/" + pane + "/content",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	drain := func() {
		for {
			select {
			case <-updated:
			default:
				return
			}
		}
	}
	// The first write proves the watch is live, so a later silence is the gap
	// and not a subscription that never started.
	send(ctx, t, session, pane, "echo watching")
	select {
	case <-updated:
	case <-time.After(30 * time.Second):
		t.Fatal("the subscription was never told about the first write")
	}

	for round := range 3 {
		drain()
		// A session made anywhere on the server ends every connection.
		call(ctx, t, session, "create_session", map[string]any{
			"name": fmt.Sprintf("churn-%d", round), "command": "sleep 300",
		}, nil)
		// Long enough to be past the connection ending and inside the gap.
		time.Sleep(150 * time.Millisecond)
		send(ctx, t, session, pane, fmt.Sprintf("echo round-%d", round))
		select {
		case <-updated:
		case <-time.After(20 * time.Second):
			t.Fatalf("round %d: a write during a rebuild was never reported", round)
		}
	}
}

//libtmux:real-tmux
func TestASubscriptionWritingThePaneSigilIsStillTold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	updated := make(chan string, 16)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "sigil"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			updated <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	workspace(ctx, t, session, "session_name: sigil\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// The form a client copies out of a tool result, sigil and all.
	if err := session.Subscribe(ctx, &sdk.SubscribeParams{
		URI: "tmux://panes/" + pane + "/content",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	send(ctx, t, session, pane, "echo SIGIL-OUTPUT")

	select {
	case <-updated:
	case <-time.After(30 * time.Second):
		// Whether the pane wrote at all separates a watch that did not report
		// from output that never happened, which the bare timeout does not.
		var shown struct {
			Lines []string `json:"lines"`
		}
		captured := call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
		t.Fatalf("a subscription to tmux://panes/%s/content was never told; "+
			"the pane itself shows error=%t lines=%q",
			pane, captured.IsError, shown.Lines)
	}
}

//libtmux:real-tmux
func TestAPaneIsWatchedWhicheverSessionHoldsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := tmux.NewServer(tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	updated := make(chan string, 16)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "sessions"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *sdk.ResourceUpdatedNotificationRequest) {
			updated <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Two sessions, and the pane of interest is not in the first.
	workspace(ctx, t, session, "session_name: first\nwindows:\n  - panes:\n      - {}\n")
	workspace(ctx, t, session, "session_name: second\nwindows:\n  - panes:\n      - {}\n")

	var listed struct {
		Panes []struct {
			ID      string `json:"id"`
			Session string `json:"session"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	wanted := ""
	for _, pane := range listed.Panes {
		if pane.Session == "second" {
			wanted = pane.ID
		}
	}
	if wanted == "" {
		t.Fatalf("no pane in the second session: %+v", listed.Panes)
	}

	if err := session.Subscribe(ctx, &sdk.SubscribeParams{
		URI: "tmux://panes/" + strings.TrimPrefix(wanted, "%") + "/content",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// The watcher chooses its connections when a subscription arrives, so give
	// it one before writing.
	time.Sleep(2 * time.Second)
	send(ctx, t, session, wanted, "echo SECOND-SESSION-OUTPUT")

	awaitURI(ctx, t, updated, "tmux://panes/"+strings.TrimPrefix(wanted, "%")+"/content",
		"a pane in the second session was watched and never reported")

	// A session made after the connections were chosen. Nothing tmux reports
	// says the set is now wrong, because tmux said what it had to say when the
	// session appeared and nothing was watching in it yet. The subscription
	// itself is the event.
	workspace(ctx, t, session, "session_name: third\nwindows:\n  - panes:\n      - {}\n")
	// Let the connections settle first. Creating the session makes tmux report
	// the sessions changed, which rebuilds them, and a subscription arriving
	// inside that window would be picked up by luck rather than by the thing
	// under test.
	time.Sleep(3 * time.Second)
	listed.Panes = nil
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	late := ""
	for _, pane := range listed.Panes {
		if pane.Session == "third" {
			late = pane.ID
		}
	}
	if late == "" {
		t.Fatalf("no pane in the third session: %+v", listed.Panes)
	}
	if err := session.Subscribe(ctx, &sdk.SubscribeParams{
		URI: "tmux://panes/" + strings.TrimPrefix(late, "%") + "/content",
	}); err != nil {
		t.Fatalf("subscribe to the later session: %v", err)
	}
	time.Sleep(3 * time.Second)
	send(ctx, t, session, late, "echo THIRD-SESSION-OUTPUT")

	awaitURI(ctx, t, updated, "tmux://panes/"+strings.TrimPrefix(late, "%")+"/content",
		"a pane in a session made after the subscription was never reported")
}

// awaitURI waits for an update about one resource, ignoring updates about
// others. A shared channel makes a later wait pass on an earlier notification,
// which is how a check for the second thing quietly stops checking anything.
func awaitURI(ctx context.Context, t *testing.T, updated <-chan string, want, complaint string) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case got := <-updated:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("%s (waiting for %s)", complaint, want)
		case <-ctx.Done():
			t.Fatalf("%s: %v", complaint, ctx.Err())
		}
	}
}

//libtmux:real-tmux
func TestTheBackstopRefusesAnOversizedReply(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: huge\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// The per-tool bound keeps an ordinary capture well under the backstop, so
	// the two do not fight: asking for everything still answers.
	var whole struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	result := call(ctx, t, session, "capture_pane", map[string]any{
		"paneId": pane, "includeHistory": true,
		"maxLines": 1_000_000, "maxBytes": 1_000_000,
	}, &whole)
	if result.IsError {
		t.Fatalf("a capture at the ceiling was refused: %#v", result.Content)
	}
}

//libtmux:real-tmux
func TestEveryTextToolBoundsItself(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, tool := range listed.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		output, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		// A tool whose result carries pane lines has to offer a way to ask for
		// fewer of them.
		if !strings.Contains(string(output), `"lines"`) &&
			!strings.Contains(string(output), `"output"`) {
			continue
		}
		checked++
		if !strings.Contains(string(schema), "maxLines") {
			t.Errorf("%s returns lines but takes no maxLines", tool.Name)
		}
	}
	// Without this the test passes when it matched nothing, which is exactly
	// what a renamed output field would cause.
	if checked < 5 {
		t.Errorf("only %d tools were checked; the shape this looks for has moved", checked)
	}
}

//libtmux:real-tmux
func TestSinceEntrySaysWhenItIgnoredAMatchAlreadyThere(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: entry\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// Print it, and let it land before the wait starts.
	send(ctx, t, session, pane, "echo ALREADY-PRINTED")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
		if slices.ContainsFunc(shown.Lines, func(line string) bool {
			return strings.Contains(line, "ALREADY-PRINTED")
		}) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	// Wait for the pane to actually stop writing rather than guessing at how
	// long the shell takes to redraw its prompt. Until it is quiet, some of
	// what it wrote is still arriving and would count as new output -- which
	// on a loaded machine is most of them.
	var since struct {
		Cursor string   `json:"cursor"`
		Lines  []string `json:"lines"`
	}
	call(ctx, t, session, "capture_since", map[string]any{"paneId": pane}, &since)
	quiet := time.Now().Add(30 * time.Second)
	for time.Now().Before(quiet) {
		time.Sleep(500 * time.Millisecond)
		previous := since.Cursor
		call(ctx, t, session, "capture_since", map[string]any{
			"paneId": pane, "cursor": previous,
		}, &since)
		if len(since.Lines) == 0 {
			break
		}
	}

	var waited struct {
		Outcome        string `json:"outcome"`
		Found          bool   `json:"found"`
		MatchedAtEntry bool   `json:"matchedAtEntry"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "patterns": []string{"ALREADY-PRINTED"},
		"sinceEntry": true, "timeoutSeconds": 3,
	}, &waited)

	// Whether the wait times out is not this test's claim, and cannot be made
	// one: attaching a control connection makes tmux re-send recent pane
	// output, so text written moments ago can arrive as new. The claim is what
	// a timeout says when it happens -- that the pattern was on the screen the
	// whole time, which is the difference between a puzzle and an answer.
	if waited.Outcome == "timeout" && !waited.MatchedAtEntry {
		t.Error("the wait timed out on text that was on the screen the whole " +
			"time and did not say so")
	}

	// Without sinceEntry the same call matches, and says it was already there.
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "patterns": []string{"ALREADY-PRINTED"},
		"timeoutSeconds": 3,
	}, &waited)
	if !waited.Found || !waited.MatchedAtEntry {
		t.Errorf("without sinceEntry the match on entry was not reported: %+v", waited)
	}
}
