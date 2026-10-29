// Command object-model shows that the values tmux hands back are records.
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
	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-object-model",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "old"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// docs:records given:ctx context.Context; session tmux.Session
	// Rename hands back the session as tmux now reports it. The record
	// already in hand keeps the name it had when tmux handed it over.
	renamed, err := session.Rename(ctx, "new")
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	held, _ := session.Name()
	current, _ := renamed.Name()
	fmt.Println("held record:", held)
	fmt.Println("returned record:", current)
	// docs:end

	// docs:record-reads given:ctx context.Context; renamed tmux.Session
	// Windows reads the record and takes no context. A session that came
	// from a call about the session carries no windows, and says so.
	if _, held := renamed.Windows(); !held {
		fmt.Println("the record holds no windows")
	}
	// SearchWindows asks tmux.
	windows, err := renamed.SearchWindows(ctx, nil)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	fmt.Println("tmux reports windows:", len(windows))
	// docs:end

	// docs:not-found given:ctx context.Context; server tmux.Server
	// A lookup that finds nothing is an error a caller can test for.
	_, lookupErr := server.Session(ctx, tmux.SessionID("$999"))
	if errors.Is(lookupErr, tmux.ErrNotFound) {
		fmt.Println("session $999: not found")
	}
	// docs:end

	// docs:cmd-result given:ctx context.Context; server tmux.Server
	// Cmd sends arguments you write yourself. A command tmux ran and
	// refused comes back as a result, with the error left nil.
	result, err := server.Cmd(ctx, "kill-window", "-t", "@999")
	if err != nil {
		return fmt.Errorf("reach tmux: %w", err)
	}
	fmt.Println("kill-window exit status:", result.ExitCode)
	// docs:end
	return nil
}
