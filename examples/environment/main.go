// Command environment demonstrates session environment access and pane discovery.
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
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-environment"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	windowName := "discovery"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: &windowName})
	if err != nil {
		return err
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		return err
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_EXAMPLE", "ready", tmux.SetEnvironmentOptions{}); err != nil {
		return err
	}
	value, present, err := session.GetEnvironment(ctx, "LIBTMUX_EXAMPLE")
	if err != nil || !present {
		return errors.Join(err, errors.New("session environment value is absent"))
	}

	result, err := server.Cmd(ctx, "display-message", "-p", "-t", pane.ID().String(), "#{socket_path}")
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
		return errors.Join(err, errors.New("resolve tmux socket path"))
	}
	environment := map[string]string{
		"TMUX":      fmt.Sprintf("%s,1,%s", result.Stdout[0], session.ID()),
		"TMUX_PANE": pane.ID().String(),
	}
	discovered, err := tmux.PaneFromEnv(ctx, environment)
	if err != nil {
		return err
	}
	fmt.Println(value.Value, discovered.ID() == pane.ID())
	return nil
}
