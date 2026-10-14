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

**Contents** — [Filters](#filters) · [Direct queries](#direct-queries) ·
[Which helper](#which-helper) · [On any slice](#on-any-slice) ·
[Why it is separate](#why-it-is-separate)

## Filters

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

[`tmuxq.Matching`](https://libtmux.org/en/go/latest/reference/tmuxq-matching/) returns a fresh slice, so changing it
cannot change the snapshot. A filter that matches nothing returns an empty
slice, not an error; the error is for a filter that cannot be one, such as a
pattern that does not compile.

Each filter below runs in [`examples/queries`](../examples/queries), against a
server with one session, `work`, and two windows: `editor` holds one pane, and
`logs` holds two, the second of them active. A result names each pane by its
window and its index there.

<!-- docs:checked examples/queries/main.go examples/queries/output.txt -->

### [`tmux.PaneFilter`](https://libtmux.org/en/go/latest/reference/tmux-panefilter/): panes, by any of their fields

A struct with one optional field per thing a pane can be asked. Set the fields
you care about and leave the rest; a field left unset matches every pane.

#### [`tmux.PaneFilter{WindowIndexGTE: new(1)}`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-windowindexgte/): a comparison

A field name with a suffix compares rather than equals. This one selects the
panes in window 1 or later: `logs.0 logs.1`.

#### [`tmux.PaneFilter{Active: new(true), WindowIndex: new(1)}`](https://libtmux.org/en/go/latest/reference/tmux-panefilter/): every field must hold

The fields of one filter combine with AND, so
[`Active`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-active/) and
[`WindowIndex`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-windowindex/) together select the active
pane of window 1 and nothing else: `logs.1`.

#### [`tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-window/): the pane's window

[`Window`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-window/) filters a pane by the window it is
in, and [`Session`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-session/) does the same for its
session. This selects both panes of the window named `logs`: `logs.0 logs.1`.

#### [`tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-anyof/): either may hold

[`AnyOf`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-anyof/) accepts a pane that any of its filters
accepts. This selects panes with index 1 and panes in window 0:
`editor.0 logs.1`.

#### [`tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-not/): everything else

[`Not`](https://libtmux.org/en/go/latest/reference/tmux-panefilter-not/) accepts what its filter refuses, here the
one pane that is not active: `logs.0`.

### [`tmux.WindowFilter`](https://libtmux.org/en/go/latest/reference/tmux-windowfilter/): windows

The same shape for the slice `snapshot.Windows()` returns.

#### [`tmux.WindowFilter{NameRegex: "^log"}`](https://libtmux.org/en/go/latest/reference/tmux-windowfilter-nameregex/): a pattern

Text fields take a pattern, and a pattern that does not compile is the error
`Matching` reports. This selects the window named `logs`.

#### [`tmux.WindowFilter{Active: new(true)}`](https://libtmux.org/en/go/latest/reference/tmux-windowfilter-active/): the current window

The window its session is showing. The session was created on `editor` and
has not moved, so this selects `editor`.

### [`tmux.SessionFilter`](https://libtmux.org/en/go/latest/reference/tmux-sessionfilter/): sessions

And for the slice `snapshot.Sessions()` returns.

#### [`tmux.SessionFilter{NameContains: new("or")}`](https://libtmux.org/en/go/latest/reference/tmux-sessionfilter-namecontains/): part of a name

This selects the session named `work`.

### Direct queries

A function for the filter with one field set, when that is the whole
question. Each returns the filter type it is named for, so it goes wherever
that filter does.

#### [`tmux.PaneActiveIs(true)`](https://libtmux.org/en/go/latest/reference/tmux-paneactiveis/): the active pane of each window

A window has exactly one active pane, so this selects one pane per window:
`editor.0 logs.1`.

#### [`tmux.PaneIndexIs(0)`](https://libtmux.org/en/go/latest/reference/tmux-paneindexis/): the first pane of each window

It selects `editor.0 logs.0`.

#### [`tmux.WindowNameIs("editor")`](https://libtmux.org/en/go/latest/reference/tmux-windownameis/): a window by name

It selects the window named `editor`.

#### [`tmux.SessionNameIs("work")`](https://libtmux.org/en/go/latest/reference/tmux-sessionnameis/): a session by name

It selects the session named `work`.

<!-- docs:end -->

There is one for each field that is usually asked alone:
[`SessionIDIs`](https://libtmux.org/en/go/latest/reference/tmux-sessionidis/),
[`SessionNameIs`](https://libtmux.org/en/go/latest/reference/tmux-sessionnameis/),
[`SessionAttachedIs`](https://libtmux.org/en/go/latest/reference/tmux-sessionattachedis/),
[`WindowSessionIDIs`](https://libtmux.org/en/go/latest/reference/tmux-windowsessionidis/),
[`WindowIDIs`](https://libtmux.org/en/go/latest/reference/tmux-windowidis/),
[`WindowIndexIs`](https://libtmux.org/en/go/latest/reference/tmux-windowindexis/),
[`WindowNameIs`](https://libtmux.org/en/go/latest/reference/tmux-windownameis/),
[`WindowActiveIs`](https://libtmux.org/en/go/latest/reference/tmux-windowactiveis/),
[`PaneSessionIDIs`](https://libtmux.org/en/go/latest/reference/tmux-panesessionidis/),
[`PaneWindowIDIs`](https://libtmux.org/en/go/latest/reference/tmux-panewindowidis/),
[`PaneIDIs`](https://libtmux.org/en/go/latest/reference/tmux-paneidis/),
[`PaneWindowIndexIs`](https://libtmux.org/en/go/latest/reference/tmux-panewindowindexis/),
[`PaneIndexIs`](https://libtmux.org/en/go/latest/reference/tmux-paneindexis/),
[`PaneCommandIs`](https://libtmux.org/en/go/latest/reference/tmux-panecommandis/),
[`PaneActiveIs`](https://libtmux.org/en/go/latest/reference/tmux-paneactiveis/),
[`PaneTitleIs`](https://libtmux.org/en/go/latest/reference/tmux-panetitleis/),
[`ClientNameIs`](https://libtmux.org/en/go/latest/reference/tmux-clientnameis/),
[`ClientReadOnlyIs`](https://libtmux.org/en/go/latest/reference/tmux-clientreadonlyis/).

A plain field name is an exact match. `In` takes several values, `Contains`
and `Regex` match text, and `GT`, `GTE`, `LT`, and `LTE` compare numbers. The
reference page for each filter lists every field it has.

## Which helper

| Question | Slice | Sequence |
| --- | --- | --- |
| Every match | [`Where`](https://libtmux.org/en/go/latest/reference/tmuxq-where/), [`Matching`](https://libtmux.org/en/go/latest/reference/tmuxq-matching/) | [`WhereSeq`](https://libtmux.org/en/go/latest/reference/tmuxq-whereseq/), [`MatchingSeq`](https://libtmux.org/en/go/latest/reference/tmuxq-matchingseq/) |
| The first match | [`First`](https://libtmux.org/en/go/latest/reference/tmuxq-first/) | [`FirstSeq`](https://libtmux.org/en/go/latest/reference/tmuxq-firstseq/) |
| The only match | [`ExactlyOne`](https://libtmux.org/en/go/latest/reference/tmuxq-exactlyone/) | [`ExactlyOneSeq`](https://libtmux.org/en/go/latest/reference/tmuxq-exactlyoneseq/) |

`Matching` takes a filter; the others take a `func(*T) bool`. `First` reports
absence with comma-ok, because no match is an ordinary answer rather than a
failure. `ExactlyOne` returns [`ErrNoMatch`](https://libtmux.org/en/go/latest/reference/tmuxq-errnomatch/) for none
and [`ErrMultipleMatches`](https://libtmux.org/en/go/latest/reference/tmuxq-errmultiplematches/) for several, so a
caller tells them apart with `errors.Is`. The sequence forms pull only as far
as they need to.

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
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, one page each |
| [`../tmux/`](../tmux/) | The library, and where filters are declared |
| [`../examples/queries`](../examples/queries) | The program that runs every query above |
| [`../examples/filter-query`](../examples/filter-query) | Filtering in Go beside letting tmux filter |
