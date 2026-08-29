# tmux

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/tmux.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/tmux)

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

The library. A typed, context-aware tmux API with no runtime dependencies.

```console
$ go get github.com/libtmux/libtmux-go/tmux
```

```go
import "github.com/libtmux/libtmux-go/tmux"
```

## Guessing a method

Three rules cover most of the surface, so a tmux command usually leads to its Go
method without a search.

**The name drops the receiver's noun**, because the receiver already carries it:

| tmux | Go |
| --- | --- |
| `kill-pane` | `Pane.Kill` |
| `rename-session` | `Session.Rename` |
| `link-window` | `Window.Link` |
| `split-window` | `Window.SplitPane` |

A noun naming a *different* object always stays, which is why
`Session.NewWindow` and `Window.SplitPane` keep theirs.

**The parameters say what is required.** A method takes only a context when the
receiver names everything (`Window.Kill`); typed positional values when every
value is required (`Session.Rename`); and a request value when any field is
optional (`Pane.Capture`).

**The result says what changed.** A method hands back a freshly materialized
record when the command changes which object you are holding or what it looks
like. Everything else returns only an error.

## The object model

`Server` is an immutable configuration handle. `NewServer` validates its
options, snapshots the effective environment and working directory, and
resolves one absolute executable and effective socket path without starting tmux. A nil
`ProcessEnvironment` snapshots the current process environment; supplied
values are cloned. Named and default sockets use a canonical frozen
`TMUX_TMPDIR`. Construction returns an error for invalid options or an unresolved
executable. The zero `Server` is invalid, and its operations return
`ErrInvalidServer`. `Session` holds `Window` views, each holding `Pane` views.

Returned values are **records, not live handles**. A `Session` you hold is what
tmux said when you asked. Nothing refreshes behind you; `Session.Refresh` and its
counterparts get you a new one.

```go
server, err := tmux.NewServer(tmux.ServerOptions{SocketName: "my-app"})
if err != nil {
	return err
}

session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
if err != nil {
	return err
}

name, ok := session.Name()      // from the record, no tmux call
windows, err := session.SearchWindows(ctx, nil)   // asks tmux
```

`session.Windows()` returns what the record already holds and never queries;
`session.SearchWindows(ctx, nil)` asks tmux. The naming is the difference.

## Options and hooks

Every tmux option and hook has a typed accessor, and the tmux name maps to the
Go name by one rule (see "Finding an option or hook" in the package docs):

```go
options, err := session.Options(ctx)
if err != nil {
	return err
}
base, ok := options.BaseIndex()

session, err = session.SetMouse(ctx, true)
```

For a name outside the catalog, `RawOption` and `SetOption` take strings.

Runnable: [`../examples/option-hook-editing`](../examples/option-hook-editing).

## Errors

Failures are classified by what tmux refused, so a missing target and a
misspelled option are different checks:

```go
if _, err := server.Session(ctx, id); errors.Is(err, tmux.ErrNotFound) {
	// tmux has no such session
}
```

A completed tmux command that failed is data, not an error, when you use
`Server.Cmd`: `CommandResult.ExitCode` carries it. Transport and validation
failures are Go errors.

## Waiting for a pane

This has its own section in the package documentation, and it is worth reading
before writing a poll loop: a shell echoes the command you sent, so searching the
screen for what you are waiting for finds your own request. Three approaches, in
the order worth reaching for them, are documented under "Waiting for a pane".

## Reading further

```console
$ go doc github.com/libtmux/libtmux-go/tmux
```

The package documentation is the reference and is written to be read start to
finish. It covers the task index, method-naming rules, snapshots and identity,
execution bindings, plans, filters, and the compatibility window.

| | |
| --- | --- |
| [`tmuxtest/`](tmuxtest/) | A real tmux server for your tests |
| [`../examples/`](../examples/) | Runnable programs |
| [`../DESIGN.md`](../DESIGN.md) | Why the surface looks like this |
| [`../BENCHMARKS.md`](../BENCHMARKS.md) | What each transport costs |
