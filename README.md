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

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

It needs Go 1.26 or newer and tmux 3.2a or newer, on a Unix host. A tmux
server for AI agents and a tmuxp-style workspace builder ship beside the
library, under [Packages](#packages).

**Contents** — [Quick start](#quick-start) ·
[Find things](#find-sessions-windows-and-panes) ·
[Type and wait](#type-into-a-pane-and-wait) ·
[Watch tmux](#watch-tmux-change) ·
[Choosing a path](#choosing-how-to-reach-tmux) ·
[Testing](#testing-your-own-code) · [Packages](#packages) ·
[Support](#support-and-status) · [Documentation](#documentation)

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

To run it, save it as `main.go` in an empty directory, with tmux on `PATH`:

```console
$ go mod init hello && \
    go get github.com/libtmux/libtmux-go/tmux@latest && \
    go run .
```

The program starts a tmux server on a socket and config of its own and kills it
on the way out, so it leaves nothing behind and never touches a tmux you have
open. Drop [`ServerOptions`][tmux-serveroptions] to its zero value and it
drives the same server
your `tmux` command does.

[`Session.Run`][tmux-session-run] is `os/exec` for a program that needs a
terminal: the command
runs in a window of its own with a tty, and what comes back is its exit status
and the screen tmux rendered. A nonzero status is a result, not an error.
[`examples/run-to-completion`](examples/run-to-completion) shows one.

Every Go block on this page is quoted by `go generate` from a program or test
that a suite runs against a real tmux on every supported release. The whole
files are also pasted back out of the page into an empty module, built, and
run, and what they print is compared with the output shown.

## Find sessions, windows, and panes

[`Server.Sessions`][tmux-server-sessions] asks tmux what it has now;
[`Server.Windows`][tmux-server-windows] and
[`Server.Panes`][tmux-server-panes] are its siblings:

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

What comes back is a record: a `Session` you hold is what tmux said when you
asked, not a live handle that changes underneath you.

To answer several questions from one moment, read the server once and filter
that read in Go. [`tmuxq.Matching`][tmuxq-matching] takes any filter the
`tmux` package declares:

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
```

<!-- docs:end -->

Or let tmux filter, which sends one command and gets back only the matches:

<!-- docs:query-in-tmux -->

```go
// Given: ctx context.Context; server tmux.Server
live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
sessions, err := server.SearchSessions(ctx, &live)
if err != nil {
	return err
}
```

<!-- docs:end -->

Runnable: [`examples/filter-query`](examples/filter-query). The filtering
helpers are in [`tmuxq/`](tmuxq/).

## Type into a pane and wait

A pane is an `io.Writer`, and a newline is the Enter key. Open an observation
before typing, then wait for text the command prints:

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

## Watch tmux change

[`Session.OpenNotifications`][tmux-session-opennotifications] returns a
stream of what tmux does, such as a
window opening or a session being renamed. tmux pushes each change when it
happens, so nothing has to guess how often to ask:

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

A subscription goes further: tmux evaluates a format for you and reports it
each time its value changes, so a program hears about a window count or a
pane's current command without asking again.

Runnable: [`examples/control-mode-subscribe`](examples/control-mode-subscribe).

## Choosing how to reach tmux

Start with a plain `Server`. The other paths trade a standing tmux client for
fewer process starts:

| Path | Construct it with | Cost | Reach for it |
| --- | --- | --- | --- |
| process | [`NewServer`][tmux-newserver] | a tmux process per command sent | one-shot commands |
| connection | [`Session.OpenControl`][tmux-session-opencontrol] | one tmux client per lane | repeated commands |
| concurrent | [`ConnectionOptions{Lanes: N}`][tmux-connectionoptions-lanes] | N tmux clients | parallel readers |
| chained | [`NewPlan`][tmux-newplan] then [`Run`][tmux-plan-run] | fewer process starts | builds and layouts |
| streaming | [`Session.OpenNotifications`][tmux-session-opennotifications] | one tmux client | watching what tmux does |

A plan records commands instead of running them, sends the ones needing no
answer together, and hands back a reference to what a step will create, so a
layout is written in one pass:

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

A connection appears in `list-clients` and counts toward `session_attached`,
which is why opening one is explicit. "Execution modes" in the package
documentation has the rules each path follows, and
[`BENCHMARKS.md`](BENCHMARKS.md) is what each costs, measured on every
supported tmux.

Runnable: [`examples/fast-path`](examples/fast-path) and
[`examples/planned-build`](examples/planned-build).

### Moving bytes

Keys and command arguments are the wrong way to move a payload: a shell reads
what it is sent, and tmux caps a whole command at 16 KiB and cannot carry a NUL
through one. [`Server.LoadBufferFrom`][tmux-server-loadbufferfrom] loads a
buffer from an `io.Reader` and [`Pane.CaptureTo`][tmux-pane-captureto] writes
a scrollback to an `io.Writer`, with neither limit.

Runnable: [`examples/byte-streams`](examples/byte-streams).

## Testing your own code

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

A wait that runs out fails with the screen the pane last held, which is what
you would have added a print statement to see. This one is
[`WaitForText`][tmuxtest-waitfortext] waiting for `ready`:

<!-- docs:output tmux/tmuxtest/testdata/wait-failure.txt -->

```text
tmuxtest: pane %1 never showed a line containing "ready"
the pane showed 3 line(s):
    | tmuxtest$ printf 'loading widgets\nconnecting\n'; cat
    | loading widgets
    | connecting
```

<!-- docs:end -->

[`tmuxtest.NewServer`][tmuxtest-newserver] gives a test whose subject is tmux
itself a server on a socket of its own, killed when the test ends.
[`tmuxtest.ScriptedTmux`][tmuxtest-scriptedtmux] answers
the tmux commands you name, so a test of your own code needs no tmux
installed. Both are in [`tmux/tmuxtest`](tmux/tmuxtest/).

## Packages

| Package | Source | Reference | What it is |
| --- | --- | --- | --- |
| `tmux` | [`tmux/`](tmux/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux) | The library. Sessions, windows, panes, options, hooks, formats, filters, snapshots, plans. |
| `tmuxtest` | [`tmux/tmuxtest/`](tmux/tmuxtest/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest) | Run your program in a real tmux and assert on what it drew. |
| `tmuxq` | [`tmuxq/`](tmuxq/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq) | Model-free generic helpers for slices and `iter.Seq`. |

Three more ship as **separate modules**, so `go get` on the library pulls in
none of them:

| Module | Source | Reference | What it is |
| --- | --- | --- | --- |
| `mcp` | [`mcp/`](mcp/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp) | **A tmux server for AI agents** over the Model Context Protocol. Install it as a binary. |
| `workspace` | [`workspace/`](workspace/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace) | Loads tmuxp-style YAML workspaces and builds them. Its CLI installs as a binary too. |
| `benchmarks` | [`benchmarks/`](benchmarks/) | — | Prints what each way of reaching tmux costs. |

### For agents

[`mcp/`](mcp/) is a standalone Model Context Protocol server that gives an agent
one tmux server: create panes, send keys, read output, wait for text.

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

See [`mcp/README.md`](mcp/README.md) for client configuration, and
[`mcp/TOOLS.md`](mcp/TOOLS.md) for the tool reference.

### For workspace automation

[`workspace/`](workspace/) loads tmuxp-style YAML workspace files and builds
them; its `tmux-workspace` CLI adds native workspace management, structured
output, and optional Python compatibility.

```console
$ go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest
```

See [`workspace/CLI.md`](workspace/CLI.md) for commands and output formats.

## Support and status

Alpha software. Releases carry an -alpha prerelease tag, the API is not
settled, and any release may change or remove exported identifiers without a
deprecation period. Pin an exact version. Not recommended for production.

- **No runtime dependencies.** The core module imports only the standard library.
- **Go 1.26+**, tmux **3.2a through 3.8-rc2** across the core, workspace, and
  MCP modules. The compatibility matrix checks ten builds spanning that
  range, the 3.8 release candidate included.
  The Go floor tracks upstream's support window, which covers the two most
  recent releases, so it rises whenever a Go release retires the oldest -
  roughly twice a year, and sooner than a toolchain pinned for longer.
- **Unix.** tmux is a Unix program, so driving one needs a Unix host: Linux and
  macOS run the suites on every change, and the BSDs, illumos and Plan 9 build.
  A Windows build compiles and resolves a tmux executable by Windows PATH and
  PATHEXT rules, so a cross-platform program still builds; it has no tmux to
  reach.
- **Tagged per directory.** Each module carries its own version: the tags are
  `mcp/vN` and `workspace/vN` beside the core's plain `vN`. Pin the exact ones
  you want in your own go.mod; the commands here fetch the newest.

## Documentation

The package documentation is the reference, written to be read start to finish
rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

It opens with a task index, and carries the rule mapping a tmux command to its
Go method — `kill-pane` is `Pane.Kill`, `rename-session` is `Session.Rename` — so a
command usually leads to its method without a lookup.

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

Please use the following BibTeX template to cite libtmux-go in scientific discourse:

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

[tmux]: https://github.com/tmux/tmux
[tmux-connectionoptions-lanes]: https://libtmux.org/en/go/latest/reference/tmux-connectionoptions-lanes/
[tmux-newplan]: https://libtmux.org/en/go/latest/reference/tmux-newplan/
[tmux-newserver]: https://libtmux.org/en/go/latest/reference/tmux-newserver/
[tmux-pane-captureto]: https://libtmux.org/en/go/latest/reference/tmux-pane-captureto/
[tmux-plan-run]: https://libtmux.org/en/go/latest/reference/tmux-plan-run/
[tmux-server-loadbufferfrom]: https://libtmux.org/en/go/latest/reference/tmux-server-loadbufferfrom/
[tmux-server-panes]: https://libtmux.org/en/go/latest/reference/tmux-server-panes/
[tmux-server-sessions]: https://libtmux.org/en/go/latest/reference/tmux-server-sessions/
[tmux-server-windows]: https://libtmux.org/en/go/latest/reference/tmux-server-windows/
[tmux-serveroptions]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions/
[tmux-session-opencontrol]: https://libtmux.org/en/go/latest/reference/tmux-session-opencontrol/
[tmux-session-opennotifications]: https://libtmux.org/en/go/latest/reference/tmux-session-opennotifications/
[tmux-session-run]: https://libtmux.org/en/go/latest/reference/tmux-session-run/
[tmuxq-matching]: https://libtmux.org/en/go/latest/reference/tmuxq-matching/
[tmuxtest-newserver]: https://libtmux.org/en/go/latest/reference/tmuxtest-newserver/
[tmuxtest-scriptedtmux]: https://libtmux.org/en/go/latest/reference/tmuxtest-scriptedtmux/
[tmuxtest-waitfortext]: https://libtmux.org/en/go/latest/reference/tmuxtest-waitfortext/
