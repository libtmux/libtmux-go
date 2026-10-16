package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

// Create a detached session named work with an editor window running cat.
// A second creation with the same name returns ErrSessionExists. Creation
// returns a session record without captured window relations; use a live
// lookup or a snapshot when those relations are needed.
func ExampleServer_NewSession_complete() {
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
		session, err := server.NewSession(ctx, tmux.NewSessionRequest{
			Name: "work", WindowName: "editor", Command: "cat", Width: 100, Height: 30,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		name, ok := session.Name()
		if !ok {
			return errors.New("created session has no name")
		}
		fmt.Println("session:", name)
		_, relations := session.Windows()
		fmt.Println("window relations captured:", relations)
		_, err = server.NewSession(ctx, tmux.NewSessionRequest{Name: "work", Command: "cat"})
		if !errors.Is(err, tmux.ErrSessionExists) {
			return errors.Join(errors.New("expected duplicate session error"), err)
		}
		fmt.Println("duplicate rejected: true")
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// session: work
	// window relations captured: false
	// duplicate rejected: true
}
