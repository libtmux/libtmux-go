// Command fast-path runs equivalent reads through a plain Server and an owned
// Connection, then shows their exact-capture boundary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

const searchesPerPath = 10

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

// start owns cleanup because log.Fatal skips deferred calls in main.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	return run(ctx, tmux.ServerOptions{SocketName: "libtmux-go-example-fast-path"})
}

// run accepts options so tests can isolate the server.
func run(ctx context.Context, options tmux.ServerOptions) error {
	server, err := tmux.NewServer(options)
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	if err := readWindows(ctx, session, searchesPerPath); err != nil {
		return err
	}
	fmt.Printf("process path: %d searches\n", searchesPerPath)

	// Keep the connection-bound record; the original still starts subprocesses.
	// docs:control-pool
	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		return fmt.Errorf("open control connection: %w", err)
	}
	defer func() { _ = connection.Close() }()
	connected := connection.Session()
	// docs:end

	if err := readWindows(ctx, connected, searchesPerPath); err != nil {
		return err
	}
	fmt.Printf("connection path: %d searches\n", searchesPerPath)

	// Arbitrary pane output can end a control frame. Exact printed capture needs
	// the original subprocess handle; file staging stays on the connection.
	processPane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		return fmt.Errorf("resolve process pane: %w", err)
	}
	connectedPane, ok, err := connected.ResolveActivePane(ctx)
	if err != nil || !ok {
		return fmt.Errorf("resolve connected pane: %w", err)
	}
	if _, err := connectedPane.Capture(ctx, tmux.CapturePaneRequest{}); !errors.Is(
		err,
		tmux.ErrConnectionRequiresProcess,
	) {
		if err == nil {
			return errors.New("connected printed capture unexpectedly succeeded")
		}
		return fmt.Errorf("refuse connected printed capture: %w", err)
	}
	directory, err := os.MkdirTemp("", "fast-path")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()

	if _, err := processPane.Capture(ctx, tmux.CapturePaneRequest{}); err != nil {
		return fmt.Errorf("capture: %w", err)
	}

	if _, err := connectedPane.CaptureToFile(
		ctx, filepath.Join(directory, "pane.txt"), tmux.CapturePaneRequest{},
	); err != nil {
		return fmt.Errorf("capture to file: %w", err)
	}
	fmt.Println("printed capture: process path")
	fmt.Println("file capture: connection path")
	return nil
}

func readWindows(ctx context.Context, session tmux.Session, count int) error {
	for range count {
		if _, err := session.SearchWindows(ctx, nil); err != nil {
			return fmt.Errorf("search windows: %w", err)
		}
	}
	return nil
}
