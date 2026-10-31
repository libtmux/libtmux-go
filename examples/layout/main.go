// Command layout builds two panes side by side and reads back what tmux made.
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
	// docs:private-server
	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-layout",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// docs:end
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "work", WindowName: "editor",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return fmt.Errorf("find window: %w", err)
	}
	// Split the window's active pane, and put the new pane on the right.
	split := tmux.SplitPaneRequest{Direction: tmux.PaneDirectionRight}
	if _, err := window.SplitPane(ctx, split); err != nil {
		return fmt.Errorf("split window: %w", err)
	}

	// Read back what tmux made, rather than trusting what was asked for.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read tmux: %w", err)
	}
	for _, pane := range snapshot.Panes() {
		width, _ := pane.Width()
		fmt.Printf("pane %d: %d columns\n", pane.Index(), width)
	}
	return nil
}
