package tmux_test

import (
	"testing"

	"github.com/libtmux/libtmux-go"
)

// libtmux:parity libtmux#export:__version__
// libtmux:parity libtmux.__about__.__version__
func TestPackageBuildMetadataSurfaceCompiles(t *testing.T) {
	t.Parallel()

	requireAssignable[string](tmux.ModulePath)
	requireAssignable[func() (string, bool)](tmux.PackageVersion)
	if tmux.ModulePath == "" {
		t.Fatal("package build metadata surface is unavailable")
	}
}
