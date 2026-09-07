# mcp-swap

Points the agent CLIs on this machine at a local build of this server, and puts
them back.

An MCP server cannot be exercised without a client, so the development loop is:
rewrite every client's configuration to run the working tree, try it, restore
what was there. Doing that by hand across eight client configurations is why
it does not get done.

## Seeing what is configured now

`status` only reads:

```console
$ go run ./internal/tools/mcp-swap status
```

```
[claude:user] tmux = uvx --no-config libtmux-mcp==0.1.0a19 (other)
[cursor] tmux = uvx --no-config libtmux-mcp==0.1.0a19 (other)
[codex] tmux = uvx --no-config libtmux-mcp==0.1.0a19 (other)
```

## Pointing them at this checkout

Look before you leap — `--dry-run` parses and validates the selected
configurations, prints what would change, and leaves the configurations and
cached server binary unchanged:

```console
$ go run ./internal/tools/mcp-swap use-local --dry-run
```

```console
$ go run ./internal/tools/mcp-swap use-local --mode build
```

Put everything back:

```console
$ go run ./internal/tools/mcp-swap revert
```

## Which build `--mode` selects

| Mode | What the clients run | Why |
| --- | --- | --- |
| `dev` | the working tree | an edit is live on the next call, nothing to rebuild |
| `build` | a compiled binary | one build, then a plain exec |
| `installed` | whatever `libtmux-mcp` is on `PATH` | testing what a user installed |
| `released` | a published version from the module cache | the only mode not involving this checkout |

## Worth knowing

**It starts the chosen build before writing anything.** The server is run once
and asked to report itself, so a configuration that could never have worked is
rejected before it replaces one that did.

**It plans the selected clients before changing any of them.** Every existing
configuration is parsed and rendered, and every new backup destination is
checked first. One malformed configuration or unusable backup therefore leaves
the whole selected set unchanged.

**This edits real configuration files.** `status` and `--dry-run` do not. Run
one of those first.

## Finding the clients this tool knows

`detect` checks the executable on `PATH` and the one global configuration file
that the swapper supports for each client:

```console
$ go run ./internal/tools/mcp-swap detect
```

The catalog has a fixed transaction order: Claude Code, Codex CLI, Cursor
Agent, Gemini CLI, Grok CLI, agy/Antigravity, opencode, and pi. `antigravity`
is accepted as an alias for `agy`. Detection is intentionally simple. A custom
installation counts only when its executable is already on `PATH`.

`--client` is repeatable, and each occurrence may contain a comma-separated
list. The order on the command line does not change transaction order:

```console
$ go run ./internal/tools/mcp-swap use \
    --client cursor,pi \
    --client antigravity \
    --dry-run
```

With no selectors, the tool considers all eight known configuration paths and
updates the ones that exist. An explicit or implicit selection that has no
existing configuration is an error rather than a successful no-op.

## Claude user and project scope

Claude has two MCP layers in `~/.claude.json`:

- `--scope project`, the default for `use`, writes
  `projects.<absolute-repository>.mcpServers`. Only this checkout gets the
  override.
- `--scope user` writes the top-level `mcpServers` fallback used by projects
  without an override.

Other clients have one supported global configuration layer. Either scope is
normalized to `user` for them.

Write Claude's user fallback while updating the selected global clients:

```console
$ go run ./internal/tools/mcp-swap use \
    --scope user \
    --client claude,codex,gemini
```

Without `--scope`, `status` shows both Claude layers:

```console
$ go run ./internal/tools/mcp-swap status --client claude
```

Both Claude layers may be swapped at the same time. Their first backups are
independent, but both edit the same physical file. An unscoped `revert` unwinds
them in strict reverse registration order. A scoped revert refuses to skip a
newer layer:

```console
$ go run ./internal/tools/mcp-swap revert --scope project --client claude
```

After the newer project layer is restored, a separate user-scoped revert is
safe. A plain `revert --client claude` restores both as one transaction.

## Environment and toolsets

Use repeatable `--env KEY=VALUE` options for client-side server settings:

```console
$ go run ./internal/tools/mcp-swap use \
    --env LIBTMUX_TOOLSETS=inspect,manage,execute \
    --env LIBTMUX_SOCKET=qa
```

The swapper preserves existing environment values, and explicit `--env`
values win. This matters because an agent process does not necessarily inherit
the shell that launched the swapper.

`LIBTMUX_SAFETY` is retired. Supplying it with `--env` is always rejected. If
an existing client entry still contains it, the swapper preserves it unless
the request explicitly supplies `LIBTMUX_TOOLSETS`. An explicit toolset value
then replaces the retired setting while leaving unrelated environment values
alone. Without that explicit migration, normal preflight lets the current MCP
server reject the retired setting instead of silently widening authority.

## The four server sources

The existing `--mode` interface remains available:

```console
$ go run ./internal/tools/mcp-swap use --mode dev
```

`dev` registers `go -C <mcp-module> run ./cmd/libtmux-mcp`, so the next client
start follows the working tree. `build` compiles once, atomically provisions a
persistent executable under the user cache, and registers that direct path:

```console
$ go run ./internal/tools/mcp-swap use --mode build
```

`installed` registers the `libtmux-mcp` executable resolved by the client's
`PATH`:

```console
$ go run ./internal/tools/mcp-swap use --mode installed
```

