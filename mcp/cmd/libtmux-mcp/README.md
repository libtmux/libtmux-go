# libtmux-mcp

The binary an MCP client launches. It serves **one** tmux server over stdin and
stdout.

Which tmux server it serves is chosen by flags at startup and cannot be changed
by a client, so a client reaches only the socket the operator selected.
It requires tmux 3.2a or newer and refuses an older binary before the MCP
transport starts.

Without a selector, it uses the dedicated socket `libtmux-mcp` and the shipped
minimal configuration. A random launch marker proves which process created the
daemon before default teardown tools are exposed, then disappears from tmux's
environment. Otherwise set exactly one of `LIBTMUX_SOCKET` and the absolute
`LIBTMUX_SOCKET_PATH`. `LIBTMUX_TMUX_CONFIG`, when present, must be a nonempty
absolute path. Explicit or existing targets never gain teardown by default.

## Installing it

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

## Answering questions without a client

A misconfigured entry in somebody's agent CLI is hard to debug through the
client that is failing to start it. These three flags answer without one:

```console
$ libtmux-mcp -version
```

```
libtmux-mcp v0.0.1-alpha.8
```

List every tool the server exposes, as a client would see them:

```console
$ libtmux-mcp -tools
```

Tool listing does not resolve or contact tmux.

Check that the server can reach the tmux it was pointed at:

```console
$ libtmux-mcp -doctor -socket-name my-application
```

## Running it

A client starts it as a subprocess; you rarely run it yourself:

```console
$ libtmux-mcp -socket-name my-application
```

It then waits on stdin. Nothing is printed, because stdout is the protocol.
Use `LIBTMUX_TOOLSETS` in the client-managed environment to select any
unordered subset of `inspect`, `manage`, `execute`, and `teardown`.
`LIBTMUX_TOOLS` includes exact names and `LIBTMUX_EXCLUDE_TOOLS` removes exact
names after inclusion. Unknown names and malformed lists fail before tmux
opens. `LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, and
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` are retired and also fail startup.

The selected tools and the static `tmux://capabilities` resource come from one
native manifest. Every listed tool carries the same capability row under
`_meta["com.git-pull.libtmux-mcp/capability"]`. The resource also reports its
schema version, frozen surface, effective names, connection provenance, and
common trust boundary.

## Worth knowing

**`libtmux-mcp: terminated signal received` is not a fault.** It is the server
handling the SIGTERM it gets when a client tears the transport down, including
when a client's connect timeout fires.

**Give it the environment a client would.** A client starts its servers with a
curated environment, not your shell's. Without a UTF-8 locale tmux rewrites
control characters in format output, which has broken the server's own
client-registration poll.

## See also

- [`mcp/README.md`](../../README.md) — client configuration
- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [`mcp-swap`](../../../internal/tools/mcp-swap) — point the agent CLIs on this machine at a local build
