# queries

Read a tmux server once, then ask that one read many things with `tmuxq`.

## Running it

```console
$ go -C examples run ./queries
```

It prints each filter beside the panes it selected, naming a pane by its
window and its index there:

<!-- docs:output examples/queries/output.txt -->

```text
3 panes, 2 active
tmux.PaneFilter{WindowIndexGTE: new(1)}: logs.0 logs.1
tmux.PaneFilter{Active: new(true), WindowIndex: new(1)}: logs.1
tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}: logs.0 logs.1
tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}: editor.0 logs.1
tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}: logs.0
tmux.PaneActiveIs(true): editor.0 logs.1
tmux.PaneIndexIs(0): editor.0 logs.0
tmux.WindowFilter{NameRegex: "^log"}: logs
tmux.WindowFilter{Active: new(true)}: editor
tmux.WindowNameIs("editor"): editor
tmux.SessionFilter{NameContains: new("or")}: work
tmux.SessionNameIs("work"): work
the only active pane of window 1: logs.1
the first pane with index 1: logs.1
```

<!-- docs:end -->

## The program

<!-- docs:file examples/queries/main.go -->

```go
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

	// First and ExactlyOne take a function rather than a filter, and
	// Predicate hands over the function a filter stands for.
	activeInLogs, err := tmux.PaneFilter{
		Active: new(true), WindowIndex: new(1),
	}.Predicate()
	if err != nil {
		return fmt.Errorf("compile filter: %w", err)
	}
	only, err := tmuxq.ExactlyOne(panes, activeInLogs)
	if err != nil {
		return fmt.Errorf("find the active pane of window 1: %w", err)
	}
	fmt.Println("the only active pane of window 1:", where([]tmux.Pane{only}))

	// A function of your own goes in the same place.
	second := func(pane *tmux.Pane) bool { return pane.Index() == 1 }
	if pane, ok := tmuxq.First(panes, second); ok {
		fmt.Println("the first pane with index 1:", where([]tmux.Pane{pane}))
	}
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
```

<!-- docs:end -->

## What to look at

**One read, then no more commands.** `server.Snapshot` asks tmux once for
every session, window, and pane. Each query filters the slice it returns in
Go, so the tenth question costs tmux nothing.

**A filter is a value.** `tmux.PaneFilter` fields combine with AND; `AnyOf` and
`Not` build the rest, and `Window` and `Session` reach through to the pane's
window and session. `tmux.PaneActiveIs(true)` is a shorthand for the
one-field filter.

**`Matching` reports the one error a filter can have.** A filter compiles
itself into a predicate, and an invalid one, such as a bad `NameRegex`, is
reported there rather than matching nothing.

**The helpers that take a function work on the same slices.** `First` reports
absence with comma-ok, and `ExactlyOne` returns `tmuxq.ErrNoMatch` or
`tmuxq.ErrMultipleMatches`.

[`filter-query`](../filter-query) sets this beside letting tmux do the
filtering, and [`any-slice`](../any-slice) uses the same helpers with no tmux
at all.

## Testing your own version

```console
$ go -C examples test ./queries
```

The test compares what the program prints with [`output.txt`](output.txt).
