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

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
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

//libtmux:real-tmux
func TestAnAbsentServerIsNotAnEmptyOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "absent-server"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	for _, listing := range []string{"list_panes", "list_windows", "list_sessions"} {
		var reported struct {
			Total      int    `json:"total"`
			ServerNote string `json:"serverNote"`
		}
		result := call(ctx, t, session, listing, map[string]any{}, &reported)
		if result.IsError {
			t.Fatalf("%s: %s", listing, resultText(result))
		}
		if reported.Total != 0 {
			t.Errorf("%s found %d on a socket with no server", listing, reported.Total)
		}
		if !strings.Contains(reported.ServerNote, "no tmux server is running") {
			t.Errorf("%s said nothing about the absent server: %q",
				listing, reported.ServerNote)
		}
	}

	// A read has no empty list to hand back, so it fails -- but with the same
	// sentence, rather than the tmux command that failed and the socket file
	// that is not there.
	for _, uri := range []string{
		"tmux://panes/0", "tmux://panes/0/content", "tmux://windows/0",
		"tmux://windows/0/panes", "tmux://sessions/anything",
	} {
		_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
		if err == nil {
			t.Errorf("%s was read on a socket with no server", uri)
			continue
		}
		if !strings.Contains(err.Error(), "no tmux server is running") {
			t.Errorf("%s says %q, want it to name the absent server", uri, err)
		}
	}

	// The same question asked directly, and the field a client iterates.
	var info struct {
		Alive           bool  `json:"alive"`
		AttachedClients []any `json:"attachedClients"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if info.Alive {
		t.Error("get_server_info called an absent server alive")
	}
	if info.AttachedClients == nil {
		t.Error("attachedClients came back null, not an empty array")
	}
}

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
			"channel": "ready for work", "timeoutSeconds": 30,
		}, &out)
		waited <- out.Signalled
	}()

	// The waiter is released by a signal, not by its own deadline.
	deadline := time.After(20 * time.Second)
	for {
		call(ctx, t, session, "signal_channel", map[string]any{
			"channel": "ready for work",
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
	for _, channel := range []string{"", "-L"} {
		if result := call(ctx, t, session, "signal_channel", map[string]any{
			"channel": channel,
		}, nil); !result.IsError {
			t.Errorf("channel %q was accepted", channel)
		}
	}
}

//libtmux:real-tmux
func TestBatchCanCreateTheFirstSessionThenReadIt(t *testing.T) {
	session, _, ctx := connect(t)
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var batch struct {
		Completed int `json:"completed"`
		Results   []struct {
			Tool   string         `json:"tool"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		} `json:"results"`
	}
	result := call(callCtx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "create_session", "arguments": map[string]any{"name": "first-in-batch"}},
			{"tool": "list_sessions"},
		},
	}, &batch)
	if result.IsError {
		t.Fatalf("first-session batch failed: %s", resultText(result))
	}
	if batch.Completed != 2 || len(batch.Results) != 2 {
		t.Fatalf("batch = %#v, want two completed calls", batch)
	}
	for _, item := range batch.Results {
		if item.Error != "" {
			t.Fatalf("%s failed after first creation: %s", item.Tool, item.Error)
		}
	}
	sessions, ok := batch.Results[1].Result["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("list_sessions result = %#v, want the created session", batch.Results[1].Result)
	}
}

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

