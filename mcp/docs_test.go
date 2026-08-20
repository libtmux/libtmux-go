package mcp_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// referenceRow matches a row of one of the reference's tables, capturing the
// first cell. A tool is named there, and the rest of the row describes it.
var referenceRow = regexp.MustCompile(`(?m)^\|([^|]*)\|`)

// referenceName matches an identifier the reference marks as one, which is how
// a tool or a prompt is written wherever it appears.
var referenceName = regexp.MustCompile("`([a-z][a-z_]+)`")

// TestTheReferenceNamesEverythingTheServerOffers gates the tool reference
// against the server rather than against the last time someone read both.
//
// TOOLS.md is where a client's operator goes to find out what is here, and a
// tool missing from it is a tool nobody reaches on purpose. Nothing about
// adding one to the server adds it to the reference, and nothing about renaming
// one renames it there, so the failure is silent in both directions: an
// undocumented tool, and an entry describing a tool that no longer answers.
//
// The widest safety level is the one asked, because the reference documents
// every tool and says which level withholds it.
func TestTheReferenceNamesEverythingTheServerOffers(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	reference, err := os.ReadFile("TOOLS.md")
	if err != nil {
		t.Fatalf("read the tool reference: %v", err)
	}

	listedTools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	offeredTools := make([]string, 0, len(listedTools.Tools))
	for _, tool := range listedTools.Tools {
		offeredTools = append(offeredTools, tool.Name)
	}

	listedPrompts, err := session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	offeredPrompts := make([]string, 0, len(listedPrompts.Prompts))
	for _, prompt := range listedPrompts.Prompts {
		offeredPrompts = append(offeredPrompts, prompt.Name)
	}

	for _, section := range []struct {
		heading string
		offered []string
	}{
		{"## Tools", offeredTools},
		{"## Prompts", offeredPrompts},
	} {
		documented := namesTabulatedUnder(string(reference), section.heading)
		if len(documented) == 0 {
			t.Errorf("%s tabulates nothing, so this gate would pass an empty "+
				"reference", section.heading)
			continue
		}
		for _, name := range section.offered {
			if !slices.Contains(documented, name) {
				t.Errorf("the server offers %s and TOOLS.md does not name it "+
					"under %s", name, section.heading)
			}
		}
		for _, name := range documented {
			if !slices.Contains(section.offered, name) {
				t.Errorf("TOOLS.md names %s under %s and the server offers no "+
					"such thing", name, section.heading)
			}
		}
	}
}

// namesTabulatedUnder reports the identifiers named in the first cell of every
// table row under a heading, up to the next heading of the same depth. One cell
// may name several, which is how the reference groups a set of tools that are
// described together.
func namesTabulatedUnder(reference, heading string) []string {
	_, after, found := strings.Cut(reference, heading+"\n")
	if !found {
		return nil
	}
	depth, _, _ := strings.Cut(heading, " ")
	if end := strings.Index(after, "\n"+depth+" "); end >= 0 {
		after = after[:end]
	}

	var names []string
	for _, row := range referenceRow.FindAllStringSubmatch(after, -1) {
		for _, name := range referenceName.FindAllStringSubmatch(row[1], -1) {
			if !slices.Contains(names, name[1]) {
				names = append(names, name[1])
			}
		}
	}
	return names
}
