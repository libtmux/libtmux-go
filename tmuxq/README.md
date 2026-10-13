<!-- libtmux-logo -->
<p align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</p>
<!-- /libtmux-logo -->

<div align="center">

# tmuxq

Generic helpers for filtering and collecting tmux records. It imports no tmux
types, so it works on any slice or `iter.Seq`.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmuxq.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq)

</div>

```console
$ go get github.com/libtmux/libtmux-go/tmuxq@latest
```

**Contents** — [Filter one read of tmux](#filter-one-read-of-tmux) ·
[Which helper](#which-helper) · [On any slice](#on-any-slice) ·
[Why it is separate](#why-it-is-separate)

## Filter one read of tmux

A snapshot is one read of a whole tmux server. `tmuxq.Matching` takes a slice
from it and a filter, and returns the records the filter accepts without
sending tmux another command. This is the whole program:

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

It prints:

<!-- docs:output examples/matching/output.txt -->

```text
2 panes, 1 active
```

<!-- docs:end -->

Splitting the session's window leaves it with two panes, and a window has
exactly one active pane.

To run it, save it as `main.go` in an empty directory, with tmux 3.2a or newer
on `PATH`:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/tmuxq@latest && \
    go run .
```

The program starts a tmux server on a socket and config of its own and kills it
on the way out, so it never touches a tmux you have open.

`tmux.PaneActiveIs(true)` is a filter the `tmux` package declares. A filter
compiles itself into a predicate, and `Matching` reports the one error that can
have, so there is no predicate to unpack first. It returns a fresh slice, so
changing the result cannot change the snapshot, and a filter that matches
nothing returns an empty slice rather than an error.

## Which helper

| Question | Slice | Sequence |
| --- | --- | --- |
| Every match | `Where`, `Matching` | `WhereSeq`, `MatchingSeq` |
| The first match | `First` | `FirstSeq` |
| The only match | `ExactlyOne` | `ExactlyOneSeq` |

`Matching` takes a filter; the others take a `func(*T) bool`. `First` reports
absence with comma-ok, because no match is an ordinary answer rather than a
failure. `ExactlyOne` returns `ErrNoMatch` for none and `ErrMultipleMatches`
for several, so a caller tells them apart with `errors.Is`. The sequence forms
pull only as far as they need to.

## On any slice

Nothing here names a tmux type, so the same helpers work on a slice of your
own. This program needs no tmux:

<!-- docs:file examples/any-slice/main.go -->

```go
// Command any-slice filters a slice of its own with tmuxq, without tmux.
package main

import (
	"fmt"

	"github.com/libtmux/libtmux-go/tmuxq"
)

type task struct {
	name string
	done bool
}

func main() {
	tasks := []task{{"build", true}, {"test", false}, {"deploy", false}}
	open := func(task *task) bool { return !task.done }

	for _, task := range tmuxq.Where(tasks, open) {
		fmt.Println("open:", task.name)
	}
	first, _ := tmuxq.First(tasks, open)
	fmt.Println("first open:", first.name)

	// Two tasks are open, so asking for the only one is an error.
	_, err := tmuxq.ExactlyOne(tasks, open)
	fmt.Println("the only open task:", err)
}
```

<!-- docs:end -->

It prints:

<!-- docs:output examples/any-slice/output.txt -->

```text
open: test
open: deploy
first open: test
the only open task: tmuxq: multiple matches
```

<!-- docs:end -->

## Why it is separate

A `PaneFilter` compiles to a predicate, and a predicate is `func(*Pane) bool`.
Nothing about applying one is specific to panes, so the applying lives here and
stays model-free, which is also why this package cannot create an import cycle
with the library.

## See also

| | |
| --- | --- |
| [`../tmux/`](../tmux/) | The library, and where filters are declared |
| [`../examples/matching`](../examples/matching) | The program above, with notes |
| [`../examples/filter-query`](../examples/filter-query) | Filtering in Go beside letting tmux filter |
