# agent-workflow

Drives the tmux MCP server the way an agent does.

It does what an agent needs to do before it is useful in somebody's terminal:
work out which pane it is running in, create a session when the socket is empty,
make room without replacing the conversation's pane, run one bounded command,
check the panes, and report the shape of what it built.

The client and server are joined in memory rather than over a pipe, so this is
one program rather than two. Everything else — tool names, arguments, the shape
of what comes back — is exactly what a client speaking to `libtmux-mcp` over
stdin and stdout sees. The example selects the unordered `inspect,execute`
toolsets when the environment does not select a surface.

## Running it

It drives an existing tmux server, so give it one:

```console
$ tmux -L demo new-session -d -s demo
```

```console
$ go -C mcp run ./examples/agent-workflow -socket-name demo
```

```
tmux 3.7b on /tmp/tmux-1000/demo
not running inside this tmux server; using active pane %0
split into %1
exit 0, 2 lines of output
  | tmux 3.7b
  | ready
2 panes in the affected window, 2 on the server:
  %0 running sleep at 0,0 47x24
  %1 running zsh at 48,0 32x24
window 80x24, layout c725,80x24,0,0{47x24,0,0,0,32x24,48,0,1}
```

Clean up with `tmux -L demo kill-server`.

## What to look at

**The target is pinned first.** `get_server_info` reports the one selected
socket and whether it already has a server. An empty socket is handled
explicitly by `create_session`; no call can select another socket.

**Self-detection comes before the split.** An agent already running inside the
tmux it drives must not replace or split the pane carrying its conversation.
`get_server_info` identifies that pane only when both the pane id and socket
match. Outside that server, the example chooses its active pane explicitly.

**Creation does not smuggle in a command.** `split_window` accepts no command or
environment payload. It starts the pane's configured process; only the separate
`run_shell_command` call receives executable input.

**Waiting is a tool, not a sleep.** `run_shell_command` sends a framed command,
waits with a fixed ceiling, and returns the exit status and bounded output. The
example does not poll the screen or expose a background job handle.

**Checking is not capturing.** `list_panes` reports command, activity, caller
identity, and geometry without returning terminal content. `get_window_info`
then returns tmux's layout string rather than assuming the split produced the
requested shape.

**The example uses the real manifest.** In-memory transport changes no tool
names, native schemas, metadata, filtering, or static capability disclosure.

## See also

- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [`Example_watchingAVisibleCommandAcrossTurns`](../../example_test.go) — visible
  pane work observed through a cursor across turns
- [`libtmux-mcp`](../../cmd/libtmux-mcp) — the binary a real client launches
