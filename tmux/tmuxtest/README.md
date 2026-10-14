<!-- libtmux-logo -->
<p align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</p>
<!-- /libtmux-logo -->

<div align="center">

# tmuxtest

Run your program inside a real tmux and assert on what it drew.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux/tmuxtest.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest)

</div>

**Contents** — [Testing a program](#testing-a-program) ·
[When a wait fails](#when-a-wait-fails) · [The waits](#the-waits) ·
[Testing without a tmux](#testing-without-a-tmux) ·
[Testing tmux itself](#testing-tmux-itself) ·
[What it guarantees](#what-it-guarantees)

## Testing a program

Run your program in a pane, type at it, and wait for what it draws. The file
below is a whole test. The server, its session, and its pane end with the
test.

<!-- docs:file examples/testing-a-program/greeter_test.go -->

```go
package greeter_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestGreeterAnswersALine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A stand-in for your program: it greets each name it reads.
	greeter := `while read name; do echo "hello, $name"; done`
	pane := tmuxtest.RunInPane(ctx, t, greeter)

	tmuxtest.Type(ctx, t, pane, "world")
	tmuxtest.WaitForLine(ctx, t, pane, "hello, world")
}
```

<!-- docs:end -->

To run it, save it as `greeter_test.go` in an empty directory, with tmux 3.2a
or newer on `PATH`:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/tmux/tmuxtest@latest && \
    go test
```

The pane echoes what `Type` sends, so the wait names the greeter's answer,
`hello, world`, which the typed `world` does not contain. Waiting for the text
you typed would pass without the program running.

`TestMain` is required: `tmuxtest.Main` cleans up before returning the exit
code, and verifies each server's process died before removing its socket. The
test holds no sleeps: each wait polls the screen until it holds, so a quick
program costs milliseconds and a slow one is still waited for.

## When a wait fails

The failure carries the screen the pane last held, which is what you would
have added a print statement to see. This is a real one, from
`WaitForText(ctx, t, pane, "ready")` on a program that never got that far:

<!-- docs:output tmux/tmuxtest/testdata/wait-failure.txt -->

```text
tmuxtest: pane %1 never showed a line containing "ready"
the pane showed 3 line(s):
    | tmuxtest$ printf 'loading widgets\nconnecting\n'; cat
    | loading widgets
    | connecting
```

<!-- docs:end -->

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

## Testing without a tmux

Where the subject is your own code rather than tmux's behaviour,
`tmuxtest.ScriptedTmux` writes an executable answering the invocations you
name and returns its path for `ServerOptions.Binary`. The test then needs no
tmux installed:

<!-- docs:scripted-tmux -->

```go
// Given: ctx context.Context; t *testing.T
binary := tmuxtest.ScriptedTmux(t,
	tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
	tmuxtest.ScriptedCommand{
		Contains: []string{"kill-pane"},
		Stderr:   "can't find pane: %7\n",
		ExitCode: 1,
	},
	tmuxtest.ScriptedCommand{Contains: []string{"list-sessions"}, Stdout: "$5\n"},
)
server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
if err != nil {
	t.Fatal(err)
}

// The point of scripting a failure is that a caller can assert on how
// their own code classifies it.
result, err := server.Cmd(ctx, "kill-pane", "-t", "%7")
if err != nil {
	t.Fatalf("Cmd() error = %v, want a completed failure", err)
}
if result.ExitCode != 1 {
	t.Errorf("ExitCode = %d, want 1", result.ExitCode)
}
```

<!-- docs:end -->

`Contains` matches whole arguments, so `kill-pane` answers that subcommand
whatever flags surround it. It answers commands only: a control connection or
a notification stream speaks tmux's own protocol, which a script cannot, so
code opening one needs `NewServer` below and a real tmux.

## Testing tmux itself

`tmuxtest.NewServer` returns a
[`tmux.Server`](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux#Server)
for a test whose subject is tmux rather than a program running inside it. It
reports construction and startup failures through `t.Fatal`.

### One server per test

<!-- docs:new-server -->

```go
// Given: ctx context.Context; t *testing.T
server := tmuxtest.NewServer(ctx, t)

session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "under-test"})
if err != nil {
	t.Fatal(err)
}
if name, _ := session.Name(); name != "under-test" {
	t.Errorf("session name = %q, want under-test", name)
}
```

<!-- docs:end -->

The server dies with the test. `TestMain` with `tmuxtest.Main`, as in the test
above, is required here too.

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

## Keeping your suite to itself

Sibling checkouts running their own suites on the same machine will fight over
tmux sockets. Give yours a namespace:

```console
$ export TMUX_TMPDIR=/tmp/my-project-test && \
    mkdir -p "$TMUX_TMPDIR" && \
    unset TMUX TMUX_PANE
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
