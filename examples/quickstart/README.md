# quickstart

Run a command in tmux and print what its terminal showed. Start here: it is the
shortest program that does something you can see.

## Running it

```console
$ go -C examples run ./quickstart
```

It prints:

<!-- docs:output examples/quickstart/output.txt -->

```text
hello from tmux
exit status: 0
```

<!-- docs:end -->

## The program

<!-- docs:file examples/quickstart/main.go -->

```go
// Command quickstart runs a command in tmux and prints what it showed.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	directory, err := os.MkdirTemp("", "libtmux-quickstart-")
	if err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()

	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(directory, "tmux.sock"),
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		if cleanup := server.Kill(context.WithoutCancel(ctx)); !errors.Is(cleanup, tmux.ErrNoServer) {
			err = errors.Join(err, cleanup)
		}
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// The command runs in a window of its own, with a terminal.
	result, err := session.Run(ctx, "echo hello from tmux", tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	for _, line := range result.Lines {
		fmt.Println(line)
	}
	fmt.Println("exit status:", result.Status)
	return nil
}
```

<!-- docs:end -->

## What to look at

**The server is the program's own.** Its `SocketPath` is in a directory from
`os.MkdirTemp`, removed after cleanup kills the server. `ConfigFile: os.DevNull`
keeps personal configuration out, and `NewServer` starts nothing.

**`Session.Run` is `os/exec` for a terminal program.** It runs the command in a
window with a tty and returns its exit status and the screen tmux rendered. A
nonzero status is a result, not an error; see [`run-to-completion`](../run-to-completion).

**Cleanup outlives the context.** The deferred `server.Kill` takes
`context.WithoutCancel(ctx)`, so it runs even when a deadline or cancellation
ends the program. `main` calls `run` because `log.Fatal` skips deferred calls.

## Running its test

[`example_test.go`](example_test.go) runs the program on a socket directory of
its own and compares what it prints with [`output.txt`](output.txt):

```console
$ go -C examples test ./quickstart
```
