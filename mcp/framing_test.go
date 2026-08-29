package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
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
		// Valid JSON that is not a JSON-RPC message ends the read loop just as
		// a syntax error does, so the filter has to reject it too.
		"{}",
		"[1,2,3]",
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
	if dropped := strings.Count(notified.String(), "ignoring a frame"); dropped != 7 {
		t.Errorf("reported %d dropped frames, want the 7 the decoder rejects:\n%s",
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
