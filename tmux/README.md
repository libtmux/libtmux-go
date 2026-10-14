<!-- libtmux-logo -->
<p align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go">
  </picture>
</p>
<!-- /libtmux-logo -->

<div align="center">

# tmux

The library. A typed, context-aware tmux API with no runtime dependencies.

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)

</div>

```console
$ go get github.com/libtmux/libtmux-go/tmux@latest
```

**Contents** — [Records, not live handles](#records-not-live-handles) ·
[Guessing a method](#guessing-a-method) ·
[Options and hooks](#options-and-hooks) · [Errors](#errors) ·
[Waiting for a pane](#waiting-for-a-pane) ·
[Reading further](#reading-further)

New here? The [top-level README](../README.md) opens with a program that runs
a command in tmux and prints what it showed. This page is about how the
library is shaped.

## Records, not live handles

A `Server` reaches one tmux server. It holds `Session` values, each holding
`Window` values, each holding `Pane` values, and every one of them is a record:
what tmux said when you asked.

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
hold, or what it looks like, hands back a new record, as `Session.Rename` does
here; [`Session.Refresh`][tmux-session-refresh] and its counterparts ask tmux
again when you want one.
The name says which kind of read you are making: `session.Windows()` returns
what the record already holds and never queries, while
`session.SearchWindows(ctx, nil)` asks tmux.

[`NewServer`][tmux-newserver] validates its configuration and starts nothing.
With no [`SocketName`][tmux-serveroptions-socketname] or
[`SocketPath`][tmux-serveroptions-socketpath] it reaches the server your own
`tmux` command does, and with no [`ConfigFile`][tmux-serveroptions-configfile]
tmux reads your configuration, so a program
that should behave the same everywhere names both.
[`../examples/object-model`](../examples/object-model) is the whole program
these blocks come from, and it does.

## Guessing a method

Three rules cover most of the surface, so a tmux command usually leads to its Go
method without a search.

**The name drops the receiver's noun**, because the receiver already carries it:

| tmux | Go |
| --- | --- |
| `kill-pane` | [`Pane.Kill`][tmux-pane-kill] |
| `rename-session` | [`Session.Rename`][tmux-session-rename] |
| `link-window` | [`Window.Link`][tmux-window-link] |
| `split-window` | [`Window.SplitPane`][tmux-window-splitpane] |

A noun naming a *different* object always stays, which is why
`Session.NewWindow` and `Window.SplitPane` keep theirs.

**The parameters say what is required.** A method takes only a context when the
receiver names everything (`Window.Kill`); typed positional values when every
value is required (`Session.Rename`); and a request value when any field is
optional (`Pane.Capture`).

**The result says what changed.** A method hands back a freshly materialized
record when the command changes which object you are holding or what it looks
like. Everything else returns only an error.

## Options and hooks

Every tmux option and hook has a typed accessor, and the tmux name maps to the
Go name by one rule, given under "Naming and call shapes" in the package
documentation. A setter returns only an error, because an option is not part
of the record you hold; read it back through `Options`:

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

For a name outside the catalog, `RawOption` and `SetOption` take strings.

Runnable: [`../examples/option-hook-editing`](../examples/option-hook-editing).

## Errors

Failures are classified by what tmux refused, so a missing target and a
misspelled option are different checks:

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

A completed tmux command that failed is data, not an error, when you use
[`Server.Cmd`][tmux-server-cmd]: [`CommandResult.ExitCode`][tmux-commandresult-exitcode]
carries it. Transport and validation
failures are Go errors.

## Waiting for a pane

Worth reading before writing a poll loop: a terminal echoes the command you
typed, so searching the screen for what you are waiting for finds your own
request. Wait for text the command prints and the typed line does not contain.
[`../examples/pane-io`](../examples/pane-io) does that with
[`PaneObservation.WaitFor`][tmux-paneobservation-waitfor], which tmux wakes as
the pane writes, and "Execution
modes" in the package documentation covers the other ways to wait, in the
order worth reaching for them.

## Reading further

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

The package documentation is the reference and is written to be read start to
finish. It covers the task index, method-naming rules, snapshots and identity,
execution bindings, plans, filters, and the compatibility window.

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/reference/) | Every exported identifier, method, and field, one page each |
| [`tmuxtest/`](tmuxtest/) | A real tmux server for your tests |
| [`../tmuxq/`](../tmuxq/) | Filtering helpers for the records this package returns |
| [`../examples/`](../examples/) | Runnable programs |
| [`../DESIGN.md`](../DESIGN.md) | Why the surface looks like this |
| [`../BENCHMARKS.md`](../BENCHMARKS.md) | What each transport costs |

[tmux-commandresult-exitcode]: https://libtmux.org/en/go/latest/reference/tmux-commandresult-exitcode/
[tmux-newserver]: https://libtmux.org/en/go/latest/reference/tmux-newserver/
[tmux-pane-kill]: https://libtmux.org/en/go/latest/reference/tmux-pane-kill/
[tmux-paneobservation-waitfor]: https://libtmux.org/en/go/latest/reference/tmux-paneobservation-waitfor/
[tmux-server-cmd]: https://libtmux.org/en/go/latest/reference/tmux-server-cmd/
[tmux-serveroptions-configfile]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-configfile/
[tmux-serveroptions-socketname]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-socketname/
[tmux-serveroptions-socketpath]: https://libtmux.org/en/go/latest/reference/tmux-serveroptions-socketpath/
[tmux-session-refresh]: https://libtmux.org/en/go/latest/reference/tmux-session-refresh/
[tmux-session-rename]: https://libtmux.org/en/go/latest/reference/tmux-session-rename/
[tmux-window-link]: https://libtmux.org/en/go/latest/reference/tmux-window-link/
[tmux-window-splitpane]: https://libtmux.org/en/go/latest/reference/tmux-window-splitpane/
