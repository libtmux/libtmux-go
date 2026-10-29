# environment

Session environment variables, and finding the pane a program is running in.

Two halves of the same idea: what tmux stores against a session, and what tmux
tells a process about where it is.

## Running it

```console
$ go -C examples run ./environment
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/environment/output.txt -->

```text
ready true
```

<!-- docs:end -->

`ready` is the value set and read back. `true` is the pane rediscovered from a
`TMUX` environment matching the pane the example split.

## The program

<!-- docs:file examples/environment/main.go -->

```go
// Command environment demonstrates session environment access and pane discovery.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

// start owns cleanup because log.Fatal skips deferred calls in main.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-environment",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-environment"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: new("discovery")})
	if err != nil {
		return err
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		return err
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_EXAMPLE", "ready", tmux.SetEnvironmentOptions{}); err != nil {
		return err
	}
	value, present, err := session.GetEnvironment(ctx, "LIBTMUX_EXAMPLE")
	if err != nil || !present {
		return errors.Join(err, errors.New("session environment value is absent"))
	}

	result, err := server.Cmd(ctx, "display-message", "-p", "-t", pane.ID().String(), "#{socket_path}")
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
		return errors.Join(err, errors.New("resolve tmux socket path"))
	}
	environment := map[string]string{
		"TMUX":      fmt.Sprintf("%s,1,%s", result.Stdout[0], session.ID()),
		"TMUX_PANE": pane.ID().String(),
	}
	discovered, err := tmux.PaneFromEnv(ctx, environment)
	if err != nil {
		return err
	}
	fmt.Println(value.Value, discovered.ID() == pane.ID())
	return nil
}
```

<!-- docs:end -->

## What to look at

**Present is not the same as empty.** `GetEnvironment` reports whether the
variable is there separately from its value, because tmux distinguishes a
variable set to nothing from one that was never set.

**`PaneFromEnv` is how a program finds itself.** tmux gives every process it
starts `TMUX` and `TMUX_PANE`. Handing those to `PaneFromEnv` returns the pane
the program is running in, without being told which one that is.

**That is why the harness scrubs them.** A test inheriting `TMUX` from the
terminal it was launched in would find the developer's pane instead of its own,
so `tmuxtest` removes those variables from the environment it hands tmux.

## Running its test

```console
$ go -C examples test ./environment
```
