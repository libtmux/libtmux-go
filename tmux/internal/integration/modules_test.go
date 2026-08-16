package integration

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// tmuxModulePath is the core module, named so that a helper resolving the
// repository root picks it rather than whichever module a workspace lists
// first.
const tmuxModulePath = "github.com/libtmux/libtmux-go"

// modules are every module in this repository, by directory.
var modules = []string{".", "examples", "workspace", "mcp", "benchmarks"}

// publishedModules maps a module of this repository that is tagged and
// consumed to the directory holding it. examples and benchmarks are neither.
var publishedModules = map[string]string{
	"github.com/libtmux/libtmux-go":           ".",
	"github.com/libtmux/libtmux-go/workspace": "workspace",
	"github.com/libtmux/libtmux-go/mcp":       "mcp",
}

// readmeInstall matches a package path the README tells a reader to fetch at a
// version, such as a go get or go install line.
var readmeInstall = regexp.MustCompile(
	`(github\.com/libtmux/libtmux-go[a-zA-Z0-9./_-]*)@(v\d[0-9A-Za-z.-]*)`,
)

// readmeTag matches a tag the README names for a module of its own, which is
// how a multi-module repository spells a version that is not the core's.
var readmeTag = regexp.MustCompile("`(workspace|mcp)/(v\\d[0-9A-Za-z.-]*)`")

// ownRequirement matches a require directive naming a module of this
// repository, capturing the version a consumer would resolve.
var ownRequirement = regexp.MustCompile(
	`(github\.com/libtmux/libtmux-go(?:/[a-z]+)?) (v\d\S*)`,
)

// TestEveryAdvertisedVersionAgrees gates the release surface against drift.
//
// A module's version is written down in two unrelated places: the commands the
// README tells a reader to run, and the require directives deciding what a
// consumer resolves. Modules are tagged per directory and their versions move
// independently, so the invariant is per module rather than one version across
// the repository -- and a require is not a local concern, because a replace
// directive beside it is read only here.
func TestEveryAdvertisedVersionAgrees(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	sources := map[string]map[string][]string{}
	record := func(module, version, where string) {
		if sources[module] == nil {
			sources[module] = map[string][]string{}
		}
		sources[module][version] = append(sources[module][version], where)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range readmeInstall.FindAllStringSubmatch(string(readme), -1) {
		module, ok := longestModulePrefix(match[1])
		if !ok {
			t.Errorf("README fetches %s, which is in no module of this repository", match[1])
			continue
		}
		record(module, match[2], "README.md fetches "+match[1])
	}
	for _, match := range readmeTag.FindAllStringSubmatch(string(readme), -1) {
		record("github.com/libtmux/libtmux-go/"+match[1], match[2],
			"README.md names the tag "+match[1]+"/"+match[2])
	}

	for _, module := range modules {
		content, err := os.ReadFile(filepath.Join(root, module, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ownRequirement.FindAllStringSubmatch(string(content), -1) {
			record(match[1], match[2], module+"/go.mod requires it")
		}
	}

	for module := range publishedModules {
		versions := sources[module]
		if len(versions) == 0 {
			t.Errorf("%s is published but no version of it is written down anywhere", module)
			continue
		}
		if len(versions) == 1 {
			continue
		}
		report := make([]string, 0, len(versions))
		for _, version := range slices.Sorted(maps.Keys(versions)) {
			places := versions[version]
			slices.Sort(places)
			report = append(report, "  "+version+": "+strings.Join(places, ", "))
		}
		t.Errorf("%s is advertised at %d versions, want one:\n%s",
			module, len(versions), strings.Join(report, "\n"))
	}
}

// longestModulePrefix returns the published module containing a package path.
// The longest match wins, since every module here sits under the core's path.
func longestModulePrefix(packagePath string) (string, bool) {
	best := ""
	for module := range publishedModules {
		if packagePath != module && !strings.HasPrefix(packagePath, module+"/") {
			continue
		}
		if len(module) > len(best) {
			best = module
		}
	}
	return best, best != ""
}

// TestEveryModuleResolvesWithoutAWorkspace gates the difference between a
// repository that works and one that only works here.
//
// A go.work file stitches these modules together on a developer's machine, and
// in doing so it hides a module whose own go.mod cannot resolve what it
// imports. A consumer has no workspace, so GOWORK=off is how a consumer sees
// them, and it is the only way this repository finds out before they do.
func TestEveryModuleResolvesWithoutAWorkspace(t *testing.T) {
	root := repositoryRoot(t)
	for _, module := range modules {
		t.Run(module, func(t *testing.T) {
			// Build into a temporary directory. A module holding a main
			// package would otherwise have a binary written into it, which
			// is a build artefact this repository deletes rather than
			// ignores -- and one a wildcard commit would carry in.
			build := exec.Command("go", "build", "-o", t.TempDir()+string(os.PathSeparator), "./...")
			build.Dir = filepath.Join(root, module)
			build.Env = append(os.Environ(), "GOWORK=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("module %s does not resolve on its own: %v\n%s",
					module, err, output)
			}
		})
	}
}

// TestTheCoreKeepsItsPrivatePackagesToItself gates the boundary that moving
// internal beneath the package bought.
//
// Go allows an internal package to be imported from anywhere under the parent
// of its internal directory, and a module boundary does not narrow that. The
// parent here is the tmux package, so the consumer modules are outside it and
// the compiler refuses them. That is a property of where the directory sits,
// which means moving it back would silently give the consumers access again.
func TestTheCoreKeepsItsPrivatePackagesToItself(t *testing.T) {
	root := repositoryRoot(t)
	probe := filepath.Join(root, "mcp", "zz_internal_boundary_probe.go")
	source := "package mcp\n\n" +
		`import _ "github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"` + "\n"
	if err := os.WriteFile(probe, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(probe) })

	build := exec.Command("go", "build", "./...")
	build.Dir = filepath.Join(root, "mcp")
	build.Env = append(os.Environ(), "GOWORK=off")
	output, err := build.CombinedOutput()
	if err == nil {
		t.Fatal("a consumer module imported the core's internal package")
	}
	if !strings.Contains(string(output), "internal package") {
		t.Fatalf("build failed for some other reason than the internal rule:\n%s", output)
	}
}

// TestTheServerInstallsFromItsOwnModule gates the path a user types.
//
// The command lives in a module of its own, so the go command resolves it
// through mcp/go.mod rather than the core's. Building it by that path is what
// confirms the install path a reader is given actually names something.
func TestTheServerInstallsFromItsOwnModule(t *testing.T) {
	root := repositoryRoot(t)
	build := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "libtmux-mcp"),
		"./cmd/libtmux-mcp")
	build.Dir = filepath.Join(root, "mcp")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the documented install path does not build: %v\n%s", err, output)
	}
}

// repositoryRoot is the directory holding the core module's go.mod, which is
// also the directory the other modules sit beside.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve this test's own path")
	}
	return moduleRootFrom(t, filepath.Dir(filename))
}

// moduleRootFrom walks up from directory to the core module's go.mod.
//
// It reads the tree rather than asking the go command, because go list reports
// every module of a workspace and answers differently depending on where and
// how it is run, while the directory holding this repository's go.mod is a
// fixed distance above any test in it either way.
func moduleRootFrom(t *testing.T, directory string) string {
	t.Helper()

	for current := directory; ; {
		content, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil && strings.Contains(string(content), "module "+tmuxModulePath+"\n") {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatalf("no go.mod declaring %s above %s", tmuxModulePath, directory)
		}
		current = parent
	}
}
