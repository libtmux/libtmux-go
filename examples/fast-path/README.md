# fast-path

What each way of reaching tmux costs, counted rather than claimed.

The same ten reads run twice — once starting a tmux process per command, once
over a control-mode connection — with the processes counted both times.

## Running it

```console
$ go -C examples run ./fast-path
```

```
over tmux processes: 30 started
over a connection:   0 started
printed capture:     1 started
capture to a file:   0 started
```

The exact counts move between tmux releases. The gap does not.

## What to look at

**Thirty, not ten.** Each read is one tmux command, but a session read is
resolved before it runs, so the ordinary path costs more processes than it has
operations.

**Zero over a connection.** A control-mode connection is already a tmux client.
Commands travel down it, so none of them starts anything.

**A printed capture still starts one.** tmux does not escape a command's output,
and pane content could otherwise end the connection's frame, so a capture that
prints goes out of band. Staging it through a buffer and a file avoids that,
which is the last line: back to zero.

**The pool returns the handle to keep.** `OpenControlPool` hands back a session
bound to the connection. The one passed in still starts a process per command,
so using the wrong one silently costs everything the connection saved.

## Testing your own version

```console
$ go -C examples test ./fast-path
```

The test asserts the relationship rather than the numbers, since the numbers are
version-specific and the claim is not.

## Measured, elsewhere

[`BENCHMARKS.md`](../../BENCHMARKS.md) has the same comparison as timings, on
every supported tmux.
