// Command pane-io treats a pane as a pair of streams: type into it with an
// io.Writer and read what it prints back through an io.Reader.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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
		SocketName: "libtmux-pane-io",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	// A plain POSIX shell, so the pane does not depend on what your login
	// shell does at startup.
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-pane-io", Command: "sh",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()
	pane, err := session.ResolveActivePane(ctx)
	if err != nil {
		return fmt.Errorf("resolve pane: %w", err)
	}

	// docs:pane-wait given:ctx context.Context; pane tmux.Pane
	// Open the observation before typing, so no output is missed.
	output, err := pane.OpenObservation(ctx)
	if err != nil {
		return fmt.Errorf("observe pane: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()

	// A pane is an io.Writer, and a newline is the Enter key.
	command := `echo "6 x 7 = $((6 * 7))"`
	if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
		return fmt.Errorf("type command: %w", err)
	}
	// The terminal echoes what is typed, so wait for the answer: the typed
	// line says "$((6 * 7))" and never "42".
	if _, err := output.WaitFor(ctx, func(text string) bool {
		return strings.Contains(text, "6 x 7 = 42")
	}); err != nil {
		return fmt.Errorf("wait for answer: %w", err)
	}
	fmt.Println("the pane answered 6 x 7 = 42")
	// docs:end

	// docs:pane-io given:ctx context.Context; pane tmux.Pane; output *tmux.PaneObservation
	// The observation is also an io.Reader over what the pane prints next, so
	// whatever reads a stream reads a pane.
	next := `echo "streamed $((1 + 1)) lines"`
	if _, err := fmt.Fprintln(pane.Writer(ctx), next); err != nil {
		return fmt.Errorf("type command: %w", err)
	}
	scanner := bufio.NewScanner(output.Reader(ctx))
	for scanner.Scan() {
		// These are the terminal's bytes: the echo comes first, and a prompt
		// can share the reply's line. Match how the reply ends.
		if strings.HasSuffix(scanner.Text(), "streamed 2 lines") {
			fmt.Println("read the line: streamed 2 lines")
			return nil
		}
	}
	return fmt.Errorf("read pane: %w", scanner.Err())
	// docs:end
}
