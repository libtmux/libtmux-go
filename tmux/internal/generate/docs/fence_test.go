package main

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// permittedFences names the fenced blocks a README may write by hand. Go is
// absent on purpose: a Go block is published only between markers, where it is
// quoted from code a suite runs.
var permittedFences = map[string]bool{
	"console": true,
	"text":    true,
	"bibtex":  true,
	"json":    true,
	"yaml":    true,
}

var fenceLine = regexp.MustCompile("^\\s*(`{3,}|~{3,})\\s*(.*)$")

// handwrittenGoAllowed names the READMEs that still write Go by hand, and
// why. A module with dependencies of its own cannot have a block compiled
// alone by this module's tests without the network, so its page is exempt
// from the Go rule and from nothing else.
var handwrittenGoAllowed = map[string]string{
	"mcp/README.md": "its blocks import the MCP SDK",
}

// strayFence is a fenced block a README should not hold.
type strayFence struct {
	line   int
	info   string
	reason string
}

// strayFences returns every fenced block outside a marker pair that is not a
// permitted kind. The fence has to be named exactly: an unlabeled block, a
// tilde fence, and a spelling such as "golang" or "Go" all render as code and
// would otherwise escape a check written against one spelling.
func strayFences(content string) []strayFence {
	var (
		found  []strayFence
		marked bool
		closer string
	)
	for index, line := range strings.Split(content, "\n") {
		if closer != "" {
			if strings.TrimSpace(line) == closer {
				closer = ""
			}
			continue
		}
		if !marked {
			if markdownWhole.MatchString(line) ||
				(markdownStart.MatchString(line) && !markdownEnd.MatchString(line)) {
				marked = true
				continue
			}
		} else if markdownEnd.MatchString(line) {
			marked = false
			continue
		}
		match := fenceLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		closer = match[1]
		if marked {
			continue
		}
		info, _, _ := strings.Cut(match[2], " ")
		switch {
		case strings.HasPrefix(closer, "~"):
			found = append(found, strayFence{index + 1, info, "uses a ~~~ fence; use backticks"})
		case info == "":
			found = append(found, strayFence{index + 1, info, "has no language; name it, text for output"})
		case !permittedFences[info]:
			found = append(found, strayFence{
				index + 1, info,
				"is a handwritten " + info + " block; quote a program or region between docs markers",
			})
		}
	}
	return found
}

// TestEveryREADMEFenceIsGeneratedOrPermitted holds every README to showing
// only Go that a suite has run.
func TestEveryREADMEFenceIsGeneratedOrPermitted(t *testing.T) {
	root, _ := repositoryRoot(t)
	checked := 0
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		content := documents[name]
		if path.Base(name) != "README.md" {
			continue
		}
		checked++
		_, exempt := handwrittenGoAllowed[name]
		for _, stray := range strayFences(content) {
			if exempt && stray.info == "go" {
				continue
			}
			t.Errorf("%s:%d: fenced block %s", name, stray.line, stray.reason)
		}
	}
	if checked < 8 {
		t.Fatalf("checked %d README files, want at least 8; the scan is broken", checked)
	}
	for name := range handwrittenGoAllowed {
		if _, ok := documents[name]; !ok {
			t.Errorf("%s is exempt from the Go rule but no longer exists", name)
		}
	}
}

func TestStrayFencesAreFound(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		document string
		want     []int
	}{
		{"permitted kinds", "```console\n$ go test\n```\n\n```text\nok\n```\n", nil},
		{"go outside markers", "```go\nfmt.Println()\n```\n", []int{1}},
		{"an import alone is still go", "```go\nimport \"x\"\n```\n", []int{1}},
		{"other spellings of go", "```golang\nx\n```\n\n```Go\nx\n```\n", []int{1, 5}},
		{"unlabeled", "```\nx\n```\n", []int{1}},
		{"tilde fence", "~~~go\nx\n~~~\n", []int{1}},
		{"indented in a list", "- item\n\n  ```go\n  x\n  ```\n", []int{3}},
		{"generated region", "<!-- docs:a -->\n\n```go\nx\n```\n\n<!-- docs:end -->\n", nil},
		{"generated file", "<!-- docs:file a/main.go -->\n\n```go\nx\n```\n\n<!-- docs:end -->\n", nil},
		{"a comment that is not a marker", "<!-- docs: note -->\n\n```go\nx\n```\n", []int{3}},
		{"after a marker pair closes", "<!-- docs:a -->\n<!-- docs:end -->\n\n```go\nx\n```\n", []int{4}},
		{"a longer fence holding a shorter one", "````text\n```go\nx\n```\n````\n", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var got []int
			for _, stray := range strayFences(test.document) {
				got = append(got, stray.line)
			}
			if len(got) != len(test.want) {
				t.Fatalf("strayFences() lines = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("strayFences() lines = %v, want %v", got, test.want)
				}
			}
		})
	}
}
