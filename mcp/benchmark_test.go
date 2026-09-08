package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These benchmarks measure decoded and validated in-memory MCP calls against a
// real tmux. Compare revisions on one machine: tmux dominates wall time, while
// allocation counts are stable. Stdio framing is excluded.

// benchServer connects one client to one server against a real tmux and builds
// the same one-window, two-pane fixture through the public MCP surface.
func benchServer(b *testing.B) (*sdk.ClientSession, context.Context, string) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	b.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, b, tmuxtest.ServerOptions{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance, err := tmuxmcp.NewServer(target)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = instance.Close() })
	serverSession, err := instance.Connect(
		ctx, tmuxmcp.AssumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "bench", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = session.Close() })

	callBenchmarkTool(ctx, b, session, "create_session", map[string]any{
		"session_name": "bench",
	})
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	if err := unmarshalStructured(
		callBenchmarkTool(ctx, b, session, "list_panes", nil), &listed,
	); err != nil {
		b.Fatal(err)
	}
	if len(listed.Panes) == 0 {
		b.Fatal("create_session returned no pane")
	}
	paneID := listed.Panes[0].ID
	callBenchmarkTool(ctx, b, session, "split_window", map[string]any{
		"pane_id": paneID,
	})
	return session, ctx, paneID
}

// BenchmarkToolCall reports what one call of each shape costs: listings, one
// pane's metadata and contents, server metadata, and validated tmux variables.
func BenchmarkToolCall(b *testing.B) {
	session, ctx, paneID := benchServer(b)
	for _, call := range []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{"list_sessions", "list_sessions", nil},
		{"list_panes", "list_panes", nil},
		{"get_pane_info", "get_pane_info", map[string]any{"pane_id": paneID}},
		{"get_server_info", "get_server_info", nil},
		{"capture_pane", "capture_pane", map[string]any{"pane_id": paneID}},
		{"get_tmux_variables", "get_tmux_variables", map[string]any{
			"names": []string{"pane_id"},
		}},
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
	session, ctx, _ := benchServer(b)
	operations := []map[string]any{
		{"tool": "list_sessions", "arguments": map[string]any{}},
		{"tool": "list_windows", "arguments": map[string]any{}},
		{"tool": "list_panes", "arguments": map[string]any{}},
	}

	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "call_read_tools_batch",
				Arguments: map[string]any{
					"operations": operations,
				},
			})
			if err != nil {
				b.Fatal(err)
			}
			if result.IsError {
				b.Fatalf("call_read_tools_batch: %#v", result.Content)
			}
		}
	})

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, operation := range operations {
				result, err := session.CallTool(ctx, &sdk.CallToolParams{
					Name:      operation["tool"].(string),
					Arguments: operation["arguments"],
				})
				if err != nil {
					b.Fatal(err)
				}
				if result.IsError {
					b.Fatalf("%s: %#v", operation["tool"], result.Content)
				}
			}
		}
	})
}

// BenchmarkCaptureSinceAgainstCapturePane reports server cost and reply bytes.
// capture_since adds fingerprinting and a cursor, so it wins only when avoiding
// repeated large captures.
func BenchmarkCaptureSinceAgainstCapturePane(b *testing.B) {
	session, ctx, paneID := benchServer(b)

	// A pane with something in it. The cursor is fixed overhead of roughly half
	// a kilobyte, so on a blank pane capture_since returns more than a whole
	// capture does; the comparison is only honest once the screen it saves
	// re-sending is bigger than the cursor that saves it.
	callBenchmarkTool(ctx, b, session, "run_shell_command", map[string]any{
		"pane_id": paneID, "command": "seq 1 400", "timeout": 30,
	})

	b.Run("capture_pane", func(b *testing.B) {
		b.ReportAllocs()
		replies := 0
		for b.Loop() {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "capture_pane", Arguments: map[string]any{"pane_id": paneID},
			})
			if err != nil {
				b.Fatal(err)
			}
			if result.IsError {
				b.Fatalf("capture_pane: %#v", result.Content)
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
			arguments := map[string]any{"pane_id": paneID}
			if cursor != "" {
				arguments["cursor"] = cursor
			}
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "capture_since", Arguments: arguments,
			})
			if err != nil {
				b.Fatal(err)
			}
			if result.IsError {
				b.Fatalf("capture_since: %#v", result.Content)
			}
			var since struct {
				Cursor string `json:"cursor"`
			}
			if err := unmarshalStructured(result, &since); err != nil {
				b.Fatal(err)
			}
			cursor = since.Cursor
			replies += replyBytes(result)
		}
		b.ReportMetric(float64(replies)/float64(b.N), "bytes/reply")
	})
}

func callBenchmarkTool(
	ctx context.Context,
	b *testing.B,
	session *sdk.ClientSession,
	name string,
	arguments map[string]any,
) *sdk.CallToolResult {
	b.Helper()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		b.Fatal(err)
	}
	if result.IsError {
		b.Fatalf("%s: %#v", name, result.Content)
	}
	return result
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
