# build-workspace

Build a tmuxp-style workspace and print what tmux made of it.

The workspace is a string in the program rather than a file beside it, so the
program runs anywhere without one.

## Running it

```console
$ go -C workspace run ./examples/build-workspace
```

It prints:

<!-- docs:output workspace/examples/build-workspace/output.txt -->

```text
session project: 2 windows
window editor: 2 panes
window logs: 3 panes
```

<!-- docs:end -->

## What to look at

**What it reports is what tmux made, not what the file asked for.** The
windows and panes it counts are read back from tmux after the build, so a pane
the file described and tmux declined to create would be missing here.

**The server is its own.** The program names a socket and an empty
configuration, so it builds the same thing whatever your `~/.tmux.conf` says,
and it stops that server before it exits.

**A pane running a command that exits takes its window with it.** Then the
session, then the server. The panes here run a shell and type a command into
it, so they stay. A workspace whose panes each run something short-lived as
their `shell` disappears before you can look at it, which is worth knowing
before writing one.

## Testing it

[`main_test.go`](main_test.go) runs the program against a real tmux, with a
`~/.tmux.conf` that would change the output if the program read it, and
compares what it prints with [`output.txt`](output.txt):

```console
$ go -C workspace test ./examples/build-workspace
```

## See also

- [`workspace/README.md`](../../README.md) — the format and what is supported
- [`examples/planned-build`](../../../examples/planned-build) — a layout built
  in code rather than from a file
