<!-- libtmux-logo -->
<p align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</p>
<!-- /libtmux-logo -->

<div align="center">

# libtmux for Go

Drive tmux from Go: sessions, windows, and panes as typed values, every tmux
option and hook as a typed accessor, and errors classified by what tmux actually
refused.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)
[![tests](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml/badge.svg)](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml)

</div>

Run a command in a real terminal and read what it printed, find and filter
what a tmux server holds, type into a pane and wait for its answer, and hear
about changes as tmux makes them. It suits tools that drive a terminal
program, test suites that need a real tmux, and agents that share a terminal.

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

It needs Go 1.26 or newer and tmux 3.3a or newer, on a Unix host. Ordinary
operations check the selected daemon before dispatch and return
`tmux.ErrVersionTooLow` for older releases, including unpatched 3.3. A newer client executable cannot
admit an older daemon. With no daemon, the executable's version admits startup;
a native precondition refuses a different version appearing before dispatch.
`tmux.NewServer` still validates configuration without starting a process.
`Server.Version` on an unbound handle and standalone `Cmd` calls with `-V` or
`-h` diagnose the executable; they do not admit later daemon operations.
Unversioned OpenBSD base tmux and bare `master` reports cannot establish this
release floor and are refused. Capability probes remain diagnostic.

