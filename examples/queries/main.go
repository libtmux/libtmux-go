// Command queries reads a tmux server once and asks that one read many things.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmuxq"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-queries",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "work", WindowName: "editor",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// Two windows to tell apart: "editor" holds one pane, "logs" holds two.
	request := tmux.NewWindowRequest{Name: new("logs")}
	logs, err := session.NewWindow(ctx, request)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	if _, err := logs.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		return fmt.Errorf("split window: %w", err)
	}

	// docs:queries-read given:ctx context.Context; server tmux.Server
	// One read of the whole server. Each query filters it in Go, so none of
	// them sends tmux another command.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read tmux: %w", err)
	}
	panes := snapshot.Panes()

	active, err := tmuxq.Matching(panes, tmux.PaneActiveIs(true))
	if err != nil {
		return fmt.Errorf("match active panes: %w", err)
	}
	// docs:end
	fmt.Println("active:", where(active))

	// Every other pane query is that call with a different filter.
	for _, query := range []struct {
		question string
		filter   tmux.PaneFilter
	}{
		{"in the logs window", tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}},
		{"in window 1 or later", tmux.PaneFilter{WindowIndexGTE: new(1)}},
		{"first in their window", tmux.PaneIndexIs(0)},
		{"active and in logs", tmux.PaneFilter{Active: new(true), Window: &tmux.WindowFilter{Name: new("logs")}}},
		{"index 1, or in window 0", tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}},
		{"not active", tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}},
		{"window name matches ^log", tmux.PaneFilter{Window: &tmux.WindowFilter{NameRegex: "^log"}}},
	} {
		matched, err := tmuxq.Matching(panes, query.filter)
		if err != nil {
			return fmt.Errorf("match panes %s: %w", query.question, err)
		}
		fmt.Printf("%s: %s\n", query.question, where(matched))
	}

	// Windows and sessions take the same call.
	windows, err := tmuxq.Matching(snapshot.Windows(), tmux.WindowNameIs("logs"))
	if err != nil {
		return fmt.Errorf("match windows: %w", err)
	}
	fmt.Println("windows named logs:", len(windows))

	// The helpers that take a function of your own work on the same slices.
	second := func(pane *tmux.Pane) bool { return pane.Index() == 1 }
	if pane, ok := tmuxq.First(panes, second); ok {
		fmt.Println("the first pane with index 1:", where([]tmux.Pane{pane}))
	}
	if _, err := tmuxq.ExactlyOne(panes, second); err != nil {
		return fmt.Errorf("find the only pane with index 1: %w", err)
	}
	fmt.Println("exactly one pane has index 1")
	return nil
}

// where names panes the way a person would: the window each is in, and its
// index there.
func where(panes []tmux.Pane) string {
	names := make([]string, 0, len(panes))
	for _, pane := range panes {
		window, _ := pane.Window()
		name, _ := window.Name()
		names = append(names, fmt.Sprintf("%s.%d", name, pane.Index()))
	}
	return strings.Join(names, " ")
}
