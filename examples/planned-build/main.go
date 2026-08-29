// Command planned-build records tmux commands, uses forward references, and
// groups compatible operations into fewer invocations.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	server := tmux.NewServer(tmux.ServerOptions{})
	return run(ctx, server)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server) (err error) {
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-planned"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr("planned")})
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	// The split's forward reference is usable before tmux reports its pane ID.
	// docs:planning
	plan := tmux.NewPlan()
	plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
	editor := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
	plan.SetPaneTitle(editor, "editor")
	plan.SendKeys(editor, tmux.SendKeysRequest{Command: tmux.Ptr("echo built")})
	plan.DisplayMessage(editor, "#{pane_title}")
	// docs:end

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