A tmux server for AI agents and a tmuxp-style workspace builder ship beside the
library, under [Packages](#packages).

**Contents** — [Quick start](#quick-start) ·
[Find things](#find-sessions-windows-and-panes) ·
[Type and wait](#type-into-a-pane-and-wait) ·
[Watch tmux](#watch-tmux-change) ·
[Choosing a path](#choosing-how-to-reach-tmux) ·
[Testing](#testing-your-own-code) · [Packages](#packages) ·
[Leaves out](#what-it-leaves-out) · [Support](#support-and-status) ·
[Documentation](#documentation)

## Quick start

Run a command in tmux and print what its terminal showed. This is the whole
program:

<!-- docs:file examples/quickstart/main.go -->

```go
// Command quickstart runs a command in tmux and prints what it showed.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-quickstart",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	// The command runs in a window of its own, with a terminal.
	result, err := session.Run(ctx, "echo hello from tmux", tmux.RunOptions{})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	for _, line := range result.Lines {
		fmt.Println(line)
	}
	fmt.Println("exit status:", result.Status)
	return nil
}
```

<!-- docs:end -->

It prints:

<!-- docs:output examples/quickstart/output.txt -->

```text
hello from tmux
exit status: 0
```

<!-- docs:end -->

To run it, save it as `main.go` in an empty directory, with tmux 3.3a or newer
on `PATH`:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/tmux@latest && \
    go run .
```

The program starts a tmux server on a socket and config of its own and kills
it on the way out, so it leaves nothing behind and never touches a tmux you
have open. Drop [`ServerOptions`][tmux-serveroptions] to its zero value and it
drives the same server your `tmux` command does.

[`Session.Run`][tmux-session-run] is `os/exec` for a program that needs a
terminal: the command runs in a window of its own with a tty, and what comes
back is its exit status and the screen tmux rendered. A nonzero status is a
result, not an error.
[`examples/run-to-completion`](examples/run-to-completion) shows one, on a
tmux server of its own.

Every Go block on this page comes from a program or test that runs against a
real tmux on every supported release, and each output shown is what that run
printed.

## Find sessions, windows, and panes

### [`Server.Sessions`][tmux-server-sessions]: what tmux has now

One call asks tmux for every session. [`Server.Windows`][tmux-server-windows]
and [`Server.Panes`][tmux-server-panes] are its siblings:

<!-- docs:list-sessions -->

```go
// Given: ctx context.Context; server tmux.Server
sessions, err := server.Sessions(ctx)
if err != nil {
	return fmt.Errorf("list sessions: %w", err)
}
for _, session := range sessions {
	name, _ := session.Name()
	fmt.Println("session:", name)
}
```

<!-- docs:end -->

Each block on this page is the body of a function that returns an error, with
the values its first line names already in hand.

[`examples/filter-query`](examples/filter-query) runs this and the two blocks
below on a tmux server of its own. Like every example, it runs from a clone
of the repository, with `go -C examples run ./filter-query`;
[`examples/README.md`](examples/README.md) has the commands to clone it.

<!-- docs:checked examples/filter-query/output.txt -->
This one prints `session: libtmux-filter`.
<!-- docs:end -->

What comes back is a record: a `Session` you hold is what tmux said when you
asked, not a live handle that changes underneath you. When no tmux server is
running, a listing returns an error matching [`ErrNoServer`][tmux-errnoserver]
rather than an empty list, so "nothing there" and "nobody to ask" stay
different answers.

### [`tmuxq.Matching`][tmuxq-matching]: several answers from one read

Read the server once with [`Server.Snapshot`][tmux-server-snapshot] and filter
that read in Go. `Matching` takes any filter the `tmux` package declares:

<!-- docs:query-in-go -->

```go
// Given: ctx context.Context; server tmux.Server
snapshot, err := server.Snapshot(ctx)
if err != nil {
	return err
}
active, err := tmuxq.Matching(snapshot.Panes(), tmux.PaneActiveIs(true))
if err != nil {
	return err
}
fmt.Println("active panes:", len(active))
```

<!-- docs:end -->

<!-- docs:checked examples/filter-query/output.txt -->
That session has one window with one pane in it, which is the active one, so
the example goes on to print `active panes: 1`.
<!-- docs:end -->

### [`Server.SearchSessions`][tmux-server-searchsessions]: let tmux filter

One command goes out and only the matches come back:

<!-- docs:query-in-tmux -->

```go
// Given: ctx context.Context; server tmux.Server
live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
sessions, err := server.SearchSessions(ctx, &live)
if err != nil {
	return err
}
fmt.Println("live matches:", len(sessions))
```

<!-- docs:end -->

<!-- docs:checked examples/filter-query/output.txt -->
One session has that name, so this prints `live matches: 1`.
<!-- docs:end -->

The filter is a tmux format, written the way the FORMATS section of the tmux
manual describes, and tmux evaluates it while it lists. [`tmuxq/`](tmuxq/) has
the filters that run in Go and the helpers that apply them.

## Type into a pane and wait

### [`PaneObservation.WaitFor`][tmux-paneobservation-waitfor]: wait for what a command prints

A pane is an `io.Writer` through [`Pane.Writer`][tmux-pane-writer], and a
newline is the Enter key. Open an observation with
[`Pane.OpenObservation`][tmux-pane-openobservation] before typing, then wait
for text the command prints:

<!-- docs:pane-wait -->

```go
// Given: ctx context.Context; pane tmux.Pane
// Open the observation before typing, so no output is missed.
output, err := pane.OpenObservation(ctx)
if err != nil {
	return fmt.Errorf("observe pane: %w", err)
}
defer func() { err = errors.Join(err, output.Close()) }()

// A pane is an io.Writer, and a newline is the Enter key.
command := `echo "6 x 7 = $((6 * 7))"`
if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
	return fmt.Errorf("type command: %w", err)
}
// The terminal echoes what is typed, so wait for the answer: the typed
// line says "$((6 * 7))" and never "42".
if _, err := output.WaitFor(ctx, func(text string) bool {
	return strings.Contains(text, "6 x 7 = 42")
}); err != nil {
	return fmt.Errorf("wait for answer: %w", err)
}
fmt.Println("the pane answered 6 x 7 = 42")
```

<!-- docs:end -->

tmux pushes each write as it happens, so nothing polls. The terminal echoes
what is typed, and that echo is output too, which is why the wait names the
answer rather than the request.

Runnable: [`examples/pane-io`](examples/pane-io), which goes on to read the
pane as an `io.Reader`.

Typing is the wrong way to move a payload: the shell in the pane reads quotes,
`$`, and backslashes in it as shell syntax.
[`Server.LoadBufferFrom`][tmux-server-loadbufferfrom] fills a tmux buffer from
an `io.Reader` instead, and [`Pane.CaptureTo`][tmux-pane-captureto] writes a
scrollback to an `io.Writer`. [`examples/byte-streams`](examples/byte-streams)
does both.

## Watch tmux change

### [`Session.OpenNotifications`][tmux-session-opennotifications]: hear changes as they happen

It returns a stream of what tmux does, such as a window opening or a session
being renamed. tmux pushes each change when it happens, so nothing has to
guess how often to ask:

<!-- docs:watching -->

```go
// Given: ctx context.Context; session tmux.Session
stream, err := session.OpenNotifications(ctx, tmux.NotificationOptions{})
if err != nil {
	return fmt.Errorf("open notification stream: %w", err)
}
defer func() { err = errors.Join(err, stream.Close()) }()

// Rename after subscribing; notifications do not include earlier changes.
if _, err := session.Rename(ctx, "control-example"); err != nil {
	return fmt.Errorf("rename session: %w", err)
}

for {
	notification, err := stream.Next(ctx)
	if err != nil {
		return fmt.Errorf("read notification: %w", err)
	}
	fmt.Printf("notification: %s\n", notification.Kind())
	if notification.Kind() == tmux.ControlNotificationSessionRenamed {
		fmt.Println("heard the rename")
		break
	}
}
```

<!-- docs:end -->

The first notification a new stream reads is `%session-changed`, which is
tmux telling the client which session it joined. The rename follows it.

<!-- docs:checked examples/control-mode-subscribe/output.txt -->
This prints `notification: %session-changed`, then
`notification: %session-renamed`, then `heard the rename`.
<!-- docs:end -->

### [`NotificationStream.Subscribe`][tmux-notificationstream-subscribe]: have tmux watch a value

A subscription goes further: tmux evaluates a format for you and reports it
each time its value changes, so a program hears about a window count or a
pane's current command without asking again:

<!-- docs:subscribing -->

```go
// Given: ctx context.Context; session tmux.Session;
// stream *tmux.NotificationStream
// A subscription is a format tmux evaluates for you: it reports the value
// when it first looks, about a second later, and then each time it changes.
if err := stream.Subscribe(ctx, tmux.SubscriptionRequest{
	Name: "windows", Format: "#{session_windows}",
}); err != nil {
	return fmt.Errorf("subscribe: %w", err)
}
if _, err := session.NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
	return fmt.Errorf("open window: %w", err)
}
for {
	notification, err := stream.Next(ctx)
	if err != nil {
		return fmt.Errorf("read notification: %w", err)
	}
	if change, ok := notification.Subscription(); ok && change.Value == "2" {
		fmt.Println("session has", change.Value, "windows")
		return nil
	}
}
```

<!-- docs:end -->

<!-- docs:checked examples/control-mode-subscribe/output.txt -->
The session started with one window, so this prints `session has 2 windows`.
<!-- docs:end -->

Runnable: [`examples/control-mode-subscribe`](examples/control-mode-subscribe)
is the program both blocks come from.

## Choosing how to reach tmux

Start with a plain `Server`, which starts one tmux process per command. The
other paths keep a tmux client attached, or send several commands at once, to
start fewer:

| Path | Construct it with | Cost | Reach for it |
| --- | --- | --- | --- |
| a process per command | [`tmux.NewServer`][tmux-newserver] | a tmux process per command sent | one-shot commands |
| a connection | [`Session.OpenControl`][tmux-session-opencontrol] | one tmux client, kept attached | repeated commands |
| several connections | [`ConnectionOptions{Lanes: N}`][tmux-connectionoptions-lanes] | N tmux clients, kept attached | readers running in parallel |
| a plan | [`tmux.NewPlan`][tmux-newplan] then [`Plan.Run`][tmux-plan-run] | fewer process starts | builds and layouts |
| a stream | [`Session.OpenNotifications`][tmux-session-opennotifications] | one tmux client, kept attached | watching what tmux does |

### [`tmux.NewPlan`][tmux-newplan]: write a layout in one pass

A plan records commands instead of running them, sends the ones needing no
answer together, and hands back a reference to what a step will create.
`new("echo built")` is how Go 1.26 writes a pointer to a value, for a field
that may be left unset:

<!-- docs:planning -->

```go
// Given: window tmux.Window
plan := tmux.NewPlan()
plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
editor := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
plan.SetPaneTitle(editor, "editor")
plan.SendKeys(editor, tmux.SendKeysRequest{Command: new("echo built")})
plan.DisplayMessage(editor, "#{pane_title}")
```

<!-- docs:end -->

[`Plan.Run`][tmux-plan-run] sends it, and reports on each step by itself even
where several shared one tmux process:

<!-- docs:planning-run -->

```go
// Given: ctx context.Context; server tmux.Server; plan *tmux.Plan
result, err := plan.Run(ctx, server)
if err != nil {
	return fmt.Errorf("run plan: %w", err)
}
if !result.OK() {
	return fmt.Errorf("plan did not complete: %w", result.Err())
}

// Results remain per operation even when operations share an invocation.
for index, op := range result.Ops {
	fmt.Printf("step %d %-16s %-8s created %q stdout %q\n",
		index, op.Command, op.Status, op.Created, op.Stdout)
}
```

<!-- docs:end -->

[`examples/planned-build`](examples/planned-build) is the program both blocks
come from.

<!-- docs:checked examples/planned-build/output.txt -->
It prints `operations: 6, tmux invocations: 4` before it runs the plan: six
tmux commands, in four process starts rather than six. The last step reads
back the title the plan set, so its line ends `stdout ["editor"]`.
<!-- docs:end -->

A connection appears in `list-clients` and counts toward `session_attached`,
which is why opening one is explicit. "Execution modes" in the package
documentation has the rules each path follows, and
[`BENCHMARKS.md`](BENCHMARKS.md) is what each costs, measured on every
supported tmux. [`examples/fast-path`](examples/fast-path) runs the same reads
down each path.

## Testing your own code

### [`tmuxtest.RunInPane`][tmuxtest-runinpane]: run your program in a real pane

[`tmux/tmuxtest`](tmux/tmuxtest/) runs your program inside a real tmux and
lets a test assert on what it drew, without a sleep in it. This is a whole
test file:

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

### [`tmuxtest.WaitForText`][tmuxtest-waitfortext]: a failed wait shows the screen

A wait that runs out fails with the screen the pane last held, which is what
you would have added a print statement to see. This one was waiting for
`ready`:

<!-- docs:output tmux/tmuxtest/testdata/wait-failure.txt -->

```text
tmuxtest: pane %1 never showed a line containing "ready"
the pane showed 3 line(s):
    | tmuxtest$ printf 'loading widgets\nconnecting\n'; cat
    | loading widgets
    | connecting
```

<!-- docs:end -->

### More in `tmuxtest`

[`tmuxtest.NewServer`][tmuxtest-newserver] gives a test whose subject is tmux
itself a server on a socket of its own, killed when the test ends.
[`tmuxtest.ScriptedTmux`][tmuxtest-scriptedtmux] answers the tmux commands you
name instead, so a test of your own code needs no tmux installed.
[`tmux/tmuxtest`](tmux/tmuxtest/) shows both.

## Packages

| Package | Source | Reference | What it is |
| --- | --- | --- | --- |
| `tmux` | [`tmux/`](tmux/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux) | The library. Sessions, windows, panes, options, hooks, formats, filters, snapshots, plans. |
| `tmuxtest` | [`tmux/tmuxtest/`](tmux/tmuxtest/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest) | Run your program in a real tmux and assert on what it drew. |
| `tmuxq` | [`tmuxq/`](tmuxq/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq) | Model-free generic helpers for slices and `iter.Seq`. |

Others ship as **separate modules**, so `go get` on the library pulls in none
of them:

| Module | What it is |
| --- | --- |
| [`mcp/`](mcp/) | A tmux server for AI agents, over the Model Context Protocol |
| [`workspace/`](workspace/) | Loads tmuxp-style YAML workspaces and builds them, with a CLI |
| [`benchmarks/`](benchmarks/) | Prints what each way of reaching tmux costs |

The MCP server:

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

The workspace CLI:

```console
$ go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest
```

## What it leaves out

- **Live objects.** A [`Session`][tmux-session] or a [`Pane`][tmux-pane] is a
  record of what tmux reported when it was read, and does not change when
  tmux does. Read again, or subscribe, to see a change.
- **A terminal emulator.** A capture and [`Session.Run`][tmux-session-run]
  return the screen tmux drew. [`PaneObservation`][tmux-paneobservation]
  returns the bytes a program wrote.
- **Your tmux configuration file.** Options and hooks are read and set on a
  running server, and nothing here edits `~/.tmux.conf`. tmux reads it when
  it starts a server, unless
  [`ServerOptions.ConfigFile`][tmux-serveroptions-configfile] names another.

## Support and status

Any release may change or remove exported identifiers without a deprecation
period, so pin an exact version. Not recommended for production.

- **No runtime dependencies.** The core module imports only the standard
  library.
- **Go 1.26+**, rising with Go's own support window, about twice a year.
  tmux **3.3a through 3.8-rc2** across the core, workspace, and MCP modules,
  tested on releases spanning that range.
- **Unix.** Linux and macOS are tested on every change, and the BSDs, illumos,
  and Plan 9 build. A Windows build compiles, so a cross-platform program
  still builds, but there is no tmux there to reach.
- **Tagged per directory.** Each module carries its own version: the tags are
  `mcp/vN` and `workspace/vN` beside the core's plain `vN`. Pin the exact ones
  you want in your own go.mod; the commands here fetch the newest.

## Documentation

The package documentation is the reference, written to be read start to finish
rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

It opens with a task index, and carries the rule mapping a tmux command to
its Go method: `kill-pane` is [`Pane.Kill`][tmux-pane-kill], and
`rename-session` is [`Session.Rename`][tmux-session-rename]. A command usually
leads to its method without a lookup.

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, method, and field, one page each |
| [`tmux/`](tmux/) | How the library is shaped: records, naming, options, errors |
| [`examples/`](examples/) | Runnable programs, one per topic, each with a page of notes |
| [`DESIGN.md`](DESIGN.md) | The conventions this package holds itself to, and the bakeoffs behind them |
| [`PARITY.md`](PARITY.md) | How the surface is checked against the Python libtmux |
| [`BENCHMARKS.md`](BENCHMARKS.md) | What each way of reaching tmux costs |
| [`CHANGELOG.md`](CHANGELOG.md) | What each release changed |
| [`SECURITY.md`](SECURITY.md) | What this software executes, and how to report a hole in it |
| [`CONTRIBUTING.md`](.github/CONTRIBUTING.md) | The gates a change has to pass, and where the writing rules live |

## Attribution

Please use the following BibTeX template to cite libtmux-go in scientific
discourse:

```bibtex
@misc{libtmux-go,
   author = {Tony Narlock},
   year = {2026},
   note = {https://libtmux.org/en/go/},
   title = {libtmux-go: Go wrapper for tmux}
}
```

## License

MIT. See [`LICENSE`](LICENSE).

[tmux-connectionoptions-lanes]: https://libtmux.org/en/go/latest/reference/tmux-connectionoptions-lanes/
[tmux-errnoserver]: https://libtmux.org/en/go/latest/reference/tmux-errnoserver/
[tmux-newplan]: https://libtmux.org/en/go/latest/reference/tmux-newplan/
[tmux-newserver]: https://libtmux.org/en/go/latest/reference/tmux-newserver/
[tmux-notificationstream-subscribe]: https://libtmux.org/en/go/latest/reference/tmux-notificationstream-subscribe/
[tmux-pane-captureto]: https://libtmux.org/en/go/latest/reference/tmux-pane-captureto/
[tmux-pane-kill]: https://libtmux.org/en/go/latest/reference/tmux-pane-kill/
[tmux-pane-openobservation]: https://libtmux.org/en/go/latest/reference/tmux-pane-openobservation/
[tmux-pane-writer]: https://libtmux.org/en/go/latest/reference/tmux-pane-writer/
[tmux-pane]: https://libtmux.org/en/go/latest/reference/tmux-pane/
[tmux-paneobservation-waitfor]: https://libtmux.org/en/go/latest/reference/tmux-paneobservation-waitfor/
[tmux-paneobservation]: https://libtmux.org/en/go/latest/reference/tmux-paneobservation/
[tmux-plan-run]: https://libtmux.org/en/go/latest/reference/tmux-plan-run/
[tmux-server-loadbufferfrom]: https://libtmux.org/en/go/latest/reference/tmux-server-loadbufferfrom/
[tmux-server-panes]: https://libtmux.org/en/go/latest/reference/tmux-server-panes/
[tmux-server-searchsessions]: https://libtmux.org/en/go/latest/reference/tmux-server-searchsessions/
[tmux-server-sessions]: https://libtmux.org/en/go/latest/reference/tmux-server-sessions/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-server-windows]: https://libtmux.org/en/go/latest/reference/tmux-server-windows/
[tmux-serveroptions-configfile]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-configfile/
[tmux-serveroptions]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions/
[tmux-session-opencontrol]: https://libtmux.org/en/go/latest/reference/tmux-session-opencontrol/
[tmux-session-opennotifications]: https://libtmux.org/en/go/latest/reference/tmux-session-opennotifications/
[tmux-session-rename]: https://libtmux.org/en/go/latest/reference/tmux-session-rename/
[tmux-session-run]: https://libtmux.org/en/go/latest/reference/tmux-session-run/
[tmux-session]: https://libtmux.org/en/go/latest/reference/tmux-session/
[tmux]: https://github.com/tmux/tmux
[tmuxq-matching]: https://libtmux.org/en/go/latest/reference/tmuxq-matching/
[tmuxtest-newserver]: https://libtmux.org/en/go/latest/reference/tmuxtest-newserver/
[tmuxtest-runinpane]: https://libtmux.org/en/go/latest/reference/tmuxtest-runinpane/
[tmuxtest-scriptedtmux]: https://libtmux.org/en/go/latest/reference/tmuxtest-scriptedtmux/
[tmuxtest-waitfortext]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitfortext/
