# snapshot-browser

One read of the whole server, walked down all three levels.

A snapshot is every session, window, and pane as they were at one instant. It is
one tmux command, and everything you ask of it afterwards is free.

## Running it

```console
$ go -C examples run ./snapshot-browser
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/snapshot-browser/output.txt -->

```text
session $0 "libtmux-snapshot"
  window @0:0
    pane %0 index 0
```

<!-- docs:end -->

## The program

<!-- docs:file examples/snapshot-browser/main.go -->

```go
// Command snapshot-browser demonstrates hierarchical snapshot traversal.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
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

	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-snapshot-browser",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-snapshot", WindowName: "browser",
	})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, session := range snapshot.Sessions() {
		name, _ := session.Name()
		fmt.Printf("session %s %q\n", session.ID(), name)
		// Snapshot records carry relations; point lookups report them unavailable.
		windows, _ := session.Windows()
		for _, window := range windows {
			fmt.Printf("  window %s:%d\n", window.ID(), window.Index())
			panes, _ := window.Panes()
			for _, pane := range panes {
				fmt.Printf("    pane %s index %d\n", pane.ID(), pane.Index())
			}
		}
	}
	return nil
}
```

<!-- docs:end -->

## What to look at

**One read, not three.** Listing sessions, then windows per session, then panes
per window is one command plus one per session plus one per window. This is one
command total.

**It is consistent with itself.** Everything in a snapshot came from the same
moment. Walking it cannot show you a window whose session has since been killed,
which a sequence of separate reads can.

**It never refreshes.** The snapshot is what tmux said when it was taken.
Nothing in it changes underneath you, which is what makes it safe to walk twice
and get the same answer.

**Values, not handles.** A pane's index, title, and current command are already
there. Reading them starts nothing.

## Testing your own version

```console
$ go -C examples test ./snapshot-browser
```

The test asserts a line from each of the three levels, since a traversal that
listed sessions and found no windows under them would still print something.

## See also

[`filter-query`](../filter-query) — filtering a snapshot in Go, next to letting
tmux filter instead.
