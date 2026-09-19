package tmux

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordsEncodeIdentityAndWhatTmuxSaid(t *testing.T) {
	t.Parallel()

	pane := paneWithExactTestTarget(serverWithRunner(&versionQueueRunner{}))
	encoded, err := json.Marshal(pane)
	if err != nil {
		t.Fatalf("Marshal(Pane) error = %v", err)
	}
	var decoded struct {
		ID          string            `json:"id"`
		SessionID   string            `json:"sessionId"`
		WindowID    string            `json:"windowId"`
		WindowIndex int               `json:"windowIndex"`
		Index       int               `json:"index"`
		Formats     map[string]string `json:"formats"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if decoded.ID != pane.ID().String() || decoded.SessionID != pane.SessionID().String() ||
		decoded.WindowID != pane.WindowID().String() {
		t.Errorf("encoded identity = %+v, want the pane's own", decoded)
	}
	// An absent map would make a consumer nil-check before indexing.
	if decoded.Formats == nil {
		t.Error("formats encoded as null, want an object")
	}
	// The server handle is the one thing a decoded record could not carry, so
	// it must not look as though it did.
	if strings.Contains(string(encoded), "server") {
		t.Errorf("encoded pane names a server: %s", encoded)
	}
}

func TestSnapshotEncodesOnlyTheKindsItListed(t *testing.T) {
	t.Parallel()

	empty := Snapshot{}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal(Snapshot) error = %v", err)
	}
	// A kind never listed and a kind listed and found empty are different
	// answers, and an empty array would spell them the same.
	for _, kind := range []string{"sessions", "windows", "panes", "clients"} {
		if strings.Contains(string(encoded), kind) {
			t.Errorf("the zero snapshot claims to have listed %s: %s", kind, encoded)
		}
	}

	// Panes are present but their kind was never listed, which is the only
	// shape that tells a guard on the listed kinds from one on emptiness.
	listed := Snapshot{state: &snapshotState{
		listed:   listedSessions,
		sessions: []Session{{sessionID: SessionID("$5")}},
		panes:    []Pane{{paneID: PaneID("%7")}},
	}}
	encoded, err = json.Marshal(listed)
	if err != nil {
		t.Fatalf("Marshal(Snapshot) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"sessions"`) {
		t.Errorf("a listed session kind did not encode: %s", encoded)
	}
	if strings.Contains(string(encoded), `"panes"`) {
		t.Errorf("an unlisted pane kind encoded: %s", encoded)
	}
}

func TestFormatValuesEncodeVerbatim(t *testing.T) {
	t.Parallel()

	values := FormatValues{values: formatValues{values: map[string]string{
		"pane_title":        "café",
		"pane_current_path": "/tmp",
	}}}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("Marshal(FormatValues) error = %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if decoded["pane_title"] != "café" || decoded["pane_current_path"] != "/tmp" {
		t.Errorf("formats = %#v, want what tmux said", decoded)
	}
}
