package integration

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// modules are every module in this repository, by directory.
var modules = []string{".", "examples", "workspace", "mcp", "benchmarks"}

// advertisedVersion matches a released version wherever this repository names
// one. tmux's own versions, such as 3.2a, are not of this shape.
var advertisedVersion = regexp.MustCompile(`v\d+\.\d+\.\d+[-0-9A-Za-z.]*`)

// ownRequirement matches a require directive naming a module of this
// repository, capturing the version a consumer would resolve.
var ownRequirement = regexp.MustCompile(
	`(github\.com/libtmux/libtmux-go(?:/[a-z]+)?) (v\d\S*)`,
)

// TestEveryAdvertisedVersionAgrees gates the release surface against drift.
//
// A version is written down in two unrelated places: the commands the README
// tells a reader to run, and the require directives deciding what a consumer of
// a module resolves. A release that bumps one and not the other leaves either a
// README installing a version nobody published, or a module requiring one --
// and a require is not a local concern, because the replace directive beside it
// is read only here.
func TestEveryAdvertisedVersionAgrees(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	sources := map[string][]string{}
	record := func(version, where string) {
		sources[version] = append(sources[version], where)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range advertisedVersion.FindAllString(string(readme), -1) {
		record(version, "README.md")
	}

	for _, module := range modules {
		path := filepath.Join(root, module, "go.mod")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ownRequirement.FindAllStringSubmatch(string(content), -1) {
			record(match[2], module+"/go.mod requires "+match[1])
		}
	}

	if len(sources) == 0 {
		t.Fatal("no version found in the README or any go.mod, so this gate proves nothing")
	}
	if len(sources) == 1 {
		return
	}
	versions := slices.Sorted(maps.Keys(sources))
	report := make([]string, 0, len(versions))
	for _, version := range versions {
		places := sources[version]
		slices.Sort(places)
		report = append(report, version+": "+strings.Join(places, ", "))
	}
	t.Fatalf("this repository advertises %d versions, want one:\n%s",
		len(versions), strings.Join(report, "\n"))
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
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locate the module root: %v", err)
	}
	return strings.TrimSpace(string(output))
}
