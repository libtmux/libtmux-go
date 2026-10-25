# Changelog

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

## v0.1.0, workspace/v0.1.0, mcp/v0.1.0

### Documentation

- Releases are numbered `0.1.N` from the first final release, `0.1.0`, and no
  release carries an `-alpha` or other prerelease tag unless the maintainer asks
  for one. Each release increments `N`, and any release may still change or
  remove exported identifiers, so pin an exact version. The README, package
  READMEs, `tmux` package documentation, `SECURITY.md` and `DESIGN.md` say so.

## v0.0.1-alpha.13, workspace/v0.0.1-alpha.13, mcp/v0.0.1-alpha.16

### Documentation

- Package READMEs no longer say CI runs on macOS. The root README support table
  lists the clipboard query, floating panes, and JSON layouts, and the pane
  wait example says it needs tmux 3.7.
- The README names the commands that escape the guard when nested inside
  another command, with the failure each causes, and `wait_for_text` states
  that its polling path can miss rows. (#82)
- The README limits the advice to name `break-pane` to nested and key-bound
  text, since a direct raw `break-pane` is refused on tmux 3.7 even with a
  name. `mcp/PARITY.md` no longer counts tools, and the `wait_for_text`
  description no longer claims the polling path misses rows from the pane's
  history. (#83)
- The README says tmux 3.3a ends the server on `refresh-client -l` at once
  when the attached client's terminal has the `Ms` clipboard capability, as
  `xterm-256color` and `tmux-256color` do. Before, it said 3.3a waits for the
  terminal not to answer, which is what 3.4 to 3.6 do. (#85)

### tmux

- Wait: add `Pane.WaitForText`, a pane-text wait that polls `capture-pane` on
  every supported tmux release and returns `PaneEndedError` when the pane or
  server ends first, and the `PaneTextWait` holds the text read so far. (#84)

## v0.0.1-alpha.12, workspace/v0.0.1-alpha.12, mcp/v0.0.1-alpha.15

### Development

- CI no longer runs the module tests on macOS. macOS stays supported on a
  best-effort basis, and CI coverage for it is highly limited. (#74)
- Move the long contract paragraphs of the `mcp`, `workspace`, and `tmux`
  READMEs to `mcp/docs/contracts.md`, `workspace/docs/contracts.md`, and
  `tmux/docs/contracts.md`. Each README keeps short bullets and links its page.
- The `workspace` README quotes `ExampleParse_unknownField`, so the refusal of
  an unknown field is shown by a test that runs.

### tmux

- Owned control clients, connections, notifications, and pane observations
  require tmux 3.7. Acquisition pins the selected socket before starting
  control mode; filesystems unable to retain it refuse acquisition. Cold
  creation can return a detached session if later attachment fails. (#60)
- Named buffer deletion and rename, including `CaptureToFile` abort cleanup,
  require tmux 3.4. Explicit named paste-and-delete remains available on 3.3a.
  Foreground `RunShell` captured client output requires 3.5; pane-targeted,
  background, and `AsTmuxCommand` jobs retain native output semantics. (#60)
- Exact tmux 3.7 refuses a colon or period in window and session names passed
  to `new-window -n`, `new-session -n` or `-s`, `rename-window` and
  `rename-session`, before any effect. (#60)
- Control acquisition names its client `client-<pid>` instead of polling
  `list-clients`, so a user's `after-list-clients` hook no longer fires.
  (#60)
- Raw `break-pane` and unnamed typed `BreakPane` refuse exact tmux 3.7;
  explicitly named typed calls retain the naming repair. Literal
  `split-window -p` refuses defective tmux 3.4; typed percentage requests
  keep their conversion. (#60)
- The same checks apply to literal commands sent through `Connection.Call` and
  `ControlClient.Call`, and resolve aliases and unique prefixes the way tmux
  does, so `breakp` and `new` are checked as the commands they name. (#60)
- Literal `select-layout` on tmux 3.3a refuses an unparseable or empty layout
  and `-o`, which exit the server. (#60)
- Literal `refresh-client -l` refuses tmux below 3.6a: 3.3a exits the server
  when the client has a terminal, and 3.4 through 3.6 exit it when that client
  leaves before its terminal answers. The typed `RefreshClient` request
  withholds the flag below the same release. (#60, #77)
- The optional feature gates read their tmux floors from one capability
  registry, and `capabilities.json` at the repository root is emitted from
  it for the docs capability pages. A test fails when the two differ. (#60)

### tmux/tmuxtest

- Screen helpers use capture polling below tmux 3.7. Explicit control helpers
  retain the control acquisition requirement. (#60)

### workspace

- `Build` keeps subprocess operations on tmux 3.3a through 3.6 and owns a
  qualified control connection on newer daemons. (#60)

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

### Development

- The README link check now also follows absolute links to this repository's
  `master` branch, and holds the root and `tmux` READMEs to a heading that
  links each package's directory.
- The `workspace` tests now hold every `tmux-workspace` command in the
  Markdown to a subcommand and flags that exist, and the `mcp` tests do the
  same for `libtmux-mcp` flags and the `LIBTMUX_` variables a command sets.

### tmux

- Require tmux 3.3a or newer. Every operation refuses an older daemon or
  executable, including unpatched 3.3 and anything before 3.2, with
  `ErrVersionTooLow` before sending anything, and a `Plan` leaves its
  operations skipped. Upgrade tmux and restart its server, or pin the previous
  release. (#59)
- Check the selected daemon, not only the executable, in `Server.RequireVersion`
  and `Server.Cmd`. A cold handle sends one extra read-only probe on its first
  dispatch. `Server.Version` on an unbound handle and `Cmd` with `-V` or `-h`
  still report the executable. (#59)
- Refuse a version that names no release, such as a bare `master`, an OpenBSD
  token or a `-dev` suffix, as unrecognized instead of too low. Releases with
  one patch letter, `-rc` and `-rcN` candidates, and `next-X.Y` builds are
  accepted, and candidates and development builds rank below the release they
  name. (#59)
- Rank `X.Y-rc` and `X.Y-rcN` below `X.Y` in `Version.Compare` and
  `Version.AtLeast`, as `next-X.Y` already ranked, so a feature gate opens on a
  candidate only when it names that candidate. (#59)
- Open the JSON layout, the 3.8 format fields and the control-client popup
  refusal from 3.8-rc2, the earliest build measured, instead of 3.8. (#59)
- Remove `WindowHookValues.WindowLinked` and `WindowUnlinked`, which tmux
  before 3.3 kept as window hooks. This is a breaking change for anything
  calling them; read the hooks from `SessionHookValues`. (#59)
- Return `3.3a` from `ControlNotificationKind.MinimumVersion` for a
  notification every supported tmux emits, where it returned `3.2a` or `3.3`.
  Pass the result to `Server.RequireVersion` unchanged; compare it against
  `MinimumSupportedVersion`, not a literal. (#59)
- Raise `MaximumTestedVersion` to `3.8`, the level a bare `master` build now
  reports. (#59)
- Add `ErrVersionCheckFailed`, which matches any failure of the version check;
  a command that fails with it was never sent. (#59)
- Add `VersionTooLowError.Reason`, the refusal without the advice to upgrade
  or pin. (#59)
- Stop `Session.Run` from handling tmux 3.2's dead-pane notice. Output that
  resembles it is kept. (#59)
- Add `FormatValues.ImageSupport` for tmux's `image_support` variable. (#59)
- Omit `DisplayPopup`'s `NoKeys` on tmux 3.9 and later, where a popup is a
  floating pane and the flag no longer exists. (#59)

### workspace

- Report a tmux below 3.3a as `tmux_unsupported`, with the fix in the message,
  from `load`, `load --append` and `freeze`. `shell` and a Python bridge load
  refuse it before starting Python. (#59)
- Report a `load` whose tmux refusal or version check failure sent nothing
  with status `error` and no retained effects, and print a refusal once in
  text output. (#59)

### mcp

- Answer a refused tmux from any tool, and at startup, with the fix: restart
  the tmux server from a newer tmux, or set `LIBTMUX_TMUX_BIN` and restart the
  server. (#59)
- Add `minimumVersion` to `get_server_info` and to `-doctor`. (#59)
- Return `versionRefusal` from `get_server_info` on a refused tmux, with the
  installed version and `alive` true while the daemon answers on its socket,
  instead of failing. (#59)

## v0.0.1-alpha.10, workspace/v0.0.1-alpha.10, mcp/v0.0.1-alpha.13

### Development

- The install gate also runs `go install` for `tmux-workspace`, the command
  `workspace/CLI.md` documents, against the latest published workspace module.
  (#24)
- The `mcp` tests now pass where the core utilities are one multi-call
  program, as on Ubuntu 26.04. (#53)

### tmux

- `RunResult.Signal` is now an `os.Signal`, nil when no signal ended the
  command, where it was tmux's text: `9` on Linux, `kill` on macOS and the
  BSDs. Compare it with `os.Kill` or a `syscall` constant. (#53)
- `RunResult.Status` is now -1 when the command did not exit on its own, as
  when a signal ended it, where it read 0. Zero now always means success.
  (#53)
- `Session.Run` now joins lines the window's width wrapped. (#53)
- `Pane.Writer` now types a write of any size, so `io.Copy` into a pane works;
  a line over tmux's 16 KiB command limit used to fail with "command too
  long". (#53)
- `Server` methods no longer fail with `exec: WaitDelay expired before I/O
  complete` on a loaded machine. (#53)
- `Server.WaitFor`, `Server.RunShell`, and `Server.IfShell` now return an
  error matching `ErrNoServer` when the tmux server exits under them, where
  they returned success. (#53)
- `Server.RunShell` and `Server.IfShell` on a connection-bound server now
  return what the command printed, where any output broke the connection.
  (#53)
- `Server.WaitFor`, `Server.RunShell`, and `Server.IfShell` on a
  connection-bound server now run on a control client of their own, so a
  canceled or slow call no longer holds a lane. `Connection.Close` ends one
  with `ErrControlClosed`. (#53)
- `Session.Run`, `Session.Start`, and `Running.Wait` now work on a session
  bound to a `Connection`, and `Running.Kill` no longer waits behind a `Wait`
  parked on a single lane. (#53)
- `Connection` no longer returns a lane to its pool before tmux finishes a
  command whose caller stopped waiting, so the next caller no longer waits
  behind it. (#53)
- `PaneObservation` now keeps only its own pane's output, so a busy pane
  beside it no longer fills the observation's queue and ends it. (#53)
- `PaneObservation.WaitFor` no longer copies its retained text on every
  write: one mebibyte in 80-byte writes takes 14 ms instead of 2.5 s, measured
  with `go test -run '^$' -bench PaneObservationWaitForBatches ./tmux/`. (#53)
- Add `NotificationOptions.BacklogBytes` to size a notification stream's
  backlog. Zero keeps the 16 MiB default; less than 64 KiB is refused. (#53)
- Remove `SubscriptionRequest.Session`, which selected nothing: tmux evaluates
  a session-scoped subscription against the stream's own session. Delete it
  from any request that sets it. (#53)
- `ControlClient.NextNotification` and `NotificationStream.Next` now
  serialize concurrent calls, so a queued notification is no longer left
  unread behind a blocked reader. (#53)
- `NotificationStream` reads after the stream ends now match
  `ErrControlClosed`, and also `os.ErrClosed` after `Close` or
  `ErrControlStreamLost` when tmux ended it. (#53)
- `CommandError` now keeps tmux's "command too long" when it redacts the
  command, where it said only that the command exited 1. (#53)

### tmux/tmuxtest

- `tmuxtest` cleanup no longer fails intermittently on Debian and Ubuntu
  builds of tmux, which answer `server exited unexpectedly` for a moment after
  `kill-server`. (#53)

### workspace

- The `build-workspace` example now builds on a tmux server and configuration
  of its own, and its page publishes what it prints. (#53)

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

### examples

- Add `layout`, which splits a window and reads back the pane widths. (#53)
- Add `queries`, which runs every filter the `tmuxq` README shows. (#53)
- Add `any-slice`, which runs the `tmuxq` helpers on a slice with no tmux.
  (#53)
- Add `object-model`, which shows that a rename returns a new record and a
  lookup that finds nothing. (#53)
- Add `testing-a-program`, a whole test file that runs a program in a pane.
  (#53)
- `quickstart` is now a whole program that runs a command with `Session.Run`
  and prints its screen and exit status. (#53)
- Every example now starts a tmux server of its own, on a private socket with
  an empty config, so it no longer reaches or reads the tmux you use. (#53)
- Every example's page now shows its program and what it prints. (#53)

## v0.0.1-alpha.9, workspace/v0.0.1-alpha.9, mcp/v0.0.1-alpha.12

### Development

- The tmux matrix, the documented supported range and the benchmark tables
  move from 3.8-rc to 3.8-rc2, which replaced it upstream. (#21)
- The documented-range check reads a numbered release candidate such as
  `3.8-rc2`. (#21)

### tmux

- On tmux 3.8, abandoning a `WaitForModeLock` wait no longer costs the
  channel an unlock: tmux drops the wait of a client that was killed (tmux
  issue 5614), so a second unlock fails. `WaitForModeLock` and `WaitFor`
  say the extra unlock applies before 3.8. (#21)
- Add `SelectLayoutRequest.Validate` to check layout requests without tmux I/O.
  Workspace parsing uses the same validation as core layout operations. (#15)
- Add `Server.ValidateLayouts` to check every layout and required pane count
  before mutation. Version-sensitive names use the selected daemon; only an
  unbound cold endpoint falls back to the configured client version. (#15)
- `Window.SelectLayout` and `Plan.SelectLayout` reject invalid classic layout
  checksums, integer overflow and malformed trees before dispatch. Custom
  layouts support up to 256 nested parents; tmux validates their geometry. (#15)
- `Plan.SelectLayout` accepts unique named-layout abbreviations for the running
  tmux version. Plans check every recorded layout before dispatching any
  operation, including forward references. (#15)
- Remove `Color88`. tmux 3.2a and later reject `-8`, so `ServerOptions.Colors`
  rejects the numeric mode 88 before looking up the executable rather than
  deferring the failure to the first command. Use `Color256` or the default.
  (#15)
- `Server.SwitchClient` now anchors its target with tmux's exact-match `=`
  prefix. An unanchored target resolves by prefix, so a session name that
  matched no session exactly but started a different one's name switched
  to that other session silently instead of failing. (#15)
- `Server.AttachSession` and `Session.Attach` now send the attached tmux
  client `SIGTERM` on Unix cancellation, requesting a graceful disconnect
  before the runner's existing bounded forced cleanup takes over, instead of
  killing it outright; other platforms are unchanged. A daemon that cannot
  respond in time may still leave the caller's terminal to restore. (#15)
- `Version.AtLeast` now ranks a `next-X.Y` development build below the
  release it names instead of treating it as already reached, so a feature
  gated on that version no longer reports present on a snapshot that has
  not shipped it yet. (#15)

### workspace

- `go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest`
  installs `tmux-workspace`, and rerunning it upgrades. (#22)
- Add the `tmux-workspace` command: `load`, `ls`, `search`, `edit`, `freeze`,
  `convert`, `import`, `shell` and `debug-info`, with human, JSON and NDJSON
  output and generated shell completion. [`workspace/CLI.md`](workspace/CLI.md)
  documents its exit codes, machine `code` values, NDJSON event vocabulary
  and environment variables. (#15)
- `load` creates a session per input, or reuses one whose name matches after
  comparing it against the document; a mismatch reports `session_mismatch`
  and exit 1 without building or changing anything. A build that fails
  partway removes the session it created. `--append` adds a document's
  windows to the invoking tmux session instead of creating one, and inside
  tmux `load` asks before attaching to a session that already exists. (#15)
- `freeze` requires `--save-to` unless an output flag returns the document
  instead. It refuses a session name that cannot stand alone as a workspace
  file name, naming `--save-to` as the way around it, and unconditionally
  refuses one holding a period, colon, NUL or newline, since `load` could
  never address that session again. (#15)
- `import tmuxinator` refuses unexpanded ERB markup before writing a file;
  `import teamocil` takes the session name from the source file, since
  teamocil documents carry none, and accepts a pane written as a plain
  command string. (#15)
- Interactive prompts and `tmux-workspace` itself cancel on SIGINT or
  SIGTERM while input or a setup script is active, reporting the final
  result with status 130. (#15)
- `Parse` validates a custom layout's checksum, unsigned 32-bit fields and
  tree structure before building. `Build` and `BuildInto` check every layout's
  name availability and pane capacity against the selected daemon before
  mutation. (#15)
- With no pane declaring `focus`, the pane left active after a build is the
  last one created in its window, which is where tmuxp leaves it. Splitting
  detached left the first pane active instead. An explicit `focus: true` is
  unchanged. (#15)
- `workspace.Build` applies environment variables and options in name order.
  It ranged over the maps directly, so Go's randomised map iteration gave two
  runs of the same document two different orders, and options that depend on
  each other landed differently each time. (#15)
- `workspace.Build` rebalances a window between splits, so a window with more
  than four panes builds at 80x24 instead of failing with tmux's "no space for
  a new pane" on the fifth. (#15)
- `workspace.Build` sends a pane's commands literally, so a command that is
  also a tmux key name -- `Space`, `Up`, `Escape` -- is typed rather than
  pressed. (#15)
- `Workspace.SuppressHistory` is now `*Bool`, and nil suppresses history,
  matching tmuxp and the CLI. Its zero value did the opposite of the
  reference. This is a breaking change for anything setting the field. (#15)
- `Workspace.Validate` reports an invalid `layout` through the same check
  `Window.SelectLayout` uses, so a bad checksum or malformed tree is refused
  before any tmux call; the error names the window and the layout. (#15)

### mcp

- `select_layout` accepts unique named-layout abbreviations for the running
  tmux version and uppercase saved-layout checksums. It uses core validation
  before window lookup; tool discovery describes named and saved inputs. (#15)
- `kill_session` now resolves its target session by exact name instead of
  tmux's `=name` prefix match, which misreads a period or colon in the name
  as a target separator; a session named with either can now actually be
  killed instead of surviving the call. (#15)

## v0.0.1-alpha.8, workspace/v0.0.1-alpha.8, mcp/v0.0.1-alpha.11

### Development

- The tmux matrix runs 3.8-rc alongside the released versions it already
  covered. (#16)
- A CI job reports each module's exported API difference against that
  module's last tag. (#16)
- A check fails when a tracked file is binary. Running a generator directly
  drops a build artifact in the repository root that the generated-file
  freshness gate cannot see, because an untracked file never appears in a
  diff. (#16)
- Every published Markdown code region compiles on its own, in a module of
  its own, so a region cannot depend on a binding an earlier region in the
  same source file created. A `given:` marker on the source comment declares
  what the region assumes and renders as the block's first line. (#16)

### tmux

- Add `Server.SessionByName` to look up a session without listing and
  comparing every session name. (#16)
- `Session.ResolveActivePane` and `Window.ResolveActivePane` report absence
  through `ErrNotFound`. Remove the boolean result from callers. (#16)
- Add `PaneObservation.WaitFor` to wait for matching output without polling
  captures. (#16)
- Window and pane handles remain valid across window renumbering and report
  a missing target after their window is removed. (#16)
- `Running.Wait` gives concurrent callers independent context deadlines
  while preserving the command's recorded outcome. (#16)
- Buffer transfers, `Pane.CaptureTo` and `Running.StreamTo` return when their
  contexts end, including when a destination write blocks. (#16)
- `Running.Kill` returns when tmux accepts the termination request. (#16)
- `Session.Start` preserves output from commands that exit quickly on tmux
  versions before 3.7. (#16)
- Add `tmuxtest.ScriptedTmux` for testing callers without a real daemon.
  (#16)
- `Session`, `Window`, `Pane`, `Client`, `FormatValues` and `Snapshot` encode
  as JSON for inspection and reporting. (#16)
- Add `ServerOptions.CommandObserver` to report command timing, transport
  and outcomes without exposing arguments or output. (#16)
- Pane input, reset and capture operations report tmux refusals instead of
  succeeding against a missing pane. (#16)
- `ErrNotFound` replaces `ErrSnapshotNotFound` and also matches commands
  refused for a missing target. Update error comparisons. (#16)
- `NewServer` preserves non-ASCII text regardless of the caller's locale.
  (#16)
- `Server.CheckAlive` replaces the removed `Server.RaiseIfDead`. (#16)
- Time-valued options and `DisplayMessageRequest.Delay` use `time.Duration`;
  replace bare integers with values such as `250*time.Millisecond`. (#16)
- `FormatValues` decodes quoted braces emitted by newer tmux builds. (#16)
- Control connections and `Window.SelectLayout` use JSON layouts on tmux
  3.8 and newer to preserve pane placement. (#16)
- Session-scoped notification subscriptions work on tmux 3.8 and newer.
  (#16)
- `Window.NewWindow` recovers an existing window when `SelectExisting` is
  set on tmux 3.8 and newer. (#16)
- `PaneObservation` stays open when an unrelated session's window closes.
  (#16)
- Split failures include tmux's explanation on releases before 3.7. (#16)
- Zero-value object targets report `MissingTargetError` with the object kind
  and the resolver to use. (#16)
- `Window.SelectLayout` accepts unique preset prefixes and identifies the
  candidates when a prefix is ambiguous. (#16)
- Control notification streams distinguish an unexpected disconnection with
  `ErrControlStreamLost` from a clean end. (#16)
- `Server.Snapshot` and its listings return empty results for a server with
  no sessions. (#16)
- Positional commands, names, keys and values beginning with `-` remain
  caller text in direct operations and plans, without changing tmux flags.
  (#16)

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

### workspace

- `Build` accepts unique preset layout prefixes, matching the core library.
  (#16)

## v0.0.1-alpha.7, workspace/v0.0.1-alpha.7, mcp/v0.0.1-alpha.10

### tmux

- Add `Server.LoadBufferFrom` and `Server.SaveBufferTo`, which stream a paste
  buffer out of an `io.Reader` and into an `io.Writer`, and `Pane.CaptureTo`,
  which writes a capture to an `io.Writer`. They carry any bytes at any size,
  where `Server.SetBuffer` is bounded by tmux's 16 KiB command limit and
  refuses NUL, and allocation does not grow with the payload. tmux gives a
  control client no stdin or stdout, so a `Connection`-bound value returns
  `ErrConnectionRequiresProcess`; `Server.LoadBuffer`, `Server.SaveBuffer` and
  `Pane.CaptureToFile` take a path and still run over a connection. (#14)
- `Server.LoadBuffer` and `Server.SaveBuffer` reject a `Path` of `-` by naming
  the streaming method to use instead of reporting that the runner exposes no
  process stdio. (#14)
- Add `Session.Run`, which runs a command in a window of its own, waits for it
  to exit, and returns its status and the screen it showed as `RunResult`. A
  nonzero status is a result rather than an error, and the window is removed
  unless `RunOptions.Keep` is set. `RunResult.Signal` is empty before tmux 3.3,
  which reports no signal for a dead pane. (#14)
- Add `Session.Start`, which returns a `Running` handle while a command is
  still going, with `Running.Wait`, `Running.StreamTo`, `Running.Kill` and
  `Running.Pane`. `StreamTo` copies what the command prints into an
  `io.Writer` and returns the same `RunResult` as `Wait`, so following a
  command needs no goroutine of the caller's. `Kill` sends SIGKILL to the
  process group, with tmux resolving the process id when it runs, and reports
  success when the pane has already gone. (#14)
- `Running.Wait` may be called more than once and concurrently, and a `Wait`
  whose context ended can be retried. It waits on tmux's signal and, on a
  widening interval, on whether the pane is dead and its command reaped, so a
  signal that never arrives cannot hold it until the caller's deadline. (#14)
- Add `ErrOutcomeUnrecorded`, which `Running.Wait` reports when a pane is dead
  and tmux has not recorded how its command ended. A pane reads as dead as
  soon as tmux closes its terminal, which is before the exit status can be
  read, so a command that failed is never reported as having exited zero.
  (#14)
- Add `Pane.Writer`, an `io.Writer` that types into a pane. Each newline is
  the Enter key. (#14)
- Add `PaneObservation.Reader`, an `io.Reader` over one pane's output after
  the observation's baseline. A lost observation ends it with
  `ErrPaneObservationLost` rather than `io.EOF`. (#14)
- Add `Notifications` range-loop iterators to `NotificationStream` and
  `PaneObservation`, matching `ControlClient.Notifications`. (#14)
- Add `NotificationStream.Subscribe` and `NotificationStream.Unsubscribe`,
  which arm tmux format subscriptions on an owned stream from a
  `SubscriptionRequest`, and `ControlNotification.Subscription`, which reads
  the `SubscriptionChange` they report. A pane-scoped subscription stops once
  that pane's process exits; scope at the window to observe pane death. (#14)

### tmuxq

- Add `Matching` and `MatchingSeq`, which apply a generated model filter to a
  slice or sequence and report the one error a filter can have. They replace
  the `Predicate` call followed by `Where` that a caller wrote before, and the
  `Filter` interface names what they accept without naming a model. (#14)

### mcp

- `paste_text` now stages text larger than tmux's 16 KiB command limit as
  appended chunks, so a large paste succeeds instead of failing with
  `command too long`. Staging stays on the control connection this server
  holds, without a tmux process or a file. (#14)

### examples

- Add `run-to-completion`, `pane-io` and `byte-streams`. (#14)
- `quickstart` now types through `Pane.Writer` and reads through
  `PaneObservation.Reader` instead of polling `Pane.Capture`, and its split
  pane runs `sh` so the example does not depend on the login shell's startup.
  (#14)

## v0.0.1-alpha.6, workspace/v0.0.1-alpha.6, mcp/v0.0.1-alpha.9

### Development

- Every CI job carries a timeout, so a job that stops making progress fails in
  minutes rather than running to GitHub's six-hour ceiling. (#12)
- A newer push to a pull request cancels the run it supersedes. Pushes to master
  keep their own group and still run concurrently. (#12)
### tmux

- Add `Server.WithProcessEnvironmentValue`, which derives a launch-only process
  environment while preserving the frozen executable and exact socket target.
  An entry such as `TMUX_TMPDIR` cannot retarget the derived handle. (#10)
- `Server.SetOption` and its siblings now name the scopes an option does carry
  when refusing it through a handle that does not. A session option such as
  `mouse` reported only `invalid option`, which reads as an option tmux does
  not have rather than the wrong receiver. (#10)

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

### internal/tools

`mcp-swap` points MCP clients at this checkout. It is a developer command and
ships in no release.

- `mcp-swap` precomputes every selected configuration and backup destination
  before the first write, rejects physical-target aliases, and rolls back
  writes in reverse order. Recovery binds configuration, backup, and state
  identities, bytes, and modes. (#10)
- `--dry-run` starts no build or server and writes no configuration, backup,
  cache, or state. (#10)
- `mcp-swap` supports all eight clients, including Pi and canonical `agy`
  (`antigravity` alias), reports a missing Pi adapter, and treats help as a
  successful query. (#10)
- `use` and `revert` now plan, stage, and commit a native client's own
  configuration as one transaction, recording what each swap replaced in a
  ledger with a file identity that detects edits made behind the tool's back. A
  partially applied swap reverts from that ledger rather than from memory. (#10)
- Add `detect`, which reports the executable and configuration file the tool
  recognises for each client, and `doctor`, which reads the swap state, the
  authentication each client would use, and any recovery artifact left behind,
  without writing anything. (#10)
- Add `use`, which takes the same selectors as `use-local` and states the server
  source it registers rather than implying a local checkout. (#10)
- Drop the MCP Go SDK dependency. The preflight handshake is written against
  the wire protocol, so the command no longer carries the released module's
  dependency set. (#10)
- Preflight no longer reports the pipe it closed alongside the reason a probe
  stopped; a timeout now names only the timeout. (#10)
- Fix staging a destination that does not exist yet under a symlinked ancestor,
  which every path under macOS's `TMPDIR` has. The planned path was held
  against a resolved parent, so the swap was refused with `destination changed
  while it was planned` before it wrote anything. A destination is now resolved
  whether or not it exists, as an existing one already was. (#10)

## v0.0.1-alpha.5, workspace/v0.0.1-alpha.5, mcp/v0.0.1-alpha.8

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
- `Plan.RunWith` also returns `ErrOutcomeUnknown` and marks a started
  subprocess operation `OpIndeterminate` when cancellation can no longer
  prove that tmux did not receive it. (#9)
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
