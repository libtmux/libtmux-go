# filter-query

The two ways to ask tmux a question, side by side: let tmux filter, or read once
and filter in Go.

They answer the same question at different costs, and the choice between them is
about how many answers you want from one read.

## Running it

```console
$ go -C examples run ./filter-query
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/filter-query/output.txt -->

```text
session: libtmux-filter
active panes: 1
typed panes: 1
live matches: 1
```

<!-- docs:end -->

## What to look at

**Let tmux filter.** One command goes out and only matches come back. tmux
evaluates the format itself, so nothing that failed to match is ever sent:

<!-- docs:query-in-tmux -->

```go
// Given: ctx context.Context; server tmux.Server
live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
sessions, err := server.SearchSessions(ctx, &live)
if err != nil {
	return err
}
fmt.Println("live matches:", len(sessions))
```

<!-- docs:end -->

**Or reuse a snapshot in Go.** A snapshot collects the server's lists once;
filtering the captured records sends no further commands. Collection is
sequential, so it does not promise one atomic view:

<!-- docs:query-in-go -->

```go
// Given: ctx context.Context; server tmux.Server
snapshot, err := server.Snapshot(ctx)
if err != nil {
	return err
}
active, err := tmuxq.Matching(snapshot.Panes(), tmux.PaneActiveIs(true))
if err != nil {
	return err
}
fmt.Println("active panes:", len(active))
```

<!-- docs:end -->

Typed filters combine fields and captured relations without another tmux
command:

<!-- docs:query-typed-in-go -->

```go
// Given: snapshot tmux.Snapshot
filter := tmux.PaneFilter{
	Active:  new(true),
	Session: &tmux.SessionFilter{Name: new("libtmux-filter")},
}
panes, err := tmuxq.Matching(snapshot.Panes(), filter)
if err != nil {
	return err
}
fmt.Println("typed panes:", len(panes))
```

<!-- docs:end -->

The snapshot retains the captured hierarchy while repeated reads may observe
later changes.

## Running its test

```console
$ go -C examples test ./filter-query
```
