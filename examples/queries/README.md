# queries

Read a tmux server once, then ask that one read many things with `tmuxq`.

## Running it

```console
$ go -C examples run ./queries
```

It prints, naming each pane by its window and its index there:

<!-- docs:output examples/queries/output.txt -->

```text
active: editor.0 logs.0
in the logs window: logs.0 logs.1
in window 1 or later: logs.0 logs.1
first in their window: editor.0 logs.0
active and in logs: logs.0
index 1, or in window 0: editor.0 logs.1
not active: logs.1
window name matches ^log: logs.0 logs.1
windows named logs: 1
the first pane with index 1: logs.1
exactly one pane has index 1
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

	// Two windows to tell apart: "editor" holds one pane, "logs" holds two.
	request := tmux.NewWindowRequest{Name: new("logs")}
	logs, err := session.NewWindow(ctx, request)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	if _, err := logs.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
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
