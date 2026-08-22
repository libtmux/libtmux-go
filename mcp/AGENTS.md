# AGENTS.md

Rules for `mcp/`, the module that serves one tmux server to Model Context
Protocol clients. The repository-wide rules are in
[`AGENTS.md`](../AGENTS.md) at the root and the files it routes to; this adds
what only applies here.

## Testing the MCP server

The Go tests drive the server over the SDK's in-memory transports. That misses
everything about the real process: JSON-RPC framing, schema validation on the
wire, argument coercion by a client, and the handshake itself. Test through a
real client as well.

### The MCP Inspector

Configure it with a client config file rather than argv. The Inspector's own
flags otherwise consume the server's, and a config file is also how a real
client is configured, so it tests the thing that will actually happen:

```console
$ npx -y @modelcontextprotocol/inspector --cli \
    --config ./mcp.json \
    --server libtmux \
    --method tools/list
```

Four things are worth knowing.

**Give the server the environment a client would.** An MCP client starts its
servers with a curated environment, not the shell's. Declaring only what the
server needs in the config's `env` is what a real client does, and it is what
found the control-connection hang: without a UTF-8 locale, tmux rewrites
control characters in format output, a tab separator came back as an
underscore, and the tmux module's client-registration poll never matched.
Testing with an inherited shell environment hides that entire class of bug.

**`libtmux-mcp: terminated signal received` is not a fault.** It is the server
handling the SIGTERM it gets when a client tears the transport down, including
when the Inspector's own 15-second connect timeout fires. Treat it as the
symptom of a client giving up, not as the cause. It used to read `context
canceled`, which named the mechanism and left the reason to be guessed at.

**Ports do not matter in `--cli` mode.** The CLI builds a `StdioClientTransport`
and spawns the command directly, binding nothing. `CLIENT_PORT` and
`SERVER_PORT` only matter for the web UI — worth setting anyway when another
agent may be using the defaults (6274, 6277).

**Spawn the binary by path.** Giving the Inspector a command it has to resolve,
such as `npx -y some-server`, can end with a bare `sh` reading the JSON-RPC
stream as a shell script: `sh: 1: {method:initialize...}: not found`.

### Driving it by hand

A raw JSON-RPC driver over stdin and stdout is the cheapest way to exercise
every tool against the real binary. Hold stdin open while waiting for replies:
closing it ends the server before it answers, which reads as a server that
produced nothing.

Worth covering, because in-memory tests have missed each of them:

- Every tool, with arguments a client would actually send.
- A tool named with a value rather than the default path. A lookup that only
  ever ran without an explicit name has hidden a broken one.
- Arguments of the wrong type. A client may coerce `command=true` into a
  boolean, and the schema should reject it.
- The protocol versions a client may negotiate.
- A stripped environment, per the Inspector note above.
- The server started from inside a pane of the tmux server it drives, which is
  how it is usually run and the only way to exercise self-detection.

### Fixtures

A workspace pane running a command that exits, such as `true`, takes its
window, its session, and then the tmux server with it. A test asserting what
survived will race that teardown and report a safety guard failing. Give a
fixture a pane that outlives the assertions made about it.

