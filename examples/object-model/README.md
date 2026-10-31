# object-model

The values tmux hands back are records: what tmux said when you asked, not live
handles that change underneath you.

## Running it

```console
$ go -C examples run ./object-model
```

It prints:

<!-- docs:output examples/object-model/output.txt -->

```text
held record: old
returned record: new
the record holds no windows
tmux reports windows: 1
session $999: not found
kill-window exit status: 1
```

<!-- docs:end -->

## The program

<!-- docs:file examples/object-model/main.go -->

```go
// Command object-model shows that the values tmux hands back are records.
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
	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-object-model",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "old"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// Rename hands back the session as tmux now reports it. The record
	// already in hand keeps the name it had when tmux handed it over.
	renamed, err := session.Rename(ctx, "new")
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	held, _ := session.Name()
	current, _ := renamed.Name()
	fmt.Println("held record:", held)
	fmt.Println("returned record:", current)

	// Windows reads the record and takes no context. A session that came
	// from a call about the session carries no windows, and says so.
	if _, held := renamed.Windows(); !held {
		fmt.Println("the record holds no windows")
	}
	// SearchWindows asks tmux.
	windows, err := renamed.SearchWindows(ctx, nil)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	fmt.Println("tmux reports windows:", len(windows))

	// A lookup that finds nothing is an error a caller can test for.
	_, lookupErr := server.Session(ctx, tmux.SessionID("$999"))
	if errors.Is(lookupErr, tmux.ErrNotFound) {
		fmt.Println("session $999: not found")
	}

	// Cmd sends arguments you write yourself. A command tmux ran and
	// refused comes back as a result, with the error left nil.
	result, err := server.Cmd(ctx, "kill-window", "-t", "@999")
	if err != nil {
		return fmt.Errorf("reach tmux: %w", err)
	}
	fmt.Println("kill-window exit status:", result.ExitCode)
	return nil
}
```

<!-- docs:end -->

## What to look at

**A mutation returns the record it changed.** `Session.Rename` hands back the
session as tmux now reports it. The `session` value already in hand still says
`old`, because nothing refreshes a record behind you. `Session.Refresh` asks
again when you want a new one.

**Identity survives a rename.** Both records carry the same `SessionID`, which
tmux never reuses for the life of the server, so a command sent through either
one reaches the same session.

**The name says which kind of read it is.** `Session.Windows` takes no
context, because it reads the record. A session handed back by a call about
the session carries no windows and says so. `Session.SearchWindows` asks tmux.

**A missing target is an error you can test for.** `server.Session` with an ID
no session holds returns an error matching `tmux.ErrNotFound`, so "it is gone"
and "tmux could not be asked" are different checks.

**A command tmux refused is a result.** `Server.Cmd` returns an error only
when tmux could not be reached or the arguments could not be sent. A command
that ran and failed, here `kill-window` on a window that does not exist, comes
back with its exit status.

## Running its test

```console
$ go -C examples test ./object-model
```

The test compares what the program prints with [`output.txt`](output.txt).
