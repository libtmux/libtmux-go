# agent-workflow

Drives the tmux MCP server the way an agent does.

It does what an agent needs to do before it is useful in somebody's terminal:
work out which pane it is running in, make room beside it, start something
there without waiting for it, check on the panes while that runs, collect the
result, and report the shape of what it built.

The client and server are joined in memory rather than over a pipe, so this is
one program rather than two. Everything else — tool names, arguments, the shape
of what comes back — is exactly what a client speaking to `libtmux-mcp` over
stdin and stdout sees. The example selects the `operate` capability profile
when the environment does not select one, because the metadata-only default
cannot build the demonstrated workspace.

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
started job libtmux-mcp-1 without waiting for it
2 of 2 panes in this window:
  %0 running sleep, 0 lines of scrollback
  %1 running zsh, 0 lines of scrollback
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

**Waiting is a tool, not a sleep — and often not needed at all.** `run_command`
with `detach` returns a handle as soon as the command is typed, so the listing
below it happens while the command runs. `get_job` collects the exit status and
the output afterwards. Neither reads the screen to guess whether the command
finished.

**Checking is not capturing.** The listing is narrowed to one window and asks
for `detail: full`, so it reports each pane's command and scrollback size
without reading any pane's contents. Every value comes from the snapshot the
listing already took, so eight panes cost what one costs. `2 of 2` is the
`total` the criteria selected from.

**Layout is reported, not assumed.** The last lines are what tmux says the
window became, which is how an agent confirms the room it made is the room it
got.

## See also

- [`mcp/TOOLS.md`](../../TOOLS.md) — the tool reference
- [`libtmux-mcp`](../../cmd/libtmux-mcp) — the binary a real client launches
