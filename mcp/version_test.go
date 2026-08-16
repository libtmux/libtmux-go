package mcp

import (
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
	for _, line := range strings.Split(strings.TrimSpace(string(found)), "\n") {
		if trimmed := strings.TrimPrefix(strings.TrimSpace(line), prefix); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}
