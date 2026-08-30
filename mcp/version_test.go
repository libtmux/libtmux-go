package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The fallback may name an unreleased version or the newest module tag. A
// tagged, superseded fallback is stale. Tag membership avoids a semver
// dependency and permits the unreleased version between a bump and its tag.
func TestFallbackVersionIsNotBehindTheTags(t *testing.T) {
	tags := releaseTags(t)
	if len(tags) == 0 {
		// A checkout without tags cannot answer this. CI fetches them for
		// exactly that reason; an export from a tarball never will.
		t.Skip("no mcp release tags in this checkout, so there is nothing to be behind")
	}
	newest := tags[0]
	for _, tag := range tags {
		if tag != fallbackVersion {
			continue
		}
		if tag != newest {
			t.Errorf("fallbackVersion is %s, which was released and superseded by %s; "+
				"set it to the release being prepared", fallbackVersion, newest)
		}
		return
	}
}

// releaseTags reports this module's release tags, newest first, with the
// prefix that scopes them to this module removed.
func releaseTags(t *testing.T) []string {
	t.Helper()
	const prefix = "mcp/"
	found, err := exec.Command(
		"git", "tag", "--list", prefix+"v*", "--sort=-v:refname",
	).Output()
	if err != nil {
		return nil
	}
	var tags []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(found)), "\n") {
		if trimmed := strings.TrimPrefix(strings.TrimSpace(line), prefix); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}

// The registry entry names a version too, and a registry pointing at a release
// that was never cut is worse than no entry: a client reads it, asks for that
// version, and finds nothing. The marker in the README is what the registry
// matches the entry against, so the three have to agree or publishing fails
// somewhere a test cannot see.
func TestTheRegistryEntryAgreesWithTheBuild(t *testing.T) {
	var entry struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Repository struct {
			Subfolder string `json:"subfolder"`
		} `json:"repository"`
	}
	raw, err := os.ReadFile("server.json")
	if err != nil {
		t.Fatalf("read the registry entry: %v", err)
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("the registry entry is not valid JSON: %v", err)
	}

	// The registry takes a semantic version; a Go tag wears a v in front of
	// one. They are the same release written two ways.
	if want := strings.TrimPrefix(fallbackVersion, "v"); entry.Version != want {
		t.Errorf("server.json names version %q and this build reports %q",
			entry.Version, want)
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}
	if marker := "mcp-name: " + entry.Name; !strings.Contains(string(readme), marker) {
		t.Errorf("the README carries no %q, which is what the registry matches "+
			"the entry against", marker)
	}
	// The server is not at the repository root, and an entry that does not say
	// so sends a reader to the wrong directory.
	if entry.Repository.Subfolder != "mcp" {
		t.Errorf("server.json points at subfolder %q, want mcp",
			entry.Repository.Subfolder)
	}
}
