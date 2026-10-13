package main

import (
	"os"
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestPlannedBuild(t *testing.T) {
	exampletest.Server(t, run)
}
