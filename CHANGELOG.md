# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

### mcp

The registry entry is named for the language rather than the project, since
every server under this namespace drives tmux and the namespace already says
whose it is.

Documentation no longer names a version to install. Two READMEs had told a
reader to fetch `v0.0.1-alpha.1` for the whole life of `v0.0.1-alpha.4`, and
that version is retracted, so the command they gave refused to run.

## mcp/v0.0.1-alpha.4

Adds the MCP registry entry and the marker it is verified against.

## v0.0.1-alpha.2, workspace/v0.0.1-alpha.2

Documentation. The alpha notice no longer uses GitHub's alert extension, which
is literal text in every other renderer. No change to either package.

## mcp/v0.0.1-alpha.3

Listings take criteria and report the total they selected from, so asking which
pane runs a command costs one answer rather than forty. `detail: full` adds each
matching pane's process state from the snapshot the listing already takes.

Waiting is bounded by a ceiling that clamps rather than refuses, and optional:
`run_command` detaches and `get_job` collects. `wait_for_text` gains
`idleSeconds` for a program whose finishing cannot be predicted.

Writes to the pane the server itself runs in ask the person first, through MCP
elicitation. `move_pane`, styled captures, per-hook reads, and the attached
clients and message log on `get_server_info` reach tmux the tools could not.

## mcp/v0.0.1-alpha.2

Retracts `mcp/v0.0.1-alpha.1`, whose go.mod carried replace directives that
`go install` refuses.

## v0.0.1-alpha.1

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
