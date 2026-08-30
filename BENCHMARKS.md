# What each execution path costs

Building one six-pane window through every path compatible with the running
tmux. Every supported release has process, one-lane connection, four-lane
connection, chained, and chained connection rows. Regenerate with:

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
process                   177ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 48ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              53ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    54ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       14ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.3a

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   191ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 57ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              67ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    59ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       11ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.4

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   187ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 66ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              63ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    55ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       12ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.5

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   202ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 71ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              76ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    72ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       18ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.6

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   183ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 61ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              67ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    59ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       14ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   200ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 69ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              76ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    56ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       18ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7a

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   184ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 66ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              74ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    72ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       11ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7b

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   197ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 58ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              66ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    55ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       14ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
```

## tmux 3.7c

```
path                       wall   processes  clients  query answer
----------------------------------------------------------------------------------------
process                   195ms          32        0  7 panes on the server [0 0 1 2 3 4 5]
connection                 72ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
concurrent x4              70ms           0        4  7 panes on the server [0 0 1 2 3 4 5]
chained                    69ms          10        0  7 panes on the server [0 0 1 2 3 4 5]
chained + connection       13ms           0        1  7 panes on the server [0 0 1 2 3 4 5]
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
runs on an idle laptop and a busy one, while their allocation counts move far
less. Publishing the times per release would be recording the load rather than
the server. Compare one revision against another on one machine, and watch the
allocations.

The table is the median of three 60-iteration samples on the machine named
above, capped to 10 runtime threads, on Linux with Go 1.26.7 and tmux 3.7c.
These calls reuse MCP's provenance-bound control connection instead of starting
a tmux process for each command. The control protocol allocates more Go objects
than the older subprocess-backed MCP path; the execution-path tables above show
the process reduction that trade buys.

| Call | allocs/op |
| --- | --- |
| `list_sessions` | 2,379 |
| `get_server_info` | 2,660 |
| `list_panes` | 2,796 |
| `list_panes` with `detail: full` | 3,026 |
| `display_message` | 6,539 |
| `capture_pane` | 7,404 |

Two of these measure a claim rather than a cost.

**A batch saves framing, not server work.** Three listings batched and the same
three sent one at a time run the same tmux commands. The batch allocates 1.59MB
against 2.25MB and, over a real pipe rather than the in-memory transport these
run on, spends one round trip rather than three. Its reason to exist is the
caller's turn, not the server's CPU.

**`capture_since` has a break-even.** It reads the pane and then fingerprints
the rows to mint a cursor, so it costs more than a plain capture and returns a
cursor in every reply. On an 80x24 pane of short lines that is about 373 bytes
against 173, and `capture_pane` wins on both counts. It earns its place on a
wide pane holding full lines, read repeatedly -- which is what it is for, and
is worth knowing is not every pane.

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
keyed on `session_attached` sees it. On tmux before 3.6, destroying the attached
session follows that session's `detach-on-destroy` policy and may end the
connection. That lifecycle difference does not change the control protocol or
the rows measured here.

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
