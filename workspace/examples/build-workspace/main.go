// Command build-workspace builds an embedded tmuxp-style workspace over control
// mode, then reports the topology and tmux process count.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
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

	var mutex sync.Mutex
	var processes int
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-build-workspace",
		Runner: tmux.CommandRunnerFunc(func(
			ctx context.Context,
			request tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			mutex.Lock()
			processes++
			mutex.Unlock()
			return tmux.SubprocessRunner().Run(ctx, request)
		}),
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
	mutex.Lock()
	started := processes
	mutex.Unlock()

	fmt.Printf("built %q with %d windows, using %d tmux processes\n",
		described.SessionName, len(windows), started)
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
