# filter-query

The two ways to ask tmux a question, side by side: let tmux filter, or read once
and filter in Go.

They answer the same question at different costs, and the choice between them is
about how many answers you want from one read.

## Running it

```console
$ go -C examples run ./filter-query
```

```
active panes: 1
live matches: 1
```

## What to look at

**Let tmux filter.** One command goes out and only matches come back. tmux
evaluates the format itself, so nothing that failed to match is ever sent:

<!-- docs:query-in-tmux -->

```go
live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
sessions, err := server.SearchSessions(ctx, &live)
```

<!-- docs:end -->

**Or read once and filter in Go.** A snapshot is one read of the whole server.
Filtering it costs nothing extra, so this wins as soon as you want several
answers from the same moment:

<!-- docs:query-in-go -->

```go
snapshot, err := server.Snapshot(ctx)
if err != nil {
	return err
}
predicate, err := tmux.PaneActiveIs(true).Predicate()
if err != nil {
	return err
}
active := tmuxq.Where(snapshot.Panes(), predicate)
```

<!-- docs:end -->

The snapshot is also consistent with itself in a way repeated reads are not:
everything in it came from the same instant.

These two blocks are generated from [`main.go`](main.go). Editing the program
rewrites them, so they cannot drift from code that compiles and runs.

## Testing your own version

```console
$ go -C examples test ./filter-query
```

The test asserts the live filter matched the session the example made, since a
filter matching nothing would print a line just the same.
