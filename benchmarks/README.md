# benchmarks

What each way of reaching tmux costs, printed as one table.

```console
$ go -C benchmarks run .
```

```
building a 6-pane window, tmux 3.7c
12th Gen Intel(R) Core(TM) i7-12700H, 20 threads, linux, go1.26.5

path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   188ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 68ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              79ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    62ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       16ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## Reading it

**The last column checks equivalence.** Every row runs one `SearchPanes` query
against the same six-pane topology. Only cost should differ.

The connection rows require tmux 3.6 or newer. Earlier releases retain the
process and chained rows instead of pretending the terminal connection can
provide weaker lifecycle guarantees.

**Count invocations, not milliseconds.** Wall clock moves with the machine and
what else is running. The process column does not.

The harness freezes a small POSIX proxy as the server executable. The proxy
records one invocation, removes its bookkeeping variables, and `exec`s the
real tmux selected at harness creation with the original arguments and standard
streams. It does not interpret tmux commands.

The same measurement runs as a test and fails if compatible lanes answer
differently:

```console
$ go -C benchmarks test ./...
```

That stops a cost comparison from measuring different work.

## See also

| | |
| --- | --- |
| [`../BENCHMARKS.md`](../BENCHMARKS.md) | The checked-in table, one per supported tmux, with the machine stated |
| [`../tmux/`](../tmux/) | The library being measured |
