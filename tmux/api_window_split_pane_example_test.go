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

// Split a window, size the new pane, type into it and read it back. The new
// pane runs cat, so the terminal's echo of the typed line is its first line
// of output. WaitForText stands between the keys and the capture because
// Capture is a point-in-time read.
func ExampleWindow_SplitPane_complete() {
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
			Name: "work", Command: "sh", Width: 100, Height: 30,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}

		window, err := session.ResolveActiveWindow(ctx)
		if err != nil {
			return fmt.Errorf("find window: %w", err)
		}
		pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
			Direction: tmux.PaneDirectionRight, Command: "cat",
		})
		if err != nil {
			return fmt.Errorf("split window: %w", err)
		}
		pane, err = pane.Resize(ctx, tmux.ResizePaneRequest{Width: tmux.PaneCells(20)})
		if err != nil {
			return fmt.Errorf("resize pane: %w", err)
		}
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: new("hello")}); err != nil {
			return fmt.Errorf("send keys: %w", err)
		}
		if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{Text: "hello"}); err != nil {
			return fmt.Errorf("wait for text: %w", err)
		}
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return fmt.Errorf("capture pane: %w", err)
		}
		width, _ := pane.Width()
		fmt.Printf("%d columns, showing %q\n", width, lines[0])
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// 20 columns, showing "hello"
}
