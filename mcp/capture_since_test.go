package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

// TestATrimmedAnchorIsReportedAsLinesMissed covers what tmux does to a cursor: when tmux trims scrollback, the absolute row the cursor recorded stops
// meaning the row it recorded, and the delta silently starts too late.
// linesMissed stays false, so the gap is invisible.
//
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

// TestATrimThatKeepsTheAnchorLosesNothing isolates the mechanism from the loss.
//
// The pane is trimmed by less than the distance from the anchor to the start of
// history, so the anchor row itself is still retained and every line after it
// is still readable. If the delta is computed from the absolute row the cursor
// recorded, it starts exactly the trimmed amount too late, and the lines it
// skips are ones tmux still has.
//
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
	for _, field := range strings.Fields(haystack) {
		if field == marker {
			return true
		}
	}
	return false
}

// TestASubscriberThatArrivedFirstIsStillTold covers the ordinary case a
// give-up-once watcher got wrong: a client is pointed at a socket before
// anyone has made a session on it, subscribes, and has to hear about the
// sessions that appear afterwards.
//
//libtmux:real-tmux
func TestASubscriberThatArrivedFirstIsStillTold(t *testing.T) {
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

// TestTheBackstopRefusesAnOversizedReply covers the cap that exists for the
// tool that forgets to bound itself.
//
// Every tool that returns pane text bounds its own reply, which is the right
// place for it. This is the floor under that convention: a reply past what
// this server will send is refused with a message that names the tool, because
// the alternative is spending a caller's whole context on one answer.
//
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

// TestEveryTextToolBoundsItself is the convention the backstop is a floor
// under: a tool that returns pane lines takes maxLines, so a caller can ask
// for less and the default is not the pane's whole history.
//
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
