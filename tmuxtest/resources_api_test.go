package tmuxtest_test

import (
	"testing"

	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

func TestTemporaryResourcePublicSurfaceCompiles(_ *testing.T) {
	_ = tmuxtest.TemporaryNamePrefix
	_ = tmuxtest.NewSession
	_ = tmuxtest.NewWindow
}
