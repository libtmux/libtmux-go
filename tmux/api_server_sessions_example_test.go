package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// List all sessions on a private server. Sessions reads a fresh snapshot;
// an absent daemon is an error, not an empty list. Names are sorted here
// only to make the example output independent of tmux ordering.
func ExampleServer_Sessions_complete() {
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
			// Cleanup has its own deadline because the operation may have
			// timed out.
			cleanup, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()
			err := server.Kill(cleanup)
			if err != nil && !errors.Is(err, tmux.ErrNoServer) {
				stopErr := fmt.Errorf("stop server at %s: %w", directory, err)
				runErr = errors.Join(runErr, stopErr)
				return
			}
			runErr = errors.Join(runErr, os.RemoveAll(directory))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range []string{"work", "docs"} {
			if _, err = server.NewSession(ctx, tmux.NewSessionRequest{
				Name:       name,
				WindowName: name + "-editor",
				Command:    "cat",
				Width:      100,
				Height:     30,
			}); err != nil {
				return fmt.Errorf("create %s session: %w", name, err)
			}
		}
		sessions, err := server.Sessions(ctx)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		names := make([]string, 0, len(sessions))
		for _, session := range sessions {
			name, ok := session.Name()
			if !ok {
				return fmt.Errorf("session %s has no name", session.ID())
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
	// [docs work]
}
