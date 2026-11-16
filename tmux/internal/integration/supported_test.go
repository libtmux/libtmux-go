package integration

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

// ciManifestPath is the one place the tmux releases and Go toolchains CI runs
// are written down. The plan script, the matrix script and these tests read it,
// and the tests workflow reads the plan.
const ciManifestPath = ".github/ci-matrix.txt"

// documentedRange matches a supported range stated to a reader. Whitespace
// rather than a space: prose wraps, and a range split across two lines is the
// same promise as one that fits on one. Hyphens too, because a range read as
// one adjective is written that way and promises the same thing.
var documentedRange = regexp.MustCompile(
	`tmux\s+\*?\*?(\d+\.\d+[a-z]?(?:-rc\d*)?)[\s-]+through[\s-]+(\d+\.\d+[a-z]?(?:-rc\d*)?)`,
)

// documentedFloorOnly matches a lower bound stated without an upper one.
var documentedFloorOnly = regexp.MustCompile(
	`(?:tmux|version)\s+(\d+\.\d+[a-z]?)\s+or\s+(?:newer|later)`,
)

// documentedNamedFloor matches a tmux or MCP floor stated as an attribute.
var documentedNamedFloor = regexp.MustCompile(
	`(?:tmux|MCP(?:\s+consumer)?)\s+has\s+a\s+(\d+\.\d+[a-z]?)\s+floor`,
)

// benchmarkSection matches the heading BENCHMARKS.md gives one tmux release's
// measurements.
var benchmarkSection = regexp.MustCompile(`(?m)^## tmux (\d+\.\d+[a-z]?(?:-rc\d*)?)\s*$`)

// ciManifestEntries lists the values of one kind in the CI manifest, in file
// order.
func ciManifestEntries(t *testing.T, root, kind string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciManifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for line := range strings.Lines(string(content)) {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == kind {
			values = append(values, fields[1])
		}
	}
	return values
}

func TestDocumentedTmuxRangesAreTested(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	tested := make([]tmux.Version, 0, 8)
	supported := ciManifestEntries(t, root, "supported")
	if len(supported) == 0 {
		t.Fatalf("%s lists no supported tmux release", ciManifestPath)
	}
	for _, raw := range supported {
		version, err := tmux.ParseVersion(raw)
		if err != nil {
			t.Fatalf("%s names %q, which is not a tmux version: %v", ciManifestPath, raw, err)
		}
		tested = append(tested, version)
	}
	slices.SortFunc(tested, tmux.Version.Compare)
	oldest, newest := tested[0].String(), tested[len(tested)-1].String()
	connectionFloor := tmux.MinimumConnectionVersion
	programFloor := "3.8"
	for _, floor := range []string{connectionFloor, programFloor} {
		if !slices.ContainsFunc(tested, func(version tmux.Version) bool {
			return version.String() == floor
		}) {
			t.Fatalf("%s omits the feature floor %s", ciManifestPath, floor)
		}
	}
	allowedRanges := map[[2]string]bool{
		{oldest, newest}:          true,
		{connectionFloor, newest}: true,
		{programFloor, newest}:    true,
	}
	allowedFloors := map[string]bool{
		oldest:          true,
		connectionFloor: true,
		programFloor:    true,
	}

	for _, document := range markdownFiles(t, root) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range documentedRange.FindAllStringSubmatch(string(content), -1) {
			if !allowedRanges[[2]string{match[1], match[2]}] {
				t.Errorf("%s promises untested tmux range %s through %s; "+
					"tested module ranges are %s-%s, %s-%s and %s-%s",
					document, match[1], match[2], oldest, newest, connectionFloor, newest, programFloor, newest)
			}
		}
		for _, matcher := range []*regexp.Regexp{documentedFloorOnly, documentedNamedFloor} {
			for _, match := range matcher.FindAllStringSubmatch(string(content), -1) {
				if !allowedFloors[match[1]] {
					t.Errorf("%s asks for untested tmux floor %s; tested floors are %s, %s and %s",
						document, match[1], oldest, connectionFloor, programFloor)
				}
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
			t.Errorf("%s supports tmux %s and BENCHMARKS.md records no "+
				"table for it", ciManifestPath, version)
		}
	}
	for _, version := range measured {
		if version == "3.2a" {
			continue // Historical measurements remain reproducible from their revision.
		}
		if !slices.ContainsFunc(tested, func(run tmux.Version) bool {
			return run.String() == version
		}) {
			t.Errorf("BENCHMARKS.md records tmux %s and %s supports no "+
				"such release", version, ciManifestPath)
		}
	}
}

var releaseName = regexp.MustCompile(`^(\d+)\.(\d+)([a-z]?)$`)

// compareReleases orders two tmux release names. A lettered point release
// follows the release it patches, so 3.3 comes before 3.3a, which is the
// opposite of how a version library ranks a suffix.
func compareReleases(t *testing.T, left, right string) int {
	t.Helper()
	key := func(name string) (int, int, string) {
		match := releaseName.FindStringSubmatch(name)
		if match == nil {
			t.Fatalf("%s names %q, which is not a tmux release", ciManifestPath, name)
		}
		major, _ := strconv.Atoi(match[1])
		minor, _ := strconv.Atoi(match[2])
		return major, minor, match[3]
	}
	leftMajor, leftMinor, leftLetter := key(left)
	rightMajor, rightMinor, rightLetter := key(right)
	return cmp.Or(
		cmp.Compare(leftMajor, rightMajor),
		cmp.Compare(leftMinor, rightMinor),
		cmp.Compare(leftLetter, rightLetter),
	)
}

// ciManifestKinds is every kind the plan understands, so a kind added to the
// manifest without a rule here fails rather than being ignored by the tests.
var ciManifestKinds = []string{"supported", "refused", "development", "control", "go"}

func TestCIManifestIsOrderedAndAgreesWithTheLibrary(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciManifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(content)) {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if fields := strings.Fields(line); len(fields) > 0 && !slices.Contains(ciManifestKinds, fields[0]) {
			t.Errorf("%s has an entry of kind %q the tests do not check", ciManifestPath, fields[0])
		}
	}

	supported := ciManifestEntries(t, root, "supported")
	for i := 1; i < len(supported); i++ {
		if compareReleases(t, supported[i-1], supported[i]) >= 0 {
			t.Errorf("%s lists %s before %s; releases run oldest first, "+
				"and the first and last are the edges", ciManifestPath, supported[i-1], supported[i])
		}
	}
	if supported[0] != tmux.MinimumSupportedVersion {
		t.Errorf("the oldest supported release is %s and the library's floor is %s",
			supported[0], tmux.MinimumSupportedVersion)
	}
	if newest := supported[len(supported)-1]; newest != tmux.MaximumTestedVersion {
		t.Errorf("the newest supported release is %s and the library's newest tested is %s",
			newest, tmux.MaximumTestedVersion)
	}
	for _, refused := range ciManifestEntries(t, root, "refused") {
		if compareReleases(t, refused, tmux.MinimumSupportedVersion) >= 0 {
			t.Errorf("%s refuses %s, which the library supports", ciManifestPath, refused)
		}
	}
	if control := ciManifestEntries(t, root, "control"); !slices.Equal(control, []string{tmux.MinimumConnectionVersion}) {
		t.Errorf("control = %v, want the library's connection floor %s", control, tmux.MinimumConnectionVersion)
	}
}
