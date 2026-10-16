# fast-path

The same reads made two ways: through a plain server, which starts a tmux
process for each command, and through a connection, one tmux client that
carries them all. The example then shows which exact-capture operations each
path supports.

Both paths support tmux 3.2a through 3.8-rc2.

## Running it

```console
$ go -C examples run ./fast-path
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/fast-path/output.txt -->

```text
process path: 10 searches
connection path: 10 searches
printed capture: process path
file capture: connection path
```

<!-- docs:end -->

## Opening a connection

A connection carries commands without starting a process for each. It appears
in `list-clients` and counts toward `session_attached`, which is why opening
one is explicit:

<!-- docs:control-pool -->

```go
// Given: ctx context.Context; session tmux.Session
connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
if err != nil {
	return fmt.Errorf("open control connection: %w", err)
}
defer func() { _ = connection.Close() }()
connected := connection.Session()
```

<!-- docs:end -->

Once established, `connection.Server()` and `connection.Session()` are bound
to that exact daemon, and values derived from them keep that owner. The
binding is terminal: closing the connection makes later operations return
`ErrControlClosed`, and an operation that needs a separate process returns
`ErrConnectionRequiresProcess`. It never falls back or rebinds. The session
the connection was opened from stays on its process path.

`Server.NewSessionConnection` creates a session and keeps its creating control
process as the first lane, for when no session exists yet.

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

## Running its test

```console
$ go -C examples test ./fast-path
```

The test runs every path against a real tmux and checks the output that each
completed operation produces.

## Measured, elsewhere

[`BENCHMARKS.md`](../../BENCHMARKS.md) counts process starts and records timings
for every supported tmux.
