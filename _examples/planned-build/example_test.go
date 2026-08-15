// Package planned_build_test shows the chained mode: tmux commands recorded
// rather than run, read before they are sent, then sent together.
//
// Two things make it worth the indirection. A step can name what an earlier
// step is going to create, so a build is written in one pass instead of
// stopping at each split to learn a pane ID. And the commands that need no
// answer travel in one tmux command list, so the window below is built in
// fewer invocations than it has operations.
package planned_build_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tmux "github.com/libtmux/libtmux-go"
	"github.com/libtmux/libtmux-go/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestPlannedBuild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v), want one session", sessions, err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr("planned")})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}

	// The whole build, recorded in one pass. The split has not run, so the pane
	// it returns does not exist -- but it can already be named, which is what
	// lets the steps after it be written here rather than after a round trip
	// that learns its ID.
	plan := tmux.NewPlan()
	plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
	editor := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
	plan.SetPaneTitle(editor, "editor")
	plan.SendKeys(editor, tmux.SendKeysRequest{Command: tmux.Ptr("echo built")})
	plan.DisplayMessage(editor, "#{pane_title}")

	// Nothing has been sent yet. Preview renders the argument vectors, leaving
	// a step nil when it names a pane no earlier step has created here -- which
	// is every step that named the split. Anything else it cannot render is a
	// mistake in the plan, and comes back as an error rather than a nil entry,
	// which is the point of reading a plan before sending it.
	preview, err := plan.Preview(tmux.Version{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	for index, argv := range preview {
		if argv == nil {
			t.Logf("step %d: rendered when the split has reported its pane", index)
			continue
		}
		t.Logf("step %d: tmux %s", index, strings.Join(argv, " "))
	}

	// Explain reports the grouping ahead of time, and why each group ends. Two
	// reasons end one early: a command whose new object's ID a later step needs,
	// and a command whose output the caller reads. tmux answers a command list
	// with one merged stdout, so neither can share it.
	dispatches := plan.Explain()
	for _, dispatch := range dispatches {
		t.Logf("tmux invocation carrying steps %v, ends because it %s",
			dispatch.Ops, dispatch.Reason)
	}
	if len(dispatches) >= plan.Len() {
		t.Errorf("%d operations took %d tmux invocations, want fewer than one each",
			plan.Len(), len(dispatches))
	}

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("Run() did not complete: %v", result.Err())
	}

	// One result per recorded operation, whatever it shared an invocation with.
	// There are more of them than there were calls above: SendKeys with a
	// command records the keys and the Enter that submits them, because that is
	// the two tmux commands Pane.SendKeys sends.
	//
	// The split reports the pane it created; the read reports what it printed.
	for index, op := range result.Ops {
		t.Logf("step %d %-16s %-8s created %q stdout %q",
			index, op.Command, op.Status, op.Created, op.Stdout)
	}
	if result.Ops[1].Created == "" {
		t.Error("the split reported no pane ID")
	}
	read := result.Ops[len(result.Ops)-1]
	if got := read.Stdout; len(got) != 1 || got[0] != "editor" {
		t.Errorf("the read reported %q, want the title the plan set", got)
	}
}
