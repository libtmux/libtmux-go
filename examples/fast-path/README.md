# fast-path

What each way of reaching tmux costs, counted rather than claimed.

The same ten reads run twice — once starting a tmux process per command, once
over a control-mode connection — with the processes counted both times.

The connection path requires tmux 3.6 or newer. The ordinary process path in
the core library retains its tmux 3.2a floor.

## Running it

```console
$ go -C examples run ./fast-path
```

```
over tmux processes: 10 started
over a connection:   0 started
printed capture:     1 started
capture to a file:   0 started
```

The exact counts move between tmux releases. The gap does not.

## What to look at

**One process per ordinary read.** Each read starts tmux, addresses the selected
daemon, and exits after the reply.

**Zero over a connection.** A control-mode connection is already a tmux client.
Commands travel down it, so none of them starts anything.

**A printed capture still starts one.** tmux does not escape a command's output,
and pane content could otherwise end the connection's frame. The terminal
connection refuses to hide that boundary by starting a process, so the example
deliberately uses its original process-bound pane. Staging through a buffer and
a file is safe on the connection, which is the last line: back to zero.

**The connection owns the handle to keep.** `Session.OpenControl` returns an
owner whose `Session` is bound to its command lanes. The session passed in
still starts a process per command, so using the wrong one silently costs
everything the connection saved.

## Testing your own version

```console
$ go -C examples test ./fast-path
```

The test asserts the relationship rather than the numbers, since the numbers are
version-specific and the claim is not.

## Measured, elsewhere

[`BENCHMARKS.md`](../../BENCHMARKS.md) has the same comparison as timings, on
every supported tmux.
