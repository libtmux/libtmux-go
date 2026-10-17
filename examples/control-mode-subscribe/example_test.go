package main

import (
	"context"
	"os"
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestControlModeSubscribe(t *testing.T) {
	exampletest.Server(t, func(ctx context.Context, server tmux.Server) error {
		tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
		return run(ctx, server)
	})
}
