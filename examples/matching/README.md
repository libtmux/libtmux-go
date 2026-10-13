# matching

Read a tmux server once, then filter that one read with `tmuxq.Matching`.

## Running it

```console
$ go -C examples run ./matching
```

It prints:

<!-- docs:output examples/matching/output.txt -->

```text
2 panes, 1 active
```

<!-- docs:end -->

## The program

<!-- docs:file examples/matching/main.go -->

```go
// Command matching reads tmux once and filters that one read.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

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
		SocketName: "libtmux-matching",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// Split the session's window, so it holds two panes and one is active.
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return fmt.Errorf("find window: %w", err)
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		return fmt.Errorf("split window: %w", err)
	}

	// One read of the whole server. Filtering it sends tmux nothing more.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read tmux: %w", err)
	}
	active, err := tmuxq.Matching(snapshot.Panes(), tmux.PaneActiveIs(true))
	if err != nil {
		return fmt.Errorf("match active panes: %w", err)
	}
	fmt.Println(len(snapshot.Panes()), "panes,", len(active), "active")
	return nil
}
```

<!-- docs:end -->

## What to look at

**One read, then no more commands.** `server.Snapshot` asks tmux once for
every session, window, and pane. `Matching` filters the slice it returns in
Go, so a second or a tenth question costs tmux nothing.

**The filter comes from the `tmux` package.** `tmux.PaneActiveIs(true)` is a
`PaneFilter`. A filter compiles itself into a predicate, and `Matching` reports
the one error that can have, so there is no predicate to unpack first.

**Why two panes and one active.** Splitting the session's window leaves it
with two panes, and a window has exactly one active pane.

[`filter-query`](../filter-query) sets this beside letting tmux do the
filtering, and [`any-slice`](../any-slice) uses the same helpers with no tmux
at all.

## Testing your own version

```console
$ go -C examples test ./matching
```

The test compares what the program prints with [`output.txt`](output.txt).
