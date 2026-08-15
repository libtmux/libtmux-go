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
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{}).WithStrictErrors()
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
