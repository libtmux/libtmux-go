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

**Contents** — [Queries](#queries) · [Which helper](#which-helper) ·
[On any slice](#on-any-slice) · [Why it is separate](#why-it-is-separate)

## Queries

A query is one call: a slice from a snapshot, and a filter. Only the filter
changes from one question to the next.

<!-- docs:queries-read -->

```go
// Given: ctx context.Context; server tmux.Server
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
```

<!-- docs:end -->

Each filter below runs in [`examples/queries`](../examples/queries), against a
server with two windows: `editor` holds one pane, and `logs` holds two, the
second of them active. A result names each pane by its window and its index
there.

<!-- docs:checked examples/queries/main.go examples/queries/output.txt -->

### `tmux.PaneActiveIs(true)`: the active pane of each window

A window has exactly one active pane, so this selects one pane per window:
`editor.0 logs.1`. It is shorthand for a filter with that one field set.

### `tmux.PaneIndexIs(0)`: the first pane of each window

The same shape for any single field. It selects `editor.0 logs.0`.

### `tmux.PaneFilter{WindowIndexGTE: new(1)}`: a comparison

A field name with a suffix compares rather than equals. This one selects the
panes in window 1 or later: `logs.0 logs.1`.

### `tmux.PaneFilter{Active: new(true), WindowIndex: new(1)}`: every field must hold

The fields of one filter combine with AND, so this is the active pane of
window 1 and nothing else: `logs.1`.

### `tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}`: the pane's window

`Window` filters a pane by the window it is in, and `Session` does the same
for its session. This selects both panes of the window named `logs`:
`logs.0 logs.1`.

### `tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}`: either may hold

`AnyOf` accepts a pane that any of its filters accepts. This selects panes
with index 1 and panes in window 0: `editor.0 logs.1`.

### `tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}`: everything else

`Not` accepts what its filter refuses, here the one pane that is not active:
`logs.0`.

### `tmux.WindowFilter{NameRegex: "^log"}`: windows, by a pattern

Windows and sessions have filters of their own, for `snapshot.Windows()` and
`snapshot.Sessions()`. This selects the window named `logs`.

<!-- docs:end -->

A plain field name is an exact match. `In` takes several values, `Contains`
and `Regex` match text, and `GT`, `GTE`, `LT`, and `LTE` compare numbers.

`Matching` returns a fresh slice, so changing it cannot change the snapshot. A
filter that matches nothing returns an empty slice, not an error; the error is
for a filter that cannot be one, such as a pattern that does not compile.

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
own. This is a whole program, and it needs no tmux:

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
| [`../examples/queries`](../examples/queries) | The program that runs every query above |
| [`../examples/filter-query`](../examples/filter-query) | Filtering in Go beside letting tmux filter |
