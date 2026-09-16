package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// layoutRoundTripTmuxBinary opts this test into a tmux 3.8 or later binary.
// The MCP server's command connection is a control client, and tmux hands a
// control client the classic layout grammar unless it requested the JSON
// shape - which only 3.8+ has - so this test is a no-op below that floor.
// Unset, it falls back to whatever "tmux" resolves to on PATH and skips
// there too if that is not new enough.
const layoutRoundTripTmuxBinary = "LIBTMUX_GO_TEST_TMUX38_BIN"

// The MCP server reads window_layout through a control connection.
// Before the control connection requested JSON layouts (tmux.controlDialect,
// "new-layouts"), tmux handed it the classic grammar, whose pane-list order
// does not survive a save-and-restore round trip on 3.8+ the way a JSON
// layout's pane ids do. Save, scramble, restore, and require every pane back
// at its exact original position - not merely the same shape.
//
//libtmux:real-tmux
func TestSelectLayoutRoundTripIsExactOnTmux38(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	binary := os.Getenv(layoutRoundTripTmuxBinary)
	request := tmux.NewSessionRequest{Name: "work"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Binary: binary, InitialSession: &request,
	})
	version, err := target.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.8")
	if err != nil {
		t.Fatal(err)
	}
	if !version.AtLeast(minimum) {
		t.Skipf(
			"tmux %s predates the JSON layout shape (3.8); set %s to a newer tmux",
			version, layoutRoundTripTmuxBinary,
		)
	}

	window, err := target.Windows(ctx)
	if err != nil || len(window) != 1 {
		t.Fatalf("Windows() = (%v, %v), want one window", window, err)
	}
	pane, ok, err := window[0].ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%v, %v, %v), want one pane", pane, ok, err)
	}
	for range 3 {
		pane, err = pane.Split(ctx, tmux.SplitPaneRequest{})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := window[0].SelectLayout(ctx, tmux.SelectLayoutRequest{
		Layout: "main-vertical-mirrored",
	}); err != nil {
		t.Fatal(err)
	}

	client := inputTestClient(ctx, t, target, nil)
	windowID := window[0].ID().String()

	baseline := callInputTool(ctx, t, client, "get_window_info",
		map[string]any{"window_id": windowID})
	if baseline.IsError {
		t.Fatalf("get_window_info refusal = %q", callToolResultText(baseline))
	}
	saved := structuredMap(t, baseline)["layout"].(string)
	if !strings.HasPrefix(strings.TrimSpace(saved), "{") {
		t.Fatalf(
			"get_window_info layout = %q, want the JSON shape from a control "+
				"connection that requested new-layouts",
			saved,
		)
	}
	before := paneGeometryByID(t, baseline)

	scrambled := callInputTool(ctx, t, client, "select_layout",
		map[string]any{"window_id": windowID, "layout": "tiled"})
	if scrambled.IsError {
		t.Fatalf("select_layout tiled refusal = %q", callToolResultText(scrambled))
	}

	restored := callInputTool(ctx, t, client, "select_layout",
		map[string]any{"window_id": windowID, "layout": saved})
	if restored.IsError {
		t.Fatalf("select_layout restore refusal = %q", callToolResultText(restored))
	}

	after := callInputTool(ctx, t, client, "get_window_info",
		map[string]any{"window_id": windowID})
	if after.IsError {
		t.Fatalf("get_window_info refusal = %q", callToolResultText(after))
	}
	got := paneGeometryByID(t, after)

	if len(before) != 4 || len(got) != len(before) {
		t.Fatalf("panes = %d before, %d after restore, want 4 both times", len(before), len(got))
	}
	for id, want := range before {
		if got[id] != want {
			t.Errorf("pane %s geometry after restore = %+v, want its original %+v", id, got[id], want)
		}
	}
}

func paneGeometryByID(t *testing.T, result *sdk.CallToolResult) map[string]paneGeometry {
	t.Helper()
	panes, ok := structuredMap(t, result)["panes"].([]any)
	if !ok {
		t.Fatalf("get_window_info panes = %#v, want a list", structuredMap(t, result)["panes"])
	}
	geometry := make(map[string]paneGeometry, len(panes))
	for _, entry := range panes {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("pane entry = %#v, want an object", entry)
		}
		id, _ := row["id"].(string)
		cell, _ := row["geometry"].(map[string]any)
		geometry[id] = paneGeometry{
			Left:   int(asFloat(cell["left"])),
			Top:    int(asFloat(cell["top"])),
			Width:  int(asFloat(cell["width"])),
			Height: int(asFloat(cell["height"])),
		}
	}
	return geometry
}

func asFloat(value any) float64 {
	f, _ := value.(float64)
	return f
}
