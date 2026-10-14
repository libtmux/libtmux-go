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

**Contents** — [Asking a snapshot](#asking-a-snapshot) ·
[Filters](#filters) · [Which helper](#which-helper) ·
[On any slice](#on-any-slice) · [Why it is separate](#why-it-is-separate)

## Asking a snapshot

A snapshot is one read of a whole tmux server. `tmuxq.Matching` takes a slice
from it and a filter, and returns the records the filter accepts:

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

`Matching` returns a fresh slice, so changing it cannot change the snapshot. A
filter that matches nothing returns an empty slice, not an error; the error is
for a filter that cannot be one, such as a regular expression that does not
compile.

## Filters

Every other question is that call with a different filter. These select panes;
`tmux.WindowFilter` and `tmux.SessionFilter` work the same way on
`snapshot.Windows()` and `snapshot.Sessions()`.

<!-- docs:checked examples/queries/main.go -->

| Panes that | Filter |
| --- | --- |
| are active in their window | `tmux.PaneActiveIs(true)` |
| are in the window named `logs` | `tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}` |
| are in window 1 or later | `tmux.PaneFilter{WindowIndexGTE: new(1)}` |
| are first in their window | `tmux.PaneIndexIs(0)` |
| are active and in `logs` | `tmux.PaneFilter{Active: new(true), Window: &tmux.WindowFilter{Name: new("logs")}}` |
| have index 1, or are in window 0 | `tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}` |
| are not active | `tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}` |
| are in a window whose name matches `^log` | `tmux.PaneFilter{Window: &tmux.WindowFilter{NameRegex: "^log"}}` |

<!-- docs:end -->

Fields of one filter combine with AND. A field takes the plain name for an
exact match and a suffix for the rest: `In` for any of several values,
`Contains` and `Regex` for text, and `GT`, `GTE`, `LT`, and `LTE` for numbers.

Each cell above is an expression from
[`examples/queries`](../examples/queries), which runs them all against a
server holding two windows, `editor` with one pane and `logs` with two. It
prints, naming each pane by its window and its index there:

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
| [`../examples/queries`](../examples/queries) | The program behind the table, with notes |
| [`../examples/filter-query`](../examples/filter-query) | Filtering in Go beside letting tmux filter |
