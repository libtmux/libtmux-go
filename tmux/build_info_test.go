package tmux

import (
	"runtime/debug"
	"testing"
)

// libtmux:parity libtmux#export:__version__
// libtmux:parity libtmux.__about__.__version__
// libtmux:parity libtmux.common.get_libtmux_version
func TestPackageVersionReadsGoModuleBuildMetadata(t *testing.T) {
	t.Parallel()

	if ModulePath != "github.com/libtmux/libtmux-go/tmux" {
		t.Fatalf("ModulePath = %q", ModulePath)
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
