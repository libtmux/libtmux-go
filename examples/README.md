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
| [`control-mode-subscribe/`](control-mode-subscribe/) | An owned notification stream | Watching what tmux says on its own |

Each directory has a page of its own explaining what to look at, and each has a
test that runs the example against a real tmux and reads what it printed. The
tests use a socket the harness owns and removes, so they reach neither your tmux
nor the socket the examples use when you run them:

```console
$ go -C examples test ./...
```

## Quoting an example in the docs

Every fenced Go block in this repository's Markdown that sits between `docs:`
markers is a region of real Go that compiles and runs, copied in by
`go generate`. The [top-level README](../README.md) is built that way, and so
are [`filter-query/README.md`](filter-query/README.md) and
[`../tmux/tmuxtest/README.md`](../tmux/tmuxtest/README.md).

A region is named where it is written:

```go
	// docs:quickstart
	windowName := "work"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: &windowName})
	// docs:end
```

and a Markdown file asks for it by that name, between `<!-- docs:<name> -->`
and `<!-- docs:end -->`. Everything between those two lines is replaced by a
fenced `go` block holding the region. A block outside a marker pair is never
touched, so a hand-written snippet says so by not being marked.

Three things about the format:

- A name is lowercase letters, digits and hyphens, and is unique across the
  whole repository rather than within its file. Two regions sharing a name is
  an error naming both files.
- Shared leading tabs come off, so a region written inside a function reads at
  column zero in the docs.
- A region may live in any Go file this repository owns. Most are here, but
  `tmuxtest-quickstart` is in a test,
  [`screen_test.go`](../tmux/tmuxtest/screen_test.go), because that is where
  the code it shows belongs.

The loop runs one way. Edit the program, then bring the quote across:

```console
$ go generate ./...
```

Editing the fenced block instead is undone the next time anyone runs that, and
the gate it is paired with — `go generate ./... && git diff --exit-code` —
fails on the difference.

### The two mistakes it does not report

Naming a region no Go file defines fails, with the file and line of the block
that named it. So does a region that never ends, one that starts inside
another, and a Markdown block that is never closed.

Two mistakes pass quietly instead:

- A Markdown marker has to begin at column zero. Indented, it is not a marker,
  the block stays empty, and nothing says so.
- A region nothing quotes is not an error, so renaming on the Markdown side
  leaves the old region compiling and unread.

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
