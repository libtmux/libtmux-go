package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// tmuxModulePath is the core module, named so that a helper resolving the
// repository root picks it rather than whichever module a workspace lists
// first.
const tmuxModulePath = "github.com/libtmux/libtmux-go"

// modules are every module in this repository, by directory.
var modules = []string{".", "examples", "workspace", "mcp", "benchmarks"}

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

// Go internal visibility follows directory ancestry, not module boundaries.
// Keeping internal beneath tmux prevents sibling consumer modules importing it.
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

// TestLatestPublishedServerInstalls gates the path a user types. A version
// suffix makes go install resolve the published module rather than the
// checkout's go.mod. Current source compilation is covered separately because
// its sibling versions may not have been released in dependency order yet.
func TestLatestPublishedServerInstalls(t *testing.T) {
	install := exec.Command(
		"go", "install", "github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest",
	)
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
			if entry.Name() == ".git" {
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
