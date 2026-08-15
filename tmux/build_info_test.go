package tmux

import (
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
)

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
// metadata reports for this package's module.
func declaredModulePath(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Path}}").Output()
	if err != nil {
		t.Fatalf("read the declared module path: %v", err)
	}
	return strings.TrimSpace(string(output))
}
