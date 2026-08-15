# tmuxtest

[![Go Reference](https://img.shields.io/static/v1?label=godoc&message=reference&color=blue)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest)

A real tmux server for your tests, on its own socket, killed when the test ends.

```go
import "github.com/libtmux/libtmux-go/tmux/tmuxtest"
```

## One server per test

```go
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestPaneReceivesKeys(t *testing.T) {
	ctx := context.Background()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// ... drive tmux; the server dies with the test
}
```

`TestMain` is required: `tmuxtest.Main` performs cleanup and restoration before
returning the exit code, and verifies each server's process actually died before
removing its socket.

## What it guarantees

- **An explicit `-S` socket per test.** No test reaches a server another test
  owns, and none reaches yours.
- **A generated minimal config.** Your `~/.tmux.conf` cannot change a result — no
  inherited `base-index`, no prompt in captured pane text, no hooks firing.
- **Inherited state dropped.** `TMUX`, `TMUX_PANE`, and `TMUX_TMPDIR` are removed
  from every tmux child, so a suite run from inside a pane stays away from the
  server hosting it.
- **Verified teardown.** Cleanup probes the daemon answering on the socket and
  confirms *that* daemon died, rather than trusting a PID recorded at startup
  which may have been reused.

## Testing without tmux installed

`ServerOptions.Runner` replaces process execution entirely, so code that drives
tmux can be unit tested on a machine that has none:

```go
server := tmux.NewServer(tmux.ServerOptions{
	Runner: tmux.CommandRunnerFunc(func(
		ctx context.Context, request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		return tmux.CommandResult{Stdout: []string{"$1"}}, nil
	}),
})
```

## Keeping your suite to itself

Sibling checkouts running their own suites on the same machine will fight over
tmux sockets. Give yours a namespace:

```console
$ export TMUX_TMPDIR=/tmp/my-project-test && mkdir -p "$TMUX_TMPDIR" && unset TMUX TMUX_PANE
```

Some real-tmux tests are load-sensitive. Three signs a failure is the machine
rather than the code: the failing test moves between runs, the failure reads as
`no server running` or `WaitDelay expired` rather than a wrong value, and the
file it is in is not one your change touched.

## See also

| | |
| --- | --- |
| [`../`](../) | The library under test |
| [`../../examples/`](../../examples/) | Runnable programs |
