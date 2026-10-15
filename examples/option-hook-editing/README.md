# option-hook-editing

Options and hooks as typed accessors rather than strings.

Every tmux option and every hook has a generated accessor with the Go type the
option actually holds, so a wrong value is a compile error instead of a runtime
complaint from tmux.

## Running it

```console
$ go -C examples run ./option-hook-editing
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/option-hook-editing/output.txt -->

```text
mouse: true
@project: libtmux
client-attached hook present: true
```

<!-- docs:end -->

## The program

<!-- docs:file examples/option-hook-editing/main.go -->

```go
// Command option-hook-editing demonstrates typed option and hook updates.
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

	// A private server: a socket and an empty config of its own, so this
	// neither touches nor is changed by a tmux you already use.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-option-hook-editing",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-options"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	if err := session.SetMouse(ctx, true); err != nil {
		return err
	}
	options, err := session.Options(ctx)
	if err != nil {
		return err
	}
	mouse, _ := options.Mouse().Get()
	fmt.Println("mouse:", mouse)

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
	status, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "#[align=left]#{session_name}"},
		tmux.SparseEntry[string]{Index: 2, Value: "#[align=right]#{window_name}"},
	)
	if err != nil {
		return err
	}
	if _, err := session.SetStatusFormat(ctx, status); err != nil {
		return err
	}

	global := server.GlobalSessionScope()
	if err := global.SetStatus(ctx, tmux.StatusOn); err != nil {
		return err
	}
	if err := global.SetHook(ctx, "client-attached", "display-message 'client attached'"); err != nil {
		return err
	}
	hooks, err := global.Hooks(ctx)
	if err != nil {
		return err
	}
	_, present := hooks.ClientAttached().Get()
	fmt.Println("client-attached hook present:", present)
	return nil
}
```

<!-- docs:end -->

## What to look at

**Scopes are separate objects.** `session.SetMouse` writes to that session;
`server.GlobalSessionScope()` writes the global value every session inherits.
Which one you hold decides what you changed, so it is a different receiver
rather than a flag.

**A status line is a sparse array.** tmux indexes status format slots, and they
need not be contiguous — this sets 0 and 2 and leaves 1 alone. `NewSparseArray`
carries that shape instead of flattening it into a slice with a hole.

**Hooks read back typed too.** `Hooks()` returns accessors, so
`ClientAttached()` is a method rather than a map lookup on a string that might
be misspelled.

**Present, not just non-empty.** The accessor reports whether the hook is set
separately from what it is set to.

## Testing your own version

```console
$ go -C examples test ./option-hook-editing
```

The test asserts `true`: `false` would mean the write was accepted and the read
did not find it, which is the failure worth catching.
