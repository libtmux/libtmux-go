# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

### All modules

- Go 1.26 is now required, raised from 1.23. The minimum tracks upstream's
  support window, so it moves as releases age out. A consumer on an older Go
  cannot build, and `go get` passes the same minimum on to anything importing
  these modules. No exported identifier changed.
- tmux 3.7c no longer fails the key-binding compatibility check. It leaves
  stdout empty for a table's sole binding exactly as 3.7 through 3.7b do, and
  the check now expects that of it. The supported range is unchanged: 3.7c is
  not in the tested matrix, so nothing here claims to check it.

### tmux/tmuxtest

- Add `SuiteRootTagVariable`, naming the environment variable that tags a
  suite's temporary root. Packages run in parallel, so a test that spawns a
  child suite could not tell its child's root from a sibling binary's.

### mcp

- `run_command` now writes the command to a file and sources it, so a tab in a
  command no longer reaches the shell's line editor as a filename completion
  and runs something else.
- `run_command` no longer returns the shell's next prompt as a line of output.
  The cursor's column is recorded beside its row, which settles whether the
  closing row belongs to the output.
- `run_command` now rejoins wrapped rows, so a line longer than the pane is
  returned as the one line the command printed.
- `run_command` now refuses a pane whose program has exited, naming
  `respawn_pane`, rather than waiting out its timeout and reporting the exited
  shell as busy.
- `outputUnavailable` now reports why output could not be read, so a command
  that printed nothing can be told from a pane that could not be read.
- `list_servers` now reports only running servers. tmux leaves a socket file
  behind when a server exits, so the directory only grows. `includeDead` brings
  them back; `name` and `maxServers` narrow further; `total` and `skipped` say
  what was left out.
- The batch tools now report `skipped`, the calls that never ran after a
  failure. tmux has no transaction, so what already ran stays.
- A batch asked to call a batch now says it cannot be called from inside one,
  rather than that the server does not serve it.
- `list_panes` now reports `panes` as an array when nothing matches, as
  `list_sessions` and `list_windows` already did.
- `exit_copy_mode` no longer advertises `scrollUp`, which it shared with
  entering copy mode and never read.
- `send_keys` now documents that its `command` argument is read as tmux key
  names, so `C-c` interrupts and `Escape` is a key.
- `show_buffer` now names the buffer it looked for and says that only buffers
  this server staged can be read, rather than reporting a non-zero exit.
- The `capture_since` cursor is now half the size. It carries a fingerprint per
  row and travels in every reply, so on a mostly blank screen it cost more than
  the screen it saved.
- The server now reports the signal that ended it — `terminated signal
  received`, or `interrupt signal received` for SIGINT — rather than the
  cancellation that signal produced.
- Add `--client` to `mcp-swap`, so a build can be tried in one agent while the
  others keep what they run.
- `mcp-swap revert` no longer discards a configuration edited after a previous
  revert. The backup is removed once used, so the next swap copies the file as
  it is then.
- `mcp/go.mod` now requires the current core. It required `v0.0.1-alpha.1` for
  the whole life of `v0.0.1-alpha.2`, and it is the one module here with no
  `replace` directive, so that requirement is what `go install` resolved.
- The registry entry is named for the language rather than the project.
- The READMEs no longer name a version to install. Both told a reader to fetch
  `v0.0.1-alpha.1`, which is retracted, so the command refused to run.

### workspace

- A rejection with no line to point at no longer reports "line 0". An empty or
  unparseable document reported a line that cannot exist.

## mcp/v0.0.1-alpha.4

- Add the MCP registry entry and the marker it is verified against.

## v0.0.1-alpha.2, workspace/v0.0.1-alpha.2

- The alpha notice no longer uses GitHub's alert extension, which renders as
  literal text elsewhere. Documentation only; neither package changed.

## mcp/v0.0.1-alpha.3

- Add criteria to `list_panes`, `list_windows` and `list_sessions`, and report
  the total each selected from.
- Add `detail: full` to `list_panes`, reporting each matching pane's process
  state from the snapshot the listing already takes.
- Add `detach` to `run_command`, and `get_job` to collect the result.
- Add `idleSeconds` to `wait_for_text`, for a program whose finishing cannot be
  predicted.
- Waits are now bounded by a ceiling that clamps rather than refuses.
- Writes to the pane the server runs in now ask through MCP elicitation.
- Add `move_pane`, styled captures, per-hook reads, and the attached clients
  and message log on `get_server_info`.

## mcp/v0.0.1-alpha.2

- Retract `mcp/v0.0.1-alpha.1`, whose `go.mod` carried replace directives that
  `go install` refuses.

## v0.0.1-alpha.1

First release.

### tmux

Sessions, windows, panes, and clients are typed values that never refresh
behind the caller: a record is what tmux said when it was asked. Every tmux
option and hook has a typed accessor reporting whether a value was set at that
level or inherited.

Filters compile to a predicate applied in Go, or push down to tmux's own format
evaluation. Errors are classified by what tmux refused, with `ErrNoServer`
separating "nothing is running" from "the question could not be answered".

Commands reach tmux by subprocess or by a control-mode connection, chosen per
server. `BENCHMARKS.md` reports what each costs.

The module imports only the standard library. It supports Go 1.23 and newer,
and tmux 3.2a through 3.7b.

### tmux/tmuxtest

A real tmux server for a test, isolated down to its socket, configuration, and
environment, and cleaned up with the test that created it — verified dead
rather than assumed.

Screen assertions poll rather than sleep, so a quick program costs milliseconds
and a slow one is still waited for. A failure reports the screen the pane last
held.

### tmuxq

Query helpers over slices of records, free of the tmux model.

### workspace

Loads tmuxp-style YAML workspaces and builds them.

### mcp

Serves one tmux server to Model Context Protocol clients. Panes are read
incrementally through a cursor, a command reports how it ended rather than
leaving a client to scrape the screen, and tools above a chosen safety tier are
withheld.
