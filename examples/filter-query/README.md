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
typed panes: 1
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
active, err := tmuxq.Matching(snapshot.Panes(), tmux.PaneActiveIs(true))
if err != nil {
	return err
}
```

<!-- docs:end -->

Typed filters combine fields and captured relations without another tmux command:

<!-- docs:query-typed-in-go -->

```go
filter := tmux.PaneFilter{
	Active:  new(true),
	Session: &tmux.SessionFilter{Name: new("libtmux-filter")},
}
panes, err := tmuxq.Matching(snapshot.Panes(), filter)
if err != nil {
	return err
}
```

<!-- docs:end -->

The snapshot retains the captured hierarchy while repeated reads may observe
later changes.

These blocks are generated from [`main.go`](main.go). Editing the program
rewrites them, so they cannot drift from code that compiles and runs.

## Testing your own version

```console
$ go -C examples test ./filter-query
```

The test asserts the live filter matched the session the example made, since a
filter matching nothing would print a line just the same.
