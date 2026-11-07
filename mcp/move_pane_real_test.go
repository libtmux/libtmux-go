package mcp

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// panesByWindow maps window id to pane ids as list_panes reports them.
func panesByWindow(call func(string, map[string]any) map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, raw := range call("list_panes", nil)["panes"].([]any) {
		pane := raw.(map[string]any)
		window := pane["windowId"].(string)
		out[window] = append(out[window], pane["id"].(string))
	}
	return out
}

//libtmux:real-tmux
func TestMovePaneAndBreakPaneRearrangeWindows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	request := tmux.NewSessionRequest{Name: "work"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{InitialSession: &request})
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v), want one", sessions, err)
	}
	windows, err := target.Windows(ctx)
	if err != nil || len(windows) != 1 {
		t.Fatalf("Windows() = (%v, %v), want one", windows, err)
	}
	first := windows[0]
	second := tmuxtest.NewWindow(ctx, t, sessions[0], tmux.NewWindowRequest{})
	firstPane, err := first.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	split, err := firstPane.Split(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatal(err)
	}

	client := inputTestClient(ctx, t, target, nil)
	call := func(name string, arguments map[string]any) map[string]any {
		t.Helper()
		result := callInputTool(ctx, t, client, name, arguments)
		if result.IsError {
			t.Fatalf("%s refusal = %q", name, callToolResultText(result))
		}
		return structuredMap(t, result)
	}
	refusal := func(name string, arguments map[string]any) string {
		t.Helper()
		result := callInputTool(ctx, t, client, name, arguments)
		if !result.IsError {
			t.Fatalf("%s succeeded, want a refusal", name)
		}
		return callToolResultText(result)
	}

	// Moving a pane into the window it is already in is refused.
	if text := refusal("move_pane", map[string]any{
		"pane_id": split.ID().String(), "target_window_id": first.ID().String(),
	}); !strings.Contains(text, "already in the target window") {
		t.Fatalf("same-window refusal = %q", text)
	}

	moved := call("move_pane", map[string]any{
		"pane_id": split.ID().String(), "target_window_id": second.ID().String(),
	})
	if moved["windowId"] != second.ID().String() {
		t.Fatalf("move_pane windowId = %v, want %s", moved["windowId"], second.ID())
	}
	got := panesByWindow(call)
	if !slices.Contains(got[second.ID().String()], split.ID().String()) {
		t.Fatalf("panes by window = %v, want %s under %s", got, split.ID(), second.ID())
	}
	if !slices.Equal(got[first.ID().String()], []string{firstPane.ID().String()}) {
		t.Fatalf("panes by window = %v, want only %s left in %s", got, firstPane.ID(), first.ID())
	}

	// Window 1 now holds a sole pane: moving it would destroy the window.
	if text := refusal("move_pane", map[string]any{
		"pane_id": firstPane.ID().String(), "target_window_id": second.ID().String(),
	}); !strings.Contains(text, "only one in its window") {
		t.Fatalf("sole-pane refusal = %q", text)
	}
	if after := panesByWindow(call); len(after[first.ID().String()]) != 1 {
		t.Fatalf("refused move changed the layout: %v", after)
	}

	// break_pane gives the moved pane a window of its own.
	broken := call("break_pane", map[string]any{"pane_id": split.ID().String(), "name": "broken"})
	brokenWindow := broken["windowId"].(string)
	after := panesByWindow(call)
	if brokenWindow == second.ID().String() || !slices.Equal(after[brokenWindow], []string{split.ID().String()}) {
		t.Fatalf("break_pane windowId = %s, panes by window = %v", brokenWindow, after)
	}

	// Breaking out a window's only pane destroys nothing: tmux keeps the
	// window and its id, so the call succeeds and the layout is unchanged.
	again := call("break_pane", map[string]any{"pane_id": split.ID().String(), "name": "alone"})
	if again["windowId"] != brokenWindow {
		t.Fatalf("sole-pane break_pane windowId = %v, want the window it was already in, %s", again["windowId"], brokenWindow)
	}
	if final := panesByWindow(call); !reflect.DeepEqual(final, after) {
		t.Fatalf("sole-pane break_pane changed the layout: %v, want %v", final, after)
	}
}
