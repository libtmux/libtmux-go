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

Generic helpers for filtering tmux records. It imports no tmux types, so it
works on any slice or `iter.Seq`.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmuxq.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq)

</div>

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/tmuxq@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

**Contents** — [Filters](#filters) · [Direct queries](#direct-queries) ·
[Which helper](#which-helper) · [On any slice](#on-any-slice) ·
[Why it is separate](#why-it-is-separate) · [See also](#see-also)

## Filters

A query is one call. It takes a slice of records, each one what tmux reported
about a pane, a window, or a session, and a filter that says which of them
you want. [`Server.Snapshot`][tmux-server-snapshot] reads every record in one
go, and only the filter changes from one question to the next.

A block that starts with `// Given:` is the body of a function that returns
an error, with the values that line names already in hand.

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
fmt.Println(len(panes), "panes,", len(active), "active")
```

<!-- docs:end -->

<!-- docs:checked examples/queries/output.txt -->
On the server described below, this prints `3 panes, 2 active`.
<!-- docs:end -->

[`tmuxq.Matching`][tmuxq-matching] returns a fresh slice, so changing it
cannot change the snapshot. A filter that matches nothing returns an empty
slice, not an error. The error is for a filter that cannot be one, such as a
pattern that does not compile.

Each filter below runs in [`examples/queries`](../examples/queries), against a
server with one session, `work`, and two windows: `editor` holds one pane, and
`logs` holds two, the second of them active. A result names each pane by its
window and its index there, so `logs.1` is the pane with index 1 in the window
named `logs`.

<!-- docs:checked examples/queries/main.go examples/queries/output.txt -->

### [`tmux.PaneFilter`][tmux-panefilter]: panes, by any of their fields

A struct with one optional field per thing a pane can be asked. Set the fields
you care about and leave the rest: a field left unset matches every pane. A
field that compares a value is a pointer, so that unset differs from zero,
and `new(1)` is how Go 1.26 writes a pointer to `1`. A `Regex` field is a
string, and a field ending in `In` is a slice; each is unset when empty.

#### [`tmux.PaneFilter{WindowIndexGTE: new(1)}`][tmux-panefilter-windowindexgte]: a comparison

A field name with a suffix compares rather than equals. This one selects the
panes in window 1 or later: `logs.0 logs.1`.

#### [`tmux.PaneFilter{Active: new(true), WindowIndex: new(1)}`][tmux-panefilter]: every field must hold

The fields of one filter combine with AND, so
[`Active`][tmux-panefilter-active] and
[`WindowIndex`][tmux-panefilter-windowindex] together select the active pane
of window 1 and nothing else: `logs.1`.

#### [`tmux.PaneFilter{Window: &tmux.WindowFilter{Name: new("logs")}}`][tmux-panefilter-window]: the pane's window

[`Window`][tmux-panefilter-window] filters a pane by the window it is in, and
[`Session`][tmux-panefilter-session] does the same for its session. This
selects both panes of the window named `logs`: `logs.0 logs.1`.

#### [`tmux.PaneFilter{AnyOf: []tmux.PaneFilter{{Index: new(1)}, {WindowIndex: new(0)}}}`][tmux-panefilter-anyof]: either may hold

[`AnyOf`][tmux-panefilter-anyof] accepts a pane that any of its filters
accepts. This selects panes with index 1 and panes in window 0:
`editor.0 logs.1`.

#### [`tmux.PaneFilter{Not: &tmux.PaneFilter{Active: new(true)}}`][tmux-panefilter-not]: everything else

[`Not`][tmux-panefilter-not] accepts what its filter refuses, here the one
pane that is not active: `logs.0`.

### [`tmux.WindowFilter`][tmux-windowfilter]: windows

The same shape, for the slice
[`snapshot.Windows()`][tmux-snapshot-windows] returns.

#### [`tmux.WindowFilter{NameRegex: "^log"}`][tmux-windowfilter-nameregex]: a pattern

`NameRegex` takes a regular expression, and one that does not compile is the
error `Matching` reports. This selects the window named `logs`.

#### [`tmux.WindowFilter{Active: new(true)}`][tmux-windowfilter-active]: the current window

The window its session is showing. The session was created on `editor` and
has not moved, so this selects `editor`.

### [`tmux.SessionFilter`][tmux-sessionfilter]: sessions

And for the slice [`snapshot.Sessions()`][tmux-snapshot-sessions] returns.

#### [`tmux.SessionFilter{NameContains: new("or")}`][tmux-sessionfilter-namecontains]: part of a name

This selects the session named `work`.

### Direct queries

A function for the filter with one field set, when that is the whole
question. Each returns the filter type it is named for, so it goes wherever
that filter does.

#### [`tmux.PaneActiveIs(true)`][tmux-paneactiveis]: the active pane of each window

A window has exactly one active pane, so this selects one pane per window:
`editor.0 logs.1`.

#### [`tmux.PaneIndexIs(0)`][tmux-paneindexis]: the first pane of each window

It selects `editor.0 logs.0`.

#### [`tmux.WindowNameIs("editor")`][tmux-windownameis]: a window by name

It selects the window named `editor`.

#### [`tmux.SessionNameIs("work")`][tmux-sessionnameis]: a session by name

It selects the session named `work`.

<!-- docs:end -->

There is one for each field that is usually asked alone:
[`SessionIDIs`][tmux-sessionidis],
[`SessionNameIs`][tmux-sessionnameis],
[`SessionAttachedIs`][tmux-sessionattachedis],
[`WindowSessionIDIs`][tmux-windowsessionidis],
[`WindowIDIs`][tmux-windowidis],
[`WindowIndexIs`][tmux-windowindexis],
[`WindowNameIs`][tmux-windownameis],
[`WindowActiveIs`][tmux-windowactiveis],
[`PaneSessionIDIs`][tmux-panesessionidis],
[`PaneWindowIDIs`][tmux-panewindowidis],
[`PaneIDIs`][tmux-paneidis],
[`PaneWindowIndexIs`][tmux-panewindowindexis],
[`PaneIndexIs`][tmux-paneindexis],
[`PaneCommandIs`][tmux-panecommandis],
[`PaneActiveIs`][tmux-paneactiveis],
[`PaneTitleIs`][tmux-panetitleis],
[`ClientNameIs`][tmux-clientnameis],
[`ClientReadOnlyIs`][tmux-clientreadonlyis].

A plain field name is an exact match. `In` takes several values, `Contains`
and `Regex` match text, and `GT`, `GTE`, `LT`, and `LTE` compare numbers. The
reference page for each filter lists every field it has.

## Which helper

| Question | Slice | Sequence |
| --- | --- | --- |
| Every match | [`Where`][tmuxq-where], [`Matching`][tmuxq-matching] | [`WhereSeq`][tmuxq-whereseq], [`MatchingSeq`][tmuxq-matchingseq] |
| The first match | [`First`][tmuxq-first] | [`FirstSeq`][tmuxq-firstseq] |
| The only match | [`ExactlyOne`][tmuxq-exactlyone] | [`ExactlyOneSeq`][tmuxq-exactlyoneseq] |

`Matching` takes a filter, and the others take a function,
`func(*T) bool`. `First` hands back a second result that is false when
nothing matched, because no match is an ordinary answer rather than a
failure. `ExactlyOne` returns [`ErrNoMatch`][tmuxq-errnomatch] for none and
[`ErrMultipleMatches`][tmuxq-errmultiplematches] for several, so a caller
tells them apart with `errors.Is`. The sequence forms read an
[`iter.Seq`](https://pkg.go.dev/iter#Seq) and pull only as far as they need
to.

### [`PaneFilter.Predicate`][tmux-panefilter-predicate]: a filter, as a function

The helpers that take a function take a filter too, once `Predicate` has
turned it into one:

<!-- docs:queries-one -->

```go
// Given: panes []tmux.Pane
// First and ExactlyOne take a function rather than a filter, and
// Predicate hands over the function a filter stands for.
activeInLogs, err := tmux.PaneFilter{
	Active: new(true), WindowIndex: new(1),
}.Predicate()
if err != nil {
	return fmt.Errorf("compile filter: %w", err)
}
only, err := tmuxq.ExactlyOne(panes, activeInLogs)
if err != nil {
	return fmt.Errorf("find the active pane of window 1: %w", err)
}
window, _ := only.Window()
name, _ := window.Name()
fmt.Printf("the only active pane of window 1: %s.%d\n", name, only.Index())
```

<!-- docs:end -->

<!-- docs:checked examples/queries/output.txt -->
Window 1 is `logs`, and its second pane is the active one, so the program
prints `the only active pane of window 1: logs.1`.
<!-- docs:end -->

[`WindowFilter`][tmux-windowfilter-predicate] and
[`SessionFilter`][tmux-sessionfilter-predicate] have the same method.

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
	open := func(t *task) bool { return !t.done }

	for _, t := range tmuxq.Where(tasks, open) {
		fmt.Println("open:", t.name)
	}
	first, _ := tmuxq.First(tasks, open)
	fmt.Println("first open:", first.name)

	// Two tasks are open, so asking for the only one is an error.
	_, err := tmuxq.ExactlyOne(tasks, open)
	fmt.Println("the only open task:", err)
}
```

<!-- docs:end -->

To run it, save it as `main.go` in an empty directory:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/tmuxq@latest && \
    go run .
```

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

A [`PaneFilter`][tmux-panefilter] stands for a function, `func(*Pane) bool`.
Nothing about applying one is specific to panes, so the applying lives here,
in a package that imports no tmux type. That is also why this package cannot
create an import cycle with the library.

## See also

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, one page each |
| [`../tmux/`](../tmux/) | The library, and where filters are declared |
| [`../examples/queries`](../examples/queries) | The program that runs every query above |
| [`../examples/filter-query`](../examples/filter-query) | Filtering in Go beside letting tmux filter |

[tmux-clientnameis]: https://libtmux.org/en/go/latest/reference/tmux-clientnameis/
[tmux-clientreadonlyis]: https://libtmux.org/en/go/latest/reference/tmux-clientreadonlyis/
[tmux-paneactiveis]: https://libtmux.org/en/go/latest/reference/tmux-paneactiveis/
[tmux-panecommandis]: https://libtmux.org/en/go/latest/reference/tmux-panecommandis/
[tmux-panefilter-active]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-active/
[tmux-panefilter-anyof]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-anyof/
[tmux-panefilter-not]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-not/
[tmux-panefilter-predicate]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-predicate/
[tmux-panefilter-session]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-session/
[tmux-panefilter-window]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-window/
[tmux-panefilter-windowindex]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-windowindex/
[tmux-panefilter-windowindexgte]: https://libtmux.org/en/go/latest/reference/tmux-panefilter-windowindexgte/
[tmux-panefilter]: https://libtmux.org/en/go/latest/reference/tmux-panefilter/
[tmux-paneidis]: https://libtmux.org/en/go/latest/reference/tmux-paneidis/
[tmux-paneindexis]: https://libtmux.org/en/go/latest/reference/tmux-paneindexis/
[tmux-panesessionidis]: https://libtmux.org/en/go/latest/reference/tmux-panesessionidis/
[tmux-panetitleis]: https://libtmux.org/en/go/latest/reference/tmux-panetitleis/
[tmux-panewindowidis]: https://libtmux.org/en/go/latest/reference/tmux-panewindowidis/
[tmux-panewindowindexis]: https://libtmux.org/en/go/latest/reference/tmux-panewindowindexis/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-sessionattachedis]: https://libtmux.org/en/go/latest/reference/tmux-sessionattachedis/
[tmux-sessionfilter-namecontains]: https://libtmux.org/en/go/latest/reference/tmux-sessionfilter-namecontains/
[tmux-sessionfilter-predicate]: https://libtmux.org/en/go/latest/reference/tmux-sessionfilter-predicate/
[tmux-sessionfilter]: https://libtmux.org/en/go/latest/reference/tmux-sessionfilter/
[tmux-sessionidis]: https://libtmux.org/en/go/latest/reference/tmux-sessionidis/
[tmux-sessionnameis]: https://libtmux.org/en/go/latest/reference/tmux-sessionnameis/
[tmux-snapshot-sessions]: https://libtmux.org/en/go/latest/reference/tmux-snapshot-sessions/
[tmux-snapshot-windows]: https://libtmux.org/en/go/latest/reference/tmux-snapshot-windows/
[tmux-windowactiveis]: https://libtmux.org/en/go/latest/reference/tmux-windowactiveis/
[tmux-windowfilter-active]: https://libtmux.org/en/go/latest/reference/tmux-windowfilter-active/
[tmux-windowfilter-nameregex]: https://libtmux.org/en/go/latest/reference/tmux-windowfilter-nameregex/
[tmux-windowfilter-predicate]: https://libtmux.org/en/go/latest/reference/tmux-windowfilter-predicate/
[tmux-windowfilter]: https://libtmux.org/en/go/latest/reference/tmux-windowfilter/
[tmux-windowidis]: https://libtmux.org/en/go/latest/reference/tmux-windowidis/
[tmux-windowindexis]: https://libtmux.org/en/go/latest/reference/tmux-windowindexis/
[tmux-windownameis]: https://libtmux.org/en/go/latest/reference/tmux-windownameis/
[tmux-windowsessionidis]: https://libtmux.org/en/go/latest/reference/tmux-windowsessionidis/
[tmuxq-errmultiplematches]: https://libtmux.org/en/go/latest/reference/tmuxq-errmultiplematches/
[tmuxq-errnomatch]: https://libtmux.org/en/go/latest/reference/tmuxq-errnomatch/
[tmuxq-exactlyone]: https://libtmux.org/en/go/latest/reference/tmuxq-exactlyone/
[tmuxq-exactlyoneseq]: https://libtmux.org/en/go/latest/reference/tmuxq-exactlyoneseq/
[tmuxq-first]: https://libtmux.org/en/go/latest/reference/tmuxq-first/
[tmuxq-firstseq]: https://libtmux.org/en/go/latest/reference/tmuxq-firstseq/
[tmuxq-matching]: https://libtmux.org/en/go/latest/reference/tmuxq-matching/
[tmuxq-matchingseq]: https://libtmux.org/en/go/latest/reference/tmuxq-matchingseq/
[tmuxq-where]: https://libtmux.org/en/go/latest/reference/tmuxq-where/
[tmuxq-whereseq]: https://libtmux.org/en/go/latest/reference/tmuxq-whereseq/
