# tmuxtest

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux/tmuxtest.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest)

Run your program inside a real tmux and assert on what it drew.

```go
import "github.com/libtmux/libtmux-go/tmux/tmuxtest"
```

**Contents** — [Testing a program](#testing-a-program) ·
[When a wait fails](#when-a-wait-fails) · [The waits](#the-waits) ·
[Testing tmux itself](#testing-tmux-itself) ·
[What it guarantees](#what-it-guarantees)

## Testing a program

Run it, wait for what it draws, type at it. The server, its session, and its
pane end with the test:

<!-- docs:tmuxtest-quickstart -->

```go
pane := tmuxtest.RunInPane(ctx, t, "printf 'ready\\n'; cat")

tmuxtest.WaitForText(ctx, t, pane, "ready")
tmuxtest.Type(ctx, t, pane, "a line for the program")
tmuxtest.WaitForLine(ctx, t, pane, "a line for the program")
```

<!-- docs:end -->

No sleeps: `WaitForText` polls, so a quick program costs milliseconds and a slow
one is still waited for. That block is generated from a test in this package, so
it cannot describe something the package does not do.

## When a wait fails

The failure carries the screen, which is what you would have added a print
statement to see:

```
tmuxtest: pane %1 never showed a line containing "ready"
the pane showed 3 line(s):
    | tmuxtest$ ./mytui --watch
    | loading widgets
    | connecting
```

## The waits

| Wait | Holds when |
| --- | --- |
| `WaitForText` | some line contains a string |
| `WaitForLine` | some line is exactly a string |
| `WaitForScreen` | a condition of your own returns true |
| `WaitForShellReady` | the pane's shell will accept typing |
| `TypeAndWait` | a command you typed has finished, pass or fail |

`Screen` reads the pane without waiting. All of them read the visible screen
rather than the scrollback, so what they search is what a failure prints.

`RunInPane` gives its panes a POSIX shell with no start-up files and the prompt
`tmuxtest$ `, so a pane shows the same thing on every machine rather than
whatever prompt the person running the tests has configured. Ask for it on a
server you build yourself with `ServerOptions.FixedShell`.

## Testing tmux itself

`NewServer` returns a [`tmux.Server`](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux#Server)
for a test whose subject is tmux rather than a program running inside it.

### One server per test

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
