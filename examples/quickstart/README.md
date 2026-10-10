# Quick start

This complete program imports libtmux, opens the normal tmux endpoint, and finds
or creates the `libtmux-go-quickstart` session and its `logs` window. It leaves
them available after it exits. Running it again reuses both objects.

## Running it

```console
$ go -C examples run ./quickstart
```

```text
workspace ready: logs
```

`Server.Ensure` starts tmux only when the selected endpoint has no daemon. New
startup loads your tmux configuration, uses a temporary detached session running
`cat`, sets `exit-empty` off, and removes the temporary session. Session hooks can
observe that startup session. A running daemon keeps its configuration and
existing objects. Ensuring availability does not give the caller a cleanup owner.

If configuration removes the private startup marker, `Ensure` leaves the
unclassified daemon's options unchanged. It retains its detached startup session
when that is the only session and `exit-empty` is enabled. Otherwise it removes
the startup session by its stable ID, including when a hook renamed it.

`FindOrCreateSession` and `FindOrCreateWindow` return ordinary handles in `Value`.
The example leaves newly created objects alive. Their optional `Owner` is useful
when cleanup is the purpose of your program; the [session cleanup
example](../session-cleanup/) demonstrates that separate behavior and pane I/O.

## Testing the displayed program

The root README includes this source with all imports. `example_test.go` checks
that the displayed block matches the program. The external runner builds each
source unchanged, supplies child-only socket defaults, and executes both twice
against absent and seeded running daemons. It owns final cleanup.

```console
$ python3 scripts/test_ordinary_examples.py \
    --runner /path/to/libtmux-docs/scripts/example_environment.py \
    --output /tmp/go-ordinary-results
```

Run that command from the repository root. `--tmux` selects a cached tmux build.
The runner requires Linux with pidfd support and a Go toolchain on `PATH`; it
performs no dependency installation. The example itself contains no test socket,
fixture, temporary directory or cleanup scope.
