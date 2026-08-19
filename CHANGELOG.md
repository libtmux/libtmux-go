# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

### All modules

The Go floor is 1.26, raised from 1.23. It now tracks upstream's support
window — Go supports a release until two newer ones exist — rather than a
version chosen once, so expect it to move as releases age out.

This is a hard requirement, not a suggestion. A consumer on an older Go sees
`module ... requires go >= 1.26.0` and cannot build; `go get` will upgrade the
toolchain and rewrite the consumer's own `go` directive to match, which passes
the same floor on to anything that imports them. A toolchain pinned with
`GOTOOLCHAIN=local`, as distribution packages and air-gapped builds often do,
has no upgrade path but the toolchain itself.

Nothing about the API changed. The syntax the floor unlocks was taken across
the tree, and `golangci-lint` now gates it so the older forms cannot return.

### mcp

The server names the signal that ended it. A client tearing the transport down
sends SIGTERM, and the exit line reported the cancellation that produced rather
than the signal itself, so an ordinary disconnect read as `context canceled` —
the mechanism, not the reason, and impossible to tell from a fault. It now
reads `terminated signal received`, or `interrupt signal received` for SIGINT.

`run_command` returns what the command actually printed. Three things could make
the reply disagree with the command. A tab anywhere in a command reached the
shell's line editor as a request to complete a filename, so a different command
ran and reported success; commands are now written to a file and sourced, and
nothing a caller sends crosses that line editor. A detached run returned the
shell's next prompt as a line of output, because the row the closing mark points
at was read whether or not the command's last line ended there; the cursor's
column is now recorded beside its row and settles that. Output was read one
entry per screen row, so a line longer than the pane arrived split — wrapped
rows are rejoined. The pane also stays readable: what it shows is one short
line rather than the whole wrapper echoed across the screen.

`run_command` refuses a pane whose program has exited, naming `respawn_pane`,
instead of waiting out its timeout on a pane that reads no keys and then
reporting the exited shell as though it were busy.

When output cannot be read, `outputUnavailable` says why. A caller could not
previously tell a command that printed nothing from a pane it failed to read.

`list_servers` reports the servers that are running. tmux leaves a socket file
behind when a server exits, so the directory only grows: on a machine that has
run test suites this was hundreds of entries, nearly all of them dead, and the
running servers were the hardest thing to find in the reply. `includeDead`
brings them back, `name` and `maxServers` narrow further, and `total` and
`skipped` say what was left out.

The batch tools name the calls they skipped. All three stop at the first
failure, and `skipped` now lists what never ran — which the mutating and
destructive batches need, since tmux has no transaction and what already ran
stays. A batch asked to call a batch says that it cannot be called from inside
one, rather than that the server does not serve it.

`send_keys` describes its `command` argument, which carries the warning that
tmux key names are read there, so `C-c` interrupts and `Escape` is a key.
`exit_copy_mode` no longer advertises `scrollUp`, which it shared with entering
copy mode and never read. `list_panes` reports `panes` as an array when nothing
matches, as `list_sessions` and `list_windows` already did.

`show_buffer` says which buffer it looked for and that only buffers this server
staged can be read, rather than reporting that a tmux command exited non-zero.

The `capture_since` cursor is half the size. It carries a fingerprint per row
and travels in every reply, so for a mostly blank screen it cost more than
sending that screen would have.

`mcp-swap` takes `--client`, so a build can be tried in one agent while the
others keep whatever they run.

The registry entry is named for the language rather than the project, since
every server under this namespace drives tmux and the namespace already says
whose it is.

Documentation no longer names a version to install. Two READMEs had told a
reader to fetch `v0.0.1-alpha.1` for the whole life of `v0.0.1-alpha.4`, and
that version is retracted, so the command they gave refused to run.

### workspace

A rejection that has no line to point at no longer claims "line 0". An empty or
unparseable document reported a line that cannot exist, sending a reader looking
there when the parser's own message named the real one.

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
