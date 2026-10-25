<!-- libtmux-logo -->
<div align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go MCP">
  </picture>
</div>
<!-- /libtmux-logo -->

<div align="center">

# libtmux-mcp

The binary an MCP client launches. It serves **one** tmux server over stdin and
stdout.

</div>

**Contents** — [Quick start](#quick-start) ·
[Answering questions without a client](#answering-questions-without-a-client) ·
[Running it](#running-it) ·
[Choosing what it serves](#choosing-what-it-serves) ·
[Worth knowing](#worth-knowing) · [What it leaves out](#what-it-leaves-out) ·
[Support and status](#support-and-status) · [See also](#see-also)

## Quick start

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

Then check that it can reach the tmux it was pointed at:

```console
$ libtmux-mcp -doctor -socket-name my-application
```

A client starts the command itself. [`mcp/README.md`](../../README.md) shows how
to register it with Claude Code, Codex CLI, Gemini CLI, and Claude Desktop.

## Answering questions without a client

A misconfigured entry in somebody's agent CLI is hard to debug through the
client that is failing to start it. These flags answer without one.

### `libtmux-mcp -version`: which build this is

```console
$ libtmux-mcp -version
```

```text
libtmux-mcp v0.0.1-alpha.16
```

### [`libtmux-mcp -tools`][guide-inspect-the-selection]: what a client would see

```console
$ libtmux-mcp -tools
```

It lists every tool the server exposes, as a client would see them. It needs
the tmux executable: it asks the socket whether a tmux server runs there,
because that decides whether `teardown` is in the default
[toolsets][topic-tool-selection].

### [`libtmux-mcp -doctor`][guide-diagnose-startup]: whether it reaches your tmux

```console
$ libtmux-mcp -doctor -socket-name my-application
```

It reports the socket, the tmux version, and what the socket holds.
[`mcp/README.md`](../../README.md#quick-start) shows what it prints.

## Running it

A client starts it as a subprocess; you rarely run it yourself:

```console
$ libtmux-mcp -socket-name my-application
```

It then waits on stdin and prints nothing, because stdout is the protocol.

- **Toolsets.** [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable] selects
  any unordered subset of `inspect`, `manage`, `execute`, and `teardown`.
- **Exact names.** [`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] includes
  names, and [`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable]
  removes names after inclusion.
- **Bad lists.** Unknown names and malformed lists fail before tmux opens. The
  retired `LIBTMUX_SAFETY` and its two siblings fail startup too.

The tools and the static [`tmux://capabilities`][tmux-capabilities] resource
come from one native manifest. [The contract][contract-capability-disclosure]
lists what the resource reports.

## Choosing what it serves

- **The operator chooses.** Flags pick the socket at startup, and a client
  cannot change it, so a client reaches only the socket you selected.
- **A dedicated default.** With no selector it uses the socket `libtmux-mcp`
  and the shipped minimal configuration.
- **Exactly one selector.** Otherwise set one of
  [`LIBTMUX_SOCKET`][mcp-socketenvironmentvariable] and the absolute
  [`LIBTMUX_SOCKET_PATH`][mcp-socketpathenvironmentvariable].
- **A config path.** [`LIBTMUX_TMUX_CONFIG`][mcp-tmuxconfigenvironmentvariable],
  when present, is a nonempty absolute path.
- **Teardown stays off.** Explicit or existing targets never gain it by default.

[The contract][contract-which-tmux] has the full text.

`-socket-path` names the socket by its absolute path where `-socket-name`
names it by name, and `-binary` names the tmux executable. `-doctor` reports
what a choice selects:

```console
$ libtmux-mcp \
    -doctor \
    -socket-path /tmp/my-application.sock
```

The environment makes the same choices, which is how a client's configuration
sets them:

```console
$ LIBTMUX_SOCKET=my-application libtmux-mcp -doctor
```

[`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable] narrows what the server
offers. This lists only the tools that read tmux:

```console
$ LIBTMUX_TOOLSETS=inspect libtmux-mcp -tools
```

A client that reads a JSON configuration passes the flags as `args` and the
environment as `env`:

```json
{
  "mcpServers": {
    "tmux": {
      "command": "/path/to/libtmux-mcp",
      "args": ["-socket-name", "my-application"],
      "env": { "LIBTMUX_TOOLSETS": "inspect" }
    }
  }
}
```

## Worth knowing

**`libtmux-mcp: terminated signal received` is routine shutdown.** It is the
server handling the SIGTERM it gets when a client tears the transport down,
including when a client's connect timeout fires.

**Give it the environment a client would.** A client starts its servers with a
curated environment, which differs from your shell's. Running it from an
inherited shell environment hides the bugs that only appear under a curated
one.

## What it leaves out

- **A second tmux server.** One command serves one tmux server, chosen when it
  starts.
- **Old tmux.** It refuses a binary older than the floor before the MCP
  transport starts. A tool that meets an older daemon answers with an error
  naming the fix.
- **The earlier settings.** `LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, and
  `LIBTMUX_MCP_PROMPTS_AS_TOOLS` fail startup.
  [`MIGRATING.md`](../../MIGRATING.md) maps each to its current setting.

## Support and status

- **tmux 3.3a or newer.** [`get_server_info`][get_server_info] reports
  `minimumVersion` beside `version`, and `-doctor` prints both. To fix an old
  daemon, restart that socket's server from a newer tmux, or point
  [`LIBTMUX_TMUX_BIN`][mcp-binaryenvironmentvariable] at one.
- **Linux, including WSL, and macOS.** CI runs the module's tests on Linux
  across the tmux matrix. macOS is supported on a best-effort basis, and CI
  coverage for it is highly limited.
- **Alpha.** Releases carry an -alpha prerelease tag. Pin an exact version.

## See also

- [Reference](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp) — this command on pkg.go.dev
- [`mcp/README.md`](../../README.md) — client configuration
- [`mcp/docs/contracts.md`](../../docs/contracts.md) — what the server promises
- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [Tool pages](https://libtmux.org/en/go/latest/mcp/tools/) — one page for each tool
- [`mcp-swap`](../../../internal/tools/mcp-swap) — point the agent CLIs on this machine at a local build

[contract-capability-disclosure]: ../../docs/contracts.md#capability-disclosure
[contract-which-tmux]: ../../docs/contracts.md#which-tmux-it-serves
[get_server_info]: https://libtmux.org/en/go/latest/mcp/tools/get_server_info/
[guide-diagnose-startup]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#diagnose-startup
[guide-inspect-the-selection]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#inspect-the-selection
[mcp-binaryenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-binaryenvironmentvariable/
[mcp-excludetoolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-excludetoolsenvironmentvariable/
[mcp-socketenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-socketenvironmentvariable/
[mcp-socketpathenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-socketpathenvironmentvariable/
[mcp-tmuxconfigenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-tmuxconfigenvironmentvariable/
[mcp-toolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-toolsenvironmentvariable/
[mcp-toolsetsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-toolsetsenvironmentvariable/
[tmux-capabilities]: https://libtmux.org/en/go/latest/mcp/resources/tmux-capabilities/
[topic-tool-selection]: https://libtmux.org/en/go/latest/mcp/topics/tool-selection/
