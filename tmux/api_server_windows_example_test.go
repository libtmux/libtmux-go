package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/libtmux/libtmux-go/tmux"
)

// List windows across every session on a private server. Each record is a
// winlink: linking one window into two sessions produces two records.
// This example gives each session one independently created window.
func ExampleServer_Windows_complete() {
	run := func() (runErr error) {
		directory, err := os.MkdirTemp("", "libtmux-go-api-")
		if err != nil {
			return fmt.Errorf("create socket directory: %w", err)
		}
		server, err := tmux.NewServer(tmux.ServerOptions{
			SocketPath: filepath.Join(directory, "tmux.sock"),
			ConfigFile: "/dev/null",
		})
		if err != nil {
			return errors.Join(err, os.RemoveAll(directory))
		}
		defer func() {
			// Cleanup has its own deadline because the operation may have timed out.
			cleanup, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
			defer cancel()
			if err := server.Kill(cleanup); err != nil && !errors.Is(err, tmux.ErrNoServer) {
				runErr = errors.Join(runErr, fmt.Errorf("stop server at %s: %w", directory, err))
				return
			}
			runErr = errors.Join(runErr, os.RemoveAll(directory))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
		defer cancel()
		for _, name := range []string{"work", "docs"} {
			if _, err = server.NewSession(ctx, tmux.NewSessionRequest{
				Name: name, WindowName: name + "-editor", Command: "cat", Width: 100, Height: 30,
			}); err != nil {
				return fmt.Errorf("create %s session: %w", name, err)
			}
		}
		windows, err := server.Windows(ctx)
		if err != nil {
			return fmt.Errorf("list windows: %w", err)
		}
		names := make([]string, 0, len(windows))
		for _, window := range windows {
			name, ok := window.Name()
			if !ok {
				return fmt.Errorf("window %s has no name", window.ID())
			}
			names = append(names, name)
		}
		slices.Sort(names)
		fmt.Println(names)
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// [docs-editor work-editor]
}
