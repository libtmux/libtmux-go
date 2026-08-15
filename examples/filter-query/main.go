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

// start owns the context and the server, so that main does nothing but
// report a failure. log.Fatal exits without running deferred calls, and the
// cancel below has to run.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmux.NewServer(tmux.ServerOptions{}).WithStrictErrors()
	return run(ctx, server)
}

// run holds the example itself, so that main runs it against a socket of this
// example's own and the test beside it runs the same code against a server the
// test harness throws away.
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

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return err
	}
	predicate, err := tmux.PaneActiveIs(true).Predicate()
	if err != nil {
		return err
	}
	active := tmuxq.Where(snapshot.Panes(), predicate)
	fmt.Println("active panes:", len(active))

	live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
	sessions, err := server.SearchSessions(ctx, &live)
	if err != nil {
		return err
	}
	fmt.Println("live matches:", len(sessions))
	return nil
}
