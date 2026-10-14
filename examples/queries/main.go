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

	// Two windows to tell apart: "editor" holds one pane, and "logs" holds
	// two, the second of them active.
	request := tmux.NewWindowRequest{Name: new("logs")}
	logs, err := session.NewWindow(ctx, request)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	split := tmux.SplitPaneRequest{Attach: true}
	if _, err := logs.SplitPane(ctx, split); err != nil {
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
	fmt.Println(len(panes), "panes,", len(active), "active")

	// Every other pane query is that call with a different filter. Each is
	// printed under its own source text, which the test holds it to.
	for _, query := range []struct {
		source string
		filter tmux.PaneFilter
	}{
		{`tmux.PaneActiveIs(true)`, tmux.PaneActiveIs(true)},
		{`tmux.PaneIndexIs(0)`, tmux.PaneIndexIs(0)},
		{`tmux.PaneFilter{WindowIndexGTE: new(1)}`, tmux.PaneFilter{WindowIndexGTE: new(1)}},
		{
			`tmux.PaneFilter{Active: new(true), WindowIndex: new(1)}`,
			tmux.PaneFilter{Active: new(true), WindowIndex: new(1)},
		},
		{
			`tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}`,
			tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}},
		},
		{
			`tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}`,
			tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}},
		},
		{
			`tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}`,
			tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}},
		},
	} {
		matched, err := tmuxq.Matching(panes, query.filter)
		if err != nil {
			return fmt.Errorf("match panes with %s: %w", query.source, err)
		}
		fmt.Printf("%s: %s\n", query.source, where(matched))
	}

	// Windows and sessions have filters of their own.
	windows, err := tmuxq.Matching(snapshot.Windows(), tmux.WindowFilter{NameRegex: "^log"})
	if err != nil {
		return fmt.Errorf("match windows: %w", err)
	}
	for _, window := range windows {
		name, _ := window.Name()
		fmt.Println(`tmux.WindowFilter{NameRegex: "^log"}:`, name)
	}
	sessions, err := tmuxq.Matching(snapshot.Sessions(), tmux.SessionNameIs("work"))
	if err != nil {
		return fmt.Errorf("match sessions: %w", err)
	}
	for _, session := range sessions {
		name, _ := session.Name()
		fmt.Println(`tmux.SessionNameIs("work"):`, name)
	}

	// The helpers that take a function of your own work on the same slices.
	second := func(pane *tmux.Pane) bool { return pane.Index() == 1 }
	if pane, ok := tmuxq.First(panes, second); ok {
		fmt.Println("the first pane with index 1:", where([]tmux.Pane{pane}))
	}
	only, err := tmuxq.ExactlyOne(panes, second)
	if err != nil {
		return fmt.Errorf("find the only pane with index 1: %w", err)
	}
	fmt.Println("the only pane with index 1:", where([]tmux.Pane{only}))
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
