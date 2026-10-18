<!-- libtmux-logo -->
<div align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</div>
<!-- /libtmux-logo -->

<div align="center">

# tmux

The library. A typed, context-aware tmux API with no runtime dependencies.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)

</div>

Add it to your module:

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

**Contents** — [A whole program](#a-whole-program) · [Records](#records) ·
[Method names](#method-names) · [Options and hooks](#options-and-hooks) ·
[Errors](#errors) · [Packages](#packages) · [See also](#see-also)

New here? The [top-level README](../README.md) opens with a program that runs
a command in tmux and prints what it showed. This page is about how the
library is shaped.

## A whole program

This one splits a window into two panes side by side and reads back what tmux
made:

<!-- docs:file examples/layout/main.go -->

```go
// Command layout builds two panes side by side and reads back what tmux made.
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
		SocketName: "libtmux-layout",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux: %w", err)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "work", WindowName: "editor",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	// Cleanup runs even when ctx is the reason the program is leaving.
	defer func() {
		err = errors.Join(err, server.Kill(context.WithoutCancel(ctx)))
	}()

	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return fmt.Errorf("find window: %w", err)
	}
	// Split the window's active pane, and put the new pane on the right.
	split := tmux.SplitPaneRequest{Direction: tmux.PaneDirectionRight}
	if _, err := window.SplitPane(ctx, split); err != nil {
		return fmt.Errorf("split window: %w", err)
	}

	// Read back what tmux made, rather than trusting what was asked for.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read tmux: %w", err)
	}
	for _, pane := range snapshot.Panes() {
		width, _ := pane.Width()
		fmt.Printf("pane %d: %d columns\n", pane.Index(), width)
	}
	return nil
}
```

<!-- docs:end -->

To run it, save it as `main.go` in an empty directory, with tmux 3.2a or newer
on `PATH`:

```console
$ go mod init demo && \
    go get github.com/libtmux/libtmux-go/tmux@latest && \
    go run .
```

It prints:

<!-- docs:output examples/layout/output.txt -->

```text
pane 0: 40 columns
pane 1: 39 columns
```

<!-- docs:end -->

### [`tmux.NewServer`][tmux-newserver]: choose which tmux to reach

<!-- docs:private-server -->

```go
// A private server: a socket and an empty config of its own, so this
// neither touches nor is changed by a tmux you already use.
server, err := tmux.NewServer(tmux.ServerOptions{
	SocketName: "libtmux-layout",
	ConfigFile: os.DevNull,
})
if err != nil {
	return fmt.Errorf("configure tmux: %w", err)
}
```

<!-- docs:end -->

`NewServer` checks its configuration and starts nothing. With no
[`SocketName`][tmux-serveroptions-socketname] or
[`SocketPath`][tmux-serveroptions-socketpath] it reaches the server your own
`tmux` command does, and with no
[`ConfigFile`][tmux-serveroptions-configfile] tmux reads your configuration.
A program that should behave the same everywhere names both, as this one does.

## Records

A [`Server`][tmux-server] reaches one tmux server. It holds
[`Session`][tmux-session] values, each holding [`Window`][tmux-window] values,
each holding [`Pane`][tmux-pane] values, and every one of them is a record:
what tmux said when you asked. The blocks in this section and under Errors
come from [`../examples/object-model`](../examples/object-model), a whole
program you can run from a clone with `go -C examples run ./object-model`;
[`../examples/README.md`](../examples/README.md) has the commands to clone it.

A block that starts with `// Given:` is the body of a function that returns
an error, with the values that line names already in hand.

### [`Session.Rename`][tmux-session-rename]: a change returns a new record

<!-- docs:records -->

```go
// Given: ctx context.Context; session tmux.Session
// Rename hands back the session as tmux now reports it. The record
// already in hand keeps the name it had when tmux handed it over.
renamed, err := session.Rename(ctx, "new")
if err != nil {
	return fmt.Errorf("rename session: %w", err)
}
held, _ := session.Name()
current, _ := renamed.Name()
fmt.Println("held record:", held)
fmt.Println("returned record:", current)
```

<!-- docs:end -->

<!-- docs:checked examples/object-model/output.txt -->
The session was created as `old`, so this prints `held record: old` and then
`returned record: new`.
<!-- docs:end -->

Nothing refreshes a record behind you. A method that changes which object you
hold, or what it looks like, hands back a new record.
[`Session.Refresh`][tmux-session-refresh] and its counterparts on a window and
a pane ask tmux again when you want one.

### [`Session.Windows`][tmux-session-windows]: read what the record holds

The name says which kind of read you are making. `Windows` takes no context,
because it reads the record and never asks tmux. Its second result reports
whether the record holds any windows at all, and a session handed back by
`Rename` holds none.
[`Session.SearchWindows`][tmux-session-searchwindows] asks tmux:

<!-- docs:record-reads -->

```go
// Given: ctx context.Context; renamed tmux.Session
// Windows reads the record and takes no context. A session that came
// from a call about the session carries no windows, and says so.
if _, held := renamed.Windows(); !held {
	fmt.Println("the record holds no windows")
}
// SearchWindows asks tmux.
windows, err := renamed.SearchWindows(ctx, nil)
if err != nil {
	return fmt.Errorf("list windows: %w", err)
}
fmt.Println("tmux reports windows:", len(windows))
```

<!-- docs:end -->

<!-- docs:checked examples/object-model/output.txt -->
This prints `the record holds no windows`, then `tmux reports windows: 1`.
<!-- docs:end -->

A record holds its windows when it came from a read of everything, which is
what [`Server.Snapshot`][tmux-server-snapshot] returns.

## Method names

These rules cover most of the surface, so a tmux command usually leads to its
Go method without a search.

**The name drops the receiver's noun**, because the receiver already carries
it:

| tmux | Go |
| --- | --- |
| `kill-pane` | [`Pane.Kill`][tmux-pane-kill] |
| `rename-session` | [`Session.Rename`][tmux-session-rename] |
| `link-window` | [`Window.Link`][tmux-window-link] |
| `split-window` | [`Window.SplitPane`][tmux-window-splitpane] |

A noun naming a *different* object always stays, which is why
[`Session.NewWindow`][tmux-session-newwindow] and `Window.SplitPane` keep
theirs.

**The parameters say what is required.** A method takes only a context when
the receiver names everything ([`Window.Kill`][tmux-window-kill]), typed
positional values when every value is required (`Session.Rename`), and a
request value when any field is optional
([`Pane.Capture`][tmux-pane-capture]).

**The result says what changed.** A method hands back a new record, read from
tmux after the change, when the command changes which object you are holding
or what it looks like. Everything else returns only an error.

## Options and hooks

### [`Session.SetMouse`][tmux-session-setmouse]: set an option, then read it back

Every tmux option and hook has a [typed accessor][topic-options-and-hooks]. The
tmux name maps to the Go name by one rule, given under "Naming and call shapes"
in the [package documentation][tmux-package]. A setter returns only an error,
because an option is not part of the record you hold. Read it back through
[`Session.Options`][tmux-session-options]:

<!-- docs:options -->

```go
// Given: ctx context.Context; session tmux.Session
if err := session.SetMouse(ctx, true); err != nil {
	return err
}
options, err := session.Options(ctx)
if err != nil {
	return err
}
mouse, _ := options.Mouse().Get()
fmt.Println("mouse:", mouse)
```

<!-- docs:end -->

<!-- docs:checked examples/option-hook-editing/output.txt -->
This prints `mouse: true`.
<!-- docs:end -->

### [`Session.SetOption`][tmux-session-setoption]: an option by its tmux name

For an option tmux has and this package has no accessor for, `SetOption` and
[`Session.RawOption`][tmux-session-rawoption] take the name and the value as
strings:

<!-- docs:raw-option -->

```go
// Given: ctx context.Context; session tmux.Session
// An option this package has no accessor for, such as one a plugin
// defines, goes by its tmux name. tmux keeps user options under "@".
settings := tmux.SetOptionOptions{}
if err := session.SetOption(ctx, "@project", "libtmux", settings); err != nil {
	return err
}
project, _, err := session.RawOption(ctx, "@project")
if err != nil {
	return err
}
fmt.Println("@project:", project)
```

<!-- docs:end -->

<!-- docs:checked examples/option-hook-editing/output.txt -->
This prints `@project: libtmux`.
<!-- docs:end -->

[`../examples/option-hook-editing`](../examples/option-hook-editing) is the
program both blocks come from. It sets a hook the same way.

## Errors

### [`tmux.ErrNotFound`][tmux-errnotfound]: a lookup that found nothing

Failures are [classified by what tmux refused][topic-errors], so a missing
target and a misspelled option are different checks:

<!-- docs:not-found -->

```go
// Given: ctx context.Context; server tmux.Server
// A lookup that finds nothing is an error a caller can test for.
_, lookupErr := server.Session(ctx, tmux.SessionID("$999"))
if errors.Is(lookupErr, tmux.ErrNotFound) {
	fmt.Println("session $999: not found")
}
```

<!-- docs:end -->

<!-- docs:checked examples/object-model/output.txt -->
This prints `session $999: not found`.
<!-- docs:end -->

### [`Server.Cmd`][tmux-server-cmd]: a failed command is a result

`Cmd` runs tmux arguments you write yourself. A command that ran and failed
is data, not an error:
[`CommandResult.ExitCode`][tmux-commandresult-exitcode] carries its status. A
tmux that could not be reached, and arguments refused before anything was
sent, are Go errors.

<!-- docs:cmd-result -->

```go
// Given: ctx context.Context; server tmux.Server
// Cmd sends arguments you write yourself. A command tmux ran and
// refused comes back as a result, with the error left nil.
result, err := server.Cmd(ctx, "kill-window", "-t", "@999")
if err != nil {
	return fmt.Errorf("reach tmux: %w", err)
}
fmt.Println("kill-window exit status:", result.ExitCode)
```

<!-- docs:end -->

<!-- docs:checked examples/object-model/output.txt -->
No window has that id, so this prints `kill-window exit status: 1`.
<!-- docs:end -->

## Packages

| Package | Source | Reference | What it is |
| --- | --- | --- | --- |
| `tmux` | [`tmux/`](https://github.com/libtmux/libtmux-go/tree/master/tmux) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux) | The library. Sessions, windows, panes, options, hooks, formats, filters, snapshots, plans. |
| `tmuxtest` | [`tmux/tmuxtest/`](https://github.com/libtmux/libtmux-go/tree/master/tmux/tmuxtest) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux/tmuxtest) | Run your program in a real tmux and assert on what it drew. |
| `tmuxq` | [`tmuxq/`](https://github.com/libtmux/libtmux-go/tree/master/tmuxq) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmuxq) | Model-free generic helpers for slices and `iter.Seq`. |
| `workspace` | [`workspace/`](https://github.com/libtmux/libtmux-go/tree/master/workspace) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/workspace) | A module of its own. Loads tmuxp-style YAML workspaces and builds them, with a CLI. |
| `mcp` | [`mcp/`](https://github.com/libtmux/libtmux-go/tree/master/mcp) | [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp) | A module of its own. A tmux server for AI agents, over the Model Context Protocol. |

### [`tmux`](https://github.com/libtmux/libtmux-go/tree/master/tmux): this package

Sessions, windows, panes, options, hooks, formats, filters, snapshots, and
plans, with no dependency outside the standard library. Reference:
[pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux) and
[libtmux.org](https://libtmux.org/en/go/latest/reference/).

[`Session.Run`][tmux-session-run] runs a command in a window of its own, with
a terminal. A nonzero exit status is a result, not an error:

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
package whose `TestMain` calls [`tmuxtest.Main`][tmuxtest-main]:

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

Install the command:

```console
$ go install github.com/libtmux/libtmux-go/workspace/cmd/tmux-workspace@latest
```

With a [workspace file][workspace-file] saved as `project.yaml`, build it on a
tmux socket named `project` without attaching to it:

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
[libtmux.org](https://libtmux.org/en/go/latest/mcp/reference/), and the [tool
reference](https://libtmux.org/en/go/latest/mcp/tools/).

Install the command:

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

An MCP client starts the server itself and speaks to it over stdin and stdout.
Register it with Claude Code:

```console
$ claude mcp add tmux -- libtmux-mcp
```

Check which socket it reaches, and what that holds, without a client:

```console
$ libtmux-mcp -doctor
```

## See also

The package documentation is written to be read start to finish. It covers
the task index, method-naming rules, snapshots and identity, execution
bindings, plans, filters, and the compatibility window:

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, method, and field, one page each |
| [`tmuxtest/`](tmuxtest/) | A real tmux server for your tests |
| [`../tmuxq/`](../tmuxq/) | Filtering helpers for the records this package returns |
| [`../examples/pane-io`](../examples/pane-io) | Type into a pane and wait for what the command prints, not for what you typed |
| [`../examples/`](../examples/) | Runnable programs |
| [`../DESIGN.md`](../DESIGN.md) | Why the surface looks like this |
| [`../BENCHMARKS.md`](../BENCHMARKS.md) | What each transport costs |

[libtmux-mcp-command]: https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp
[tmux-commandresult-exitcode]: https://libtmux.org/en/go/latest/reference/tmux-commandresult-exitcode/
[tmux-errnotfound]: https://libtmux.org/en/go/latest/reference/tmux-errnotfound/
[tmux-newserver]: https://libtmux.org/en/go/latest/reference/tmux-newserver/
[tmux-package]: https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux
[tmux-pane-capture]: https://libtmux.org/en/go/latest/reference/tmux-pane-capture/
[tmux-pane-kill]: https://libtmux.org/en/go/latest/reference/tmux-pane-kill/
[tmux-pane]: https://libtmux.org/en/go/latest/reference/tmux-pane/
[tmux-server-cmd]: https://libtmux.org/en/go/latest/reference/tmux-server-cmd/
[tmux-server-snapshot]: https://libtmux.org/en/go/latest/reference/tmux-server-snapshot/
[tmux-server]: https://libtmux.org/en/go/latest/reference/tmux-server/
[tmux-serveroptions-configfile]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-configfile/
[tmux-serveroptions-socketname]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-socketname/
[tmux-serveroptions-socketpath]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-socketpath/
[tmux-session-newwindow]: https://libtmux.org/en/go/latest/reference/tmux-session-newwindow/
[tmux-session-options]: https://libtmux.org/en/go/latest/reference/tmux-session-options/
[tmux-session-rawoption]: https://libtmux.org/en/go/latest/reference/tmux-session-rawoption/
[tmux-session-refresh]: https://libtmux.org/en/go/latest/reference/tmux-session-refresh/
[tmux-session-rename]: https://libtmux.org/en/go/latest/reference/tmux-session-rename/
[tmux-session-run]: https://libtmux.org/en/go/latest/reference/tmux-session-run/
[tmux-session-searchwindows]: https://libtmux.org/en/go/latest/reference/tmux-session-searchwindows/
[tmux-session-setmouse]: https://libtmux.org/en/go/latest/reference/tmux-session-setmouse/
[tmux-session-setoption]: https://libtmux.org/en/go/latest/reference/tmux-session-setoption/
[tmux-session-windows]: https://libtmux.org/en/go/latest/reference/tmux-session-windows/
[tmux-session]: https://libtmux.org/en/go/latest/reference/tmux-session/
[tmux-window-kill]: https://libtmux.org/en/go/latest/reference/tmux-window-kill/
[tmux-window-link]: https://libtmux.org/en/go/latest/reference/tmux-window-link/
[tmux-window-splitpane]: https://libtmux.org/en/go/latest/reference/tmux-window-splitpane/
[tmux-window]: https://libtmux.org/en/go/latest/reference/tmux-window/
[tmuxq-first]: https://libtmux.org/en/go/latest/reference/tmuxq-first/
[tmuxq-where]: https://libtmux.org/en/go/latest/reference/tmuxq-where/
[tmuxtest-main]: https://libtmux.org/en/go/latest/reference/tmuxtest-main/
[tmuxtest-newserver]: https://libtmux.org/en/go/latest/reference/tmuxtest-newserver/
[topic-errors]: https://libtmux.org/en/go/latest/topics/errors-and-exceptions/
[topic-options-and-hooks]: https://libtmux.org/en/go/latest/topics/options-and-hooks/
[workspace-file]: https://github.com/libtmux/libtmux-go/tree/master/workspace#a-workspace-file
