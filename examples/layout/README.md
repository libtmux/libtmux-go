# layout

Split a window into two panes side by side, then read back what tmux made.

## Running it

```console
$ go -C examples run ./layout
```

It prints:

<!-- docs:output examples/layout/output.txt -->

```text
pane 0: 40 columns
pane 1: 39 columns
```

<!-- docs:end -->

## The program

<!-- docs:file examples/layout/main.go -->

```go
// Command layout builds two panes side by side and reads back what tmux made.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/libtmux/libtmux-go/tmux"
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
		SocketName: "libtmux-layout",
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

	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return fmt.Errorf("find window: %w", err)
	}
	// Split the window's active pane, and put the new pane on the right.
	split := tmux.SplitPaneRequest{Direction: tmux.PaneDirectionRight}
	if _, err := window.SplitPane(ctx, split); err != nil {
		return fmt.Errorf("split window: %w", err)
	}

	// Read back what tmux made, rather than trusting what was asked for.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read tmux: %w", err)
	}
	for _, pane := range snapshot.Panes() {
		width, _ := pane.Width()
		fmt.Printf("pane %d: %d columns\n", pane.Index(), width)
	}
	return nil
}
```

<!-- docs:end -->

## What to look at

**What it prints is what tmux made.** The widths come from a read of the
server after the split, not from the request, so a split tmux shaped
differently than asked would show here.

**The two widths add up to one less than the window.** A detached window is
80 columns wide, and tmux spends one column on the border between the panes.

## Running its test

```console
$ go -C examples test ./layout
```

The test compares what the program prints with [`output.txt`](output.txt).
