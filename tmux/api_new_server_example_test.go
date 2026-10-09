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

func ExampleNewServer_environmentDefaults() {
	server, err := tmux.NewServer(tmux.ServerOptions{
		ProcessEnvironment: []string{
			"PATH=" + os.Getenv("PATH"),
			"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/example.sock",
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(server.SocketPath())
	// Output: /tmp/libtmux-go-test/example.sock
}

// Construct a server handle for a private socket and check that construction
// does not start tmux. Creating the first session starts the daemon.
// The temporary server is stopped even when an operation fails.
func ExampleNewServer_complete() {
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
		if err := server.CheckAlive(ctx); !errors.Is(err, tmux.ErrNoServer) {
			return errors.Join(errors.New("expected no daemon before session creation"), err)
		}
		fmt.Println("construction started tmux: false")
		_, err = server.NewSession(ctx, tmux.NewSessionRequest{
			Name: "work", WindowName: "editor", Command: "cat", Width: 100, Height: 30,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		if err := server.CheckAlive(ctx); err != nil {
			return fmt.Errorf("check created server: %w", err)
		}
		fmt.Println("session started tmux: true")
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// construction started tmux: false
	// session started tmux: true
}
