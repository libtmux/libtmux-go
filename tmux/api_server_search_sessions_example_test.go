package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// Filter live sessions by name using tmux's format expression syntax.
// A nil filter lists all sessions. No matches returns an empty slice;
// the returned session records do not capture window relations.
func ExampleServer_SearchSessions_complete() {
	run := func() (runErr error) {
		directory, err := os.MkdirTemp("", "libtmux-go-api-")
		if err != nil {
			return fmt.Errorf("create socket directory: %w", err)
		}
		server, err := tmux.NewServer(tmux.ServerOptions{
			SocketPath: filepath.Join(directory, "tmux.sock"),
			ConfigFile: "/dev/null",
		})
		if err != nil {
			return errors.Join(err, os.RemoveAll(directory))
		}
		defer func() {
			// Cleanup has its own deadline because the operation may have
			// timed out.
			cleanup, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()
			err := server.Kill(cleanup)
			if err != nil && !errors.Is(err, tmux.ErrNoServer) {
				stopErr := fmt.Errorf("stop server at %s: %w", directory, err)
				runErr = errors.Join(runErr, stopErr)
				return
			}
			runErr = errors.Join(runErr, os.RemoveAll(directory))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range []string{"work-one", "work-two", "scratch"} {
			if _, err = server.NewSession(ctx, tmux.NewSessionRequest{
				Name: name, Command: "cat", Width: 100, Height: 30,
			}); err != nil {
				return fmt.Errorf("create %s session: %w", name, err)
			}
		}
		filter := tmux.TmuxFilter("#{m:work-*,#{session_name}}")
		matched, err := server.SearchSessions(ctx, &filter)
		if err != nil {
			return fmt.Errorf("filter sessions: %w", err)
		}
		names := make([]string, 0, len(matched))
		for _, session := range matched {
			name, ok := session.Name()
			if !ok {
				return errors.New("matched session has no name")
			}
			if _, loaded := session.Windows(); loaded {
				return errors.New("live search unexpectedly loaded windows")
			}
			names = append(names, name)
		}
		slices.Sort(names)
		fmt.Println("matched:", names)
		all, err := server.SearchSessions(ctx, nil)
		if err != nil {
			return fmt.Errorf("list unfiltered sessions: %w", err)
		}
		fmt.Println("unfiltered:", len(all))
		missing := tmux.TmuxFilter("#{==:#{session_name},missing}")
		none, err := server.SearchSessions(ctx, &missing)
		if err != nil {
			return fmt.Errorf("search missing session: %w", err)
		}
		fmt.Println("missing:", len(none))
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// matched: [work-one work-two]
	// unfiltered: 3
	// missing: 0
}
