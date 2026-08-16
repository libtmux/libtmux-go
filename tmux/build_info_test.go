package tmux

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// callerFile is this file's own path, which locates the module root beside it.
func callerFile() (uintptr, string, bool) {
	pc, filename, _, ok := runtime.Caller(0)
	return pc, filename, ok
}

// libtmux:parity libtmux#export:__version__
// libtmux:parity libtmux.__about__.__version__
// libtmux:parity libtmux.common.get_libtmux_version
func TestPackageVersionReadsGoModuleBuildMetadata(t *testing.T) {
	t.Parallel()

	// Read it from go.mod rather than repeating the string. Asserting the
	// constant against itself is what let this drift: the package moved into a
	// directory, the constant followed the import path, and every test kept
	// passing because each one built its fixture from the constant.
	declared := declaredModulePath(t)
	if ModulePath != declared {
		t.Fatalf("ModulePath = %q, but go.mod declares %q", ModulePath, declared)
	}

	tests := []struct {
		name   string
		info   *debug.BuildInfo
		want   string
		wantOK bool
	}{
		{
			name: "main module release",
			info: &debug.BuildInfo{Main: debug.Module{
				Path: ModulePath, Version: "v0.62.0",
			}},
			want:   "v0.62.0",
			wantOK: true,
		},
		{
			name: "dependency release",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.invalid/application"},
				Deps: []*debug.Module{{Path: ModulePath, Version: "v0.61.0"}},
			},
			want:   "v0.61.0",
			wantOK: true,
		},
		{
			name: "development main module",
			info: &debug.BuildInfo{Main: debug.Module{
				Path: ModulePath, Version: "(devel)",
			}},
		},
		{
			name: "unrelated build",
			info: &debug.BuildInfo{Main: debug.Module{
				Path: "example.invalid/application", Version: "v1.0.0",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := packageVersionFromBuildInfo(test.info)
			if got != test.want || ok != test.wantOK {
				t.Fatalf("packageVersionFromBuildInfo() = (%q, %t), want (%q, %t)",
					got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestPackageVersionMatchesCurrentBuildInfo(t *testing.T) {
	t.Parallel()

	info, available := debug.ReadBuildInfo()
	want, wantOK := packageVersionFromBuildInfo(info)
	got, gotOK := PackageVersion()
	if !available {
		want, wantOK = "", false
	}
	if got != want || gotOK != wantOK {
		t.Fatalf("PackageVersion() = (%q, %t), want (%q, %t)", got, gotOK, want, wantOK)
	}
}

// declaredModulePath is the module path in go.mod, which is what Go build
// metadata reports for this package's module. It reads the file rather than
// asking the go command, which reports every module of a workspace.
func declaredModulePath(t *testing.T) string {
	t.Helper()

	_, filename, ok := callerFile()
	if !ok {
		t.Fatal("resolve this test's own path")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(filename)), "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if declared, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			return declared
		}
	}
	t.Fatal("go.mod declares no module path")
	return ""
}
