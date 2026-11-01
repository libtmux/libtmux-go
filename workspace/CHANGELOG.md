# Changelog

Notable changes to `github.com/libtmux/libtmux-go/workspace`.

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

The modules are tagged together, so a release heading below names the tags of
its release; this module's tag is `workspace/vX.Y.Z`. The tmux module and `mcp`
keep their own changelogs: [core][core-changelog] and
[`mcp`][mcp-changelog]. Changes that span modules, such as
documentation, CI and release tooling, are in the core changelog.

[core-changelog]: https://github.com/libtmux/libtmux-go/blob/master/CHANGELOG.md
[mcp-changelog]: https://github.com/libtmux/libtmux-go/blob/master/mcp/CHANGELOG.md

## Unreleased

## v0.1.2, workspace/v0.1.2, mcp/v0.1.2 (2026-11-01)

No entries for `workspace`.

## v0.1.1, workspace/v0.1.1, mcp/v0.1.1

### workspace

- `tmux-workspace` no longer passes the socket `TMUX` names as `-S` when neither
  `-S` nor `-L` is given, except for `--append`, which asks for the current
  session. Otherwise it uses `LIBTMUX_SOCKET_PATH`, else `LIBTMUX_SOCKET`, and
  with neither set it passes tmux no socket, so tmux picks the server, which
  inside tmux is still the one `TMUX` names. Outside `--append`, a socket path
  holding a comma follows tmux's reading of `TMUX`, which stops at the first
  comma.
- `tmux-workspace shell` and loads that run Python pass tmuxp `-S` for a socket
  chosen by `LIBTMUX_SOCKET_PATH` or `LIBTMUX_SOCKET`, which tmuxp does not
  read.

## v0.1.0, workspace/v0.1.0, mcp/v0.1.0

No entries for `workspace`.

## v0.0.1-alpha.13, workspace/v0.0.1-alpha.13, mcp/v0.0.1-alpha.16

No entries for `workspace`.

## v0.0.1-alpha.12, workspace/v0.0.1-alpha.12, mcp/v0.0.1-alpha.15

### workspace

- `Build` keeps subprocess operations on tmux 3.3a through 3.6 and owns a
  qualified control connection on newer daemons. (#60)

## v0.0.1-alpha.11, workspace/v0.0.1-alpha.11, mcp/v0.0.1-alpha.14

### workspace

- Report a tmux below 3.3a as `tmux_unsupported`, with the fix in the message,
  from `load`, `load --append` and `freeze`. `shell` and a Python bridge load
  refuse it before starting Python. (#59)
- Report a `load` whose tmux refusal or version check failure sent nothing
  with status `error` and no retained effects, and print a refusal once in
  text output. (#59)

## v0.0.1-alpha.10, workspace/v0.0.1-alpha.10, mcp/v0.0.1-alpha.13

### workspace

- The `build-workspace` example now builds on a tmux server and configuration
  of its own, and its page publishes what it prints. (#53)

## v0.0.1-alpha.9, workspace/v0.0.1-alpha.9, mcp/v0.0.1-alpha.12

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

## v0.0.1-alpha.8, workspace/v0.0.1-alpha.8, mcp/v0.0.1-alpha.11

### workspace

- `Build` accepts unique preset layout prefixes, matching the core library.
  (#16)

## v0.0.1-alpha.7, workspace/v0.0.1-alpha.7, mcp/v0.0.1-alpha.10

No entries for `workspace`.

## v0.0.1-alpha.6, workspace/v0.0.1-alpha.6, mcp/v0.0.1-alpha.9

No entries for `workspace`.

## v0.0.1-alpha.5, workspace/v0.0.1-alpha.5, mcp/v0.0.1-alpha.8

### workspace

- Add `Workspace.InitialSessionRequest` and `BuildInto`, allowing callers to
  populate a session over a caller-owned connection without transferring
  ownership. (#9)
- `Build` now creates the session and temporary connection together, closes
  the connection before returning, and verifies that the session survived.
  (#9)

## v0.0.1-alpha.4, workspace/v0.0.1-alpha.4, mcp/v0.0.1-alpha.7

### workspace

- The document check now rejects two windows claiming one `window_index`,
  naming the line and both windows. tmux refuses the second itself, but reports
  only that `new-window` exited non-zero.

## v0.0.1-alpha.3, workspace/v0.0.1-alpha.3, mcp/v0.0.1-alpha.6

### workspace

- A rejection with no line to point at no longer reports "line 0". An empty or
  unparseable document reported a line that cannot exist.

## v0.0.1-alpha.2, workspace/v0.0.1-alpha.2

No entries for `workspace`.

## v0.0.1-alpha.1

### workspace

Loads tmuxp-style YAML workspaces and builds them.
