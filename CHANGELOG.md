# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

### tmux

- `NewServer` now reports construction errors, rejects invalid configuration,
  and freezes the executable, environment, working directory, and socket
  selection. The zero `Server` is invalid. (#9)
- `Server.Executable`, `Server.SocketSelection`, and `Server.WithSocketPath`
  expose and derive the frozen execution target without contacting tmux.
  `Server.SocketPath` now returns the resolved absolute path. (#9)
- Remove `CommandRunner`, `Engine`, `ControlPool`, `WithEngine`, the record
  `WithServer` methods, and `ServerOptions.Runner`. Use an owned `Connection`,
  re-resolve records on the target server, and use `ServerOptions.Binary` for
  executable fixtures. (#9)
- Add `Server.OpenControl`, `Session.OpenControl`, and
  `Server.NewSessionConnection` for owned control-mode execution. On tmux 3.2a
  through 3.5, destroying the attached session may close the connection
  according to `detach-on-destroy`. (#9)
- `ControlClient.Call` returns every reply frame from aliases that execute zero
  or multiple commands. `ControlClient.Cmd` now returns
  `ErrControlReplyCount` unless exactly one frame was produced. (#9)
- `Session`, `Window`, `Pane`, `Client`, and plan references now retain
  daemon identity. Values from a replaced daemon fail with
  `ErrDaemonReplaced`, and matching identifiers from different daemons no
  longer compare equal. (#9)
- `Server.OpenNotifications` and `Session.OpenNotifications` add owned,
  bounded notification streams. A full backlog drains its retained prefix
  before returning `ErrControlNotificationOverflow`. (#9)
- `NotificationOptions.PauseAfter` and `NotificationStream.ContinuePane` use
  tmux's per-pane hold when a reader falls behind. Held output is not replayed,
  so recapture pane content after continuing. (#9)
- `Pane.OpenObservation` adds a baseline plus a serialized, gap-checked
  notification reader. It permanently returns `ErrPaneObservationLost` when
  the pane leaves its window or the stream ends, preserving tmux's exit reason.
  (#9)
- `Plan.RunWith` marks every operation in an ambiguous grouped failure as
  `OpIndeterminate` and returns `ErrOutcomeUnknown`. The `Marked` planner and
  `Dispatch.Marked` are removed; use `Sequential` for exact attribution.
  (#9)
- `Pane.SendKeySequence` sends an ordered key sequence in one tmux operation.
  (#9)

### mcp

- MCP now supports tmux 3.2a or newer, lowered from 3.6. On tmux 3.2a through
  3.5, destroying the attached session may end the instance. (#9)
- Add `LIBTMUX_MCP_CAPABILITIES` as an independent access allowlist. Empty or
  unset exposes metadata only; set `operate` to retain the previous
  non-destructive surface. `display_message` also leaves `readonly`, because
  tmux formats can execute shell commands. (#9)
- `mcp.NewServer` now returns an owned `Instance`, and package-level `Connect`
  is removed. Use `Instance.Connect`, `Run`, and `Close`; mark eligible
  custom transports with `AssumeResponseCommit`. (#9)
- `Instance` scopes consent, subscriptions, waits, and detached jobs to one
  client and bounds unsettled calls. Exceeding a bound closes only the
  offending client with `ErrRequestCapacity`. (#9)
- `Instance` no longer adopts a replacement tmux daemon on the same socket.
  Restart the MCP server after `ErrDaemonReplaced`. (#9)
- `run_command` applies its effective timeout to setup, execution, and output
  collection, preserves the pane shell's error mode, and refuses known
  non-POSIX shells before delivery. (#9)
- `get_job` now explains every unfinished answer, including zero-timeout polls,
  and names the pane's current command when available. It distinguishes a
  shell still starting from another program consuming the input. (#9)
- `wait_for_text` matches readable terminal text across split UTF-8, control
  sequences, and carriage-return or backspace overwrites. It also fails as soon
  as the observed pane leaves its window. (#9)
- `capture_since` now reports missed lines when tmux discarded a cursor
  anchored on the pane's first row. (#9)
- `create_session`, `create_window`, and `build_workspace` retain created
  identifiers in error results when tmux completed the mutation before later
  setup failed. (#9)
- Buffer and wait-channel names now accept embedded whitespace. (#9)
- Pane-content resources now advertise `text/plain`. Resource subscriptions
  retain pane output only where requested and hand replacement observers over
  without a notification gap. (#9)
- Audit records now digest caller-supplied session names instead of recording
  them in cleartext. (#9)
- `AdvertisedTools` and `libtmux-mcp -tools` report the configured tool
  surface without resolving or contacting tmux. (#9)
- `mcp-swap` validates configuration and completes an MCP handshake before
  changing files. Updates are atomic, preserve TOML and JSONC syntax, and
  `--dry-run` performs the same validation without writing. (#9)
- `list_panes` with `pathUnder` now treats symlinked directory spellings as
  the same path while still excluding sibling prefixes. (#9)
- `send_keys_batch` sends its sequence in one tmux command instead of starting
  one process per key. (#9)
- `list_servers` defaults to 100 results, caps requests at 1000, and applies
  the limit before launching socket probes. A requested target is retained
  even with a name filter; truncation is reported and directory-read failures
  surface. (#9)

### workspace

- Add `Workspace.InitialSessionRequest` and `BuildInto`, allowing callers to
  populate a session over a caller-owned connection without transferring
  ownership. (#9)
- `Build` now creates the session and temporary connection together, closes
  the connection before returning, and verifies that the session survived.
  (#9)

## v0.0.1-alpha.4, workspace/v0.0.1-alpha.4, mcp/v0.0.1-alpha.7

### tmux

- `NewSession` with `KillExisting` no longer kills a session whose name matches
  another session's identifier. Creating one could end that session, and end
  the server with it when it was the last.
- `HasSession` with `Pattern` false now answers for the name itself. `Pattern`
  true still accepts tmux's full target syntax.
- `NewSession` now rejects a session name containing a control character, a
  DEL, or malformed UTF-8. tmux accepted these before 3.7 and stored the name
  visibility-encoded.
- A control-mode connection no longer returns another command's reply. tmux
  writes a guard block for every command it runs, not only the client's own,
  and reading a stranger's shifted every later reply by one.
- `SelectLayout` now accepts `main-horizontal-mirrored` and
  `main-vertical-mirrored` on tmux 3.5 and newer.
- `RefreshClientRequest.RequestClipboard` now requires tmux 3.4, lowered from
  3.7.
- `FormatValues.SessionActive` reports tmux's `session_active`.
- A socket directory tmux refuses to use now returns `ErrNoServer` rather than
  an unrecognised command failure. The reason tmux gave is carried on the
  error.

### mcp

#### Watching a pane

- A subscription to `tmux://panes/%1/content` now receives notifications. Only
  the sigil-less spelling worked, and every tool returns a pane with its sigil.
- A subscriber is now notified of writes that happen while the watcher rebuilds
  its connections.
- A notification suppressed by the coalescing window is now deferred rather
  than dropped, so the last write of a burst is reported.

#### Running commands

- `run_command` now returns the output of a command that clears the screen or
  erases the scrollback. Both previously read as a command that printed
  nothing.
- `run_command` no longer returns its own sourcing line as output.
- `run_command` now reports output that is entirely blank lines, rather than
  reporting no output.
- `run_command` no longer writes its wrapper's errors into the pane after a
  timeout.

#### Panes that cannot read

- `send_keys`, `send_keys_batch`, `paste_text`, `paste_buffer` and
  `run_command` now refuse a pane whose program has exited, naming
  `respawn_pane`. They reported the keys sent, or a count, for keystrokes
  nothing read.
- The same five now refuse a pane in copy mode, where a key is read as that
  mode's binding rather than reaching the program. A binding that waits for a
  further key never answers the client that sent one.

#### Safety

- A write to the pane the server runs in is now refused when the client cannot
  be asked about it. It was previously allowed, which made the guard advisory
  for every client without elicitation support.
- `kill_window`, `kill_session` and `kill_server` now refuse a target holding
  the pane the server runs in.
- A batched write to that pane now asks, as a direct call already did.
- Writes to that pane can now be allowed for the rest of the session. Ending
  the pane, its window, its session, or the server asks every time.
- `show_environment` now returns variable names without their values. A call
  with no arguments returned every value, credentials included.

#### Schemas

- `scope`, `direction`, `detail`, and `get_recipe`'s `name` now publish their
  accepted values as a JSON Schema `enum`.
- A call inside a batch is now validated against the tool's schema. The batch
  tools were the one route to a handler with no schema in front of it.
- Collections in a reply are now typed `array` rather than `null` or `array`.
- `scope` and `direction` no longer accept case variants such as `SERVER` or
  `RIGHT`. Use the canonical spellings the schema publishes.
- `show_option`, `set_option` and `show_hooks` now reject `windowId` at pane
  scope rather than ignoring it. Pass `scope: window` to read at window scope.
- `select_layout` now refuses `layout` and `spread` together rather than
  honouring one. Pass `spread` to even the panes already in the window, or a
  layout to replace the arrangement.
- `capture_since` cursors are now version 2, about a third smaller. A version 1
  cursor is refused; call `capture_since` without one to start again.

#### Diagnostics

- `list_panes`, `list_windows` and `list_sessions` now report `serverNote` when
  no tmux server is running on the socket.
- A resource read that names nothing now returns `-32002 Resource not found`
  and names the listing to call, rather than tmux's `display-message exited 1`.
- `kill_pane`, `kill_window`, `move_pane`, `move_window`, `select_pane` and
  `select_window` now name the listing that finds an id, rather than repeating
  tmux's `snapshot object not found`.
- `respawn_pane` now reports `gone` when the pane cannot be read back after a
  respawn that itself succeeded. A command that exits takes the pane with it,
  which read as a failed respawn naming a snapshot that did not hold it.
- `-doctor` now reports a `LIBTMUX_SAFETY` value it did not recognise. An
  unrecognised value still selects `readonly`.

#### Resources and completions

- A resource URI now decodes percent-escapes, so
  `tmux://sessions/spaced%20name` reads the session named `spaced name`. A name
  needing an escape reached tmux with the escape still in it.
- Completion now answers in the dialect the caller asked in: tmux's own
  spelling for a prompt argument, the URI spelling for a resource slot. A
  completed prompt argument named a pane that every tool rejects.

#### Additions

- Add `onError` to the three batch tools, choosing between stopping at the
  first failure and running the calls after it. Stopping remains the default.
- Add `tmux://sessions/{session}` and `tmux://windows/{window}` resources.
- Add annotations to every tool, marking it read-only, mutating, settling or
  destructive, so a client can tell a read from a kill before it calls.
- Add `mcp/TOOLS.md`, a per-tool reference generated from the registered
  schemas. `go generate` rewrites it, so a schema and the documentation of it
  cannot drift.
- Add `LIBTMUX_SOCKET`, `LIBTMUX_SOCKET_PATH` and `LIBTMUX_TMUX_BIN`, matching
  the Python server of the same name. A flag naming a socket wins over them.
- Add `skipped` to `list_panes`, `list_windows` and `list_sessions`, the count
  their criteria left out.
- Add `mcp/PARITY.md`, comparing this server with the Python server of the same
  name.

#### Reliability

- The server now survives a tmux restart.
- A frame that will not parse no longer ends the server.

### workspace

- The document check now rejects two windows claiming one `window_index`,
  naming the line and both windows. tmux refuses the second itself, but reports
  only that `new-window` exited non-zero.

## v0.0.1-alpha.3, workspace/v0.0.1-alpha.3, mcp/v0.0.1-alpha.6

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

### workspace

- A rejection with no line to point at no longer reports "line 0". An empty or
  unparseable document reported a line that cannot exist.

## mcp/v0.0.1-alpha.5

- The registry entry is named for the language rather than the project.
- The READMEs no longer name a version to install. Both told a reader to fetch
  `v0.0.1-alpha.1`, which is retracted, so the command refused to run.

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
