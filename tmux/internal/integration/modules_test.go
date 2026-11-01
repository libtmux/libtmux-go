package integration

import (
	"cmp"
	"encoding/json"
	"fmt"
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
var modules = []string{".", "examples", "workspace", "mcp", "benchmarks", "internal/tools"}

func TestGeneratedJobCoversEveryModuleWithGenerators(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if !moduleHasGenerator(t, root, module) {
			continue
		}
		command := "go generate ./..."
		if module != "." {
			command = "go -C " + filepath.ToSlash(module) + " generate ./..."
		}
		if !strings.Contains(string(workflow), command) {
			t.Errorf("generated job does not run %q for module %s", command, module)
		}
	}
}

func moduleHasGenerator(t *testing.T, root, module string) bool {
	t.Helper()

	directory := filepath.Join(root, module)
	found := false
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != directory {
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if found || filepath.Ext(path) != ".go" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = strings.Contains(string(content), "//go:"+"generate ")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

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

// changelogOpening is the sentence a module's changelog opens with. It names
// the module, so a changelog pasted over another module's fails where it was
// pasted.
var changelogOpening = regexp.MustCompile("(?m)^Notable changes to `([^`]+)`\\.$")

// moduleDirective matches the module line of a go.mod.
var moduleDirective = regexp.MustCompile(`(?m)^module (\S+)\s*$`)

// changelogFailures lists every way the changelogs of the modules in directories
// fall short of the policy.
//
// A module is fetched as its whole directory, so a CHANGELOG.md beside its
// go.mod reaches every consumer with each tag; a module without one has nothing
// for a release to carry. Each claim is answerable from the directory:
//
//   - the file exists beside go.mod;
//   - it opens with the module path that go.mod declares; and
//   - it has an Unreleased section for the next release to land under.
func changelogFailures(root string, directories []string) []string {
	var failures []string
	for _, directory := range directories {
		manifest, err := os.ReadFile(filepath.Join(root, directory, "go.mod"))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s/go.mod: %v", directory, err))
			continue
		}
		declared := moduleDirective.FindSubmatch(manifest)
		if declared == nil {
			failures = append(failures, directory+"/go.mod: declares no module")
			continue
		}
		module, path := string(declared[1]), filepath.ToSlash(filepath.Join(directory, "CHANGELOG.md"))
		changelog, err := os.ReadFile(filepath.Join(root, directory, "CHANGELOG.md"))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s is published and has no changelog", path, module))
			continue
		}
		switch opening := changelogOpening.FindSubmatch(changelog); {
		case opening == nil:
			failures = append(failures, fmt.Sprintf("%s: does not open with \"Notable changes to `%s`.\"", path, module))
		case string(opening[1]) != module:
			failures = append(failures, fmt.Sprintf("%s: is the changelog of %s, not %s", path, opening[1], module))
		}
		if !strings.Contains("\n"+outsideFences(string(changelog)), "\n## Unreleased\n") {
			failures = append(failures, path+": has no \"## Unreleased\" section")
		}
	}
	return failures
}

// TestEveryPublishedModuleKeepsAChangelog holds each module this repository
// tags to a changelog of its own, so a release of one module does not have to
// be read out of another's history.
func TestEveryPublishedModuleKeepsAChangelog(t *testing.T) {
	t.Parallel()

	for _, failure := range changelogFailures(repositoryRoot(t), slices.Sorted(maps.Values(publishedModules))) {
		t.Error(failure)
	}
}

