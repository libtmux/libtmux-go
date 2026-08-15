# tmuxq

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmuxq.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq)

Generic helpers for filtering and collecting tmux records. It imports no tmux
types, so it works on any slice or `iter.Seq`.

```go
import "github.com/libtmux/libtmux-go/tmuxq"
```

## Why it is separate

A `PaneFilter` compiles to a predicate, and a predicate is just
`func(Pane) bool`. Nothing about applying one is specific to panes, so the
applying lives here and stays model-free — which is also why this package cannot
create an import cycle with the library.

## Using it

Read once, then answer several questions from that one read:

```go
snapshot, err := server.Snapshot(ctx)
if err != nil {
	return err
}

active, err := tmux.PaneActiveIs(true).Predicate()
if err != nil {
	return err
}

panes := tmuxq.Where(snapshot.Panes(), active)
first, ok := tmuxq.First(snapshot.Panes(), active)
```

`Where` returns a fresh slice, so changing it cannot change the snapshot. `First`
uses comma-ok rather than a sentinel, because "no match" is not an error.

For the cardinality helpers — exactly one, at most one — the package owns its own
sentinels so a caller can tell "none" from "several" with `errors.Is`.

Runnable: [`../examples/filter-query`](../examples/filter-query).

## See also

| | |
| --- | --- |
| [`../tmux/`](../tmux/) | The library, and where filters are declared |
