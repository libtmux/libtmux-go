# benchmarks

What each way of reaching tmux costs, printed as one table.

```console
$ go -C benchmarks run .
```

```
building a 6-pane window, tmux 3.7c
12th Gen Intel(R) Core(TM) i7-12700H, 10 threads, linux, go1.26.5

path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   102ms          17        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 18ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              21ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    48ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection        8ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## Reading it

**The last column checks equivalence.** Every row runs one `SearchPanes` query
against the same six-pane topology. Only cost should differ.

Every supported tmux release has every row. Before tmux 3.6, destroying the
session attached to a connection follows that session's `detach-on-destroy`
policy and may end the connection; this workload keeps its attached session.

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
