# tmux

A typed Go wrapper for [tmux], with no runtime dependencies.

```console
$ go get github.com/libtmux/libtmux-go
```

The import needs a name, because the path ends in the repository name and the
package is called `tmux`:

```go
import tmux "github.com/libtmux/libtmux-go"
```

```go
server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-app"})
session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
if err != nil {
    return err
}
window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Command: "top"})
```

The package documentation is the reference, and is written to be read
start to finish rather than searched:

```console
$ go doc github.com/libtmux/libtmux-go
```

It opens with a task index, then the rule that maps a tmux command to its Go
method, so a command usually leads to its method without a lookup: `kill-pane`
is `Pane.Kill`, `rename-session` is `Session.Rename`.

## What it gives you

Sessions, windows, and panes as records that never refresh behind your back;
every tmux option and hook as a typed accessor; searches that run inside tmux
or in Go; and errors classified by what tmux actually refused, so a missing
target and a misspelled option are different `errors.Is` checks.

Reading a pane back, and waiting for one, are their own topic — a shell echoes
the command you sent, so searching the screen for what you are waiting for
finds your own request. See "Waiting for a pane" in the package documentation.

## Going faster

Every command starts a tmux process by default. How they are carried, whether
they are batched, and how many run at once are three independent switches, each
one visible line to turn on and one to take back. "Choosing a mode" in the
package documentation is the table of all five, with what each costs and when
to reach for it.

A control-mode connection carries commands without starting a process for each:

```go
connected, session, pool, err := server.OpenControlPool(
    ctx, session, tmux.ControlPoolRequest{},
)
```

That connection is a tmux client while it is open: it appears in
`list-clients`, counts toward `session_attached`, and fires a `client-attached`
hook. That is why it is chosen rather than automatic.

A plan is the other axis. It records commands instead of running them, sends
the ones that need no answer to tmux together, and hands back a reference to
what a step is going to create, so a build is written in one pass:

```go
plan := tmux.NewPlan()
pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
plan.SendKeys(pane, tmux.SendKeysRequest{Command: tmux.Ptr("top")})
result, err := plan.Run(ctx, server)
```

## Testing against it

The `tmuxtest` package gives tests a real tmux server that cleans itself up.
`ServerOptions.Runner` replaces process execution entirely, so code that drives
tmux can be unit tested on a machine without it.

## In this repository

| Path | What it is |
| --- | --- |
| [`workspace/`](workspace/) | Loads tmuxp-style YAML workspaces and builds them |
| [`mcp/`](mcp/) | Serves one tmux server to Model Context Protocol clients |
| [`DESIGN.md`](DESIGN.md) | The conventions this package holds itself to |
| [`PARITY.md`](PARITY.md) | How the surface is checked against libtmux |
| [`BENCHMARKS.md`](BENCHMARKS.md) | What each way of reaching tmux costs |

Both consumers are separate modules, so `go get` on this one pulls in neither.

[tmux]: https://github.com/tmux/tmux
