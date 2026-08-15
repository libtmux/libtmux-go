# agent-workflow

Drives the tmux MCP server the way an agent does.

It does the four things an agent needs to do before it is useful in somebody's
terminal: work out which pane it is running in, make room beside it, run
something there and wait for the result, and report the shape of what it built.

The client and server are joined in memory rather than over a pipe, so this is
one program rather than two. Everything else — tool names, arguments, the shape
of what comes back — is exactly what a client speaking to `libtmux-mcp` over
stdin and stdout sees.

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
not running inside this tmux server; using its active pane
split into %1
exit 0, 2 lines of output
  | tmux 3.7b
  | ready
window 80x24, layout c725,80x24,0,0{47x24,0,0,0,32x24,48,0,1}
  %0 at 0,0 47x24
  %1 at 48,0 32x24
```

Clean up with `tmux -L demo kill-server`.

## What to look at

**Self-detection comes first.** An agent already running inside the tmux it is
driving must not split the pane it lives in. The second line reports which case
it found; run it from inside `tmux -L demo` and it says the other thing.

**Waiting is a tool, not a sleep.** The run reports an exit code and the output
it produced, so the agent knows the command finished rather than guessing how
long to wait.

**Layout is reported, not assumed.** The last lines are what tmux says the
window became, which is how an agent confirms the room it made is the room it
got.

## See also

- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [`libtmux-mcp`](../../cmd/libtmux-mcp) — the binary a real client launches
