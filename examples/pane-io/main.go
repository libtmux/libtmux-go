// Command pane-io treats a pane as a pair of streams: type into it with an
// io.Writer and read what it prints back through an io.Reader.
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	// A plain POSIX shell, so the pane does not depend on what your login
	// shell does at startup.
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-pane-io", Command: "sh",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		return fmt.Errorf("resolve pane: %w", err)
	}

	// docs:pane-io
	// Open the reader before typing, so nothing the command prints is missed.
	output, err := pane.OpenObservation(ctx)
	if err != nil {
		return fmt.Errorf("observe pane: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()

	if _, err := fmt.Fprintln(pane.Writer(ctx), "printf 'ready\\n'"); err != nil {
		return fmt.Errorf("type command: %w", err)
	}
	scanner := bufio.NewScanner(output.Reader(ctx))
	for scanner.Scan() {
		if scanner.Text() == "ready" {
			fmt.Println("heard ready")
			return nil
		}
	}
	return fmt.Errorf("read pane: %w", scanner.Err())
	// docs:end
}
