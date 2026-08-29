package mcp_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

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
	said := resultText(result)
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

//libtmux:real-tmux
func TestRespawningALivePaneNamesTheWayOut(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: respawn\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	result := call(ctx, t, session, "respawn_pane", map[string]any{"paneId": pane}, nil)
	if !result.IsError {
		t.Fatal("respawning a live pane without kill was accepted")
	}
	said := resultText(result)
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
