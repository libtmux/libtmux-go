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

// start owns cleanup because log.Fatal skips deferred calls in main.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmux.NewServer(tmux.ServerOptions{})
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
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
		// Snapshot records carry relations; point lookups report them unavailable.
		windows, _ := session.Windows()
		for _, window := range windows {
			fmt.Printf("  window %s:%d\n", window.ID(), window.Index())
			panes, _ := window.Panes()
			for _, pane := range panes {
				command, _ := pane.CurrentCommand()
				fmt.Printf("    pane %s %q\n", pane.ID(), command)
			}
		}
	}
	return nil
}
