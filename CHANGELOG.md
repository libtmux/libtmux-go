# Changelog

Notable changes to `github.com/libtmux/libtmux-go`.

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

The other published modules keep their own changelogs:
[`workspace`][workspace-changelog] and [`mcp`][mcp-changelog]. Changes that
span modules, such as documentation, CI and release tooling, are listed here.

[workspace-changelog]: https://github.com/libtmux/libtmux-go/blob/master/workspace/CHANGELOG.md
[mcp-changelog]: https://github.com/libtmux/libtmux-go/blob/master/mcp/CHANGELOG.md

## Unreleased

### Documentation

- The quickstart owns a unique temporary socket path and cleans it up. Other
  fixed-name examples state that their socket name must be unused.
- Control-based examples state their tmux 3.7 requirement. Version-refusal
  guidance distinguishes a withheld operation from its read-only probe, and
  MCP startup guidance describes the tmux sessions that outlive a client.
- Snapshot examples describe sequential collection rather than an atomic
  single-command read. Queries over the captured records still run locally.

### internal/tools

- `mcp-swap use` and `revert` no longer stop with `configuration changed since
  mcp-swap wrote its newest layer` or `configuration changed before revert`
  after a client rewrites its own configuration, as Claude Code and Grok do.
  They refuse when the `tmux` entry they wrote differs. (#111)
- `mcp-swap use --scope project` from a second checkout now stops once the file
  has changed since the last swap, naming the first checkout and the
  `revert --scope project` to run first. (#111)
- `mcp-swap revert` of a file a client has rewritten puts back only the `tmux`
  entry, or removes it when the file had none, and keeps the client's changes
  and the file's mode. A file still as the swapper left it is restored whole.
  (#111)
- `mcp-swap use` now refuses to copy an empty `LIBTMUX_TOOLSETS` out of an
  entry it did not write, which the Go server reads as no toolsets. The error
  names `--env LIBTMUX_TOOLSETS=inspect,manage,execute` for the default
  toolsets and `--env LIBTMUX_TOOLSETS=` to keep the empty value. (#112)

## v0.1.1, workspace/v0.1.1, mcp/v0.1.1

### tmux

- `Window.SelectLayout` and `Server.ValidateLayouts` accept a JSON layout from
  tmux 3.8, the first release that reads one. An older daemon is refused with
  `ErrVersionTooLow` before `select-layout` is sent; upgrade to tmux 3.8 to
  apply a JSON layout. (#96)

  On tmux 3.8, the pane format fields it added, such as `history_added` and
  `pane_modal_flag`, are requested and read. On an older daemon they are not
  requested, so their `FormatValues` accessors report no value.
  `Pane.DisplayPopup` on tmux 3.8 returns an error when `TargetClient` names a
  control-mode client, which tmux 3.8 would otherwise silently ignore; an
  older daemon is sent the request unchanged.

- `tmux.NewServer` now uses the socket in `LIBTMUX_SOCKET_PATH`, else the name
  in `LIBTMUX_SOCKET`, when `ServerOptions` names no socket. A process that sets
  either variable now reaches that socket, with `LIBTMUX_SOCKET_PATH` winning
  when both are set. A variable that is empty or only whitespace counts as
  unset, each one alone. With neither set, tmux still gets no `-L` or `-S` and
  picks the server itself, so a program that sets neither behaves as before.
  `ServerOptions.ProcessEnvironment` supplies the variables when it is set.
- Add `SocketSelection.Source`, which reports what chose the socket as
  `tmux.SocketSourceExplicit`, `tmux.SocketSourceEnvironmentPath`,
  `tmux.SocketSourceEnvironmentName`, or `tmux.SocketSourceTmux`.

### tmux/tmuxtest

- `tmuxtest.NewServerWithOptions` and `tmuxtest.NewControlMode` now remove
  every `LIBTMUX_SOCKET*` variable, as well as `TMUX` and `TMUX_PANE`, from the
  environment of the tmux they start, and no longer remove `TMUX_TMPDIR`.
  A child that runs tmux without a socket therefore looks in the directory
  the process was given.
- `tmuxtest.Main` now fails the run when a test leaves a tmux server running,
  even if every test passed. It scans the whole suite directory for sockets,
  stops each server answering on one with `kill-server`, and reports how many
  it found without naming a path. Before, only servers registered with the
  harness were stopped, and a server started by a bare `tmux` outlived the
  run with its directory deleted. `Main` keeps the directory while a server
  still answers in it.
- `tmuxtest.Main` exits 2 without running a test when the suite directory is
  not private to the user.
- `tmuxtest.Main` clears `TMUX`, `TMUX_PANE` and every `LIBTMUX_SOCKET*`
  variable from the test process for the length of the suite. Before, only
  `TMUX_TMPDIR` was redirected, so a suite started from inside tmux sent a
  command that named no socket to the server around it, because tmux follows
  `TMUX` over `TMUX_TMPDIR`. `Main` now exits 2 without running a test when any
  of them is still set, or when the default socket would land outside the
  suite's directory.

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

## v0.0.1-alpha.2, workspace/v0.0.1-alpha.2

- The alpha notice no longer uses GitHub's alert extension, which renders as
  literal text elsewhere. Documentation only; neither package changed.

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
