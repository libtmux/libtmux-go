// Command filter-query demonstrates snapshot predicates and live tmux filters.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxq"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{}).WithStrictErrors()
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
