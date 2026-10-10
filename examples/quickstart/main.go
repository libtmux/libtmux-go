// docs:quickstart
// Command quickstart opens a workspace on the selected tmux server.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	server, err = server.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("ensure tmux server: %w", err)
	}
	session, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-go-quickstart", WindowName: "work",
	}, tmux.OwnershipOptions{})
	if err != nil {
		return fmt.Errorf("find or create session: %w", err)
	}
	window, err := session.Value.FindOrCreateWindow(ctx,
		tmux.NewWindowRequest{Name: new("logs")}, tmux.OwnershipOptions{})
	if err != nil {
		return fmt.Errorf("find or create window: %w", err)
	}
	name, _ := window.Value.Name()
	fmt.Println("workspace ready:", name)
	return nil
}

// docs:end