`released` runs the published Go module version. `--ref` accepts one safe
version component and only applies to this mode:

```console
$ go run ./internal/tools/mcp-swap use \
    --mode released \
    --ref v0.0.1-alpha.8
```

Omitting `--ref` selects `latest`, preserving the previous source behavior.
Path separators, NULs, underscores, and `.` or `..` components are rejected.

Build and provisioning work happens before the shared transaction lock. The
MCP probe then runs the exact persistent command, arguments, and merged
environment that the clients will receive. Once the lock is held, the tool
re-reads every config and recovery artifact and requires the final process
specification to equal the preflighted one.

`--no-preflight` is an escape hatch for a server that cannot be launched in
the current environment:

```console
$ go run ./internal/tools/mcp-swap use --no-preflight
```

It skips only the MCP handshake. It does not skip parsing, recovery, alias, or
transaction checks.

## Configuration boundaries

The tool writes these files only:

| Client | Configuration | Entry |
| --- | --- | --- |
| Claude | `~/.claude.json` | user or repository-scoped `mcpServers.tmux` |
| Codex | `~/.codex/config.toml` | `mcp_servers.tmux` |
| Cursor | `~/.cursor/mcp.json` | `mcpServers.tmux` |
| Gemini | `~/.gemini/settings.json` | `mcpServers.tmux` |
| Grok | `~/.grok/config.toml` | `mcp_servers.tmux` |
| agy | `~/.gemini/config/mcp_config.json` | `mcpServers.tmux` |
| opencode | `$XDG_CONFIG_HOME/opencode/opencode.jsonc` | `mcp.tmux` |
| pi | `~/.pi/agent/mcp.json` | `mcpServers.tmux` |

Project-local Cursor, Gemini, opencode, and pi files are not walked. Use those
clients' native configuration commands when workspace precedence matters.

opencode merges `config.json`, `opencode.json`, and `opencode.jsonc`; the last
file wins, so the swapper owns the JSONC file that opencode itself writes. An
older entry in a sibling file can still merge underneath it.

pi does not ship a built-in MCP client. Its file is consumed by the
third-party `pi-mcp-adapter` extension. `detect` and `status` report when the
adapter package directory is absent.

JSON and JSONC must be valid UTF-8 and may not contain duplicate object member
names. TOML is parsed as a complete document, so invalid syntax, duplicate
keys, and non-string process environment values fail before planning. JSONC
comments outside the replaced server entry and TOML comments and unrelated
values survive the splice. Unknown TOML child tables or multiline values that
the focused writer cannot preserve are rejected rather than flattened.

## Preflight contract

Unless `--no-preflight` is present, every distinct final process receives a
bounded JSON-RPC exchange: `initialize`, `notifications/initialized`, and
`ping`. Success requires JSON-RPC 2.0, the matching response ID, an object
`result`, a nonempty protocol version, a capabilities object, and nonempty
`libtmux` server information.

Standard output and error are read concurrently and capped. A healthy stdio
server may remain alive after the response; the probe accepts it, closes input,
then terminates and reaps the probe process group. Timeouts, malformed replies,
and oversized output follow the same cleanup path. On supported POSIX systems,
descendants are killed with the probe instead of being left behind holding a
pipe or tmux connection.

## Recovery and cross-port locking

Every mutating command takes the blocking POSIX record lock at
`$XDG_STATE_HOME/libtmux-mcp-dev/swap/state.lock`. The path is shared with the
Python and other native port swappers, so two implementations cannot publish
overlapping transactions. The Go tool also serializes goroutines in its own
process because POSIX record locks alone are process-wide.

Go recovery state is private to
`$XDG_STATE_HOME/libtmux-mcp-dev/swap/go/state.json`. It is versioned,
checksummed, mode `0600`, and strict about unknown or duplicate fields. Backup
names use `.bak.mcp-swap-go-<sequence>` and are also mode `0600`. The Go tool
does not guess at or adopt Python legacy state.

The first backup for a layer is kept across re-swaps. The ledger records the
config's symlink route, physical device/inode identity, mode, size, digest,
process specification, and the ordered recovery chain. Before publication,
all outputs are staged and synchronized. Configs, backups, recovery state, and
the lock must not alias one another, including through hard links or symlinks.

The lock descriptor is revalidated throughout the transaction. On POSIX,
closing any descriptor for the same file can release a process record lock, so
an opened config alias to the held lock is retained until explicit unlock and
the transaction fails closed.

If an exact rollback can be authenticated, completed steps are unwound in
reverse order. Otherwise, recovery files are retained. Do not delete an
untracked backup merely to make an error disappear; it can be the only
remaining pre-swap copy.

## Read-only diagnostics

`doctor` reads configurations and Go recovery state without creating the state
directory or taking the transaction lock:

```console
$ go run ./internal/tools/mcp-swap doctor
```

It reports config readability, outstanding recovery layers, invalid or
missing backups, orphaned Go backups, lingering `LIBTMUX_SAFETY` entries, and
the names of authentication environment variables that override stored CLI
logins. It reports variable names only, never their values.

## Development

The swapper is a private, non-published workspace module. Run its focused
suite from the repository root:

```console
$ go test ./internal/tools/mcp-swap
```

Mutation tests replace `HOME`, `XDG_CONFIG_HOME`, and `XDG_STATE_HOME` with
temporary roots. They must never inspect or write live client configuration.
