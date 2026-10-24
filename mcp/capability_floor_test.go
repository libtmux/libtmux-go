package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// stageBuffer requires tmux 3.4 for the named-buffer cleanup it depends on.
// The floor is written out in input_tools.go because this module cannot read
// the core module's registry; this test ties it to the committed manifest.
func TestStagedBufferFloorMatchesTheNamedBufferCapability(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Capabilities []struct {
			ID    string `json:"id"`
			Since string `json:"since"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, capability := range manifest.Capabilities {
		if capability.ID == "buffer-delete-named" {
			if capability.Since != stagedBufferMinimum {
				t.Fatalf("buffer-delete-named since %s, staged buffers require %s",
					capability.Since, stagedBufferMinimum)
			}
			return
		}
	}
	t.Fatal("capabilities.json has no buffer-delete-named record")
}
