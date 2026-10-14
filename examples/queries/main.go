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

	// Every other query is that call with a different filter. Each is printed
	// under its own source text, which the test holds it to.
	if err := ask(panes, where, []query[tmux.Pane]{
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
		{`tmux.PaneActiveIs(true)`, tmux.PaneActiveIs(true)},
		{`tmux.PaneIndexIs(0)`, tmux.PaneIndexIs(0)},
	}); err != nil {
		return err
	}
	if err := ask(snapshot.Windows(), windowNames, []query[tmux.Window]{
		{`tmux.WindowFilter{NameRegex: "^log"}`, tmux.WindowFilter{NameRegex: "^log"}},
		{`tmux.WindowFilter{Active: new(true)}`, tmux.WindowFilter{Active: new(true)}},
		{`tmux.WindowNameIs("editor")`, tmux.WindowNameIs("editor")},
	}); err != nil {
		return err
	}
	if err := ask(snapshot.Sessions(), sessionNames, []query[tmux.Session]{
		{`tmux.SessionFilter{NameContains: new("or")}`, tmux.SessionFilter{NameContains: new("or")}},
		{`tmux.SessionNameIs("work")`, tmux.SessionNameIs("work")},
	}); err != nil {
		return err
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

// query is a filter beside the source text that built it.
type query[T any] struct {
	source string
	filter tmuxq.Filter[T]
}

// ask runs each query against records and prints what it selected.
func ask[T any](records []T, name func([]T) string, queries []query[T]) error {
	for _, query := range queries {
		matched, err := tmuxq.Matching(records, query.filter)
		if err != nil {
			return fmt.Errorf("match with %s: %w", query.source, err)
		}
		fmt.Printf("%s: %s\n", query.source, name(matched))
	}
	return nil
}

func windowNames(windows []tmux.Window) string {
	names := make([]string, 0, len(windows))
	for _, window := range windows {
		name, _ := window.Name()
		names = append(names, name)
	}
	return strings.Join(names, " ")
}

func sessionNames(sessions []tmux.Session) string {
	names := make([]string, 0, len(sessions))
	for _, session := range sessions {
		name, _ := session.Name()
		names = append(names, name)
	}
	return strings.Join(names, " ")
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
