# libtmux for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)
[![tests](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml/badge.svg)](https://github.com/libtmux/libtmux-go/actions/workflows/tests.yml)

Alpha software. Releases carry an -alpha prerelease tag, the API is not
settled, and any release may change or remove exported identifiers without a
deprecation period. Pin an exact version. Not recommended for production.

Drive tmux from Go: sessions, windows, and panes as typed values, every tmux
option and hook as a typed accessor, and errors classified by what tmux actually
refused.

- **No runtime dependencies.** The core module imports only the standard library.
- **Go 1.26+**, tmux **3.2a through 3.7c** across the core, workspace, and MCP
  modules. The compatibility matrix checks every release in that range.
  The Go floor tracks upstream's support window, which covers the two most
  recent releases.
- **Records never refresh behind you.** A `Session` you hold is what tmux said
  when you asked, not a live handle that changes underneath.

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

Modules are tagged per directory, so each consumer carries its own version:
the tags are `mcp/vN` and `workspace/vN` beside the core's plain `vN`. Pin the
exact ones you want in your own go.mod; the commands here fetch the newest.

**Contents** — [Quick start](#quick-start) · [Querying](#what-querying-looks-like)
· [Choosing a mode](#choosing-a-mode) · [Watching tmux](#watching-tmux) ·
[Packages](#packages) · [For agents](#for-agents) ·
[Testing your code](#testing-your-own-code) · [Documentation](#documentation)

## Quick start

Make a window, split it, type a command into the new pane, and read the reply
back through an `io.Reader`:

<!-- docs:quickstart -->

```go
window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: new("work")})
if err != nil {
	return fmt.Errorf("create window: %w", err)
}
pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
	Direction: tmux.PaneDirectionRight, Command: "sh",
})
if err != nil {
	return fmt.Errorf("split window: %w", err)
}
output, err := pane.OpenObservation(ctx)
if err != nil {
	return fmt.Errorf("watch pane: %w", err)
}
defer func() { err = errors.Join(err, output.Close()) }()
if _, err := fmt.Fprintln(pane.Writer(ctx), "printf 'libtmux ready\\n'"); err != nil {
	return fmt.Errorf("send command: %w", err)
}
```

<!-- docs:end -->

Every Go block below marked this way is generated from a program in
[`examples/`](examples/) that is compiled, linted, run against a real tmux, and
swept across every supported release — so none of it can drift from code that
works.

Runnable: [`examples/quickstart`](examples/quickstart) — `go -C examples run ./quickstart`.

## Running a command to completion

`Session.Run` is `os/exec` for a program that needs a terminal: the command
runs in a window of its own with a tty, and what comes back is its exit status
and the screen tmux rendered. A nonzero status is a result, not an error, and
the wait is tmux's own rather than a poll:

<!-- docs:run-to-completion -->

```go
result, err := session.Run(ctx, "tty; exit 3", tmux.RunOptions{})
if err != nil {
	return fmt.Errorf("run command: %w", err)
}
for _, line := range result.Lines {
	fmt.Println("screen:", line)
}
fmt.Println("exited", result.Status)
```

<!-- docs:end -->

Runnable: [`examples/run-to-completion`](examples/run-to-completion).

## What querying looks like

Two ways to ask, and they answer the same question at different costs.

**Let tmux filter**, which sends one command and gets back only matches:

<!-- docs:query-in-tmux -->

```go
live := tmux.TmuxFilter("#{==:#{session_name},libtmux-filter}")
sessions, err := server.SearchSessions(ctx, &live)
```

<!-- docs:end -->

**Or read once and filter in Go**, when you want several answers from one read:

<!-- docs:query-in-go -->

```go
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

Typed filters compose, and the generated ones push down into tmux's own `-f`
where tmux can evaluate them:

```go
filter := tmux.PaneFilter{
	Active:      tmux.Ptr(true),
	CurrentPath: tmux.Ptr("/home/you/project"),
}
panes, err := server.SearchPanes(ctx, &filter)
```

Runnable: [`examples/filter-query`](examples/filter-query).

## Choosing an execution path

A plain `Server` uses the executable, environment, working directory, and
socket selection frozen by `NewServer`. Values derived from it retain that
subprocess binding. Guards on materialized values assume stable, trusted tmux
parser primitives and aliases. Establish a connection before socket
replacement when exact-daemon ownership is required.

| Path | Construct it with | Cost | Reach for it |
| --- | --- | --- | --- |
| process | `NewServer` | one tmux process per operation | one-shot commands |
| connection | `Session.OpenControl` | one tmux client per lane | repeated commands |
| concurrent | `ConnectionOptions{Lanes: N}` | N tmux clients | parallel readers |
| chained | `NewPlan` then `Run` | fewer process starts | builds and layouts |
| streaming | `Session.OpenNotifications(ctx, NotificationOptions{})` | one tmux client | watching what tmux does |

Plans run over either a plain server or a connection-bound server. Unsupported
capability policy is separate: `ServerOptions.Unsupported` decides whether a
request naming an unavailable tmux flag is refused — the default — or
carried out without it and reported to a warning handler.

A connection carries commands without starting a process for each. It appears
in `list-clients` and counts toward `session_attached`, which is why opening one
is explicit:

<!-- docs:control-pool -->

```go
connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
if err != nil {
	return fmt.Errorf("open control connection: %w", err)
}
defer func() { _ = connection.Close() }()
connected := connection.Session()
```

<!-- docs:end -->

Once established, `connection.Server()` and `connection.Session()` are bound
to that exact daemon. Values derived from them retain that owner. The binding
is terminal: closing the connection makes later operations return
`ErrControlClosed`, and an operation that needs a separate process returns
`ErrConnectionRequiresProcess`. It never falls back or rebinds. The original
session remains on its frozen subprocess binding.

`Server.NewSessionConnection` creates a session and retains its creating
control process as the first lane. It returns the ordinary created session and
an owned connection; use `connection.Session()` for connected operations.

A plan records commands instead of running them, sends the ones needing no
answer together, and hands back a reference to what a step *will* create — so a
build is written in one pass:

<!-- docs:planning -->

```go
plan := tmux.NewPlan()
plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
editor := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
plan.SetPaneTitle(editor, "editor")
plan.SendKeys(editor, tmux.SendKeysRequest{Command: new("echo built")})
plan.DisplayMessage(editor, "#{pane_title}")
```

<!-- docs:end -->

Runnable: [`examples/fast-path`](examples/fast-path) and
[`examples/planned-build`](examples/planned-build).
[`BENCHMARKS.md`](BENCHMARKS.md) is what each path costs, measured on every
supported tmux.

## Watching tmux

`Session.OpenNotifications` and `Server.OpenNotifications` return owned
streams. Zero options retain tmux changes but suppress pane output; set
`IncludePaneOutput` when watching pane content. tmux pushes each change when it
happens rather than making a poll guess how often to ask. Before tmux 3.6,
destroying the attached session follows its `detach-on-destroy` policy and may
end the stream:

<!-- docs:watching -->

```go
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

A subscription asks tmux to evaluate a format and report it when its value
changes, so a program hears about a state it cares about — a window count, a
pane's current command — without asking again:

<!-- docs:subscribing -->

```go
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

Runnable: [`examples/control-mode-subscribe`](examples/control-mode-subscribe)
and, for a pane as an `io.Writer` and `io.Reader`, [`examples/pane-io`](examples/pane-io).

## Moving bytes

Keys and command arguments are the wrong way to move a payload: a shell reads
what it is sent, and tmux caps a whole command at 16 KiB and cannot carry a NUL
through one at all. A buffer loaded from an `io.Reader` has neither limit, and a
capture written to an `io.Writer` never holds a scrollback in memory:

<!-- docs:byte-streams -->

```go
name := "payload"
if err := server.LoadBufferFrom(ctx, payload, tmux.LoadBufferFromOptions{
	Name: &name,
}); err != nil {
	return fmt.Errorf("load payload: %w", err)
}
if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
	BufferName: &name, DeleteAfter: true,
}); err != nil {
	return fmt.Errorf("paste payload: %w", err)
}
// tmux accepts the paste before the pane has echoed it.
if err := tmux.Poll(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	return slices.ContainsFunc(lines, func(line string) bool {
		return strings.Contains(line, "'quoted'")
	}), err
}); err != nil {
	return fmt.Errorf("wait for pasted payload: %w", err)
}

file, err := os.Create(archive)
if err != nil {
	return fmt.Errorf("create archive: %w", err)
}
defer func() { err = errors.Join(err, file.Close()) }()
compressor := gzip.NewWriter(file)
if err := pane.CaptureTo(ctx, compressor, tmux.CapturePaneRequest{
	Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary,
}); err != nil {
	return fmt.Errorf("capture scrollback: %w", err)
}
if err := compressor.Close(); err != nil {
	return fmt.Errorf("finish archive: %w", err)
}
```

<!-- docs:end -->

`Server.LoadBufferFrom`, `Server.SaveBufferTo` and `Pane.CaptureTo` use tmux's
own stdin and stdout, which tmux offers no control client, so they need a
process; `Server.LoadBuffer`, `Server.SaveBuffer` and `Pane.CaptureToFile` take
a path and work over a connection. Runnable:
[`examples/byte-streams`](examples/byte-streams).

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
| `workspace` | [`workspace/`](workspace/) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace) | Loads tmuxp-style YAML workspaces and builds them. |
| `benchmarks` | [`benchmarks/`](benchmarks/) | — | Prints what each way of reaching tmux costs. |

### For agents

[`mcp/`](mcp/) is a standalone Model Context Protocol server that gives an agent
one tmux server: create panes, send keys, read output, wait for text.

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

See [`mcp/README.md`](mcp/README.md) for client configuration, and
[`mcp/TOOLS.md`](mcp/TOOLS.md) for the tool reference.

## Testing your own code

[`tmux/tmuxtest`](tmux/tmuxtest/) runs your program inside a real tmux and lets
a test assert on what it drew, with no sleeps. Run it, wait for what it draws,
type at it:

<!-- docs:tmuxtest-quickstart -->

```go
pane := tmuxtest.RunInPane(ctx, t, "printf 'ready\\n'; cat")

tmuxtest.WaitForText(ctx, t, pane, "ready")
tmuxtest.Type(ctx, t, pane, "a line for the program")
tmuxtest.WaitForLine(ctx, t, pane, "a line for the program")
```

<!-- docs:end -->

A wait that runs out fails with the screen the pane last held, rather than
sending you back to add a print statement:

```
tmuxtest: pane %1 never showed a line containing "ready"
the pane showed 3 line(s):
    | tmuxtest$ ./mytui --watch
    | loading widgets
    | connecting
```

It works for a test whose subject is tmux itself too, giving a server on its own
socket that is killed when the test ends:

```go
func TestSomething(t *testing.T) {
	ctx := context.Background()
	server := tmuxtest.NewServer(ctx, t)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "under-test"})
	// ...
}
```

`NewServer` snapshots its effective environment and working directory, resolves
one absolute executable, and returns an error before starting tmux when
configuration or resolution fails. Later environment and directory changes do
not retarget the handle, and the zero `Server` is invalid. Tests of process
behavior can point `ServerOptions.Binary` at an executable fixture;
construction still resolves and freezes it. Use `tmuxtest` when the behavior
belongs to a real tmux daemon.

## Documentation

The package documentation is the reference, written to be read start to finish
rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

It opens with a task index, then the rule mapping a tmux command to its Go
method — `kill-pane` is `Pane.Kill`, `rename-session` is `Session.Rename` — so a
command usually leads to its method without a lookup.

| | |
| --- | --- |
| [`DESIGN.md`](DESIGN.md) | The conventions this package holds itself to, and the bakeoffs behind them |
| [`PARITY.md`](PARITY.md) | How the surface is checked against the Python libtmux |
| [`BENCHMARKS.md`](BENCHMARKS.md) | What each way of reaching tmux costs |
| [`CHANGELOG.md`](CHANGELOG.md) | What each release changed |
| [`CONTRIBUTING.md`](.github/CONTRIBUTING.md) | The gates a change has to pass |
| [`WRITING.md`](.github/WRITING.md) | How this repository writes: docs, the changelog, commits |
| [`SECURITY.md`](SECURITY.md) | What this software executes, and how to report a hole in it |
| [`AGENTS.md`](AGENTS.md) | Which of the above applies to what you are changing |
| [`examples/`](examples/) | Runnable programs for each of the above |

## License

MIT. See [`LICENSE`](LICENSE).

[tmux]: https://github.com/tmux/tmux
