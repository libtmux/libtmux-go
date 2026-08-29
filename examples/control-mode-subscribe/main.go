// Command control-mode-subscribe watches a persistent tmux control client
// receive changes without polling.
package main

import (
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
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-control"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	stream, err := session.OpenNotifications(ctx)
	if err != nil {
		return fmt.Errorf("open notification stream: %w", err)
	}
	defer func() { err = errors.Join(err, stream.Close()) }()

	// Rename after subscribing; notifications do not include earlier changes.
	if _, err := session.Rename(ctx, "control-example"); err != nil {
		return fmt.Errorf("rename session: %w", err)
	}

	// docs:watching
	for {
		notification, err := stream.Next(ctx)
		if err != nil {
			return fmt.Errorf("read notification: %w", err)
		}
		fmt.Printf("notification: %s\n", notification.Kind())
		if notification.Kind() == tmux.ControlNotificationSessionRenamed {
			fmt.Println("heard the rename")
			return nil
		}
	}
	// docs:end
}
