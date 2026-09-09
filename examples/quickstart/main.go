// Command quickstart demonstrates a complete session, window, and pane lifecycle.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

// start owns cleanup because log.Fatal skips deferred calls in main.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-go-quickstart", WindowName: "start",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	// docs:quickstart
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: new("work")})
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight, Command: "sh",
	})
	if err != nil {
		return fmt.Errorf("split window: %w", err)
	}
	output, err := pane.OpenObservation(ctx)
	if err != nil {
		return fmt.Errorf("watch pane: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	if _, err := fmt.Fprintln(pane.Writer(ctx), "printf 'libtmux ready\\n'"); err != nil {
		return fmt.Errorf("send command: %w", err)
	}
	// docs:end

	scanner := bufio.NewScanner(output.Reader(ctx))
	for scanner.Scan() {
		if scanner.Text() == "libtmux ready" {
			fmt.Println("libtmux ready")
			return nil
		}
	}
	return fmt.Errorf("read pane: %w", scanner.Err())
}
