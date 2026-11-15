// Command run-to-completion runs a program in a tmux pane and reads its exit
// status and screen back, the way os/exec would if the program had a terminal.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
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

	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-run-to-completion",
		ConfigFile: os.DevNull,
	})
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

	// docs:run-capability given:ctx context.Context; server tmux.Server
	capability, err := server.Capability(ctx, "program-lifetime")
	if err != nil {
		return err
	}
	if !capability.Supported {
		return fmt.Errorf("managed programs require tmux %s", capability.Since)
	}
	// docs:end

	// docs:run-to-completion given:ctx context.Context; session tmux.Session
	// The command runs with a terminal, so test -t holds where a pipe fails.
	arguments := []string{"-c", "test -t 1 && echo stdout is a terminal; exit 3"}
	result, err := session.RunProgram(ctx, "sh", arguments, tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	for _, line := range result.Lines {
		fmt.Println("screen:", line)
	}
	fmt.Println("exited", result.Status)
	// docs:end

	// docs:run-streaming given:ctx context.Context; session tmux.Session
	// Subscribe before triggering output. The screen returned by Wait remains
	// the final rendered result; this reader follows terminal bytes.
	running, err := session.StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	observation, err := running.Pane().OpenObservation(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, observation.Close()) }()
	if err := running.Pane().SendKeys(ctx, tmux.SendKeysRequest{Command: new("live output"), Literal: true}); err != nil {
		return err
	}
	line, err := bufio.NewReader(observation.Reader(ctx)).ReadString('\n')
	if err != nil {
		return err
	}
	fmt.Println("stream:", strings.TrimSpace(line))
	if err := observation.Close(); err != nil {
		return err
	}
	if err := running.Kill(ctx); err != nil {
		return fmt.Errorf("stop command: %w", err)
	}
	streamed, err := running.Wait(ctx)
	if err != nil {
		return err
	}
	fmt.Println("stopped:", streamed.Signal)
	// docs:end

	// docs:run-input given:ctx context.Context; session tmux.Session
	terminal, err := session.StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		return err
	}
	if err := terminal.Pane().SendKeys(ctx, tmux.SendKeysRequest{Command: new("hello terminal"), Literal: true}); err != nil {
		return err
	}
	if err := terminal.Pane().SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-d"}}); err != nil {
		return err
	}
	echoed, err := terminal.Wait(ctx)
	if err != nil {
		return err
	}
	if echoed.Status != 0 || !slices.Contains(echoed.Lines, "hello terminal") {
		return fmt.Errorf("unexpected terminal result: %v", echoed)
	}
	fmt.Println("input: hello terminal")
	// docs:end
	return nil
}
