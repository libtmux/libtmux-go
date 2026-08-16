# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## v0.0.1-alpha.1 — unreleased

First release.

### tmux

Sessions, windows, panes, and clients as typed values that never refresh behind
you: a record is what tmux said when you asked. Every tmux option and hook has a
typed accessor reporting whether a value was set here or inherited. Filters
compile to a predicate applied in Go, or push down to tmux's own format
evaluation. Errors are classified by what tmux refused, with `ErrNoServer`
separating "nothing is running" from "the question could not be answered". The
module imports only the standard library.

Commands reach tmux by subprocess or by a control-mode connection, chosen per
server, so the cost of a call is a decision rather than a default.
`BENCHMARKS.md` reports what each mode costs.

Supports Go 1.23 and newer, and tmux 3.2a through 3.7b, checked against every
release in that range.

### tmux/tmuxtest

A real tmux server for a test, isolated down to its socket, configuration, and
environment, cleaned up with the test that created it — verified dead rather
than assumed. Screen assertions poll rather than sleep, so a quick program costs
milliseconds and a slow one is still waited for, and a failure reports the
screen the pane last held.

### tmuxq

Query helpers over slices of records, free of the tmux model.

### workspace

Loads tmuxp-style YAML workspaces and builds them.

### mcp

Serves one tmux server to Model Context Protocol clients. Reads panes
incrementally through a cursor, runs a command and reports how it ended rather
than leaving a client to scrape the screen, and hides tools above a chosen
safety tier.
