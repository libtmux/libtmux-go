# Examples

Runnable programs, one per topic. Each creates its own tmux session and kills it
on the way out, so running one leaves nothing behind.

These are a **module of their own**, so an example reaching for a dependency can
never put that dependency in the library's `go.mod`. Run them with `go -C`:

```console
$ go -C examples run ./quickstart
```

| Example | Shows | Read it for |
| --- | --- | --- |
| [`quickstart/`](quickstart/) | Session, window, split, send keys, capture | The shortest path from nothing to a pane running your command |
| [`filter-query/`](filter-query/) | Snapshot predicates and live tmux filters | The two ways to ask tmux a question, and what each costs |
| [`snapshot-browser/`](snapshot-browser/) | One read, whole hierarchy | Walking sessions, windows and panes without re-querying |
| [`option-hook-editing/`](option-hook-editing/) | Typed options and hooks | Reading and writing tmux settings by their Go names |
| [`environment/`](environment/) | Session and global environment | What tmux passes to the processes it starts |
| [`fast-path/`](fast-path/) | Control-mode connection | What a transport is worth, counted in tmux processes |
| [`planned-build/`](planned-build/) | Recording commands, then sending them | Naming a pane before it exists, and reading a plan before it runs |
| [`control-mode-subscribe/`](control-mode-subscribe/) | `Notifications` as a range loop | Watching what tmux says on its own |

Each directory has a page of its own explaining what to look at, and each has a
test that runs the example against a real tmux and reads what it printed. The
tests use a socket the harness owns and removes, so they reach neither your tmux
nor the socket the examples use when you run them:

```console
$ go -C examples test ./...
```

## Requirements

A tmux on `PATH`, version 3.2a or newer. To keep these off a tmux server you are
using:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-examples && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

## See also

| | |
| --- | --- |
| [`../tmux/`](../tmux/) | The library |
| [`../README.md`](../README.md) | Start here |
