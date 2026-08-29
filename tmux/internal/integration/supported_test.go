package integration

import (
	"os"
	"os/exec"
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

// documentedNamedFloor matches a tmux or MCP floor stated as an attribute.
var documentedNamedFloor = regexp.MustCompile(
	`(?:tmux|MCP(?:\s+consumer)?)\s+has\s+a\s+(\d+\.\d+[a-z]?)\s+floor`,
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
		for _, matcher := range []*regexp.Regexp{documentedFloorOnly, documentedNamedFloor} {
			for _, match := range matcher.FindAllStringSubmatch(string(content), -1) {
				if !allowedFloors[match[1]] {
					t.Errorf("%s asks for untested tmux floor %s; tested floors are %s and %s",
						document, match[1], oldest, connectionFloor)
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

func TestMatrixRejectsRequestedVersionWithoutBinary(t *testing.T) {
	output, err := runMatrixScript(t, t.TempDir(),
		"LIBTMUX_MATRIX_VERSIONS=3.7c",
		"LIBTMUX_MATRIX_MODULES=.",
	)
	if err == nil {
		t.Fatalf("matrix without the requested binary reported success:\n%s", output)
	}
	if !strings.Contains(output, "3.7c/bin/tmux is not executable") {
		t.Fatalf("matrix error did not name the missing binary:\n%s", output)
	}
}

func TestMatrixRejectsBinaryReportingAnotherVersion(t *testing.T) {
	matrix := t.TempDir()
	installMatrixTmux(t, matrix, "3.7c", "tmux 3.7b")
	output, err := runMatrixScript(t, matrix,
		"LIBTMUX_MATRIX_VERSIONS=3.7c",
		"LIBTMUX_MATRIX_MODULES=.",
	)
	if err == nil {
		t.Fatalf("matrix labeled tmux 3.7b as 3.7c:\n%s", output)
	}
	if !strings.Contains(output, `expected "tmux 3.7c", got "tmux 3.7b"`) {
		t.Fatalf("matrix error did not report the version mismatch:\n%s", output)
	}
}

func TestMatrixRejectsUnknownModule(t *testing.T) {
	matrix := t.TempDir()
	installMatrixTmux(t, matrix, "3.7c", "tmux 3.7c")
	output, err := runMatrixScript(t, matrix,
		"LIBTMUX_MATRIX_VERSIONS=3.7c",
		"LIBTMUX_MATRIX_MODULES=typo",
	)
	if err == nil {
		t.Fatalf("matrix skipped an unknown module and reported success:\n%s", output)
	}
	if !strings.Contains(output, `unknown module "typo"`) {
		t.Fatalf("matrix error did not name the unknown module:\n%s", output)
	}
}

func TestMatrixRejectsNoSelectedModules(t *testing.T) {
	matrix := t.TempDir()
	installMatrixTmux(t, matrix, "3.7c", "tmux 3.7c")
	output, err := runMatrixScript(t, matrix,
		"LIBTMUX_MATRIX_VERSIONS=3.7c",
		"LIBTMUX_MATRIX_MODULES=   ",
	)
	if err == nil {
		t.Fatalf("matrix ran no module cells and reported success:\n%s", output)
	}
	if !strings.Contains(output, "no modules selected") {
		t.Fatalf("matrix error did not report the empty selection:\n%s", output)
	}
}

func TestMatrixCanRequireItsDirectory(t *testing.T) {
	matrix := filepath.Join(t.TempDir(), "missing")
	output, err := runMatrixScript(t, matrix,
		"LIBTMUX_MATRIX_REQUIRED=1",
		"LIBTMUX_MATRIX_VERSIONS=3.7c",
		"LIBTMUX_MATRIX_MODULES=.",
	)
	if err == nil {
		t.Fatalf("required matrix skipped its missing directory:\n%s", output)
	}
	if !strings.Contains(output, "required matrix directory does not exist") {
		t.Fatalf("matrix error did not report the required directory:\n%s", output)
	}
}

func TestMatrixCountsEverySelectedCell(t *testing.T) {
	matrix := t.TempDir()
	installMatrixTmux(t, matrix, "3.7b", "tmux 3.7b")
	installMatrixTmux(t, matrix, "3.7c", "tmux 3.7c")
	log := filepath.Join(t.TempDir(), "go-calls")
	output, err := runMatrixScript(t, matrix,
		"LIBTMUX_MATRIX_VERSIONS=3.7b 3.7c",
		"LIBTMUX_MATRIX_MODULES=. workspace",
		"LIBTMUX_MATRIX_TEST_LOG="+log,
	)
	if err != nil {
		t.Fatalf("valid matrix failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "matrix: 4 cells across 2 tmux versions, all green") {
		t.Fatalf("matrix did not report its selected cells:\n%s", output)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(calls))); got != 4 {
		t.Fatalf("matrix ran %d cells, want 4:\n%s", got, calls)
	}
}

func runMatrixScript(t *testing.T, matrix string, variables ...string) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	writeMatrixExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
if [ -n "${LIBTMUX_MATRIX_TEST_LOG:-}" ]; then
    printf '%s\n' "$PWD" >> "$LIBTMUX_MATRIX_TEST_LOG"
fi
exit 0
`)
	root := repositoryRoot(t)
	command := exec.Command("bash", filepath.Join(root, "scripts", "matrix.sh"))
	command.Dir = root
	command.Env = append([]string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"LIBTMUX_TMUX_MATRIX=" + matrix,
		"TMUX_TMPDIR=" + t.TempDir(),
	}, variables...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func installMatrixTmux(t *testing.T, matrix, version, reported string) {
	t.Helper()
	writeMatrixExecutable(
		t,
		filepath.Join(matrix, version, "bin", "tmux"),
		"#!/bin/sh\nprintf '%s\\n' '"+reported+"'\n",
	)
}

func writeMatrixExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
