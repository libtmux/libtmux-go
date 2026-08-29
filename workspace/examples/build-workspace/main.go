// Command build-workspace builds an embedded tmuxp-style workspace, then
// reports its topology.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
)

const document = `
session_name: example-project
start_directory: /tmp
environment:
  PROJECT_ENV: development
options:
  history-limit: '5000'
windows:
  - window_name: editor
    layout: main-vertical
    focus: true
    panes:
      - shell: sh -c 'sleep 300'
      - shell: sh -c 'sleep 300'
  - window_name: logs
    panes:
      - shell: sh -c 'sleep 300'
`

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	described, err := workspace.Parse([]byte(document))
	if err != nil {
		// Parse rejects a field it does not recognise and a value tmux could
		// not accept, so a workspace that loads is one worth building.
		return fmt.Errorf("parse: %w", err)
	}

	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-build-workspace",
	})
	if err != nil {
		return fmt.Errorf("construct tmux server: %w", err)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	}()

	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		// Build is not atomic, so the returned session identifies whatever was
		// created before the failure.
		return fmt.Errorf("build %q: %w", described.SessionName, err)
	}

	// The returned record comes from a creation call, so its relations are
	// empty. Reading what exists means asking tmux again.
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		return fmt.Errorf("search windows: %w", err)
	}
	fmt.Printf("built %q with %d windows\n", described.SessionName, len(windows))
	for _, window := range windows {
		name, _ := window.Formats().WindowName()
		index, _ := window.Formats().WindowIndex()
		panes, err := window.SearchPanes(ctx, nil)
		if err != nil {
			return fmt.Errorf("search panes: %w", err)
		}
		fmt.Printf("  window %d %-8s %d pane(s)\n", index, name, len(panes))
	}
	return nil
}
