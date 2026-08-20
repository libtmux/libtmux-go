package integration

import (
	"go/version"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// goDirective matches the go line of a go.mod or a go.work, capturing the
// language version without the patch component a module file may carry.
var goDirective = regexp.MustCompile(`(?m)^go (\d+\.\d+)(?:\.\d+)?\s*$`)

// lintGoVersion matches the language version golangci-lint reports availability
// against, which is the run.go setting.
var lintGoVersion = regexp.MustCompile(`(?m)^\s+go:\s*"?(\d+\.\d+)"?\s*$`)

// workflowGoVersions matches the toolchains the tests workflow runs, which is a
// YAML flow sequence of quoted versions.
var workflowGoVersions = regexp.MustCompile(`(?m)^\s+go-version:\s*\[([^\]]+)\]`)

// readmeFloor matches the language requirement the README states to a reader.
var readmeFloor = regexp.MustCompile(`\bGo (\d+\.\d+)\+`)

// TestEveryStatementOfTheLanguageFloorAgrees gates a version that several files
// carry and nothing derives.
//
// The floor tracks upstream's support window, so it moves, and every place
// stating it has to move with it. No tool reports a disagreement: the go
// directive does not gate standard library APIs, so a build succeeds either
// way, and go vet reads only the module it runs in. A lint configuration left
// behind stops offering the syntax the floor unlocked, a workflow left behind
// tests a release nothing claims to support, and a README left behind is simply
// wrong.
//
// Unlike a require directive there is no upstream to check against -- a floor
// is a decision rather than a copy of something. The core module's go directive
// is the one the go command itself obeys, so it is the one the rest are read
// against.
func TestEveryStatementOfTheLanguageFloorAgrees(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	stated := map[string]string{}
	state := func(name string, pattern *regexp.Regexp) {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		match := pattern.FindSubmatch(content)
		if match == nil {
			t.Fatalf("%s states no language version, and this gate is the only "+
				"reason the others stay in step with it", name)
		}
		stated[name] = string(match[1])
	}

	for _, module := range modules {
		state(filepath.ToSlash(filepath.Join(module, "go.mod")), goDirective)
	}
	state("go.work", goDirective)
	state(".golangci.yml", lintGoVersion)
	state("README.md", readmeFloor)
	state(".github/workflows/tests.yml", workflowGoVersions)

	// The workflow names a range, and only its oldest entry claims anything
	// about the floor. A newer one claims that nothing has broken above it.
	const workflow = ".github/workflows/tests.yml"
	tested := strings.Split(stated[workflow], ",")
	for i, entry := range tested {
		tested[i] = strings.Trim(strings.TrimSpace(entry), `"'`)
	}
	slices.SortFunc(tested, func(left, right string) int {
		return version.Compare("go"+left, "go"+right)
	})
	stated[workflow] = tested[0]

	floor := stated["go.mod"]
	for _, name := range slices.Sorted(maps.Keys(stated)) {
		if stated[name] != floor {
			t.Errorf("%s states Go %s and go.mod states Go %s; the floor is one "+
				"decision, and every file carrying it moves together",
				name, stated[name], floor)
		}
	}
}
