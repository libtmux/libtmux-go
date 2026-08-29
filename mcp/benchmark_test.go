package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These benchmarks measure decoded and validated in-memory MCP calls against a
// real tmux. Compare revisions on one machine: tmux dominates wall time, while
// allocation counts are stable. Stdio framing is excluded.

// benchServer connects one client to one server against a real tmux.
func benchServer(b *testing.B) (*sdk.ClientSession, context.Context) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	b.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, b, tmuxtest.ServerOptions{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(b, target).Connect(ctx, serverTransport, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "bench"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = session.Close() })

	if _, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "build_workspace",
		Arguments: map[string]any{
			"document": "session_name: bench\nwindows:\n  - panes:\n      - {}\n      - {}\n",
		},
	}); err != nil {
		b.Fatal(err)
	}
	return session, ctx
}

// BenchmarkToolCall reports what one call of each shape costs: a listing, a
// listing that reads every pane's state, one pane's contents, and a format
// expansion, which is the cheapest thing tmux can be asked.
func BenchmarkToolCall(b *testing.B) {
	session, ctx := benchServer(b)
	for _, call := range []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{"list_sessions", "list_sessions", map[string]any{}},
		{"list_panes", "list_panes", map[string]any{}},
		{"list_panes_full", "list_panes", map[string]any{"detail": "full"}},
		{"get_server_info", "get_server_info", map[string]any{}},
		{"capture_pane", "capture_pane", map[string]any{}},
		{"display_message", "display_message", map[string]any{"format": "#{pane_id}"}},
	} {
		b.Run(call.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result, err := session.CallTool(ctx, &sdk.CallToolParams{
					Name: call.tool, Arguments: call.arguments,
				})
				if err != nil {
					b.Fatal(err)
				}
				if result.IsError {
					b.Fatalf("%s: %#v", call.tool, result.Content)
				}
			}
		})
	}
}

// BenchmarkBatchAgainstSerial compares elapsed time and allocations for one
// batch call with the same three calls made separately.
func BenchmarkBatchAgainstSerial(b *testing.B) {
	session, ctx := benchServer(b)
	calls := []map[string]any{
		{"tool": "list_sessions", "arguments": map[string]any{}},
		{"tool": "list_windows", "arguments": map[string]any{}},
		{"tool": "list_panes", "arguments": map[string]any{}},
	}

	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name:      "call_readonly_tools_batch",
				Arguments: map[string]any{"calls": calls},
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, call := range calls {
				if _, err := session.CallTool(ctx, &sdk.CallToolParams{
					Name:      call["tool"].(string),
					Arguments: call["arguments"],
				}); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// BenchmarkCaptureSinceAgainstCapturePane reports server cost and reply bytes.
// capture_since adds fingerprinting and a cursor, so it wins only when avoiding
// repeated large captures.
func BenchmarkCaptureSinceAgainstCapturePane(b *testing.B) {
	session, ctx := benchServer(b)
	var pane string
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "list_panes", Arguments: map[string]any{},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := unmarshalStructured(result, &listed); err != nil || len(listed.Panes) == 0 {
		b.Skip("no pane to read")
	}
	pane = listed.Panes[0].ID

	// A pane with something in it. The cursor is fixed overhead of roughly half
	// a kilobyte, so on a blank pane capture_since returns more than a whole
	// capture does; the comparison is only honest once the screen it saves
	// re-sending is bigger than the cursor that saves it.
	if _, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"paneId": pane, "command": "seq 1 400", "timeoutSeconds": 30,
		},
	}); err != nil {
		b.Fatal(err)
	}

	b.Run("capture_pane", func(b *testing.B) {
		b.ReportAllocs()
		replies := 0
		for b.Loop() {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "capture_pane", Arguments: map[string]any{"paneId": pane},
			})
			if err != nil {
				b.Fatal(err)
			}
			replies += replyBytes(result)
		}
		b.ReportMetric(float64(replies)/float64(b.N), "bytes/reply")
	})

	b.Run("capture_since", func(b *testing.B) {
		var cursor string
		replies := 0
		b.ReportAllocs()
		for b.Loop() {
			arguments := map[string]any{"paneId": pane}
			if cursor != "" {
				arguments["cursor"] = cursor
			}
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "capture_since", Arguments: arguments,
			})
			if err != nil {
				b.Fatal(err)
			}
			var since struct {
				Cursor string `json:"cursor"`
			}
			if err := unmarshalStructured(result, &since); err == nil {
				cursor = since.Cursor
			}
			replies += replyBytes(result)
		}
		b.ReportMetric(float64(replies)/float64(b.N), "bytes/reply")
	})
}

// replyBytes is how much of a caller's context one reply would spend.
func replyBytes(result *sdk.CallToolResult) int {
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return 0
	}
	return len(encoded)
}

// unmarshalStructured decodes a tool's structured result.
func unmarshalStructured(result *sdk.CallToolResult, into any) error {
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}
