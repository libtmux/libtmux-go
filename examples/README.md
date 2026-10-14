# Examples

Runnable programs, one per topic. Each starts what it needs in tmux and
removes it on the way out, so running one leaves nothing behind there.
`byte-streams` also writes an archive to your temporary directory, and names
it.

```console
$ go -C examples run ./quickstart
```

Every page shows the program's output, and a test compares that output with
what the program prints, on every supported tmux.

## Start here

| Example | Shows |
| --- | --- |
| [`quickstart/`](quickstart/) | Run a command in tmux and print its screen and exit status |
| [`object-model/`](object-model/) | Records, not live handles: what a rename returns, and a lookup that finds nothing |
| [`testing-a-program/`](testing-a-program/) | A whole `go test` file that runs a program in a pane and waits for its answer |

## Find things

| Example | Shows |
| --- | --- |
| [`filter-query/`](filter-query/) | List sessions, filter one read in Go, or let tmux filter |
| [`queries/`](queries/) | One read of the server, asked many things with `tmuxq` |
| [`any-slice/`](any-slice/) | The `tmuxq` helpers on a slice of your own, with no tmux |
| [`snapshot-browser/`](snapshot-browser/) | Walk sessions, windows, and panes from one read |

## Drive panes

| Example | Shows |
| --- | --- |
| [`run-to-completion/`](run-to-completion/) | `os/exec` for a program that needs a terminal, and a command stopped midway |
| [`pane-io/`](pane-io/) | Type into a pane and wait for its answer; read a pane as an `io.Reader` |
| [`byte-streams/`](byte-streams/) | Move a payload in and a scrollback out with no quoting and no temporary file |

## Change and watch tmux

| Example | Shows |
| --- | --- |
| [`option-hook-editing/`](option-hook-editing/) | Read and write tmux options and hooks by their Go names |
| [`environment/`](environment/) | What tmux passes to the processes it starts |
| [`planned-build/`](planned-build/) | Record commands, read the plan, then send it in fewer tmux invocations |
| [`fast-path/`](fast-path/) | A plain server beside an owned connection |
| [`control-mode-subscribe/`](control-mode-subscribe/) | Hear what tmux does as it happens, and subscribe to a value |

## In the other modules

Two more live beside the modules they show, because each needs that module's
dependencies:

| Example | Shows |
| --- | --- |
| [`workspace/examples/build-workspace`](../workspace/examples/build-workspace) | Build a tmuxp-style workspace and print what tmux made of it |
| [`mcp/examples/agent-workflow`](../mcp/examples/agent-workflow) | Drive the tmux MCP server the way an agent does |

## Which tmux they reach

`quickstart`, `object-model`, `queries`, and `filter-query` start a tmux
server of their own,
on a private socket with an empty config, and kill it when they finish. They
print the same thing on every machine and never touch a tmux you have open.
`any-slice` needs no tmux, and `testing-a-program` is a test, which runs in a
server the test harness owns.

The others take the server your `tmux` command reaches, make a session there,
and kill that session on the way out. They also read your tmux configuration,
so an index or a count on one of those pages can differ under a `base-index`
or a hook of your own; each page shows what a server with nothing else on it
and no configuration prints. To keep those off a tmux you are using,
give them a socket directory of their own:

```console
$ export TMUX_TMPDIR=/tmp/libtmux-go-examples && \
    mkdir -p "$TMUX_TMPDIR" && \
    unset TMUX TMUX_PANE
```

All of them need tmux 3.2a or newer on `PATH`.

## Testing them

The tests run on sockets of their own, so they reach neither your tmux nor the
one the examples use when you run them:

```console
$ go -C examples test ./...
```

These are a module of their own, so an example reaching for a dependency can
never put that dependency in the library's `go.mod`. How the pages quote the
programs, and the tests that hold them to it, are in
[CONTRIBUTING.md](../.github/CONTRIBUTING.md#generated-documentation).

## See also

| | |
| --- | --- |
| [`../README.md`](../README.md) | Start here |
| [`../tmux/`](../tmux/) | How the library is shaped |
| [`../tmux/tmuxtest/`](../tmux/tmuxtest/) | Testing a program in a real tmux |
