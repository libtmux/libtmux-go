package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The record has to be useful to an operator and useless to anyone who steals
// it. Everything below is one of those two claims.

func TestPayloadsAreDigestedRatherThanRecorded(t *testing.T) {
	t.Parallel()
	// The shape of a send_keys call carrying something nobody should log.
	arguments := json.RawMessage(`{
		"paneId": "%3",
		"command": "deploy --token ghp_abcdefghijklmnop",
		"suppressHistory": true
	}`)

	summary := summarizeArguments(arguments)
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	recorded := string(encoded)

	if strings.Contains(recorded, "ghp_abcdefghijklmnop") || strings.Contains(recorded, "deploy") {
		t.Fatalf("the command reached the record: %s", recorded)
	}
	if summary["paneId"] != "%3" {
		t.Errorf("paneId = %v, want it logged as itself", summary["paneId"])
	}
	if summary["suppressHistory"] != true {
		t.Errorf("suppressHistory = %v, want the flag kept", summary["suppressHistory"])
	}
	command, ok := summary["command"].(map[string]any)
	if !ok {
		t.Fatalf("command = %#v, want a digest", summary["command"])
	}
	if want := len("deploy --token ghp_abcdefghijklmnop"); command["len"] != want {
		t.Errorf("len = %v, want %d", command["len"], want)
	}
	if prefix, _ := command["sha256"].(string); len(prefix) != 12 {
		t.Errorf("sha256 = %q, want a short stable prefix", prefix)
	}
}

// The same payload twice is the same digest, which is what makes a loop
// visible without making the record a transcript.
func TestTheSamePayloadDigestsTheSame(t *testing.T) {
	t.Parallel()
	first := digest("tmux kill-server")
	second := digest("tmux kill-server")
	other := digest("tmux kill-server ")

	if first["sha256"] != second["sha256"] {
		t.Error("the same command produced different digests")
	}
	if first["sha256"] == other["sha256"] {
		t.Error("a different command produced the same digest")
	}
}

// A field nobody classified is a payload, because that is the safe way to be
// wrong when a tool is added.
func TestAnUnknownFieldIsTreatedAsAPayload(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{"somethingNewAndSecret": "hunter2"}`))
	if _, digested := summary["somethingNewAndSecret"].(map[string]any); !digested {
		t.Errorf("an unclassified field was logged as itself: %#v", summary)
	}
}

// A batch carries its calls' arguments one level down, and they are payloads
// there too.
func TestNestedArgumentsAreSummarizedToo(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{
		"calls": [{"tool": "send_keys", "arguments": {"command": "secret", "paneId": "%1"}}]
	}`))
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("a nested command reached the record: %s", encoded)
	}
	if !strings.Contains(string(encoded), "send_keys") {
		t.Errorf("the nested tool name was lost, which is what makes it readable: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"%1"`) {
		t.Errorf("the nested pane id was lost: %s", encoded)
	}
}

// Every name logged in the clear has to be an identifier. This is the list a
// reviewer should look at when a tool gains a field.
func TestNothingContentShapedIsLoggedInTheClear(t *testing.T) {
	t.Parallel()
	for _, name := range auditedIdentifierNames() {
		switch name {
		case "command", "text", "value", "data", "document", "keys", "format", "pattern":
			t.Errorf("%q is logged in the clear but carries what a caller supplied", name)
		}
	}
}

func TestUnreadableArgumentsAreReportedRatherThanGuessed(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{not json`))
	if summary["unreadable"] != true {
		t.Errorf("summary = %#v, want it to say so", summary)
	}
}
