// Command snapshot-browser demonstrates hierarchical snapshot traversal.
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

	server := tmux.NewServer(tmux.ServerOptions{}).WithStrictErrors()
	return run(ctx, server)
}

// run holds the example itself, so that main runs it against a socket of this
// example's own and the test beside it runs the same code against a server the
// test harness throws away.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-snapshot", WindowName: "browser",
	})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, session := range snapshot.Sessions() {
		name, _ := session.Name()
		fmt.Printf("session %s %q\n", session.ID(), name)
		for _, window := range session.Windows() {
			fmt.Printf("  window %s:%d\n", window.ID(), window.Index())
			for _, pane := range window.Panes() {
				command, _ := pane.CurrentCommand()
				fmt.Printf("    pane %s %q\n", pane.ID(), command)
			}
		}
	}
	return nil
}
