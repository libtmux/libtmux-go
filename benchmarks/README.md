# benchmarks

What each way of reaching tmux costs, printed as one table.

```console
$ go -C benchmarks run .
```

```
building a 6-pane window, tmux 3.7b
12th Gen Intel(R) Core(TM) i7-12700H, 20 threads, linux, go1.26.5

mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 619ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            127ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           165ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 111ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        21ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 11ms           6        1  2 panes on the server [0 0]
snapshot, bound          11ms           5        1  2 panes on the server [0 0]
```

## Reading it

**The last column is the point.** It is one `SearchPanes` query asked of every
mode. It is identical in every row, on every supported tmux — everything else on
the page is cost, and cost is the only thing a caller changes by picking a mode.

**Count invocations, not milliseconds.** Wall clock moves with the machine and
what else is running. The process column does not.

The same measurement runs as a test, and fails if the modes ever answer
differently:

```console
$ go -C benchmarks test ./...
```

That is what stops the table from quietly comparing two different things.

## See also

| | |
| --- | --- |
| [`../BENCHMARKS.md`](../BENCHMARKS.md) | The checked-in table, one per supported tmux, with the machine stated |
| [`../tmux/`](../tmux/) | The library being measured |
