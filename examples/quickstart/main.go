// Command quickstart demonstrates a complete session, window, and pane lifecycle.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

// start owns the context and the server, so that main does nothing but
// report a failure. log.Fatal exits without running deferred calls, and the
// cancel below has to run.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := tmux.NewServer(tmux.ServerOptions{})
	return run(ctx, server)
}

// run holds the example itself, so that main runs it against a socket of this
// example's own and the test beside it runs the same code against a server the
// test harness throws away.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-go-quickstart", WindowName: "start",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	windowName := "work"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: &windowName})
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
	})
	if err != nil {
		return fmt.Errorf("split window: %w", err)
	}
	command := "printf 'libtmux ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command, Literal: true}); err != nil {
		return fmt.Errorf("send command: %w", err)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start: tmux.CaptureBoundary,
			End:   tmux.CaptureBoundary,
		})
		if err != nil {
			return fmt.Errorf("capture pane: %w", err)
		}
		if slices.Contains(lines, "libtmux ready") {
			fmt.Println("libtmux ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
