package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A version written down by hand goes stale the moment it is not, and this one
// did: it named v0.0.1-alpha.1 for the whole life of v0.0.1-alpha.2, so every
// build from source reported a release it was not.
//
// Nothing can make the constant update itself -- the toolchain has no version
// to give a working tree, which is the case it exists for. What can be checked
// is that it is not behind, and that is a comparison against the tags rather
// than against another written-down value.
//
// The rule is the weakest one that catches staleness: the constant may not
// name a release that has already been tagged unless that release is the most
// recent. Being ahead of every tag is the ordinary state between the commit
// that bumps it and the tag that follows, so that stays legal; naming a tag
// with newer ones behind it is exactly the bug and nothing else is.
//
// No version arithmetic, deliberately. Comparing pre-release orderings would
// need a semver dependency this module does not have, and the question here is
// membership rather than order.
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
