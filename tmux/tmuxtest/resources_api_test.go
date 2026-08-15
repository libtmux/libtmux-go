package tmuxtest_test

import (
	"testing"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestTemporaryResourcePublicSurfaceCompiles(_ *testing.T) {
	_ = tmuxtest.TemporaryNamePrefix
	_ = tmuxtest.NewSession
	_ = tmuxtest.NewWindow
}
