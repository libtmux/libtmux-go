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

// Send literal text followed by Enter to a pane running cat, then wait for
// the text to appear. Literal controls tmux key parsing; it does not prevent
// the pane's application from interpreting input. SendKeys returning does
// not establish that the application has processed the text.
func ExamplePane_SendKeys_complete() {
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
		session, err := server.NewSession(ctx, tmux.NewSessionRequest{
			Name: "work", WindowName: "editor", Command: "cat", Width: 100, Height: 30,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		pane, err := session.ResolveActivePane(ctx)
		if err != nil {
			return fmt.Errorf("resolve pane: %w", err)
		}
		text := "api input"
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &text, Literal: true}); err != nil {
			return fmt.Errorf("send literal input: %w", err)
		}
		// Capture is a point-in-time read, so wait for a complete matching line.
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{
				Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary,
			})
			if err != nil {
				return fmt.Errorf("capture pane: %w", err)
			}
			if slices.Contains(lines, "api input") {
				fmt.Println("captured: api input")
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("wait for pane text: %w", ctx.Err())
			case <-ticker.C:
			}
		}
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// captured: api input
}
