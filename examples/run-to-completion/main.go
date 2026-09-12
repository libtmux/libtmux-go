// Command run-to-completion runs a program in a tmux pane and reads its exit
// status and screen back, the way os/exec would if the program had a terminal.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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

	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-run"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	// docs:run-to-completion
	result, err := session.Run(ctx, "tty; exit 3", tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	for _, line := range result.Lines {
		fmt.Println("screen:", line)
	}
	fmt.Println("exited", result.Status)
	// docs:end

	// docs:run-streaming
	// Start returns while the command is still running, so its output can be
	// followed and it can be stopped from another goroutine. The stream begins
	// where StreamTo opens it, so this command waits before its first line;
	// result.Lines holds the screen either way.
	running, err := session.Start(ctx, "sleep 1; seq 1 3; sleep 30", tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	stopped := make(chan error, 1)
	go func() {
		time.Sleep(2 * time.Second)
		stopped <- running.Kill(ctx)
	}()
	streamed, err := running.StreamTo(ctx, os.Stdout)
	if err != nil {
		return fmt.Errorf("stream command: %w", err)
	}
	if err := <-stopped; err != nil {
		return fmt.Errorf("stop command: %w", err)
	}
	fmt.Println("stopped by signal", streamed.Signal)
	// docs:end
	return nil
}
