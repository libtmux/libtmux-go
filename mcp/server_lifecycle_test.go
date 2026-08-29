package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
