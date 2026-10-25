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

// Type a command into a shell and wait for what it prints. The wait polls the
// visible screen on every supported tmux release, and the typed line is
// already on that screen, so the text names the answer and not the command.
func ExamplePane_WaitForText() {
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
		pane, err := session.ResolveActivePane(ctx)
		if err != nil {
			return fmt.Errorf("resolve pane: %w", err)
		}

		// docs:pane-wait-text given:ctx context.Context; pane tmux.Pane
		// A pane is an io.Writer, and a newline is the Enter key.
		command := `echo "6 x 7 = $((6 * 7))"`
		if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
			return fmt.Errorf("type command: %w", err)
		}
		// The screen already holds the typed line, which says "$((6 * 7))"
		// and never "42", so the answer is what the wait names.
		if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
			Text: "6 x 7 = 42",
		}); err != nil {
			return fmt.Errorf("wait for answer: %w", err)
		}
		fmt.Println("the pane answered 6 x 7 = 42")
		// docs:end
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// the pane answered 6 x 7 = 42
}
