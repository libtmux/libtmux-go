# Against the Python server

There are two MCP servers for tmux under the libtmux name: this one, and
[libtmux-mcp](https://github.com/libtmux/libtmux-mcp) in Python. They serve the
same tmux and answer to the same clients, so a person choosing between them, or
running both, wants to know where they differ.

This is not the root [PARITY.md](../PARITY.md). That one is a contract: every
supported Python *library* capability has a Go equivalent, proved from a symbol
manifest. This is a comparison of two *servers*, and neither is a port of the
other. Nothing here is a promise.

Every claim below was measured, not read: both servers were started and driven
over stdio JSON-RPC by the same script, and this one was additionally driven
from inside a pane of the tmux server it drives. Where a number appears it came
back off the wire.

## The surface, counted

| | here | Python |
| --- | --- | --- |
| Tools | 58 | 56 |
| Arguments across them | 202 | 293 |
| Arguments with a closed set published as `enum` | 10 | 5 |
| Tools carrying an output schema | 58 | 56 |
| Tools carrying annotations | 58 | 56 |
| Collections published as null-or-array | 0 | 0 |
| Prompts | 4 | 4 |
| Resources, and templates | 1 and 6 | 0 and 6 |
| Capabilities declared | completions, logging, prompts, resources (with subscribe), tools | experimental, extensions, logging, prompts, resources, tools |

## The tools

Fifty-five tool names are the same on both and are grouped the same way. The
arguments are not: this server names them in camelCase and the Python one in
snake_case, on every argument it has — 215 snake_case and no camelCase, counted
off its own schemas. `paneId` is `pane_id`, `timeoutSeconds` is `timeout`.
Swapping one server for the other therefore changes every call, not just the
command that starts it.

The argument counts differ for a reason worth knowing before choosing. Python
takes `socket_name` on 48 of its tools, `session_id` on 33, and `window_id` on
26, because a call chooses its own target; strip those four targeting arguments
and its 293 become 142, against this server's 202. The trade runs both ways:
Python spends arguments on reaching any tmux server, and this one spends them
on narrowing a listing — `list_panes` here takes `command`, `pathUnder`,
`dead`, `active`, and `detail` where Python takes one `filters`.

Three tools are here and not there:

- `build_workspace` builds a whole session from a tmuxp-style document, so
  laying out five panes is one call rather than five.
- `get_job` collects a `run_command` that was started with `detach`, which is
  how a build runs without spending the turn waiting for it.
- `move_pane` moves a pane between windows, or breaks it out into its own.

One is here behind `LIBTMUX_MCP_PROMPTS_AS_TOOLS=1`: `get_recipe` offers the same
text as the MCP prompts, for a client that reads tools and not prompts. The
Python server has the same idea under `LIBTMUX_MCP_PROMPTS_AS_TOOLS=1`, which
turns each prompt into a tool of its own.

One is there and not here: `show_hook`, for a single hook. Here that is
`show_hooks` with a `name`.

## Knowing its own pane

Both servers work out which pane they are running in from `TMUX` and
`TMUX_PANE`, and both compare the socket rather than the pane id alone, since
another tmux on another socket has a `%1` too.

This one also falls back to the process tree. A client that starts its servers
with a curated environment passes neither variable, and a server started that
way is inside a pane and cannot see it from the environment; it finds the pane
whose process is one of its own ancestors.

Both report what they found. This one puts `insideThisServer` and
`callerPaneId` on `get_server_info` and `isCaller` on every pane summary.

They guard different things.

The Python server refuses the five tools that end something: `kill_pane`,
`respawn_pane`, `kill_window`, `kill_session`, and `kill_server`. The refusal
is flat — "Use a manual tmux command if intended" — and there is no way to
proceed through the protocol.

This one refuses the same five, and also everything that types into that pane:
`send_keys`, `paste_text`, `clear_pane`, and `run_command`. Typing is the case
that is hard to notice and impossible to undo — keystrokes land in the terminal
the conversation is happening in, interrupting the client or answering a prompt
nobody saw. Rather than refusing outright, it asks the person, through MCP
elicitation. It refuses when the person declines or the client cannot ask.
That refusal names the way out: another pane, `split_window`, or `list_panes`
to find one where `isCaller` is false.

A yes about writing there can be kept for the rest of the session, because a
guard that asks before every keystroke is one people learn to click through.
It covers writing to that pane and nothing else — ending the pane, its window,
its session, or the server asks again, and those forms do not offer to keep the
answer.

## Watching a pane

Both offer `capture_since`, which returns what a pane wrote since the cursor
the last call handed back, so a pane checked every turn costs its new lines
rather than its whole screen. Both offer `wait_for_text` and
`wait_for_channel`, so a client waits rather than polling.

This one also lets a client subscribe. It declares the `subscribe` capability,
holds a tmux control-mode connection per session for as long as anything is
subscribed, and sends `notifications/resources/updated` when a watched pane
writes — measured at 20ms from the write, over a real process. A client that
can subscribe never asks; it is told. The Python server's handshake declares no
subscribe capability, so its resources are read when a client asks and not
before.

## What the schemas say

Both publish closed value sets as JSON Schema `enum`, so a client validates
before the call and a model picks from a list rather than reading a sentence:
ten arguments here, five there. The Python server writes them as
`typing.Literal` and pydantic emits the enum; this one keeps a table of tool and
argument and writes it into the schema at registration.

Both refuse a value outside a set, an argument of the wrong type, and a field
they do not have — driven at all three, both answered with an error rather than
running. What each says differs: Python answers a bad `detail` with pydantic's
"Unexpected keyword argument", because it has no `detail` at all, where this one
names the values it takes.

Both publish every collection as an array rather than as null-or-array, so a
client can count what came back without checking for null first.

Both run their batch tools through the same per-tool schemas as a direct call.

Both batches choose what a failure does to the calls after it — `on_error`
there, `onError` here — and both default to stopping. Stopping suits a sequence,
where a step nobody took makes the ones after it wrong; continuing suits
independent calls, where one failure otherwise turns the whole batch into
something a caller cannot tell the state of.

## Being found

Both carry four MCP prompts. Two are the same job under different names:
diagnosing a pane, and laying out a workspace. Two are only there —
`run_and_wait` and `interrupt_gracefully` — and two are only here,
`watch_pane` and `recover_pane`.

This one also answers `completion/complete` and says so in its handshake, so a
client's picker offers the panes and sessions that exist when a prompt argument
or a resource template blank is being filled. The Python handshake declares no
completions capability, and declares `experimental` and `extensions`, which
this one does not. And it ships `TOOLS.md`, generated from the schemas, so
the reference cannot drift from the tools.

## Addressing the hierarchy

Both expose tmux as resources under `tmux://`.

The Python templates all take `{?socket_name}`, so one server reads from
several tmux servers, one read at a time. This one fixes its target at launch,
by `-socket-name`, `-socket-path`, `LIBTMUX_SOCKET`, or `LIBTMUX_SOCKET_PATH`,
and `-doctor` says which was taken. Nothing in a call can retarget it. That is
a deliberate trade: a client cannot reach a tmux the operator did not point it
at, and `list_servers` tells a person which others exist so they can start a
second instance.

The two differ on how a window is named. There it is a session and an index,
`tmux://sessions/{session}/windows/{window_index}`. Here it is the window's own
id, `tmux://windows/{window}` — written without its sigil, because a percent
sign begins an escape in a URI and an index changes when a window moves.

## Limiting what a client can do

Both read `LIBTMUX_SAFETY`, with the same three levels — `readonly`,
`mutating`, `destructive` — meaning the same things, so an operator running
both can keep the same operation ceiling. The Go server adds
`LIBTMUX_MCP_CAPABILITIES` as an independent allowlist and defaults it to
metadata-only; the Python server has no equivalent capability partition.

## Testing the server

The two ship different kinds of artefact for it. The Python server has sphinx
documentation and a `justfile`. This one has an agent skill,
`.agents/skills/testing-the-mcp-server/`, which carries the socket layout, the
fidelity layers, an exhaustive real-tmux schema gate, and a per-client matrix
of isolation levers and what each client's failure actually means. Its raw
driver covers JSON-RPC framing, protocol negotiation, curated client
environments, and real handshakes that the in-memory tests do not.
