package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPayloadsAreDigestedRatherThanRecorded(t *testing.T) {
	t.Parallel()
	arguments := json.RawMessage(`{
		"pane_id": "%3",
		"command": "deploy --token ghp_abcdefghijklmnop",
		"suppress_history": true
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
	if summary["pane_id"] != "%3" {
		t.Errorf("pane_id = %v, want it logged as itself", summary["pane_id"])
	}
	if summary["suppress_history"] != true {
		t.Errorf("suppress_history = %v, want the flag kept", summary["suppress_history"])
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

func TestAnUnknownFieldIsTreatedAsAPayload(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{"somethingNewAndSecret": "hunter2"}`))
	if _, digested := summary["somethingNewAndSecret"].(map[string]any); !digested {
		t.Errorf("an unclassified field was logged as itself: %#v", summary)
	}
}

func TestUserChosenNamesAreDigested(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{
		"session_name": "customer-secret-project",
		"pane_id": "%3"
	}`))
	if _, digested := summary["session_name"].(map[string]any); !digested {
		t.Errorf("session_name was logged in cleartext: %#v", summary["session_name"])
	}
	if summary["pane_id"] != "%3" {
		t.Errorf("stable pane id = %v, want %%3", summary["pane_id"])
	}
}

func TestNestedArgumentsAreSummarizedToo(t *testing.T) {
	t.Parallel()
	summary := summarizeArguments(json.RawMessage(`{
		"operations": [{"tool": "send_keys", "arguments": {"keys": ["secret"], "pane_id": "%1"}}]
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
