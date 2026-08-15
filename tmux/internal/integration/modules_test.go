package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// modules are every module in this repository, by directory.
var modules = []string{".", "examples", "workspace", "mcp", "benchmarks"}

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
			build := exec.Command("go", "build", "./...")
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
