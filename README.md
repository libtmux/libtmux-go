<!-- libtmux-logo -->
<div align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</div>
<!-- /libtmux-logo -->

<div align="center">

# libtmux for Go

Drive tmux from Go: [sessions][tmux-session], [windows][tmux-window], and
[panes][tmux-pane] as typed values, every tmux
[option and hook as a typed accessor][topic-options-and-hooks], and
[errors classified][topic-errors] by what tmux actually refused.

[![tests](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml/badge.svg)](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)
[![libtmux.org](https://img.shields.io/badge/docs-libtmux.org-0b7285)](https://libtmux.org/en/go/)
[![workspace reference](https://img.shields.io/badge/workspace-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace)
[![mcp reference](https://img.shields.io/badge/mcp-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp)

</div>

[Run a command][tmux-session-run] in a real terminal and read what it printed,
[find][tmux-server-sessions] and [filter][tmuxq-matching] what a tmux server
holds, [type into a pane and wait][tmux-pane-waitfortext] for its
answer, and [hear about changes][tmux-session-opennotifications] as tmux makes
them.

It suits tools that drive a terminal program, test suites that need a real
tmux, and agents that share a terminal.

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

It needs Go 1.26 or newer and tmux 3.3a or newer, on a Unix host. Against an
older daemon, an operation returns [`tmux.ErrVersionTooLow`][tmux-errversiontoolow]
and withholds the requested operation. A read-only version probe may run first.

tmux release candidates are not supported: a feature can require one only until
its release ships, and a candidate such as `X.Y-rcN` ranks below `X.Y`. See
[Release candidates](https://libtmux.org/en/tmux/guides/tmux-support/#release-candidates).

The detail is in [the library contracts][contract-requirements].

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
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	directory, err := os.MkdirTemp("", "libtmux-quickstart-")
	if err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()

	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(directory, "tmux.sock"),
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		if cleanup := server.Kill(context.WithoutCancel(ctx)); !errors.Is(cleanup, tmux.ErrNoServer) {
			err = errors.Join(err, cleanup)
		}
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

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

The program creates a socket path in a new temporary directory, then kills its
server and removes that directory on the way out. With
[`ServerOptions`][tmux-serveroptions] at its zero value, it drives the same
server your `tmux` command does.

[`Session.Run`][tmux-session-run] is `os/exec` for a program that needs a
terminal. The command runs in a window of its own with a tty, and you get back
its exit status and the screen tmux rendered.

A nonzero exit status arrives in the result.
[`examples/run-to-completion`](examples/run-to-completion) shows one, on a tmux
server of its own.

Every Go block on this page comes from a program or test that runs on the
releases its features support, against a real tmux wherever it uses one. Each
output shown is what that run printed.

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

What comes back is a record: the [`Session`][tmux-session] you hold is what
tmux said when you asked, and it stays as it was when tmux changes.

With no tmux server running, a listing returns an error matching
[`ErrNoServer`][tmux-errnoserver] instead of an empty list, so "nothing there"
and "nobody to ask" stay different answers.

### [`tmuxq.Matching`][tmuxq-matching]: several answers from one read

Collect a snapshot with [`Server.Snapshot`][tmux-server-snapshot], then filter
the captured records in Go. Collection uses sequential listings and identity
probes; it is not an atomic view of one instant.
[`Matching`][tmuxq-matching] takes any filter the `tmux` package declares:

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

### [`Pane.WaitForText`][tmux-pane-waitfortext]: wait for text on every release

`Pane.WaitForText` polls the pane's visible screen until it holds the text, on
every tmux release from 3.3a, and never opens a control client. It returns the
screen it last read with every error. A pane or server that ends first returns
a [`PaneEndedError`][tmux-paneendederror], which matches `ErrPaneEnded` and,
through the failed read, `ErrNotFound` or `ErrNoServer`. A deadline or cancel
returns `ctx.Err()`.

A pane is an `io.Writer` through [`Pane.Writer`][tmux-pane-writer], and a
newline is the Enter key. Type, then wait for text the command prints:

<!-- docs:pane-wait-text -->

```go
// Given: ctx context.Context; pane tmux.Pane
// A pane is an io.Writer, and a newline is the Enter key.
command := `echo "6 x 7 = $((6 * 7))"`
if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
	return fmt.Errorf("type command: %w", err)
}
// The screen already holds the typed line, which says "$((6 * 7))"
// and never "42", so the answer is what the wait names.
if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
	Text: "6 x 7 = 42",
}); err != nil {
	return fmt.Errorf("wait for answer: %w", err)
}
fmt.Println("the pane answered 6 x 7 = 42")
```

<!-- docs:end -->

The wait is level-triggered: text already on screen matches at once, with
`MatchedAtEntry` set, and the shell's echo of a typed line matches too, so name
the answer and not the command. It reads the visible screen only, so text that
scrolled off is not seen, and a poll sees the final state of text a program
overwrote. Each read runs the owner's `after-capture-pane` hook. See
[`docs/contracts.md`](tmux/docs/contracts.md#waiting-for-pane-text).

### [`PaneObservation.WaitFor`][tmux-paneobservation-waitfor]: wait for what a command prints

Needs tmux 3.7. It reports only output written after its baseline, where
`Pane.WaitForText` matches what is already on screen.
[`tmux.Poll`][tmux-poll] is for a state tmux does not announce, not for pane
text.

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
answer the command prints.

Runnable: [`examples/pane-io`](examples/pane-io), which goes on to read the
pane as an `io.Reader`.

To move a payload, skip the typing: the shell in the pane reads quotes, `$`,
and backslashes in it as syntax.

[`Server.LoadBufferFrom`][tmux-server-loadbufferfrom] fills a tmux buffer from
an `io.Reader`, and [`Pane.CaptureTo`][tmux-pane-captureto] writes a scrollback
to an `io.Writer`. [`examples/byte-streams`](examples/byte-streams) does both.

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

Start with a plain [`Server`][tmux-server]: one tmux process per command. The
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
which is why opening one is explicit.

"Execution modes" in the [package documentation][tmux-package] has the rules
each path follows. [`BENCHMARKS.md`](BENCHMARKS.md) is what each costs,
measured on every supported tmux.

[`examples/fast-path`](examples/fast-path) runs the same reads down each path.

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

A wait that runs out fails with the screen the pane last held, which is what a
print statement would have shown. This one was waiting for `ready`:

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

| Module | Source | Reference | What it is |
| --- | --- | --- | --- |
| `mcp` | [`mcp/`](mcp/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp) | A tmux server for AI agents, over the Model Context Protocol |
| `workspace` | [`workspace/`](workspace/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace) | Loads tmuxp-style YAML workspaces and builds them, with a CLI |
| `benchmarks` | [`benchmarks/`](benchmarks/) | — | Prints what each way of reaching tmux costs |

The MCP server:

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

The workspace CLI:

```console
$ go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest
```

### [`tmux`](https://github.com/libtmux/libtmux-go/tree/master/tmux): the library

Sessions, windows, panes, options, hooks, formats, filters, snapshots, and
plans, with no dependency outside the standard library. Reference:
[pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux) and
[libtmux.org](https://libtmux.org/en/go/latest/reference/).

[`Session.Run`][tmux-session-run] runs a command in a window of its own, with
a terminal. A nonzero exit status arrives in the result:

<!-- docs:run-to-completion -->

```go
// Given: ctx context.Context; session tmux.Session
// The command runs with a terminal, so test -t holds where a pipe fails.
command := "test -t 1 && echo stdout is a terminal; exit 3"
result, err := session.Run(ctx, command, tmux.RunOptions{})
if err != nil {
	return fmt.Errorf("run command: %w", err)
}
for _, line := range result.Lines {
	fmt.Println("screen:", line)
}
fmt.Println("exited", result.Status)
```

<!-- docs:end -->

<!-- docs:checked examples/run-to-completion/output.txt -->
This prints `screen: stdout is a terminal`, then `exited 3`.
<!-- docs:end -->

### [`tmuxq`](https://github.com/libtmux/libtmux-go/tree/master/tmuxq): filters for any slice

Helpers that find records in a slice or an `iter.Seq`. The package imports no
tmux type, so they work on a slice of your own as well as on the records
`tmux` returns. Reference:
[pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq) and
[libtmux.org](https://libtmux.org/en/go/latest/reference/).

[`tmuxq.Where`][tmuxq-where] keeps what a function accepts, and
[`tmuxq.First`][tmuxq-first] returns the first match. This needs no tmux:

<!-- docs:tmuxq-slice -->

```go
type task struct {
	name string
	done bool
}
tasks := []task{{"build", true}, {"test", false}, {"deploy", false}}
open := func(t *task) bool { return !t.done }

for _, t := range tmuxq.Where(tasks, open) {
	fmt.Println("open:", t.name)
}
first, _ := tmuxq.First(tasks, open)
fmt.Println("first open:", first.name)
```

<!-- docs:end -->

<!-- docs:checked examples/any-slice/output.txt -->
This prints `open: test` and `open: deploy`, then `first open: test`.
<!-- docs:end -->

### [`tmuxtest`](https://github.com/libtmux/libtmux-go/tree/master/tmux/tmuxtest): a real tmux for tests

Runs your program in a real pane and lets a test assert on what it drew. The
server, its sessions, and its panes end with the test. Reference:
[pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest)
and [libtmux.org](https://libtmux.org/en/go/latest/reference/).

[`tmuxtest.NewServer`][tmuxtest-newserver] gives a test whose subject is tmux
itself a server on a socket of its own. This is the body of a test, in a
package whose `TestMain` calls [`tmuxtest.Main`][tmuxtest-main], as in
[the test above](#testing-your-own-code):

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

### [`workspace`](https://github.com/libtmux/libtmux-go/tree/master/workspace): tmuxp-style workspaces

Loads tmuxp-style YAML workspace files and builds them in tmux, from Go or with
the `tmux-workspace` command. Reference:
[pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace),
[libtmux.org](https://libtmux.org/en/go/latest/workspace/reference/), and the
[command reference](https://libtmux.org/en/go/latest/workspace/cli/).
[`workspace/CLI.md`][workspace-cli-md] covers every command and its output
formats.

With the command installed as above and a [workspace file][workspace-file]
saved as `project.yaml`, [`tmux-workspace load`][workspace-cli-load] builds it
on a tmux socket named `project` without attaching to it:

```console
$ tmux-workspace load \
    -L project \
    -d \
    ./project.yaml
```

### [`mcp`](https://github.com/libtmux/libtmux-go/tree/master/mcp): a tmux server for agents

Serves one tmux server to Model Context Protocol clients, as the
[`libtmux-mcp`][libtmux-mcp-command] command or from a program of your own.
Reference: [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp),
[libtmux.org](https://libtmux.org/en/go/latest/mcp/reference/), and the
[tool pages](https://libtmux.org/en/go/latest/mcp/tools/).

[`mcp/README.md`][mcp-readme] covers client configuration, and
[`mcp/TOOLS.md`][mcp-tools] lists each tool's arguments and results.

An MCP client starts the server installed above itself, and speaks to it over
stdin and stdout. Register it with Claude Code:

```console
$ claude mcp add tmux -- libtmux-mcp
```

Check which socket it reaches, and what that holds, without a client, with
[`libtmux-mcp -doctor`][guide-diagnose-startup]:

```console
$ libtmux-mcp -doctor
```

The [`benchmarks`](https://github.com/libtmux/libtmux-go/tree/master/benchmarks)
module is a command that prints what each way of reaching tmux costs.

## What it leaves out

- **Live objects.** A [`Session`][tmux-session] or a [`Pane`][tmux-pane] is a
  record of what tmux reported when it was read, and does not change when
  tmux does. Read again, or subscribe, to see a change.
- **A terminal emulator.** [A capture][tmux-pane-capture] and
  [`Session.Run`][tmux-session-run]
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
  tmux **3.3a through 3.8** across the core, workspace, and MCP modules,
  tested on releases spanning that range.
- **Linux, including WSL, is the primary platform.** CI runs every module's
  tests on Linux against tmux 3.3a and 3.8 on each push, and across tmux 3.3a
  through 3.8 for each release. macOS is supported on a best-effort basis, and
  CI coverage for it is highly limited.
- **Other hosts.** The BSDs, illumos, and Plan 9 build. A Windows build
  compiles, and CI tests executable lookup there; no tmux runs there.
- **Tagged per directory.** Each module carries its own version: the tags are
  `mcp/vN` and `workspace/vN` beside the core's plain `vN`. Pin the exact ones
  you want in your own go.mod; the commands here fetch the newest.

## Documentation

The package documentation is the reference, written to be read start to
finish:

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
| [`tmux/docs/contracts.md`](tmux/docs/contracts.md) | What the library promises: the tmux version check |
| [`examples/`](examples/) | Runnable programs, one per topic, each with a page of notes |
| [`DESIGN.md`](DESIGN.md) | The conventions this package holds itself to, and the bakeoffs behind them |
| [`PARITY.md`](PARITY.md) | How the surface is checked against the Python libtmux |
| [`BENCHMARKS.md`](BENCHMARKS.md) | What each way of reaching tmux costs |
| [`CHANGELOG.md`](CHANGELOG.md) | What each release changed |
| [`SECURITY.md`](SECURITY.md) | What this software executes, and how to report a hole in it |
| [`CONTRIBUTING.md`](.github/CONTRIBUTING.md) | The gates a change has to pass, and where the writing rules live |
| [`WRITING.md`](.github/WRITING.md) | How this repository writes: docs, the changelog, commits |
| [`AGENTS.md`](AGENTS.md) | Which of the above applies to what you are changing |

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

[libtmux-mcp-command]: https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp
[mcp-readme]: https://github.com/libtmux/libtmux-go/tree/master/mcp
[mcp-tools]: https://github.com/libtmux/libtmux-go/blob/master/mcp/TOOLS.md
[contract-requirements]: tmux/docs/contracts.md#requirements-by-feature
[guide-diagnose-startup]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#diagnose-startup
[tmux-connectionoptions-lanes]: https://libtmux.org/en/go/latest/reference/tmux-connectionoptions-lanes/
[tmux-errnoserver]: https://libtmux.org/en/go/latest/reference/tmux-errnoserver/
[tmux-errversiontoolow]: https://libtmux.org/en/go/latest/reference/tmux-errversiontoolow/
[tmux-newplan]: https://libtmux.org/en/go/latest/reference/tmux-newplan/
[tmux-newserver]: https://libtmux.org/en/go/latest/reference/tmux-newserver/
[tmux-notificationstream-subscribe]: https://libtmux.org/en/go/latest/reference/tmux-notificationstream-subscribe/
[tmux-package]: https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux
[tmux-pane-capture]: https://libtmux.org/en/go/latest/reference/tmux-pane-capture/
[tmux-pane-captureto]: https://libtmux.org/en/go/latest/reference/tmux-pane-captureto/
[tmux-pane-kill]: https://libtmux.org/en/go/latest/reference/tmux-pane-kill/
[tmux-pane-openobservation]: https://libtmux.org/en/go/latest/reference/tmux-pane-openobservation/
[tmux-pane-waitfortext]: https://libtmux.org/en/go/latest/reference/tmux-pane-waitfortext/
[tmux-pane-writer]: https://libtmux.org/en/go/latest/reference/tmux-pane-writer/
[tmux-pane]: https://libtmux.org/en/go/latest/reference/tmux-pane/
[tmux-paneendederror]: https://libtmux.org/en/go/latest/reference/tmux-paneendederror/
[tmux-paneobservation-waitfor]: https://libtmux.org/en/go/latest/reference/tmux-paneobservation-waitfor/
[tmux-paneobservation]: https://libtmux.org/en/go/latest/reference/tmux-paneobservation/
[tmux-plan-run]: https://libtmux.org/en/go/latest/reference/tmux-plan-run/
[tmux-poll]: https://libtmux.org/en/go/latest/reference/tmux-poll/
[tmux-server-loadbufferfrom]: https://libtmux.org/en/go/latest/reference/tmux-server-loadbufferfrom/
[tmux-server-panes]: https://libtmux.org/en/go/latest/reference/tmux-server-panes/
[tmux-server-searchsessions]: https://libtmux.org/en/go/latest/reference/tmux-server-searchsessions/
[tmux-server-sessions]: https://libtmux.org/en/go/latest/reference/tmux-server-sessions/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-server-windows]: https://libtmux.org/en/go/latest/reference/tmux-server-windows/
[tmux-server]: https://libtmux.org/en/go/latest/reference/tmux-server/
[tmux-serveroptions-configfile]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-configfile/
[tmux-serveroptions]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions/
[tmux-session-opencontrol]: https://libtmux.org/en/go/latest/reference/tmux-session-opencontrol/
[tmux-session-opennotifications]: https://libtmux.org/en/go/latest/reference/tmux-session-opennotifications/
[tmux-session-rename]: https://libtmux.org/en/go/latest/reference/tmux-session-rename/
[tmux-session-run]: https://libtmux.org/en/go/latest/reference/tmux-session-run/
[tmux-session]: https://libtmux.org/en/go/latest/reference/tmux-session/
[tmux-window]: https://libtmux.org/en/go/latest/reference/tmux-window/
[tmux]: https://github.com/tmux/tmux
[tmuxq-first]: https://libtmux.org/en/go/latest/reference/tmuxq-first/
[tmuxq-matching]: https://libtmux.org/en/go/latest/reference/tmuxq-matching/
[tmuxq-where]: https://libtmux.org/en/go/latest/reference/tmuxq-where/
[tmuxtest-main]: https://libtmux.org/en/go/latest/reference/tmuxtest-main/
[tmuxtest-newserver]: https://libtmux.org/en/go/latest/reference/tmuxtest-newserver/
[tmuxtest-runinpane]: https://libtmux.org/en/go/latest/reference/tmuxtest-runinpane/
[tmuxtest-scriptedtmux]: https://libtmux.org/en/go/latest/reference/tmuxtest-scriptedtmux/
[tmuxtest-waitfortext]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitfortext/
[topic-errors]: https://libtmux.org/en/go/latest/topics/errors-and-exceptions/
[topic-options-and-hooks]: https://libtmux.org/en/go/latest/topics/options-and-hooks/
[workspace-cli-load]: https://libtmux.org/en/go/latest/workspace/cli/load/
[workspace-cli-md]: https://github.com/libtmux/libtmux-go/blob/master/workspace/CLI.md
[workspace-file]: https://github.com/libtmux/libtmux-go/tree/master/workspace#a-workspace-file
