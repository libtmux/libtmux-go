package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

// List pane records across every session and window on a private server.
// Panes materializes each linked view, so callers that link windows may see
// the same underlying pane ID in more than one session.
func ExampleServer_Panes_complete() {
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
		panes, err := server.Panes(ctx)
		if err != nil {
			return fmt.Errorf("list panes: %w", err)
		}
		for _, pane := range panes {
			if pane.ID() == "" {
				return errors.New("pane has no stable ID")
			}
		}
		fmt.Println("panes:", len(panes))
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// panes: 2
}
