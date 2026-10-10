# Session cleanup

A whole session, window, and pane lifecycle: make a session, add a window, split
it, type a command into the new pane, and read the pane's output back until the
command's line arrives.

This cleanup demonstration touches every level of the hierarchy and joins
body and cleanup errors. The [ordinary quick start](../quickstart/) leaves its
workspace available after the program exits.

## Running it

```console
$ go -C examples run ./session-cleanup
```

```
libtmux ready
```

`libtmux ready` is the pane's own output, captured back out of it. The example
constructs `tmux.NewServer(tmux.ServerOptions{})`, creates an `Owned` session and
defers `CloseInto(&err)`. Cleanup uses the captured daemon and session ID with an
independent deadline, including after body cancellation. The returned error
retains body and cleanup failures; known creation IDs roll back after a failed
acquisition.

## What to look at

**Writing and reading are the standard interfaces.** `pane.Writer(ctx)` is an
`io.Writer` whose newline is the Enter key, so `fmt.Fprintln` submits a line
the way a person would. `output.Reader(ctx)` is an `io.Reader` over what the
pane prints after the observation opened, so `bufio.Scanner` reads it back a
line at a time and the program returns the moment the line arrives — no loop
that captures and sleeps.

**Open the reader before typing.** An observation starts when it is opened;
typing afterwards means the reply cannot be missed.

**The split runs `sh`.** A pane's default shell is your login shell, and what
that does at startup is yours. A plain POSIX shell keeps the example about tmux.

**`new("work")`** is Go 1.26's way to take the address of a literal. Request
fields are pointers only where tmux distinguishes an empty value from an
omitted one; the rest are plain values.

**The pane never refreshes.** The `pane` value is what tmux said when the split
happened. Reading through the observation asks tmux for more; it does not
update the value in hand.

## Testing your own version

[`example_test.go`](example_test.go) compiles and executes the unchanged program.
Its child environment supplies `LIBTMUX_SOCKET_PATH` or `LIBTMUX_SOCKET_NAME` to
select a private endpoint. The harness checks the session exists during the
body, the pane prints its reply, and the session is removed after success or an
injected body failure. A separate failure checks that cleanup errors reach the
caller. The harness removes its own daemon and reports teardown failures.

```console
$ go -C examples test ./session-cleanup
```

## Complete program

<!-- docs:session-cleanup -->

```go
// Command session-cleanup demonstrates a complete session, window, and pane lifecycle.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return run(ctx)
}

func run(ctx context.Context) (err error) {
	server, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	owned, err := server.OwnSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-go-quickstart", WindowName: "start",
	}, tmux.OwnershipOptions{})
	defer owned.CloseInto(&err)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	session := owned.Value()

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: new("work")})
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight, Command: "sh",
	})
	if err != nil {
		return fmt.Errorf("split window: %w", err)
	}
	output, err := pane.OpenObservation(ctx)
	if err != nil {
		return fmt.Errorf("watch pane: %w", err)
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	if _, err := fmt.Fprintln(pane.Writer(ctx), "printf 'libtmux ready\\n'"); err != nil {
		return fmt.Errorf("send command: %w", err)
	}

	scanner := bufio.NewScanner(output.Reader(ctx))
	for scanner.Scan() {
		if scanner.Text() == "libtmux ready" {
			fmt.Println("libtmux ready")
			return nil
		}
	}
	return fmt.Errorf("read pane: %w", scanner.Err())
}
```

<!-- docs:end -->
