# planned-build

Commands recorded rather than run, read before they are sent, then sent
together.

The indirection pays for itself. A step can name what an earlier step is
*going* to create, so a build is written in one pass instead of stopping at
each split to learn a pane ID. And commands needing no answer travel in one tmux
command list, so the window is built in fewer invocations than it has
operations.

## Running it

```console
$ go -C examples run ./planned-build
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/planned-build/output.txt -->

```text
step 0: tmux select-layout -t @1 tiled
step 1: tmux split-window -t @1 -v -P -F#{pane_id}
step 2: rendered when the split has reported its pane
step 3: rendered when the split has reported its pane
step 4: rendered when the split has reported its pane
step 5: rendered when the split has reported its pane
tmux invocation carrying steps [0], ends because it alone
tmux invocation carrying steps [1], ends because it creates
tmux invocation carrying steps [2 3 4], ends because it chained
tmux invocation carrying steps [5], ends because it captures
operations: 6, tmux invocations: 4
step 0 select-layout    complete created "" stdout []
step 1 split-window     complete created "%2" stdout []
step 2 select-pane      complete created "" stdout []
step 3 send-keys        complete created "" stdout []
step 4 send-keys        complete created "" stdout []
step 5 display-message  complete created "" stdout ["editor"]
```

<!-- docs:end -->

## The program

<!-- docs:file examples/planned-build/main.go -->

```go
// Command planned-build records tmux commands, uses forward references, and
// groups compatible operations into fewer invocations.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
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
		SocketName: "libtmux-planned-build",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-planned"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: new("planned")})
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	// The split's forward reference is usable before tmux reports its pane ID.
	plan := tmux.NewPlan()
	plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
	editor := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
	plan.SetPaneTitle(editor, "editor")
	plan.SendKeys(editor, tmux.SendKeysRequest{Command: new("echo built")})
	plan.DisplayMessage(editor, "#{pane_title}")

	// Preview leaves unresolved forward-reference steps nil; other rendering
	// failures return an error.
	preview, err := plan.Preview(tmux.Version{})
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	for index, argv := range preview {
		if argv == nil {
			fmt.Printf("step %d: rendered when the split has reported its pane\n", index)
			continue
		}
		fmt.Printf("step %d: tmux %s\n", index, strings.Join(argv, " "))
	}

	// Groups end when later steps need a created ID or when merged stdout would
	// prevent result attribution.
	dispatches := plan.Explain()
	for _, dispatch := range dispatches {
		fmt.Printf("tmux invocation carrying steps %v, ends because it %s\n",
			dispatch.Ops, dispatch.Reason)
	}
	if len(dispatches) >= plan.Len() {
		return fmt.Errorf("%d operations took %d tmux invocations, want fewer than one each",
			plan.Len(), len(dispatches))
	}
	fmt.Printf("operations: %d, tmux invocations: %d\n", plan.Len(), len(dispatches))

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
	if result.Ops[1].Created == "" {
		return errors.New("the split reported no pane ID")
	}
	read := result.Ops[len(result.Ops)-1]
	if got := read.Stdout; len(got) != 1 || got[0] != "editor" {
		return fmt.Errorf("the read reported %q, want the title the plan set", got)
	}
	return nil
}
```

<!-- docs:end -->

## What to look at

**A pane named before it exists.** The split has not run, so the pane it returns
is not real yet — but it can already be named, which is what lets every step
after it be written here rather than after a round trip.

**Preview renders what will be sent.** A step naming a pane no earlier step
created here comes back nil, and that is not an error: it is the plan saying
this one renders once the split reports. Anything it genuinely cannot render is
an error, which is the point of reading a plan before sending it.

**Explain says why each group ends.** A group ends early at a command whose
new object's ID a later step needs, and at a command whose output is read.
tmux answers a command list with one merged stdout, so neither can share it.

**More results than calls.** `SendKeys` with a command records the keys and the
Enter that submits them, because that is the two tmux commands it sends.

## Running its test

```console
$ go -C examples test ./planned-build
```
