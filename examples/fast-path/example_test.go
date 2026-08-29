package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	exampletest.RequireTmux(t, ctx, tmuxtest.NewServer(ctx, t), "3.6")
	options := tmux.ServerOptions{SocketPath: filepath.Join(t.TempDir(), "tmux.sock")}
	printed := exampletest.Output(t, func() error { return run(ctx, options) })

	for _, want := range []string{
		"process path: 10 searches",
		"connection path: 10 searches",
		"printed capture: process path",
		"file capture: connection path",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("printed %q, want it to contain %q", printed, want)
		}
	}
}
