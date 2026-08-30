# libtmux-mcp

The binary an MCP client launches. It serves **one** tmux server over stdin and
stdout.

Which tmux server it serves is chosen by flags at startup and cannot be changed
by a client, so a client reaches only the socket the operator selected.
It requires tmux 3.2a or newer and refuses an older binary before the MCP
transport starts.

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
With no access configuration it exposes topology metadata only. Set
`LIBTMUX_MCP_CAPABILITIES=operate` in the client-managed environment for the
ordinary content, pane, workspace, layout, and settings tools.

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
- [`mcp-swap`](../mcp-swap) — point the agent CLIs on this machine at a local build
