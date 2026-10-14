// Command build-workspace builds a tmuxp-style workspace and prints what tmux
// made of it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
)

// document is a workspace file. A program of your own would read one from
// disk with os.ReadFile.
const document = `
session_name: project
windows:
  - window_name: editor
    layout: main-vertical
    panes:
      - echo editing
      - echo testing
  - window_name: logs
    panes:
      - echo api
      - echo worker
      - echo database
`

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	// Parse refuses a field it does not know rather than dropping it, so a
	// workspace that loads is one that was understood.
	parsed, err := workspace.Parse([]byte(document))
	if err != nil {
		return fmt.Errorf("parse workspace: %w", err)
	}

	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-build-workspace",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		return fmt.Errorf("build workspace: %w", err)
	}

	// Ask tmux what exists now, rather than repeating what the file asked for.
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	name, _ := session.Name()
	fmt.Printf("session %s: %d windows\n", name, len(windows))
	for _, window := range windows {
		panes, err := window.SearchPanes(ctx, nil)
		if err != nil {
			return fmt.Errorf("list panes: %w", err)
		}
		windowName, _ := window.Name()
		fmt.Printf("window %s: %d panes\n", windowName, len(panes))
	}
	return nil
}
