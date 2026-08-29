# build-workspace

Loads a tmuxp-style YAML workspace, builds it, and reports what tmux actually
created.

It reads the workspace from its own source rather than from a file, so the
program runs anywhere without one.

## Running it

```console
$ go -C workspace run ./examples/build-workspace
```

```text
built "example-project" with 2 windows
  window 1 editor   2 pane(s)
  window 2 logs     1 pane(s)
```

## What to look at

**The builder owns its temporary connection.** Session creation and every build
command use one retained control connection. The builder closes it before
returning an ordinary session handle.

**What it reports is what tmux made, not what the file asked for.** The windows
and panes listed are read back from tmux after the build, so a pane the file
described and tmux declined to create would be missing here.

**A pane running a command that exits takes its window with it.** Then the
session, then the server. A workspace whose panes all run something short-lived
disappears before you can look at it, which is worth knowing before writing one.

## See also

- [`workspace/README.md`](../../README.md) — the schema and what is supported
- [`examples/planned-build`](../../../examples/planned-build) — the same
  fewer-invocations idea, built by hand instead of from a file