//libtmux:real-tmux
func TestABatchToldToContinueRunsTheCallsAfterAFailure(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: continuing\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// A failure between two calls that would each succeed on their own.
	calls := []map[string]any{
		{"tool": "get_pane_info", "arguments": map[string]any{"paneId": pane}},
		{"tool": "get_pane_info", "arguments": map[string]any{"paneId": "%9000"}},
		{"tool": "list_panes", "arguments": map[string]any{}},
	}
	type report struct {
		Results []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
		Completed int      `json:"completed"`
		Failed    int      `json:"failed"`
		Skipped   []string `json:"skipped"`
	}

	var stopped report
	call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls}, &stopped)
	if stopped.Completed != 1 || len(stopped.Results) != 2 {
		t.Errorf("stopping ran %d and reported %d results, want 1 and 2",
			stopped.Completed, len(stopped.Results))
	}
	if !slices.Equal(stopped.Skipped, []string{"list_panes"}) {
		t.Errorf("stopping skipped %q, want the call after the failure", stopped.Skipped)
	}

	var continued report
	call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls, "onError": "continue"}, &continued)
	if continued.Completed != 2 {
		t.Errorf("continuing ran %d of the two that work", continued.Completed)
	}
	if continued.Failed != 1 {
		t.Errorf("continuing reported %d failures, want 1", continued.Failed)
	}
	if len(continued.Results) != 3 {
		t.Fatalf("continuing reported %d results, want one per call", len(continued.Results))
	}
	if len(continued.Skipped) != 0 {
		t.Errorf("continuing skipped %q, and it skips nothing", continued.Skipped)
	}
	if continued.Results[1].Error == "" {
		t.Error("the failing call is not reported as failed")
	}
	if continued.Results[2].Tool != "list_panes" || continued.Results[2].Error != "" {
		t.Errorf("the call after the failure did not run: %+v", continued.Results[2])
	}

	// An unknown value is refused rather than read as the default.
	if result := call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls[:1], "onError": "carry-on"}, nil); !result.IsError {
		t.Error("a batch took an onError it does not have")
	}
}

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

	// The closed sets are part of the same schema, so a batch has to hold a
	// value to them too, and by the schema rather than by whatever the handler
	// happens to tolerate.
	for _, arguments := range []map[string]any{
		{"paneId": panes[0], "direction": "sideways"},
		// Accepted by the handler, which folds case, and outside the set the
		// schema publishes.
		{"paneId": panes[0], "direction": "RIGHT"},
	} {
		batch.Completed, batch.Results = 0, nil
		call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
			"calls": []map[string]any{{"tool": "split_window", "arguments": arguments}},
		}, &batch)
		if batch.Completed != 0 {
			t.Errorf("a batch took direction %q", arguments["direction"])
		}
		if len(batch.Results) == 0 || !strings.Contains(batch.Results[0].Error, "enum") {
			t.Errorf("direction %q refused for the wrong reason: %+v",
				arguments["direction"], batch.Results)
		}
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a rejected direction still split a pane: %d then %d", before, after)
	}
}

//libtmux:real-tmux
func TestACommandThatPrintedBlankLinesSaysSo(t *testing.T) {
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: blanks\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	for _, printed := range []struct {
		command string
		want    []string
	}{
		{"echo", []string{""}},
		{`printf '\n\n'`, []string{"", ""}},
		{`printf '\n\nx\n'`, []string{"", "", "x"}},
		// Still distinguishable from a command that printed nothing at all.
		{"true", nil},
	} {
		t.Run(printed.command, func(t *testing.T) {
			var reported struct {
				Output     []string `json:"output"`
				ExitStatus *int     `json:"exitStatus"`
			}
			call(ctx, t, session, "run_command", map[string]any{
				"paneId": pane, "command": printed.command, "timeoutSeconds": 15,
			}, &reported)
			if reported.ExitStatus == nil || *reported.ExitStatus != 0 {
				t.Fatalf("exit status = %v, want 0", reported.ExitStatus)
			}
			if !slices.Equal(reported.Output, printed.want) {
				t.Errorf("output = %q, want %q", reported.Output, printed.want)
			}
		})
	}
}

