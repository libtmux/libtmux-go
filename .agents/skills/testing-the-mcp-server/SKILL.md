---
name: testing-the-mcp-server
description: >-
  Test the libtmux-go MCP server end to end — drive the real binary over raw
  JSON-RPC, validate every reply against the schema it advertises, and point
  installed agent CLIs (Claude, Codex, Cursor, Gemini, grok, agy, opencode) at
  a local build. Use when verifying the server beyond `go test`, checking a
  branch works in a real client, reproducing a client-reported bug, exercising
  approval or cancellation flows, or wiring a checkout into the CLIs with
  mcp-swap. Reach for it when asked to "test the MCP", "does this work in the
  agents", "probe every tool", or "check it across the CLIs".
---

# Testing the MCP server

`go test ./...` proves the handlers. It does not prove that a client can
discover a tool, get past an approval gate, call it with arguments a model
would actually send, and survive cancelling it mid-flight. Those failures live
in the gap between the in-memory transport the Go tests use and a real process
speaking JSON-RPC over a pipe.

## The socket is a flag here, not an environment variable

The Python server reads `LIBTMUX_SOCKET` and takes `socket_name` on every
targeted tool. This one does neither. An operator picks the socket once, at
launch, and a client reaches only that socket:

```console
$ libtmux-mcp -socket-name mcp-target
```

Setting `LIBTMUX_SOCKET` on this server does nothing at all. A harness that
exports it and expects isolation will silently drive the default socket
instead — which, on a machine running a real session, is somebody's work.

## Three tmux servers, never one

| Role | Socket | Who touches it |
| --- | --- | --- |
| The real session | default | a person, interactively — never a test |
| Harness | `tmux -L cli-harness` | the driver, sending keys to a CLI's TUI |
| Target | `tmux -L mcp-target` | the server under test, via `-socket-name` |

Keeping the target apart from the harness is the load-bearing part: a
destructive tool call on the socket hosting the agent's own TUI tears down the
agent mid-test, and its redraws pollute every capture.

Give the whole run its own root so a sibling checkout's suite cannot end a
server this one is using:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-probe && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

## Climb only as high as the question needs

### Layer 0 — the real binary over raw JSON-RPC

Fastest and most deterministic, and it reaches everything the Go tests cannot:
framing, schema validation on the wire, protocol negotiation, and the
environment a client actually supplies. Write a driver that spawns the binary,
holds stdin open, and matches replies by id.

Two rules decide whether it works:

- **Hold stdin open.** Closing it ends the server before it answers, which
  reads as a server that produced nothing.
- **Replace the environment, do not inherit it.** A client starts its servers
  with a curated environment. Passing your shell's hides a whole class of bug —
  and hides the self-detection path, since a curated environment carries
  neither `TMUX` nor `TMUX_PANE`.

The highest-yield check at this layer is free: every tool declares an
`outputSchema`, so validate each reply's `structuredContent` against the tool's
own advertised schema. A contract the server publishes and then breaks is a bug
no assertion had to be written for.

### Layer 1 — a headless CLI, one shot

Proves a real client can discover the tools and that a model will call them.
Every CLI has a config-isolation lever, so none of this needs a global config
rewrite. See `references/cli-matrix.md` for the per-CLI invocation, the
cheapest proof each one offers, and the approval-bypass flag each needs.

### Layer 2 — a CLI's TUI, driven by send-keys

The only layer that reaches an interactive approval prompt and mid-flight
cancellation. Send the prompt text and `Enter` as two separate `send-keys`
calls; batching them is what produces the "needs a double Enter" pitfall.

## Pointing the CLIs at a local build

`mcp-swap` rewrites every installed CLI's configuration and puts it back. It
starts the chosen build and asks it to report itself before writing anything,
so a configuration that could never have worked never replaces one that did.

Read first — `status` and `--dry-run` write nothing:

```console
$ go -C mcp run ./cmd/mcp-swap status
```

```console
$ go -C mcp run ./cmd/mcp-swap use-local --mode build
```

```console
$ go -C mcp run ./cmd/mcp-swap revert
```

## What the server already tells you

Before probing, ask it. `-doctor` reports the tmux it found, the socket, what
that server holds, the safety tier in force, and whether it is running inside a
pane:

```console
$ libtmux-mcp -doctor -socket-name mcp-target
```

`-tools` prints the advertised surface with each tool's classification, which
is how to confirm a tier withheld what it should:

```console
$ LIBTMUX_SAFETY=readonly libtmux-mcp -tools
```

## Gotchas that have cost real time

**A fixture pane must outlive the assertions.** A pane running a command that
exits takes its window, then its session, then the server. A test asserting
what survived races that teardown and reports a safety guard failing.

**Test geometry-dependent tools in a session nobody is attached to.** An
attached client resizes panes underneath the assertions.

**One tmux is not enough.** Anything touching capture needs the supported
range: `capture-pane -J` keeps the padding a row was padded with, and tmux only
trims it itself from 3.4. A gate on the default tmux passes while 3.2a and 3.3a
fail.

**Isolate a timing failure before believing it.** Three signs it is the machine
and not the code: the failing test moves between runs, the failure reads as
`no server running` or a timeout rather than a wrong value, and the file it is
in is not one the change touched.

**A CLI that fails is usually not the server.** Auth walls, vendor tier blocks
and broken installs all look like a dead server from the outside. Check the
client's own status before writing the verdict down.
