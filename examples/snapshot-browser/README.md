# snapshot-browser

One read of the whole server, walked down all three levels.

A snapshot is every session, window, and pane as they were at one instant. It is
one tmux command, and everything you ask of it afterwards is free.

## Running it

```console
$ go -C examples run ./snapshot-browser
```

```
session $0 "libtmux-snapshot"
  window @0:1
    pane %0 "zsh"
```

## What to look at

**One read, not three.** Listing sessions, then windows per session, then panes
per window is one command plus one per session plus one per window. This is one
command total.

**It is consistent with itself.** Everything in a snapshot came from the same
moment. Walking it cannot show you a window whose session has since been killed,
which a sequence of separate reads can.

**It never refreshes.** The snapshot is what tmux said when it was taken.
Nothing in it changes underneath you, which is what makes it safe to walk twice
and get the same answer.

**Values, not handles.** A pane's name and current command are already there.
Reading them starts nothing.

## Testing your own version

```console
$ go -C examples test ./snapshot-browser
```

The test asserts a line from each of the three levels, since a traversal that
listed sessions and found no windows under them would still print something.

## See also

[`filter-query`](../filter-query) — filtering a snapshot in Go, next to letting
tmux filter instead.