//libtmux:real-tmux
func TestATimedOutRunLeavesNothingOfItsOwnInThePane(t *testing.T) {
	// A plain POSIX shell, which is what the wrapper is written for, and a
	// prompt that is one character rather than whoever's shell the suite
	// inherited.
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: outlived\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var timedOut struct {
		TimedOut bool `json:"timedOut"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "sleep 3", "timeoutSeconds": 1,
	}, &timedOut)
	if !timedOut.TimedOut {
		t.Fatal("the command did not outlast its wait")
	}

	// Wait for the pane to go quiet, which is after the command has finished
	// and the wrapper behind it has run its own lines. Waiting for the command
	// to leave instead returns before those lines are written.
	var quiet struct {
		Outcome string `json:"outcome"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "idleSeconds": 2, "timeoutSeconds": 30,
	}, &quiet)
	if quiet.Outcome != "idle" {
		t.Fatalf("the pane settled as %q, not idle", quiet.Outcome)
	}

	var shown struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
	whole := strings.Join(shown.Lines, "\n")
	for _, leaked := range []string{"cannot create", "Directory nonexistent", "status"} {
		if strings.Contains(whole, leaked) {
			t.Errorf("the pane holds %q after a timed-out run:\n%s", leaked, whole)
		}
	}

	// And the next run reads its own output rather than the leftovers.
	var next struct {
		Output []string `json:"output"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo after-the-timeout",
	}, &next)
	if len(next.Output) != 1 || next.Output[0] != "after-the-timeout" {
		t.Errorf("the next run read %q", next.Output)
	}
}

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

//libtmux:real-tmux
func TestATimeoutIsLoggedToAClientThatAsked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{
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
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
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
			// Pane scope was the one that read nothing back: tmux walks
			// pane, window, session, server from the pane it is given, so a
			// caller who meant the window got the active pane's answer.
			"a window at pane scope", "show_option",
			map[string]any{"name": "history-limit", "scope": "pane", "windowId": "@0"},
			true,
		},
		{
			"a window at pane scope, setting one", "set_option",
			map[string]any{
				"name": "history-limit", "value": "5000",
				"scope": "pane", "windowId": "@0",
			},
			true,
		},
		{
			"a window at the default scope, which is pane", "show_hooks",
			map[string]any{"windowId": "@0"},
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

//libtmux:real-tmux
func TestServerInfoDoesNotInventAHealthyEmptyServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	unreachable := mustTmuxServer(t, executableFixtureOptions(t, fixtureUnavailable, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	}))
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, unreachable).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
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
		PaneID string `json:"paneId"`
	}
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "reaped", "name": "doomed", "command": "sleep 300",
	}, &doomedWindow); result.IsError {
		t.Fatalf("create_window: %s", resultText(result))
	}
	doomed := doomedWindow.PaneID

	// The reply may already say the pane went: reading it back is the last
	// thing a respawn does, and a command that exits can beat that read.
	var restarted struct {
		PaneID string `json:"paneId"`
		Gone   bool   `json:"gone"`
	}
	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": doomed, "command": "true", "kill": true,
	}, &restarted); result.IsError {
		t.Fatalf("respawn_pane on %s: %s", doomed, resultText(result))
	}
	if restarted.PaneID != doomed {
		t.Errorf("respawn_pane answered for %q, want %q", restarted.PaneID, doomed)
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

//libtmux:real-tmux
func TestAnIdThatNamesNothingSaysWhichListingFindsOne(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: named\nwindows:\n  - panes:\n      - {}\n")

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One absent id per argument, with the listing that would have found one.
	absent := map[string]struct{ value, lister string }{
		"paneId":   {"%9000", "list_panes"},
		"windowId": {"@9000", "list_windows"},
	}
	// A batch takes a list of calls rather than an id, and reports what the
	// call inside it said; the tools it dispatches are covered on their own.
	exempt := map[string]bool{
		"call_readonly_tools_batch": true, "call_mutating_tools_batch": true,
		"call_destructive_tools_batch": true,
	}
	// A listing reads an id as a criterion rather than a target: nothing
	// matching it is an empty list and not a missing object, and the reply's
	// total says what the criteria selected from.
	criteria := map[string]bool{"list_panes": true, "list_windows": true}
	asked := 0
	for _, tool := range listed.Tools {
		if exempt[tool.Name] {
			continue
		}
		for argument, want := range absent {
			if _, takes := schemaOf(t, tool).Properties[argument]; !takes {
				continue
			}
			asked++
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: tool.Name, Arguments: map[string]any{argument: want.value},
			})
			var said string
			switch {
			case err != nil:
				said = err.Error()
			case result.IsError:
				said = resultText(result)
			default:
				if !criteria[tool.Name] {
					t.Errorf("%s accepted %s %s", tool.Name, argument, want.value)
				}
				continue
			}
			// A tool may refuse for a reason of its own before it looks the id
			// up -- a missing second argument, a guard. What it must not do is
			// repeat tmux's own words for an id that is not there.
			if strings.Contains(said, "snapshot object not found") {
				t.Errorf("%s answers a missing %s with tmux's message: %s",
					tool.Name, argument, said)
			}
			if strings.Contains(said, "no pane") || strings.Contains(said, "no window") {
				if !strings.Contains(said, want.lister) {
					t.Errorf("%s says %q without naming %s",
						tool.Name, said, want.lister)
				}
			}
		}
	}
	if asked < 20 {
		t.Errorf("only %d tools take an id, which is fewer than this server has", asked)
	}
}

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
