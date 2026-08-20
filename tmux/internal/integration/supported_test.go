package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

// workflowTmuxVersions matches the tmux releases the tests workflow builds and
// runs everything against, which is a YAML flow sequence of quoted versions.
var workflowTmuxVersions = regexp.MustCompile(`(?m)^\s+tmux-version:\s*\[([^\]]+)\]`)

// documentedRange matches a supported range stated to a reader. Whitespace
// rather than a space: prose wraps, and a range split across two lines is the
// same promise as one that fits on one.
var documentedRange = regexp.MustCompile(
	`tmux\s+\*?\*?(\d+\.\d+[a-z]?)\s+through\s+(\d+\.\d+[a-z]?)`,
)

// documentedFloorOnly matches a lower bound stated without an upper one.
var documentedFloorOnly = regexp.MustCompile(
	`(?:tmux|version)\s+(\d+\.\d+[a-z]?)\s+or\s+(?:newer|later)`,
)

// TestTheSupportedTmuxRangeIsTheOneTested gates a promise against the thing
// that keeps it.
//
// The README says which tmux releases work and that every release in the range
// is checked. Only the workflow's matrix does any checking, so it is what the
// promise is read against: a range naming a release the matrix does not build
// is a claim nothing stands behind, and a floor above the oldest one tested
// tells a reader to upgrade for no reason.
func TestTheSupportedTmuxRangeIsTheOneTested(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	matrix := workflowTmuxVersions.FindSubmatch(workflow)
	if matrix == nil {
		t.Fatal("the tests workflow builds no tmux matrix, so the supported " +
			"range is a claim nothing checks")
	}

	tested := make([]tmux.Version, 0, 8)
	for entry := range strings.SplitSeq(string(matrix[1]), ",") {
		raw := strings.Trim(strings.TrimSpace(entry), `"'`)
		version, err := tmux.ParseVersion(raw)
		if err != nil {
			t.Fatalf("the workflow matrix names %q, which is not a tmux version: %v", raw, err)
		}
		tested = append(tested, version)
	}
	slices.SortFunc(tested, tmux.Version.Compare)
	oldest, newest := tested[0].String(), tested[len(tested)-1].String()

	for _, document := range markdownFiles(t, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range documentedRange.FindAllStringSubmatch(string(content), -1) {
			if match[1] != oldest || match[2] != newest {
				t.Errorf("%s promises tmux %s through %s and the workflow runs "+
					"%s through %s", document, match[1], match[2], oldest, newest)
			}
		}
		for _, match := range documentedFloorOnly.FindAllStringSubmatch(string(content), -1) {
			if match[1] != oldest {
				t.Errorf("%s asks for tmux %s or newer and the oldest one "+
					"tested is %s", document, match[1], oldest)
			}
		}
	}
}
