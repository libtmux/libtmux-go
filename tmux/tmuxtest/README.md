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

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/tmux/tmuxtest@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

**Contents** — [Testing a program](#testing-a-program) ·
[The waits](#the-waits) · [Testing without a tmux](#testing-without-a-tmux) ·
[Testing tmux itself](#testing-tmux-itself) ·
[What it guarantees](#what-it-guarantees) ·
[Keeping your suite to itself](#keeping-your-suite-to-itself) ·
[See also](#see-also)

## Testing a program

### [`tmuxtest.RunInPane`][tmuxtest-runinpane]: run your program in a real pane

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

It prints `ok` with the time the test took, or a failure that shows the
screen, as below.

The test holds no sleeps. tmux tells each wait when the pane writes, and the
wait reads the screen then, so a quick program costs milliseconds and a slow
one is still waited for.

### [`tmuxtest.WaitForLine`][tmuxtest-waitforline]: wait for the answer, not the echo

A terminal shows what is typed into it, so the pane holds the `world` that
[`tmuxtest.Type`][tmuxtest-type] sent before the greeter has read it. The wait
names the greeter's answer, `hello, world`, which the typed text does not
contain. Waiting for the text you typed would pass without the program
running.

### [`tmuxtest.Main`][tmuxtest-main]: clean up after the suite

`TestMain` is required. `Main` runs the tests, then stops every server they
started and checks that each server's process died before removing its
socket, and only then hands back the exit code.

### [`tmuxtest.WaitForText`][tmuxtest-waitfortext]: a failed wait shows the screen

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
| [`WaitForText`][tmuxtest-waitfortext] | some line contains a string |
| [`WaitForLine`][tmuxtest-waitforline] | some line is exactly a string |
| [`WaitForScreen`][tmuxtest-waitforscreen] | a condition of your own returns true |
| [`WaitForShellReady`][tmuxtest-waitforshellready] | the pane's shell will accept typing |
| [`TypeAndWait`][tmuxtest-typeandwait] | a command you typed has finished, pass or fail |

[`Screen`][tmuxtest-screen] reads the pane without waiting. All of them read
the visible screen rather than the scrollback, so what they search is what a
failure prints.

`RunInPane` gives its panes a POSIX shell that reads no start-up files and
shows the prompt `tmuxtest$ `, so a pane looks the same on every machine
whatever prompt the person running the tests has configured. Ask for that
shell on a server you build yourself with
[`tmuxtest.ServerOptions.FixedShell`][tmuxtest-serveroptions-fixedshell].

## Testing without a tmux

A block that starts with `// Given:` is the body of a test, with the values
that line names already in hand.

### [`tmuxtest.ScriptedTmux`][tmuxtest-scriptedtmux]: answer the commands you name

Where the subject is your own code rather than tmux's behaviour,
`ScriptedTmux` writes an executable that answers the invocations you name,
and returns its path for
[`tmux.ServerOptions.Binary`][tmux-serveroptions-binary].
The test then needs no tmux installed:

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

[`Contains`][tmuxtest-scriptedcommand-contains] matches whole arguments, so
`kill-pane` answers that subcommand whatever flags surround it. A script
answers commands only. A control connection or a notification stream speaks
tmux's own protocol, which a script cannot, so code that opens one needs a
real tmux and the server below.

## Testing tmux itself

### [`tmuxtest.NewServer`][tmuxtest-newserver]: a server for one test

It returns a [`tmux.Server`][tmux-server] for a test whose subject is tmux
rather than a program running inside it. A server that cannot be configured or
started fails the test through `t.Fatal`, so there is no error to check.

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

[`tmuxtest.NewSession`][tmuxtest-newsession] and
[`tmuxtest.NewWindow`][tmuxtest-newwindow] make a session or a window on a
server you already hold, and remove it when the test ends.

## What it guarantees

- **A socket of its own per test.** Each server is reached by an explicit
  socket path, so no test reaches a server another test owns, and none
  reaches yours.
- **A generated minimal config.** Your `~/.tmux.conf` cannot change a result:
  no inherited `base-index`, no prompt in captured pane text, no hooks firing.
- **Inherited state dropped.** `TMUX`, `TMUX_PANE`, and `TMUX_TMPDIR` are
  removed from the environment of each tmux the harness starts, which it
  points at a socket path instead, so a suite run from inside a pane stays
  away from the server hosting it.
- **Verified teardown.** Cleanup asks the server answering on the socket for
  its process and confirms *that* process died, rather than trusting a PID
  recorded at startup, which may have been reused.

## Keeping your suite to itself

There is nothing to set up. `tmuxtest.Main` gives each run of a suite a
temporary directory of its own and points the test process's `TMPDIR`,
`GOTMPDIR`, and `TMUX_TMPDIR` at it until the suite ends. Two checkouts
running their suites on one machine then share no socket, and neither shares
one with a tmux you are using. The directory's path is kept short, because a
socket path may hold only about a hundred bytes.

## See also

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, method, and field, one page each |
| [`../`](../) | The library under test |
| [`../../examples/testing-a-program`](../../examples/testing-a-program) | The test above, as a directory you can run |
| [`../../examples/`](../../examples/) | Runnable programs |

[tmux-server]: https://libtmux.org/en/go/latest/reference/tmux-server/
[tmux-serveroptions-binary]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-binary/
[tmuxtest-main]: https://libtmux.org/en/go/latest/reference/tmuxtest-main/
[tmuxtest-newserver]: https://libtmux.org/en/go/latest/reference/tmuxtest-newserver/
[tmuxtest-newsession]: https://libtmux.org/en/go/latest/reference/tmuxtest-newsession/
[tmuxtest-newwindow]: https://libtmux.org/en/go/latest/reference/tmuxtest-newwindow/
[tmuxtest-runinpane]: https://libtmux.org/en/go/latest/reference/tmuxtest-runinpane/
[tmuxtest-screen]: https://libtmux.org/en/go/latest/reference/tmuxtest-screen/
[tmuxtest-scriptedcommand-contains]: https://libtmux.org/en/go/latest/reference/tmuxtest-scriptedcommand-contains/
[tmuxtest-scriptedtmux]: https://libtmux.org/en/go/latest/reference/tmuxtest-scriptedtmux/
[tmuxtest-serveroptions-fixedshell]: https://libtmux.org/en/go/latest/reference/tmuxtest-serveroptions-fixedshell/
[tmuxtest-type]: https://libtmux.org/en/go/latest/reference/tmuxtest-type/
[tmuxtest-typeandwait]: https://libtmux.org/en/go/latest/reference/tmuxtest-typeandwait/
[tmuxtest-waitforline]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitforline/
[tmuxtest-waitforscreen]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitforscreen/
[tmuxtest-waitforshellready]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitforshellready/
[tmuxtest-waitfortext]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitfortext/
