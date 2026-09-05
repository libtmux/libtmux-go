package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAFrameThatIsNotJSONIsDroppedRatherThanFatal(t *testing.T) {
	const good = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	stream := strings.Join([]string{
		good,
		"{ this is not json",
		"not even braces",
		`{"unterminated": `,
		"",
		"   ",
		"\x00\x01binary",
		"[garbage",
		"[1,2,3]",
		// Valid JSON that is not a JSON-RPC message ends the read loop just as
		// a syntax error does, so the filter has to reject it too.
		"{}",
		`{"jsonrpc":"2.0"}`,
		good,
	}, "\n") + "\n"

	var notified bytes.Buffer
	reader := wholeJSONLines(io.NopCloser(strings.NewReader(stream)), &notified)
	passed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Everything that reaches the decoder has to parse, or it loses sync.
	for _, line := range strings.Split(strings.TrimRight(string(passed), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !decodable([]byte(line)) {
			t.Errorf("a line the decoder rejects reached it: %q", line)
		}
	}
	if got := strings.Count(string(passed), good); got != 2 {
		t.Errorf("kept %d of the 2 good frames", got)
	}
	if dropped := strings.Count(notified.String(), "ignoring a frame"); dropped != 8 {
		t.Errorf("reported %d dropped frames, want the 8 the decoder rejects:\n%s",
			dropped, notified.String())
	}
}

func TestALongFrameSurvivesTheFilter(t *testing.T) {
	encoded, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]string{"text": strings.Repeat("x", 512*1024)},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := wholeJSONLines(io.NopCloser(
		strings.NewReader(string(encoded)+"\n")), io.Discard)
	passed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(passed)) != string(encoded) {
		t.Errorf("a %d byte frame did not survive: got %d bytes",
			len(encoded), len(strings.TrimSpace(string(passed))))
	}
}

func TestAnOversizedFrameIsBoundedAndTheNextFrameSurvives(t *testing.T) {
	const good = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	stream := strings.Repeat("x", jsonRPCFrameMaxBytes+1) + "\n" + good + "\n"
	var notified bytes.Buffer
	reader := wholeJSONLines(io.NopCloser(strings.NewReader(stream)), &notified)
	passed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(passed)) != good {
		t.Fatalf("frame after oversized input = %q, want valid next frame", passed)
	}
	if !strings.Contains(notified.String(), "past 8388608 bytes") {
		t.Fatalf("oversized frame diagnostic = %q", notified.String())
	}
}

func TestTheFilterEndsWhenThePipeDoes(t *testing.T) {
	reader := wholeJSONLines(io.NopCloser(strings.NewReader("{ bad\n")), io.Discard)
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read: %v", err)
	}
	// A stream of nothing but bad frames ends at EOF rather than spinning.
	reader = wholeJSONLines(io.NopCloser(strings.NewReader("")), io.Discard)
	buffer := make([]byte, 8)
	if _, err := reader.Read(buffer); err != io.EOF {
		t.Errorf("read on an empty stream = %v, want EOF", err)
	}
}

func TestStdioRejectsAnOversizedRequestIDBeforeDispatch(t *testing.T) {
	serverPipe, clientPipe := net.Pipe()
	t.Cleanup(func() {
		_ = clientPipe.Close()
		_ = serverPipe.Close()
	})
	if err := clientPipe.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64
	server := sdk.NewServer(&sdk.Implementation{Name: "framing-test", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "mark"}, func(
		context.Context,
		*sdk.CallToolRequest,
		map[string]any,
	) (*sdk.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"calls": calls.Add(1)}, nil
	})
	transport := jsonLineTransport(serverPipe, serverPipe, io.Discard)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(t.Context(), transport) }()

	reader := bufio.NewReader(clientPipe)
	write := func(message map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := clientPipe.Write(append(encoded, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ([]byte, map[string]any) {
		t.Helper()
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return line, response
	}

	write(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "framing-test", "version": "1"},
		},
	})
	_, initialized := read()
	if initialized["result"] == nil {
		t.Fatalf("initialize response = %#v", initialized)
	}
	write(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})

	acceptedID := strings.Repeat("i", jsonRPCRequestIDMaxBytes-2)
	write(map[string]any{
		"jsonrpc": "2.0", "id": acceptedID, "method": "tools/call",
		"params": map[string]any{"name": "mark", "arguments": map[string]any{}},
	})
	_, accepted := read()
	if accepted["id"] != acceptedID || calls.Load() != 1 {
		t.Fatalf("accepted response/calls = (%T, %d), want near-bound ID and one call",
			accepted["id"], calls.Load())
	}

	write(map[string]any{
		"jsonrpc": "2.0", "id": strings.Repeat("i", 1_000_000),
		"method": "tools/call",
		"params": map[string]any{"name": "mark", "arguments": map[string]any{}},
	})
	line, rejected := read()
	wireError, _ := rejected["error"].(map[string]any)
	idNull := rejected["id"] == nil
	if len(line) > 1_000_000 || !idNull ||
		wireError["code"] != float64(-32600) || calls.Load() != 1 {
		t.Fatalf("oversized response = (%d bytes, id null %t, error %#v, calls %d)",
			len(line), idNull, wireError, calls.Load())
	}

	_ = clientPipe.Close()
	select {
	case <-serverDone:
	case <-time.After(10 * time.Second):
		t.Fatal("stdio server did not stop after the client closed")
	}
}
