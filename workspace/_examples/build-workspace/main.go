// Command build-workspace loads a tmuxp-style YAML workspace and builds it,
// then reports what tmux actually created.
//
// It reads the workspace from its own source rather than from a file so the
// program runs anywhere, and it counts the tmux processes the build started.
// That count barely moves as a file grows, because the build runs over a
// control connection: the windows and panes below cost about what a file ten
// times the size would.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/workspace"
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
	server := tmux.NewServer(tmux.ServerOptions{
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
	}).WithStrictErrors()
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
