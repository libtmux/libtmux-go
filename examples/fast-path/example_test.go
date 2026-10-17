package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

// TestFastPath uses a short isolated socket below tmux's path limit.
func TestFastPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	options := tmux.ServerOptions{SocketPath: filepath.Join(t.TempDir(), "tmux.sock")}
	server, err := tmux.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	printed := exampletest.Output(t, func() error { return run(ctx, options) })

	exampletest.MatchesFile(t, printed, "output.txt")
}
