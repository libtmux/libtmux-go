# fast-path

The same ten reads run through a plain server and an owned connection. The
example then shows which exact-capture operations each path supports.

The connection path requires tmux 3.6 or newer. The ordinary process path in
the core library retains its tmux 3.2a floor.

## Running it

```console
$ go -C examples run ./fast-path
```

```
process path: 10 searches
connection path: 10 searches
printed capture: process path
file capture: connection path
```

## What to look at

**The plain server keeps its process path.** Each read uses the executable,
environment, working directory, and socket selection frozen by `NewServer`.

**The connection keeps its own path.** `Session.OpenControl` returns an owner
whose session is bound to its command lanes and exact daemon. The original
session remains process-bound.

**Printed capture stays on the process path.** Pane content can contain control
protocol delimiters. A terminal connection refuses `Pane.Capture` instead of
starting a hidden process. `Pane.CaptureToFile` stages the same capture through
a tmux buffer and remains safe on the connection.

## Testing your own version

```console
$ go -C examples test ./fast-path
```

The test runs every path against a real tmux and checks the output that each
completed operation produces.

## Measured, elsewhere

[`BENCHMARKS.md`](../../BENCHMARKS.md) counts process starts and records timings
for every supported tmux.
