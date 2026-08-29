---
name: testing-the-mcp-server
description: >-
  Test the libtmux-go MCP server end to end — drive the real binary over raw
  JSON-RPC, run the exhaustive advertised-schema gate, and point installed
  agent CLIs (Claude, Codex, Cursor, Gemini, grok, agy, opencode) at a local
  build. Use when verifying the server beyond `go test`, checking a branch
  works in a real client, reproducing a client-reported bug, exercising
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

## One socket, chosen once

The Python server takes `socket_name` on every targeted tool, so a client picks
the tmux per call. This one does not: an operator picks the socket at launch
and a client reaches only that socket.

```console
$ libtmux-mcp -socket-name mcp-target
```

`LIBTMUX_SOCKET` names it when no flag does, matching the Python server, so one
configuration serves both. A flag wins over the variable. Ask the server which
it took rather than assuming — `-doctor` names the origin:

```console
$ libtmux-mcp -doctor
```

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
environment a client actually supplies. `references/drive.py` is that driver:
it spawns the binary with an environment it builds rather than inherits, holds
stdin open, matches replies by id, declines any question the server asks, and
reads a plan of JSON-RPC calls from stdin.

```console
$ ./references/drive.py "$(command -v libtmux-mcp)" TMUX_TMPDIR=/tmp/libtmux-go-probe \
    <<< '[{"method":"tools/call","params":{"name":"list_panes","arguments":{}}}]'
```

It reports which advertised tools a plan never called, so a sweep can be grown
until that list is empty. It also flags a tool that declares an `outputSchema`
and answers without structured content. It does not interpret JSON Schema, so
that flag is a presence check rather than schema validation.

`references/compare.py` is its sibling for the question "how does this differ
from another MCP server". It counts what a handshake declares and what a tool
list carries, drives the three calls a client gets wrong, and prints one JSON
report; run it against each server and diff. Use it rather than reading one
server's source against the other's schemas — that is how a comparison here
came to claim both servers took the same arguments, when one names every
argument in snake_case and the other in camelCase.

Two rules decide whether it works:

- **Hold stdin open.** Closing it ends the server before it answers, which
  reads as a server that produced nothing.
- **Replace the environment, do not inherit it.** A client starts its servers
  with a curated environment. Passing your shell's hides a whole class of bug —
  and hides the self-detection path, since a curated environment carries
  neither `TMUX` nor `TMUX_PANE`.
- **Run the driver from inside a pane to reach self-detection at all.** Started
  anywhere else, `isCaller` is null in every reply and every guard that depends
  on it is untested. `tmux new-window -d "drive.py ..."` on the server under
  test sets `TMUX` and `TMUX_PANE` for you. Aim a destructive tool at the
  pane's window, session, and server as well as at the pane: a guard that knows
  one pane and not what holds it is reached one level up.

Pair the wire probe with the exhaustive schema gate:

```console
$ go -C mcp test \
    -run '^TestEveryToolAnswersTheSchemaItPublishes$' \
    -count=1 \
    .
```

That real-tmux sweep calls every advertised tool, round-trips each structured
reply through JSON, and validates it with `jsonschema-go`. Keep the claims
separate: the driver proves process framing and the client environment; the Go
test proves the complete advertised output contract.

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
