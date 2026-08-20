# What each mode costs

Building one 6-pane window, four ways, plus the two snapshot reads. Regenerate
with:

```console
$ go -C benchmarks run .
```

The `benchmarks` module is the source of these numbers and also the gate on
them. The command above prints the table; `go -C benchmarks test ./...` runs the
same measurement and fails if the modes answer one query differently, so the
table cannot quietly start comparing two different things. CI runs both and
publishes what it printed, so these can be checked against a run other than the
one they were recorded on.

Recorded on a 12th Gen Intel Core i7-12700H, 20 threads, Linux, go1.26.5.
Each table below is the same workload on a different tmux.

## tmux 3.2a

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 502ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            117ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           121ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 218ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        21ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 48ms           6        1  2 panes on the server [0 0]
snapshot, bound          19ms           5        1  2 panes on the server [0 0]
```

## tmux 3.3a

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 584ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode             88ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           192ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 106ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        32ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                  9ms           6        1  2 panes on the server [0 0]
snapshot, bound          13ms           5        1  2 panes on the server [0 0]
```

## tmux 3.4

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 654ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            119ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           185ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 187ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        67ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 24ms           6        1  2 panes on the server [0 0]
snapshot, bound          18ms           5        1  2 panes on the server [0 0]
```

## tmux 3.5

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 659ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            109ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           184ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 191ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        42ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 16ms           6        1  2 panes on the server [0 0]
snapshot, bound           6ms           5        1  2 panes on the server [0 0]
```

## tmux 3.6

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 648ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            148ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           189ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 195ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        49ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 10ms           6        1  2 panes on the server [0 0]
snapshot, bound          23ms           5        1  2 panes on the server [0 0]
```

## tmux 3.7

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 636ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode             87ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           130ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 149ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        44ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 23ms           6        1  2 panes on the server [0 0]
snapshot, bound           7ms           5        1  2 panes on the server [0 0]
```

## tmux 3.7a

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 491ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            133ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           145ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 115ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        27ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 11ms           6        1  2 panes on the server [0 0]
snapshot, bound          11ms           5        1  2 panes on the server [0 0]
```

## tmux 3.7b

```
mode                     wall   processes  clients  query answer
--------------------------------------------------------------------------------------
process                 493ms          44        0  7 panes on the server [0 0 1 2 3 4 5]
control mode            112ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4           139ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                 246ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + control        90ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
snapshot                 35ms           6        1  2 panes on the server [0 0]
snapshot, bound          22ms           5        1  2 panes on the server [0 0]
```

## What one MCP call costs

The tables above measure the ways of reaching tmux. This measures the layer a
client talks to: one tool call, decoded and validated, against a real tmux.

```console
$ go -C mcp test -run '^$' -bench . -benchtime 60x .
```

Only the allocation counts are recorded, and deliberately. Talking to tmux
dominates the wall clock of every call here, and the clock moves with whatever
else the machine is running -- the same six calls span 15ms to 44ms between
runs on an idle laptop and a busy one, while their allocation counts repeat to
within a tenth of a percent. Publishing the times per release would be
recording the load rather than the server. Compare one revision against another
on one machine, and watch the allocations.

| Call | allocs/op |
| --- | --- |
| `list_sessions` | 2,186 |
| `get_server_info` | 2,377 |
| `list_panes` | 2,712 |
| `list_panes` with `detail: full` | 2,894 |
| `display_message` | 6,001 |
| `capture_pane` | 6,024 |

Two of these measure a claim rather than a cost.

**A batch is not faster than its parts.** Three listings batched and the same
three sent one at a time come out level on the clock; the batch allocates
1.88MB against 2.52MB and, over a real pipe rather than the in-memory transport
these run on, spends one round trip rather than three. Its reason to exist is
the caller's turn, not the server's CPU.

**`capture_since` has a break-even.** It reads the pane and then fingerprints
the rows to mint a cursor, so it costs more than a plain capture and returns a
cursor of about half a kilobyte in every reply. On an 80x24 pane of short lines
that is 585 bytes against 152, and `capture_pane` wins on both counts. It earns
its place on a wide pane holding full lines, read repeatedly -- which is what it
is for, and is worth knowing is not every pane.

## Reading it

**The answer column is the point.** It is identical in every row, on every
version above. Everything else on this page is cost, and cost is the only thing
a caller changes by picking a mode.

**Count invocations, not milliseconds.** Wall clock moves with the machine, the
tmux version, and what else is running. The process column does not: every
version reports the same counts for the build rows and for the snapshot rows.
When these numbers are compared against a CI run, compare those.

**Part of the process-to-chained gap is work, not batching.** The direct build
materializes a record after each mutation, because that is what its methods
return. A plan reports IDs and statuses instead. A caller who needs a record per
step is choosing the direct API knowingly, and the 44 includes what that costs.

**A control connection is a tmux client.** That is the clients column, and it is
why connecting is a choice rather than a default: a configuration keyed on
`session_attached` sees it.

**Chaining pays on processes, less on a connection.** Grouping removes a process
per command, which is the whole win over a tmux process. Over a connection there
is no process to remove and tmux still answers each command in a list
separately, so what a plan is worth there is the forward reference and one round
of results rather than speed.

**Streaming is not a row, and cannot be.** The build rows are four ways of
doing the same work, which is what makes them comparable at all. Streaming does
no work: it reads what tmux says on its own while something else does, so there
is no build to time and no query to answer. What it costs is the connection it
rides on, which is the control-mode row.

**`chained` is one planner of three.** A plan groups its commands through a
`Planner`, and the row above uses the default, which folds every run of
commands that neither answer nor create something. `Marked` folds more, putting
a pane creation and the commands decorating it in one list, and `Sequential`
folds nothing, which is how a failure that grouping made ambiguous gets
isolated. All three produce the same results and differ only in invocations;
`TestPlannersAgreeOnResultsAndDifferOnCost` is where that is measured.

**Concurrency is a size, not a speed.** `concurrent x4` opens four connections
so four commands can be in flight. The build above is a sequence, so it has
little to overlap and the row moves with the machine rather than with the number
of connections; the column that changes reliably is `clients`, because each
connection is one more attached tmux client. Raise it for parallel readers.

**The snapshot rows count tmux commands, not processes.** Both run over one
connection and start none, so processes would read zero for both and show
nothing. They differ only in whether the transport reports
`InstanceBoundEngine`: a connection that stayed open already proves what a
snapshot's closing identity read asks, so a bound one skips it. The opening read
stays, because the listing formats are chosen from the version it reports. An
engine that wraps another and forgets to forward that property pays for the
closing read again.
