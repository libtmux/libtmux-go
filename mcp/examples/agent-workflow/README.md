# agent-workflow

Drive the tmux MCP server the way an agent does.

It does what an agent needs to do before it is useful in somebody's terminal:
work out which pane it is running in, create a session when the socket is
empty, make room without replacing the conversation's pane, start work and
wait for what it prints, run one command and read its exit status, and read
back what it built.

The client and server are joined in memory rather than over a pipe, so this is
one program rather than two. Everything else is what a client speaking to
`libtmux-mcp` over stdin and stdout sees: tool names, arguments, and the shape
of what comes back. The example grants itself the `inspect`, `manage`, and
`execute` toolsets when the environment does not choose.

## Running it

```console
$ go -C mcp run ./examples/agent-workflow -socket-name demo
```

On a socket with nothing on it, it starts a session there and prints:

<!-- docs:output mcp/examples/agent-workflow/output.txt -->

```text
no tmux server on that socket yet; starting one
not running inside this tmux server; using active pane %0
split into %1
2 panes in this window while the work runs
the work printed agent-workflow-ready
the command exited 0 and printed:
  | bounded work
the window now holds 2 panes: %0 %1
```

<!-- docs:end -->

Clean up with `tmux -L demo kill-server`.

Pointed at a tmux you already use, it works beside what is there, so the pane
ids differ. Run from inside that tmux, it reports its own pane and leaves it
alone.

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

**Waiting is a tool, not a sleep.** `wait_for_text` watches the pane for the
line the work prints, and stops early on the line that would mean it failed.
`run_shell_command` runs one command, waits no longer than the timeout it was
given, and returns the exit status and what the command printed. The example
never polls the screen.

**The wait names text the typed command does not contain.** The pane echoes
what is pasted into it, so the work prints `agent-workflow-ready` from a
format string: the typed line holds `agent-workflow-%s` and never the word the
wait is looking for.

**Checking is not capturing.** `list_panes` reports command, activity, caller
identity, and geometry without returning terminal content. `get_window_info`
then reads the window back rather than assuming the split produced what was
asked for.

**The example uses the real manifest.** In-memory transport changes no tool
names, native schemas, metadata, filtering, or static capability disclosure.

## Testing it

[`main_test.go`](main_test.go) runs the program against a real tmux on a
socket of its own and compares what it prints with [`output.txt`](output.txt):

```console
$ go -C mcp test ./examples/agent-workflow
```

## See also

- [`mcp/README.md`](../../README.md) — installing the server and what it offers
- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [`Example_watchingAVisibleCommandAcrossTurns`](../../example_test.go) — visible
  pane work observed through a cursor across turns
- [`libtmux-mcp`](../../cmd/libtmux-mcp) — the binary a real client launches
