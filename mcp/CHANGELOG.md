# Changelog

Notable changes to `github.com/libtmux/libtmux-go/mcp`.

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

The modules are tagged together, so a release heading below names the tags of
its release; this module's tag is `mcp/vX.Y.Z`. The tmux module and `workspace`
keep their own changelogs: [core][core-changelog] and
[`workspace`][workspace-changelog]. Changes that span modules, such as
documentation, CI and release tooling, are in the core changelog.

[core-changelog]: https://github.com/libtmux/libtmux-go/blob/master/CHANGELOG.md
[workspace-changelog]: https://github.com/libtmux/libtmux-go/blob/master/workspace/CHANGELOG.md

## Unreleased

## v0.1.2, workspace/v0.1.2, mcp/v0.1.2 (2026-11-01)

### mcp

- Add `LIBTMUX_ALLOW_ATTENDED_INPUT`, a launch setting that is off by default.
  Set to `1`, it lets the input tools deliver to a pane a tmux client is
  viewing, which they otherwise refuse with `pane is attended by a tmux client`.
  It lifts that refusal only: a dead pane, a pane with input disabled, a pane in
  a mode, and a synchronized pane with such a member are still refused. Any
  other value stops startup with a message that names the variable.
  `tmux://capabilities` reports the setting as `allowAttendedInput`, and
  `libtmux-mcp -doctor` prints it. (#113)
- `run_shell_command` now says in its description that a tmux command run there
  acts on the session's current window unless it is targeted, and to pass
  `-t "$TMUX_PANE"` to mean the pane. (#114)

## v0.1.1, workspace/v0.1.1, mcp/v0.1.1

### mcp

- `libtmux-mcp` no longer starts a server of its own on the socket `libtmux-mcp`
  when no socket is selected. It passes tmux no socket, so tmux picks the
  server: the one `TMUX` names inside tmux, otherwise its default socket. A
  launch that relied on the old default now reaches your own sessions; name a
  socket with `-socket-name`, `-socket-path`, `LIBTMUX_SOCKET`, or
  `LIBTMUX_SOCKET_PATH` to keep an agent apart from them.
- `libtmux-mcp` no longer refuses to start when `LIBTMUX_SOCKET` and
  `LIBTMUX_SOCKET_PATH` are both set: the path wins. A variable that is empty or
  only whitespace now counts as unset instead of failing startup, and a
  relative `LIBTMUX_SOCKET_PATH` is accepted and resolved against the working
  directory. `-socket-path` still requires an absolute path.
- `libtmux-mcp` no longer trims the spaces around `LIBTMUX_SOCKET` and
  `LIBTMUX_SOCKET_PATH`. A padded value is used as written, and only an empty
  or all-whitespace value counts as unset.
- `tmux://capabilities` now reports `socketProvenance` as `explicit`,
  `env-socket-path`, `env-socket`, or `tmux-choice`, replacing
  `default-dedicated` and `operator-current`. `libtmux-mcp -doctor` prints the
  same term.
- `teardown` is no longer in the default toolsets for any launch. Select it
  with `LIBTMUX_TOOLSETS`.
- Remove `RunDefaultMinimal` and `MaterializeMinimalConfig`, and the third
  parameter of `AdvertisedToolsFor`, with the default server they served. Use
  `Run` and `AdvertisedToolsFor(ctx, target)`.

## v0.1.0, workspace/v0.1.0, mcp/v0.1.0

No entries for `mcp`.

## v0.0.1-alpha.13, workspace/v0.0.1-alpha.13, mcp/v0.0.1-alpha.16

No entries for `mcp`.

## v0.0.1-alpha.12, workspace/v0.0.1-alpha.12, mcp/v0.0.1-alpha.15

### mcp

- Startup, `capture_since`, and `run_shell_command` remain available on tmux
  3.3a. Non-empty `paste_text` requires 3.4 before staging; `wait_for_text`
  polls with `capture-pane` below 3.7, joining wrapped lines as the control
  path does. (#60)
- `wait_for_text` ends with `paneDied` and the output so far when the pane,
  its program (under `remain-on-exit`), its window or session, or the server
  goes, and with `streamLost` when the
  pane is alive but its output stream ended. (#60)

## v0.0.1-alpha.11, workspace/v0.0.1-alpha.11, mcp/v0.0.1-alpha.14

### mcp

- Answer a refused tmux from any tool, and at startup, with the fix: restart
  the tmux server from a newer tmux, or set `LIBTMUX_TMUX_BIN` and restart the
  server. (#59)
- Add `minimumVersion` to `get_server_info` and to `-doctor`. (#59)
- Return `versionRefusal` from `get_server_info` on a refused tmux, with the
  installed version and `alive` true while the daemon answers on its socket,
  instead of failing. (#59)

## v0.0.1-alpha.10, workspace/v0.0.1-alpha.10, mcp/v0.0.1-alpha.13

### mcp

- `send_keys`, `send_keys_batch`, `paste_text`, `run_shell_command`, and every
  other tool now refuse a required pane, window, or session id sent empty,
  inside batches too, and audit the refusal. An empty id used to mean the
  active pane; send the id. (#53)
- `run_shell_command` now finds its output once the pane's history has
  filled, where it returned nothing or only the end, and reports
  `lines_missed` when tmux discarded the start. (#53)
- `run_shell_command` now reports `truncated`, `truncated_lines`, and
  `truncated_bytes`. (#53)
- `run_shell_command` now returns what a timed-out command printed so far,
  where it returned no output. (#53)
- `run_shell_command` no longer returns a prompt with the output when a shell
  with no line editor, such as dash, draws it after the typed keys. (#53)
- `run_shell_command` now says at once when its shell went away, because the
  pane was respawned or tmux exited, where it waited out its timeout. (#53)
- `run_shell_command` no longer leaves a pane refusing all input after a
  timeout: `send_keys` and `paste_text` reach it, an interrupt or
  `respawn_pane` frees it, and an interrupted run reports exit status 130.
  (#53)
- `run_shell_command`, `paste_text`, `send_keys`, and `send_keys_batch` now
  wait up to two seconds for a new pane's shell, where they refused the pane.
  A new pane that settles on anything but a shell is still refused. (#53)
- `list_windows` now returns an error pointing at `list_sessions` when
  `session` names no session, where it returned an empty list. (#53)
- `list_sessions` and the other read-only tools now answer the first call
  after the tmux server exits as they document an unreachable server, where
  they returned the lost connection's error. (#53)
- `wait_for_text` now fills its text content with the outcome when a wait
  ends without lines, such as a timeout. (#53)
- Every tool now rejects an unknown argument with the arguments it accepts,
  on direct calls and inside `call_read_tools_batch`. (#53)
- `call_read_tools_batch` now names the failing operation and its tool when it
  rejects a batch. (#53)
- Every tool from a toolset the server was not started with is now refused
  with that toolset's name and `LIBTMUX_TOOLSETS`. (#53)
- Every tool now refuses a malformed pane, window, or session id with what an
  id looks like and which tool lists them. (#53)
- `agent-workflow` now uses a socket of its own unless given `-socket-name`,
  and takes `-trace` to print the calls it makes. (#53)

## v0.0.1-alpha.9, workspace/v0.0.1-alpha.9, mcp/v0.0.1-alpha.12

### mcp

- `select_layout` accepts unique named-layout abbreviations for the running
  tmux version and uppercase saved-layout checksums. It uses core validation
  before window lookup; tool discovery describes named and saved inputs. (#15)
- `kill_session` now resolves its target session by exact name instead of
  tmux's `=name` prefix match, which misreads a period or colon in the name
  as a target separator; a session named with either can now actually be
  killed instead of surviving the call. (#15)

## v0.0.1-alpha.8, workspace/v0.0.1-alpha.8, mcp/v0.0.1-alpha.11

### mcp

- `wait_for_text` distinguishes existing text and pending input from new
  output. Read the `alreadyOnScreen` outcome and `pendingInputOnly` field in
  place of `matchedAtEntry`. (#16)
- `wait_for_text` resumes from the exact position its supplied cursor names.
  (#16)
- `select_layout` accepts saved layouts and unique preset prefixes; JSON
  layouts preserve exact pane placement on tmux 3.8 and newer. (#16)
- `libtmux-mcp --tools` lists the tools the selected socket would advertise.
  (#16)
- `libtmux-mcp` creates its socket directory before starting a daemon. (#16)
- `libtmux-mcp` recovers after its daemon exits; the next `create_session`
  can start a new server. (#16)
- Server inspection and session creation work before a running daemon has
  any sessions. (#16)
- `send_keys` and `send_keys_batch` accept `enter` to submit literal text.
  (#16)
- Attachment reports exclude every control client this MCP process owns.
  (#16)

## v0.0.1-alpha.7, workspace/v0.0.1-alpha.7, mcp/v0.0.1-alpha.10

### mcp

- `paste_text` now stages text larger than tmux's 16 KiB command limit as
  appended chunks, so a large paste succeeds instead of failing with
  `command too long`. Staging stays on the control connection this server
  holds, without a tmux process or a file. (#14)

## v0.0.1-alpha.6, workspace/v0.0.1-alpha.6, mcp/v0.0.1-alpha.9

### mcp

- Replace ordered safety tiers with one native 45-tool capability manifest. The
  manifest governs registration, startup-frozen toolset and exact-name
  selection, schemas, annotations, per-tool metadata, aggregate authority, and
  the static `tmux://capabilities` resource. (#10)
- Rename four tools. `run_command` is now `run_shell_command` and no longer
  offers detached mode; `clear_pane` is now `clear_pane_scrollback` and is
  classified as teardown; `call_readonly_tools_batch` is now
  `call_read_tools_batch` with exact inspect-only nested authority; `set_option`
  is replaced by the constrained `set_mouse_enabled`, `set_history_limit`, and
  `set_synchronize_panes`. (#10)
- Add `get_tmux_variables`, which reads validated variable names and replaces
  `display_message`. There is no free-form tmux-format interpreter. (#10)
- Remove `enter_copy_mode` and `exit_copy_mode`. Use capture, history,
  snapshot, search, and cursor tools to observe pane output without taking over
  an attached person's mode; `Pane.CopyMode` remains in the core tmux module
  for applications that own the interaction. (#10)
- Remove `build_workspace`, `move_pane`, `set_environment`, and `kill_server`,
  which have no direct replacement. Compose the retained creation and layout
  tools for the first two, and kill selected sessions explicitly rather than the
  server. (#10)
- Remove host-adjacent and off-manifest families: dynamic resources, prompts,
  subscriptions, server discovery, generic mutation batches, buffers, pipe
  tools, and background job handles. Migrate recipes to the typed-tool
  workflows. (#10)
- `LIBTMUX_MCP_CAPABILITIES` and `LIBTMUX_MCP_PROMPTS_AS_TOOLS` now fail startup
  instead of being ignored. Migrate capability classes to documented toolset or
  exact-name selection. (#10)
- Pin the default server to the dedicated `libtmux-mcp` socket with minimal
  configuration, authenticate launch ownership with a retained random marker,
  and remove that nonce from tmux's environment. Default teardown is granted
  only to the process that created the daemon, so a server the client did not
  create is offered the 41 non-teardown tools. (#10)
- Add separate named-socket, absolute socket-path, and absolute tmux-config
  selectors. A malformed selector fails before tmux opens. (#10)
- Bound pattern count and size. Pane search stops after 200 panes, 20,000
  lines, 1,000,000 bytes of matching input, or five seconds, and reports the
  ceiling it reached. (#10)
- Bound read batches. Each nested operation is validated, full nested MCP
  envelopes are preserved when they fit, truncated results retain explicit
  rows, and the complete JSON-RPC response is capped at 1,000,000 bytes. (#10)
- Request IDs over 512 KiB now fail before dispatch rather than consuming the
  response budget. (#10)
- `tmux://capabilities` now reports a schema version, frozen state, effective
  names, connection provenance, and the common trust boundary. (#10)
- `send_keys`, `send_keys_batch`, `paste_text`, and `run_shell_command` now fail
  closed when required pane or client state is missing or malformed, a
  configured member is dead, input-disabled, modal, or attended, or input could
  reach the caller or an active run. (#10)
- `run_shell_command` accepts a known POSIX foreground shell and uses exactly
  two complete checkpoints. Its bookkeeping is isolated, so authored Bash and
  zsh commands retain inherited `ERR` and `DEBUG` traps and the parent keeps its
  trap and option state. (#10)
- `run_shell_command` now refuses a pane whose process changed between its two
  checkpoints, including a replacement running the same program, and refuses a
  pane whose `pane_pid` tmux does not report. A shell swapped for another copy
  of itself previously matched on its command name and read as unchanged. (#10)
- Fix `set_mouse_enabled`, which set a session option at server scope and so
  failed every call with `invalid option: set-option mouse`. It now writes the
  global session option, which is what `set -g mouse` writes. (#10)
- Fix `paste_text` with `enter`, which never ran the command in a shell that
  honours bracketed paste. The text and its newline were pasted bracketed,
  which tells the terminal to insert what arrives rather than act on it. A
  paste asking for `enter` is no longer bracketed; an explicit `bracket` still
  wins. (#10)
- Fix `show_option`'s `effective`, which was published, documented as including
  an inherited value, and never read. An effective read now falls back to the
  scope's global table and reports `inherited` when the value came from there;
  `set` continues to report whether the object asked about sets one of its
  own. (#10)

## v0.0.1-alpha.5, workspace/v0.0.1-alpha.5, mcp/v0.0.1-alpha.8

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
- MCP output truncation reports the exact number of UTF-8 bytes removed,
  including bytes discarded from a partial leading code point. (#9)
- MCP construction and `AdvertisedTools` return schema errors instead of
  silently registering tools with missing or unresolved schemas. (#9)
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

## v0.0.1-alpha.4, workspace/v0.0.1-alpha.4, mcp/v0.0.1-alpha.7

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

## v0.0.1-alpha.3, workspace/v0.0.1-alpha.3, mcp/v0.0.1-alpha.6

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

## mcp/v0.0.1-alpha.5

- The registry entry is named for the language rather than the project.
- The READMEs no longer name a version to install. Both told a reader to fetch
  `v0.0.1-alpha.1`, which is retracted, so the command refused to run.

## mcp/v0.0.1-alpha.4

- Add the MCP registry entry and the marker it is verified against.

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

### mcp

Serves one tmux server to Model Context Protocol clients. Panes are read
incrementally through a cursor, a command reports how it ended rather than
leaving a client to scrape the screen, and tools above a chosen safety tier are
withheld.
