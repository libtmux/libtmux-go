package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A marker in a command a caller just typed is on screen as the shell's echo
// of it, before the command runs. Reporting that as a match tells an agent the
// command finished when it has not started.
//
//libtmux:real-tmux
func TestWaitForTextSeparatesAnEchoFromOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "echo-trap"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	panes := snapshot.Panes()
	if len(panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(panes))
	}
	pane := panes[0]
	tmuxtest.WaitForShellReady(ctx, t, pane)

	// Typed without Enter, so the echo is all that reaches the screen and the
	// wait below cannot race the command running.
	const marker = "GO-ECHO-MARKER"
	typed := "echo " + marker
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &typed, Literal: true, SkipEnter: true,
	}); err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForScreen(ctx, t, pane, "the typed line echoed", func(screen []string) bool {
		return slices.ContainsFunc(screen, func(line string) bool {
			return strings.Contains(line, marker)
		})
	})

	var waited struct {
		Outcome        string `json:"outcome"`
		Found          bool   `json:"found"`
		MatchedAtEntry bool   `json:"matchedAtEntry"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"pane_id":  pane.ID().String(),
		"patterns": []any{marker},
		"timeout":  5,
	}, &waited)
	if waited.Outcome != "alreadyOnScreen" || waited.Found || !waited.MatchedAtEntry {
		t.Fatalf("wait_for_text = %#v, want alreadyOnScreen with found false", waited)
	}
}

// TestWaitForTextNeverMatchesUnsubmittedInputTypedAfterAttaching pins the
// live race an entry-baseline read alone cannot catch: the
// wait attaches before anything is typed, then this server's own send_keys
// types a marker without Enter. The kernel's echo of those literal keys is
// genuinely new output on the notification stream after the wait began
// watching, and must still not end the wait - the command has not run.
//
//libtmux:real-tmux
func TestWaitForTextNeverMatchesUnsubmittedInputTypedAfterAttaching(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "pending-race"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	panes := snapshot.Panes()
	if len(panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(panes))
	}
	pane := panes[0]
	tmuxtest.WaitForShellReady(ctx, t, pane)

	const marker = "LIBTMUX-PENDING-MARKER"
	waitDone := make(chan *sdk.CallToolResult, 1)
	go func() {
		result, _ := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "wait_for_text",
			Arguments: map[string]any{
				"pane_id": pane.ID().String(), "patterns": []any{marker}, "timeout": 6,
			},
		})
		waitDone <- result
	}()

	// Give the observation time to attach before typing, so the kernel echo
	// arrives as genuinely new output on a wait already watching. This first
	// tool call also lazily binds this session's own command connection, so
	// two clients - not one - confirm the observation itself is attached.
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, error) {
		raw, cmdErr := target.Cmd(ctx, "list-clients")
		if cmdErr != nil {
			return false, cmdErr
		}
		return len(raw.Stdout) >= 2, nil
	}); err != nil {
		t.Fatalf("observation client never attached: %v", err)
	}

	var sent struct {
		Sent int `json:"sent"`
	}
	call(ctx, t, session, "send_keys", map[string]any{
		"pane_id": pane.ID().String(), "keys": []any{"echo " + marker}, "literal": true,
	}, &sent)
	tmuxtest.WaitForScreen(ctx, t, pane, "the typed line echoed", func(screen []string) bool {
		return slices.ContainsFunc(screen, func(line string) bool {
			return strings.Contains(line, marker)
		})
	})

	select {
	case result := <-waitDone:
		t.Fatalf("wait_for_text matched its own unsubmitted input: %s", surfaceResultText(result))
	case <-time.After(300 * time.Millisecond):
	}

	call(ctx, t, session, "send_keys", map[string]any{
		"pane_id": pane.ID().String(), "keys": []any{"Enter"},
	}, &sent)

	var result *sdk.CallToolResult
	select {
	case result = <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("wait_for_text did not return after the marker actually ran")
	}
	if result == nil || result.IsError {
		t.Fatalf("wait_for_text after Enter = %v", result)
	}
	var output struct {
		Outcome string   `json:"outcome"`
		Found   bool     `json:"found"`
		Lines   []string `json:"lines"`
	}
	if err := decodeStructured(result, &output); err != nil {
		t.Fatal(err)
	}
	if output.Outcome != "matched" || !output.Found {
		t.Fatalf("wait_for_text after Enter = %+v, want matched/found", output)
	}
	// The command's own output line - the bare marker, not the echoed command
	// text - has to be there: the wait did not merely give up and report
	// whatever was in the buffer once anything new arrived.
	if !slices.ContainsFunc(output.Lines, func(line string) bool {
		return strings.TrimSpace(line) == marker
	}) {
		t.Fatalf("wait_for_text after Enter lines = %#v, want the command's own output line %q",
			output.Lines, marker)
	}
}

// TestWaitForTextAlreadyOnScreenDistinguishesPendingFromRealOutput pins that
// repeating a plain wait_for_text after Enter must not read
// identically to the pre-Enter call. pendingInputOnly is the discriminator -
// true while the marker is only this server's own unsubmitted input, false
// once it has actually run - and entryNote explains either state in words.
//
//libtmux:real-tmux
func TestWaitForTextAlreadyOnScreenDistinguishesPendingFromRealOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "pending-flag"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	panes := snapshot.Panes()
	if len(panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(panes))
	}
	pane := panes[0]
	tmuxtest.WaitForShellReady(ctx, t, pane)

	const marker = "LIBTMUX-FLAG-MARKER"
	var sent struct {
		Sent int `json:"sent"`
	}
	call(ctx, t, session, "send_keys", map[string]any{
		"pane_id": pane.ID().String(), "keys": []any{"echo " + marker}, "literal": true,
	}, &sent)
	tmuxtest.WaitForScreen(ctx, t, pane, "the typed line echoed", func(screen []string) bool {
		return slices.ContainsFunc(screen, func(line string) bool {
			return strings.Contains(line, marker)
		})
	})

	var before struct {
		Outcome          string `json:"outcome"`
		Found            bool   `json:"found"`
		PendingInputOnly bool   `json:"pendingInputOnly"`
		EntryNote        string `json:"entryNote"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"pane_id": pane.ID().String(), "patterns": []any{marker}, "timeout": 5,
	}, &before)
	if before.Outcome != "alreadyOnScreen" || before.Found || !before.PendingInputOnly || before.EntryNote == "" {
		t.Fatalf("before Enter = %+v, want alreadyOnScreen with pendingInputOnly set and a note", before)
	}

	call(ctx, t, session, "send_keys", map[string]any{
		"pane_id": pane.ID().String(), "keys": []any{"Enter"},
	}, &sent)
	tmuxtest.WaitForLine(ctx, t, pane, marker)

	var after struct {
		Outcome          string `json:"outcome"`
		Found            bool   `json:"found"`
		PendingInputOnly bool   `json:"pendingInputOnly"`
		EntryNote        string `json:"entryNote"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"pane_id": pane.ID().String(), "patterns": []any{marker}, "timeout": 5,
	}, &after)
	if after.Outcome != "alreadyOnScreen" || after.Found || after.PendingInputOnly || after.EntryNote == "" {
		t.Fatalf("after Enter = %+v, want alreadyOnScreen without pendingInputOnly, still noted", after)
	}
}

// TestWaitForTextResumesFromARealCursor pins that cursor is decoded and
// used as a resume position, the same as capture_since's own cursor,
// instead of its string value being discarded down to a boolean "ignore
// the baseline" trigger. A cursor taken before a command ran finds that
// command's output immediately, rather than timing out waiting for it to
// happen again.
//
//libtmux:real-tmux
func TestWaitForTextResumesFromARealCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "cursor-resume"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	panes := snapshot.Panes()
	if len(panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(panes))
	}
	pane := panes[0]
	tmuxtest.WaitForShellReady(ctx, t, pane)

	var initial struct {
		Cursor string `json:"cursor"`
	}
	call(ctx, t, session, "capture_since", map[string]any{
		"pane_id": pane.ID().String(),
	}, &initial)
	if initial.Cursor == "" {
		t.Fatal("capture_since returned no cursor")
	}

	const marker = "LIBTMUX-CURSOR-MARKER"
	command := "echo " + marker
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command, Literal: true}); err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForLine(ctx, t, pane, marker)

	var waited struct {
		Outcome string   `json:"outcome"`
		Found   bool     `json:"found"`
		Lines   []string `json:"lines"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"pane_id": pane.ID().String(), "patterns": []any{marker},
		"timeout": 5, "cursor": initial.Cursor,
	}, &waited)
	if waited.Outcome != "matched" || !waited.Found {
		t.Fatalf("wait_for_text with a real cursor = %+v, want matched/found immediately", waited)
	}
}
