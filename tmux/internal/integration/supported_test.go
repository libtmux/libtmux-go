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
// runs compatible module coverage against. It is a YAML flow sequence of
// quoted versions.
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

// benchmarkSection matches the heading BENCHMARKS.md gives one tmux release's
// measurements.
var benchmarkSection = regexp.MustCompile(`(?m)^## tmux (\d+\.\d+[a-z]?)\s*$`)

func TestDocumentedTmuxRangesAreTested(t *testing.T) {
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
	connectionFloor := tmux.MinimumConnectionVersion
	if !slices.ContainsFunc(tested, func(version tmux.Version) bool {
		return version.String() == connectionFloor
	}) {
		t.Fatalf("the workflow matrix omits the connection floor %s", connectionFloor)
	}
	allowedRanges := map[[2]string]bool{
		{oldest, newest}:          true,
		{connectionFloor, newest}: true,
	}
	allowedFloors := map[string]bool{
		oldest:          true,
		connectionFloor: true,
	}

	for _, document := range markdownFiles(t, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range documentedRange.FindAllStringSubmatch(string(content), -1) {
			if !allowedRanges[[2]string{match[1], match[2]}] {
				t.Errorf("%s promises untested tmux range %s through %s; "+
					"tested module ranges are %s-%s and %s-%s",
					document, match[1], match[2], oldest, newest, connectionFloor, newest)
			}
		}
		for _, match := range documentedFloorOnly.FindAllStringSubmatch(string(content), -1) {
			if !allowedFloors[match[1]] {
				t.Errorf("%s asks for untested tmux floor %s; tested floors are %s and %s",
					document, match[1], oldest, connectionFloor)
			}
		}
	}

	// The benchmarks record one table per release, and a release added to the
	// matrix without one leaves the table set claiming to cover a range it does
	// not.
	recorded, err := os.ReadFile(filepath.Join(root, "BENCHMARKS.md"))
	if err != nil {
		t.Fatal(err)
	}
	var measured []string
	for _, match := range benchmarkSection.FindAllStringSubmatch(string(recorded), -1) {
		measured = append(measured, match[1])
	}
	for _, version := range tested {
		if !slices.Contains(measured, version.String()) {
			t.Errorf("the workflow runs tmux %s and BENCHMARKS.md records no "+
				"table for it", version)
		}
	}
	for _, version := range measured {
		if !slices.ContainsFunc(tested, func(run tmux.Version) bool {
			return run.String() == version
		}) {
			t.Errorf("BENCHMARKS.md records tmux %s and the workflow runs no "+
				"such release", version)
		}
	}
}
