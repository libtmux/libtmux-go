// Command control-mode-subscribe watches what a tmux server does, rather than
// asking it repeatedly.
//
// A control-mode connection is a tmux client that stays open. tmux pushes what
// happens down it, so a change is heard once, when it happens, instead of being
// discovered by a poll that has to guess how often to ask.
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

// start owns the context and the server, so that main does nothing but
// report a failure. log.Fatal exits without running deferred calls, and the
// cancel below has to run.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmux.NewServer(tmux.ServerOptions{})
	return run(ctx, server)
}

// run holds the example itself, so that main runs it against a socket of this
// example's own and the test beside it runs the same code against a server the
// test harness throws away.
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

	control, err := server.OpenControl(ctx, session)
	if err != nil {
		return fmt.Errorf("open control connection: %w", err)
	}
	defer func() { err = errors.Join(err, control.Close()) }()

	// Renamed after the connection is open, so the notification it causes is one
	// this connection is there to hear. A rename done first would be history the
	// stream never mentions.
	if _, err := session.Rename(ctx, "control-example"); err != nil {
		return fmt.Errorf("rename session: %w", err)
	}

	// docs:watching
	for notification, err := range control.Notifications(ctx) {
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
	return errors.New("control stream ended before the rename it was watching for")
}
