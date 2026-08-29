# What each execution path costs

Building one six-pane window through every path compatible with the running
tmux. Releases before 3.6 have process and chained rows; tmux 3.6 and newer also
have one-lane, four-lane, and chained connection rows. Regenerate with:

```console
$ go -C benchmarks run .
```

The `benchmarks` module is the source of these numbers and also the gate on
them. The command above prints the table; `go -C benchmarks test ./...` runs the
same measurement and fails if compatible paths answer differently, so a cost
comparison cannot quietly measure different work. CI runs both and publishes
what it printed, so these can be checked against another run.

Recorded on a 12th Gen Intel Core i7-12700H, 20 threads, Linux, go1.26.5.
Each table below contains the same workload on a different tmux.

## tmux 3.2a

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   414ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
chained                   124ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.3a

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   439ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
chained                    91ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.4

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   336ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
chained                    70ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.5

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   288ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
chained                    93ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.6

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   226ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 97ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4             125ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    82ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       21ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   253ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                104ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              94ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    61ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       27ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7a

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   271ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                160ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4             125ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    95ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       18ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7b

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   210ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 72ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              90ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    62ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       16ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7c

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   188ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 68ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              79ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    62ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       16ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
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

## What waiting and watching cost

The claims this server makes about not spending a caller's turn, measured
rather than asserted. Reproduce with `scripts/` or the probe in the testing
skill; these were taken on the machine named above, against tmux 3.7b.

| Question | Measured |
| --- | --- |
| Does a long wait block anything else? | a `list_sessions` costs **3.0ms** on a quiet server and **5.7ms** with eight 20-second waits in flight |
| How fast does a pane change reach a subscriber? | **23ms** median from the write to the `notifications/resources/updated` |
| Is a burst coalesced? | 500 lines written in 2.0s produced **1 notification**, not 500 |
| What do twenty more subscribers cost? | **no additional control-mode client** while they watch panes of one session: one connection serves them all, and it closes when the last subscriber goes |
| What does watching across sessions cost? | **one control-mode client per session holding a watched pane**, however many panes that is: tmux reports a pane's output only to a client attached to its session. Six sessions with a watched pane each cost six; four panes in one session cost one |
| Can a call in flight be taken back? | `notifications/cancelled` is answered in **1ms** median, 2ms worst |
| How many calls can be in flight at once? | **64 fired, 64 answered in 0.21s**, every id matched |

The first row is the one to keep an eye on. These tools await throughout, so a
wait holds no connection and blocks no other call; if that number ever starts
tracking the number of waits open, something has started blocking that did not
before.

## Reading it

**The answer column checks equivalence.** Every row builds the same six-pane
topology and runs the same `SearchPanes` query. Only the execution path changes.

**Count invocations, not milliseconds.** Wall clock moves with the machine, the
tmux version, and what else is running. The process column does not: every
version reports 32 for direct subprocess operations and 10 for the plan;
connection rows report zero. Compare those counts against CI before comparing
wall time.

**Part of the process-to-chained gap is work, not batching.** The direct build
materializes a record after each mutation, because that is what its methods
return. A plan reports IDs and statuses instead. A caller who needs a record per
step is choosing the direct API knowingly, and the 32 includes what that costs.

**A control connection is a tmux client.** That is the clients column, and it is
why `Session.OpenControl` is explicit rather than the default: a configuration
keyed on `session_attached` sees it. The rows begin at tmux 3.6 because the
owned connection's terminal daemon binding relies on `no-detach-on-destroy`.

**Chaining pays on processes, less on a connection.** Grouping removes a process
per command, which is the whole win over a tmux process. Over a connection there
is no process to remove and tmux still answers each command in a list
separately, so what a plan is worth there is the forward reference and one round
of results rather than speed.

**Streaming is not a row.** The rows compare ways to perform the same build.
`Session.OpenNotifications` performs no build; it owns one observation client
and reads what tmux says while something else does the work.

**`chained` is one planner of two.** A plan groups its commands through a
`Planner`, and the row above uses the default, which folds every run of
commands that neither answer nor create something. `Sequential` folds nothing,
which is how a failure that grouping made ambiguous gets isolated. Both
produce the same successful results and differ only in invocations;
`TestPlannersAgreeOnResultsAndDifferOnCost` is where that is measured.

**Concurrency is a size, not a speed.** `concurrent x4` uses one owned
`Connection` with four lanes, so four commands can be in flight. The build is a
sequence with little to overlap, so the row moves with machine load. The stable
change is `clients`: every lane is one attached tmux client. Add lanes for
parallel readers, not for a serial workload.
