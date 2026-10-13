// Command object-model shows that the values tmux hands back are records.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-object-model",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "old"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// Rename hands back the session as tmux now reports it. The record
	// already in hand keeps the name it had when tmux handed it over.
	renamed, err := session.Rename(ctx, "new")
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	held, _ := session.Name()
	current, _ := renamed.Name()
	fmt.Println("held record:", held)
	fmt.Println("returned record:", current)

	// docs:not-found given:ctx context.Context; server tmux.Server
	// A lookup that finds nothing is an error a caller can test for.
	_, lookupErr := server.Session(ctx, tmux.SessionID("$999"))
	if errors.Is(lookupErr, tmux.ErrNotFound) {
		fmt.Println("session $999: not found")
	}
	// docs:end
	return nil
}
