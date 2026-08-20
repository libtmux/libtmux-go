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

// What a tool call costs, which is the number a client feels.
//
// BENCHMARKS.md measures the ways of reaching tmux. This measures the layer on
// top: one MCP round trip, decoded and validated, against a real tmux. A
// regression here shows up as a slower agent rather than as a failing test.
//
// Read these as a relative signal, not as a cost. Talking to tmux dominates
// every number, so the protocol is the smaller part of each, and the wall
// clock moves with whatever else the machine is running. What is worth
// comparing is one revision against another on one machine, and the allocation
// counts, which do not move with load.
//
// The transport is the in-memory pair, so the pipe a real client speaks over is
// excluded and everything else is included.

// benchServer connects one client to one server against a real tmux.
func benchServer(b *testing.B) (*sdk.ClientSession, context.Context) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	b.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, b, tmuxtest.ServerOptions{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil)
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

// BenchmarkBatchAgainstSerial measures what a batch actually saves, which is
// not server time. Over this transport the two run the same tmux commands and
// come out level on the clock; what the batch saves is a third of the
// allocations and, over a real pipe, two of the three round trips. Its reason
// to exist is the caller's turn rather than the server's CPU, so a change that
// made batching slower per call than its parts would still be worth having and
// this is here to show which of the two moved.
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

// BenchmarkCaptureSinceAgainstCapturePane measures the trade capture_since
// makes, which is not a faster call.
//
// It costs more on the server than a plain capture: it reads the pane and then
// fingerprints the rows to mint a cursor, so it does strictly more work. The
// saving it offers is in what comes back, which is the scarce thing, and it is
// not free either -- the cursor is around half a kilobyte in every reply.
//
// So there is a break-even, and bytes/reply is reported to keep it visible: a
// screen smaller than the cursor is cheaper to re-send whole. On the 80x24
// pane of short lines here, capture_pane wins on both counts. capture_since
// earns its place on a wide pane holding full lines, checked repeatedly --
// which is what it is for, and is worth knowing is not every pane.
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
