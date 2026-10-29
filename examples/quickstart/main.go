// Command quickstart runs a command in tmux and prints what it showed.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	directory, err := os.MkdirTemp("", "libtmux-quickstart-")
	if err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()

	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(directory, "tmux.sock"),
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		if cleanup := server.Kill(context.WithoutCancel(ctx)); !errors.Is(cleanup, tmux.ErrNoServer) {
			err = errors.Join(err, cleanup)
		}
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// The command runs in a window of its own, with a terminal.
	result, err := session.Run(ctx, "echo hello from tmux", tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	for _, line := range result.Lines {
		fmt.Println(line)
	}
	fmt.Println("exit status:", result.Status)
	return nil
}
