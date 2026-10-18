# Examples

Runnable programs, one per topic. Each starts what it needs in tmux and
removes it on the way out, so no tmux server is left running after one.
`byte-streams` also writes an archive to your temporary directory, and names
it.

They live in the repository rather than the module you install, so run them
from a clone:

```console
$ git clone https://github.com/libtmux/libtmux-go.git
```

```console
$ cd libtmux-go
```

```console
$ go -C examples run ./quickstart
```

Every page of a program shows its output, and a test compares that output
with what the program prints, on every supported tmux.

## Start here

| Example | Shows | Read it for |
| --- | --- | --- |
| [`quickstart/`](quickstart/) | Run a command in tmux and print its screen and exit status | The shortest path from nothing to a pane running your command |
| [`layout/`](layout/) | Split a window into two panes, then read back what tmux made | Trusting what tmux reports about a split rather than what you asked for |
| [`object-model/`](object-model/) | Records, not live handles: what a rename returns, and a lookup that finds nothing | Why a rename hands back a new record, and how a lookup that finds nothing is reported |
| [`testing-a-program/`](testing-a-program/) | A whole `go test` file that runs a program in a pane and waits for its answer | The shape of a whole test file: `TestMain`, a pane, a typed line, and a wait for the answer |

## Find things

| Example | Shows | Read it for |
| --- | --- | --- |
| [`filter-query/`](filter-query/) | List sessions, filter one read in Go, or let tmux filter | The two ways to ask tmux a question, and what each costs |
| [`queries/`](queries/) | One read of the server, asked many things with `tmuxq` | Asking one read many questions, with each filter shape `tmuxq` takes |
| [`any-slice/`](any-slice/) | The `tmuxq` helpers on a slice of your own, with no tmux | `tmuxq` on a type of your own, with no tmux installed |
| [`snapshot-browser/`](snapshot-browser/) | Walk sessions, windows, and panes from one read | Walking sessions, windows and panes without re-querying |

## Drive panes

| Example | Shows | Read it for |
| --- | --- | --- |
| [`run-to-completion/`](run-to-completion/) | `os/exec` for a program that needs a terminal, and a command stopped midway | A nonzero exit as a result, and stopping a command that is still running |
| [`pane-io/`](pane-io/) | Type into a pane and wait for its answer; read a pane as an `io.Reader` | Typing a line and scanning the reply with the standard interfaces |
| [`byte-streams/`](byte-streams/) | Move a payload in and a scrollback out with no quoting and no temporary file | Why a payload goes in through a buffer instead of the keyboard |

## Change and watch tmux

| Example | Shows | Read it for |
| --- | --- | --- |
| [`option-hook-editing/`](option-hook-editing/) | Read and write tmux options and hooks by their Go names | A wrong value being a compile error instead of a complaint from tmux |
| [`environment/`](environment/) | What tmux passes to the processes it starts | Setting a variable for a session, and finding which pane a program runs in |
| [`planned-build/`](planned-build/) | Record commands, read the plan, then send it in fewer tmux invocations | Naming a pane before it exists, and reading a plan before it runs |
| [`fast-path/`](fast-path/) | A plain server beside an owned connection | Plain and connected reads, plus their exact-capture boundary |
| [`control-mode-subscribe/`](control-mode-subscribe/) | Hear what tmux does as it happens, and subscribe to a value | Watching what tmux says on its own |

## In the other modules

These live beside the modules they show, because each needs that module's
dependencies:

| Example | Shows | Read it for |
| --- | --- | --- |
| [`workspace/examples/build-workspace`](../workspace/examples/build-workspace) | Build a tmuxp-style workspace and print what tmux made of it | Building a workspace from a string, and reading back what tmux made of it |
| [`mcp/examples/agent-workflow`](../mcp/examples/agent-workflow) | Drive the tmux MCP server the way an agent does | The loop an agent runs: find its own pane, make room, start work, wait, and read back |

## Which tmux they reach

Each program starts a tmux server of its own, on a private socket with an
empty config, and that server is gone when the program finishes. They print
the same thing on every machine and never touch a tmux you have open.
`any-slice` needs no tmux, and `testing-a-program` is a test, which runs in a
server the test harness owns.

Every example but `any-slice` needs tmux 3.3a or newer on `PATH`.

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