// TestChangelogFailuresAreFound pins the check above to the ways a changelog
// goes missing, with one that meets the policy beside them, so a change that
// stops it looking cannot go unnoticed.
func TestChangelogFailuresAreFound(t *testing.T) {
	t.Parallel()

	const (
		good    = "# Changelog\n\nNotable changes to `example.test/lib`.\n\n## Unreleased\n"
		openAs  = "# Changelog\n\nNotable changes to `%s`.\n\n"
		fenced  = "```markdown\n## Unreleased\n```\n"
		heading = "## Unreleased\n"
	)
	for _, test := range []struct {
		name       string
		changelog  *string
		manifest   string // the go.mod text; empty means one that declares example.test/lib
		noManifest bool
		want       string
	}{
		{name: "a changelog that meets the policy", changelog: new(good)},
		{name: "no changelog", want: "example.test/lib is published and has no changelog"},
		{
			name:      "another module's changelog pasted over this one",
			changelog: new(fmt.Sprintf(openAs, "example.test/other") + heading),
			want:      "is the changelog of example.test/other, not example.test/lib",
		},
		{name: "no opening sentence", changelog: new("# Changelog\n\n" + heading), want: "does not open with"},
		{
			name:      "no Unreleased section",
			changelog: new(fmt.Sprintf(openAs, "example.test/lib") + "## v0.1.0\n"),
			want:      "has no \"## Unreleased\" section",
		},
		{
			name:      "an Unreleased heading inside a code fence",
			changelog: new(fmt.Sprintf(openAs, "example.test/lib") + fenced),
			want:      "has no \"## Unreleased\" section",
		},
		{name: "no go.mod", changelog: new(good), noManifest: true, want: "lib/go.mod:"},
		{name: "a go.mod that declares no module", changelog: new(good), manifest: "go 1.26\n", want: "declares no module"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "lib"), 0o750); err != nil {
				t.Fatal(err)
			}
			manifest := cmp.Or(test.manifest, "module example.test/lib\n\ngo 1.26\n")
			if !test.noManifest {
				if err := os.WriteFile(filepath.Join(root, "lib", "go.mod"), []byte(manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.changelog != nil {
				if err := os.WriteFile(filepath.Join(root, "lib", "CHANGELOG.md"), []byte(*test.changelog), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := changelogFailures(root, []string{"lib"})
			switch {
			case test.want == "" && len(got) != 0:
				t.Errorf("changelogFailures = %q, want none", got)
			case test.want != "" && (len(got) != 1 || !strings.Contains(got[0], test.want)):
				t.Errorf("changelogFailures = %q, want one containing %q", got, test.want)
			}
		})
	}
}

// ownRequirement matches a require directive naming a module of this
// repository, capturing the version a consumer would resolve.
var ownRequirement = regexp.MustCompile(
	`(github\.com/libtmux/libtmux-go(?:/[a-z]+)?) (v\d\S*)`,
)

// Consumer modules must require the newest published release. Comparing pins
// only with each other misses a uniformly stale set, especially behind local
// replace directives.
func TestEveryRequirementNamesTheNewestRelease(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, module := range modules {
		content, err := os.ReadFile(filepath.Join(root, module, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ownRequirement.FindAllStringSubmatch(string(content), -1) {
			required, pinned := match[1], match[2]
			directory, ok := publishedModules[required]
			if !ok {
				continue
			}
			newest, tagged := newestRelease(t, root, directory)
			if !tagged {
				// A checkout without tags cannot answer this. CI fetches them
				// for exactly that reason; an export from a tarball never will.
				continue
			}
			if pinned != newest {
				t.Errorf("%s/go.mod requires %s %s, and %s is released; "+
					"a require is what a consumer resolves, so raise it",
					module, required, pinned, newest)
			}
		}
	}
}

// newestRelease reports a module's newest release tag, and whether the checkout
// has any. Tags carry the directory as a prefix, which is how a module that is
// not at the repository root is versioned, and the prefix is not part of the
// version a require names.
func newestRelease(t *testing.T, root, directory string) (string, bool) {
	t.Helper()

	prefix := ""
	if directory != "." {
		prefix = directory + "/"
	}
	list := exec.Command("git", "tag", "--list", prefix+"v*", "--sort=-v:refname")
	list.Dir = root
	found, err := list.Output()
	if err != nil {
		return "", false
	}
	newest, _, _ := strings.Cut(strings.TrimSpace(string(found)), "\n")
	if newest == "" {
		return "", false
	}
	return strings.TrimPrefix(newest, prefix), true
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

// TestEveryModuleBuildsWithWorkspaceSources proves the current source graph is
// coherent. Release metadata is a separate claim: sibling modules cannot name
// an unreleased core version.
func TestEveryModuleBuildsWithWorkspaceSources(t *testing.T) {
	root := repositoryRoot(t)
	workspace := filepath.Join(root, "go.work")
	for _, module := range modules {
		t.Run(module, func(t *testing.T) {
			build := exec.Command(
				"go", "build", "-o", t.TempDir()+string(os.PathSeparator), "./...",
			)
			build.Dir = filepath.Join(root, module)
			build.Env = append(os.Environ(), "GOWORK="+workspace)
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("module %s does not build with repository sources: %v\n%s",
					module, err, output)
			}
		})
	}
}

// TestEveryModuleMetadataResolvesWithoutAWorkspace gates standalone go.mod and
// go.sum files without pretending current source can compile against sibling
// versions that have not been released yet.
//
// A go.work file stitches these modules together on a developer's machine, and
// in doing so it hides incomplete module metadata. A consumer has no workspace,
// so GOWORK=off is how a consumer resolves it. go mod tidy -diff checks that
// graph without compiling unreleased source against released siblings.
func TestEveryModuleMetadataResolvesWithoutAWorkspace(t *testing.T) {
	root := repositoryRoot(t)
	for _, module := range modules {
		t.Run(module, func(t *testing.T) {
			tidy := exec.Command("go", "mod", "tidy", "-diff")
			tidy.Dir = filepath.Join(root, module)
			tidy.Env = append(os.Environ(), "GOWORK=off")
			if output, err := tidy.CombinedOutput(); err != nil {
				t.Fatalf("module %s metadata is not standalone and tidy: %v\n%s",
					module, err, output)
			}
		})
	}
}

// TestInstallableModuleHasNoLocalOverrides keeps the MCP command installable as
// a versioned module. Go rejects replace and exclude directives in that mode.
func TestInstallableModuleHasNoLocalOverrides(t *testing.T) {
	root := repositoryRoot(t)
	edit := exec.Command("go", "mod", "edit", "-json")
	edit.Dir = filepath.Join(root, "mcp")
	edit.Env = append(os.Environ(), "GOWORK=off")
	output, err := edit.Output()
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Replace []json.RawMessage
		Exclude []json.RawMessage
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		t.Fatalf("decode mcp/go.mod: %v", err)
	}
	if len(metadata.Replace) != 0 || len(metadata.Exclude) != 0 {
		t.Fatalf("mcp/go.mod has %d replace and %d exclude directives; want none",
			len(metadata.Replace), len(metadata.Exclude))
	}
}

func TestPublishedMCPModuleExcludesDeveloperCommands(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	list := exec.Command("go", "list", "./...")
	list.Dir = filepath.Join(root, "mcp")
	list.Env = append(os.Environ(), "GOWORK=off")
	output, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list published MCP packages: %v\n%s", err, output)
	}
	for packagePath := range strings.Lines(string(output)) {
		if strings.HasSuffix(strings.TrimSpace(packagePath), "/cmd/mcp-swap") {
			t.Fatalf("published MCP module includes developer command %s", strings.TrimSpace(packagePath))
		}
	}
}

// Go internal visibility follows directory ancestry, not module boundaries.
// Keeping internal beneath tmux prevents sibling consumer modules importing it.
func TestTheCoreKeepsItsPrivatePackagesToItself(t *testing.T) {
	root := repositoryRoot(t)
	goDirective, err := moduleGoDirective(root)
	if err != nil {
		t.Fatal(err)
	}
	// A module of its own rather than a file dropped into mcp: a probe written
	// into the repository is visible to every other test building that module
	// while it is there, and TestEveryModuleBuildsWithWorkspaceSources builds
	// exactly that one.
	directory := t.TempDir()
	writeDownstreamFile(t, filepath.Join(directory, "go.mod"), fmt.Sprintf(
		`module example.invalid/libtmux-boundary-probe

go %s

require github.com/libtmux/libtmux-go v0.0.0

replace github.com/libtmux/libtmux-go => %q
`, goDirective, root))
	writeDownstreamFile(t, filepath.Join(directory, "probe.go"), "package probe\n\n"+
		`import _ "github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"`+"\n")

	build := exec.Command("go", "build", "./...")
	build.Dir = directory
	build.Env = append(os.Environ(), "GOWORK=off")
	output, err := build.CombinedOutput()
	if err == nil {
		t.Fatal("a consumer module imported the core's internal package")
	}
	if !strings.Contains(string(output), "internal package") {
		t.Fatalf("build failed for some other reason than the internal rule:\n%s", output)
	}
}

// TestLatestPublishedServerInstalls gates the path a user types. A version
// suffix makes go install resolve the published module rather than the
// checkout's go.mod. Current source compilation is covered separately because
// its sibling versions may not have been released in dependency order yet.
func TestLatestPublishedServerInstalls(t *testing.T) {
	installLatest(t, "github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp")
}

// TestLatestPublishedWorkspaceCLIInstalls gates the command workspace/CLI.md
// tells a user to type, the same way.
func TestLatestPublishedWorkspaceCLIInstalls(t *testing.T) {
	installLatest(t, "github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace")
}

// installLatest runs go install for pkg at @latest with the workspace off and
// a throwaway GOBIN, so nothing lands on the caller's PATH.
func installLatest(t *testing.T, pkg string) {
	t.Helper()

	install := exec.Command("go", "install", pkg+"@latest")
	install.Env = append(
		os.Environ(),
		"GOWORK=off",
		"GOBIN="+t.TempDir(),
	)
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("the documented @latest install path does not build: %v\n%s", err, output)
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

// Installation prose must not duplicate release pins. CHANGELOG.md is exempt
// because released versions there are historical records.
func TestNoDocumentationPinsAModuleVersion(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The go tool ignores a name starting with "." or "_", such
			// as a worktree checked out under .claude.
			if name := entry.Name(); path != root &&
				(strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" || entry.Name() == "CHANGELOG.md" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		where, _ := filepath.Rel(root, path)
		for _, match := range readmeInstall.FindAllStringSubmatch(string(content), -1) {
			if _, ok := longestModulePrefix(match[1]); !ok {
				continue
			}
			t.Errorf("%s tells a reader to fetch %s@%s; use @latest, so the "+
				"instruction cannot outlive the release it names",
				where, match[1], match[2])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
