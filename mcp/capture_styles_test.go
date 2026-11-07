package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestCaptureStylesIsOptIn checks that styles is opt-in on capture_pane and snapshot_pane. Off, the
// lines are plain; on, the capture-pane -e escapes come through.
//
//libtmux:real-tmux
func TestCaptureStylesIsOptIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{
		Name:    "styles",
		Command: `printf '\033[31mred\033[0m\n'; sleep 600`,
	}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	windows, err := target.Windows(ctx)
	if err != nil || len(windows) != 1 {
		t.Fatalf("Windows() = (%v, %v), want one window", windows, err)
	}
	pane, err := windows[0].ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paneID := pane.ID().String()

	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	waited, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "wait_for_text",
		Arguments: map[string]any{
			"pane_id": paneID, "patterns": []string{"red"}, "timeout": 20,
		},
	})
	var wait struct {
		Matched string `json:"matched"`
	}
	if err == nil {
		err = decodeStructured(waited, &wait)
	}
	// The text may already be on screen when the wait attaches; that is a match too.
	if err != nil || wait.Matched != "red" {
		t.Fatalf("wait_for_text = (%+v, %v), want found; %#v", wait, err, waited.StructuredContent)
	}

	for _, tool := range []string{"capture_pane", "snapshot_pane"} {
		read := func(args map[string]any) string {
			t.Helper()
			args["pane_id"] = paneID
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			var out struct {
				Lines []string `json:"lines"`
			}
			if err := decodeStructured(result, &out); err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			return strings.Join(out.Lines, "\n")
		}
		plain := read(map[string]any{})
		if !strings.Contains(plain, "red") || strings.Contains(plain, "\x1b") {
			t.Errorf("%s default = %q, want plain red and no ESC", tool, plain)
		}
		styled := read(map[string]any{"styles": true})
		if !strings.Contains(styled, "\x1b[31m") || !strings.Contains(styled, "red") {
			t.Errorf("%s styles = %q, want ESC[31m around red", tool, styled)
		}
	}
}
