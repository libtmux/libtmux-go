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

// Add a logs window to an existing session without selecting it. NewWindow
// returns the newly materialized window and its initial pane relation;
// the session record from NewSession still carries no window relations.
func ExampleSession_NewWindow_complete() {
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
		session, err := server.NewSession(ctx, tmux.NewSessionRequest{
			Name:       "work",
			WindowName: "editor",
			Command:    "cat",
			Width:      100,
			Height:     30,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		name := "logs"
		window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
			Name: &name, Command: "cat",
		})
		if err != nil {
			return fmt.Errorf("create logs window: %w", err)
		}
		windowName, ok := window.Name()
		if !ok {
			return errors.New("created window has no name")
		}
		panes, relations := window.Panes()
		fmt.Println("window:", windowName)
		fmt.Println("initial panes:", len(panes), "captured:", relations)
		windows, err := session.SearchWindows(ctx, nil)
		if err != nil {
			return fmt.Errorf("list current windows: %w", err)
		}
		fmt.Println("live windows:", len(windows))
		current, err := session.ResolveActiveWindow(ctx)
		if err != nil {
			return fmt.Errorf("resolve current window: %w", err)
		}
		currentName, ok := current.Name()
		if !ok {
			return errors.New("current window has no name")
		}
		fmt.Println("current window:", currentName)
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// window: logs
	// initial panes: 1 captured: true
	// live windows: 2
	// current window: editor
}
