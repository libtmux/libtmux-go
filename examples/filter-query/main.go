// Command filter-query demonstrates snapshot predicates and live tmux filters.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmuxq"
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
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-filter"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	// docs:query-in-go
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return err
	}
	predicate, err := tmux.PaneActiveIs(true).Predicate()
	if err != nil {
		return err
	}
	active := tmuxq.Where(snapshot.Panes(), predicate)
	// docs:end
	fmt.Println("active panes:", len(active))

	// docs:query-in-tmux
	live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
	sessions, err := server.SearchSessions(ctx, &live)
	// docs:end
	if err != nil {
		return err
	}
	fmt.Println("live matches:", len(sessions))
	return nil
}
