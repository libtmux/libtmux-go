package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// Open a private server and create a session on it. Find that session, then
// read the server once and walk from a pane up to its window and session
// without another tmux command. A search asks tmux to filter instead.
// The temporary server is stopped even when an operation fails.
func ExampleServer_complete() {
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
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Kill(cleanup); err != nil && !errors.Is(err, tmux.ErrNoServer) {
				runErr = errors.Join(runErr, fmt.Errorf("stop server at %s: %w", directory, err))
				return
			}
			runErr = errors.Join(runErr, os.RemoveAll(directory))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err = server.NewSession(ctx, tmux.NewSessionRequest{
			Name: "work", WindowName: "editor", Command: "cat", Width: 100, Height: 30,
		}); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		found, err := server.HasSession(ctx, tmux.HasSessionRequest{Target: "work"})
		if err != nil {
			return fmt.Errorf("find session: %w", err)
		}
		fmt.Println("has work:", found)
		_, err = server.SessionByName(ctx, "missing")
		fmt.Println("missing is not found:", errors.Is(err, tmux.ErrNotFound))
		snapshot, err := server.Snapshot(ctx)
		if err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		panes := snapshot.Panes()
		if len(panes) == 0 {
			return errors.New("snapshot holds no panes")
		}
		window, ok := panes[0].Window()
		if !ok {
			return errors.New("pane lost its window")
		}
		session, ok := window.Session()
		if !ok {
			return errors.New("window lost its session")
		}
		name, _ := session.Name()
		fmt.Println("panes:", len(panes), "session:", name)
		filter := tmux.TmuxFilter("#{==:#{session_name},work}")
		matched, err := server.SearchPanes(ctx, &filter)
		if err != nil {
			return fmt.Errorf("search panes: %w", err)
		}
		fmt.Println("matched:", len(matched))
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// has work: true
	// missing is not found: true
	// panes: 1 session: work
	// matched: 1
}
