package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// TestPlanTargetsWhatItHasNotCreatedYet is the gate on forward references.
//
// A plan is worth having only if a step can address what an earlier step is
// going to create, because that is what lets a build be written in one pass.
// Here a split is recorded, then keys are sent to the pane it will create and a
// format is read back from it, all before tmux has been asked for anything.
//
//libtmux:real-tmux
func TestPlanTargetsWhatItHasNotCreatedYet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatal(err)
	}

	plan := tmux.NewPlan()
	pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
	plan.RenameWindow(window.Ref(), "planned")
	plan.DisplayMessage(pane, "#{pane_id}")

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("Run() did not complete: %v", result.Err())
	}

	created := result.Ops[0].Created
	if created == "" {
		t.Fatal("the split reported no pane ID")
	}
	// The message step resolved the ref to the pane the split actually made.
	if got := result.Ops[2].Stdout; len(got) != 1 || got[0] != created {
		t.Errorf("display-message through the forward ref = %q, want [%q]", got, created)
	}

	panes, err := server.SearchPanes(ctx, nil)
	if err != nil {
		t.Fatalf("SearchPanes() error = %v", err)
	}
	found := false
	for _, candidate := range panes {
		if string(candidate.ID()) == created {
			found = true
		}
	}
	if !found {
		t.Errorf("pane %q the plan created is not on the server", created)
	}
}

// TestPlanExplainsItsDispatchesBeforeRunning gates the promise that a plan can
// be read before it is run: what would be sent, and how it would be grouped,
// with no server involved.
func TestPlanExplainsItsDispatchesBeforeRunning(t *testing.T) {
	t.Parallel()

	plan := tmux.NewPlan()
	pane := plan.SplitPane(tmux.WindowRef("@1"), tmux.SplitPaneRequest{})
	plan.RenameWindow(tmux.WindowRef("@1"), "one")
	plan.SelectLayout(tmux.WindowRef("@1"), tmux.SelectLayoutRequest{Layout: "tiled"})
	plan.DisplayMessage(pane, "#{pane_id}")

	dispatches := plan.Explain()
	want := []struct {
		count  int
		reason string
	}{
		{count: 1, reason: "creates"},  // its ID is what the last step targets
		{count: 2, reason: "chained"},  // neither answers, so both travel together
		{count: 1, reason: "captures"}, // its output has to be told apart
	}
	if len(dispatches) != len(want) {
		t.Fatalf("Explain() returned %d dispatches, want %d: %+v", len(dispatches), len(want), dispatches)
	}
	for index, expected := range want {
		if len(dispatches[index].Ops) != expected.count || dispatches[index].Reason != expected.reason {
			t.Errorf(
				"dispatch %d = %d operations, reason %q; want %d, %q",
				index, len(dispatches[index].Ops), dispatches[index].Reason,
				expected.count, expected.reason,
			)
		}
	}

	// Four operations, three tmux commands, and the plan says so without
	// having run anything.
	if plan.Len() != 4 {
		t.Errorf("Len() = %d, want 4", plan.Len())
	}
}

// TestPlanPreviewLeavesUnresolvedStepsNil gates Preview reporting what it
// cannot know rather than inventing it: a step targeting an object an earlier
// step will create has no argument vector until the plan runs.
func TestPlanPreviewLeavesUnresolvedStepsNil(t *testing.T) {
	t.Parallel()

	window := tmux.WindowRef("@1")
	plan := tmux.NewPlan()
	plan.RenameWindow(window, "named")
	pane := plan.SplitPane(window, tmux.SplitPaneRequest{})
	plan.DisplayMessage(pane, "#{pane_id}")

	preview, err := plan.Preview(tmux.Version{})
	if err != nil {
		t.Fatalf("Preview() error = %v, want a plan that renders", err)
	}
	if len(preview) != 3 {
		t.Fatalf("Preview() returned %d entries, want 3", len(preview))
	}
	if preview[0] == nil || preview[1] == nil {
		t.Errorf("steps with known targets did not render: %q", preview)
	}
	if preview[2] != nil {
		t.Errorf("step targeting an uncreated pane rendered as %q, want nil", preview[2])
	}
}

// TestPlanRefusesTheZeroRef gates the zero value addressing nothing.
//
// A Ref is either an object that exists or the one a numbered step will create,
// and both are produced by a constructor. The zero value is neither, so a plan
// holding one refuses rather than resolving it to the first step, which is what
// a zero-based step index would have made it silently mean.
func TestPlanRefusesTheZeroRef(t *testing.T) {
	t.Parallel()

	plan := tmux.NewPlan()
	plan.RenameWindow(tmux.Ref{}, "nowhere")

	var refused *tmux.PlanError
	if _, err := plan.Preview(tmux.Version{}); !errors.As(err, &refused) ||
		!errors.Is(err, tmux.ErrPlan) {
		t.Errorf("Preview() error = %v, want a plan error naming the step", err)
	}
	if got := (tmux.Ref{}).String(); got != "{no target}" {
		t.Errorf("Ref{}.String() = %q", got)
	}
}

// TestPlanStopsAtTheFirstFailure gates the status a caller reads back.
//
// tmux abandons a command list at its first failure, so a plan cannot pretend
// the rest ran. The operations after a failure are skipped, and they are
// reported as skipped rather than as failures of their own.
//
//libtmux:real-tmux
func TestPlanStopsAtTheFirstFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatal(err)
	}

	plan := tmux.NewPlan()
	plan.RenameWindow(window.Ref(), "before")
	plan.SendKeys(tmux.PaneRef("%999"), tmux.SendKeysRequest{Command: tmux.Ptr("nope")})
	plan.RenameWindow(window.Ref(), "after")

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() returned a transport error: %v", err)
	}
	if result.OK() {
		t.Fatal("Run() reported a plan targeting a missing pane as complete")
	}
	if result.Err() == nil {
		t.Error("Err() reported no failure")
	}

	statuses := []tmux.OpStatus{
		result.Ops[0].Status,
		result.Ops[1].Status,
		result.Ops[2].Status,
	}
	// All three chain into one dispatch, so tmux blames the dispatch and the
	// plan blames its first operation; nothing after it ran.
	if statuses[2] != tmux.OpSkipped {
		t.Errorf("operation after the failure = %v, want skipped", statuses[2])
	}
	t.Logf("statuses = %v", statuses)

	name, ok := window.Name()
	t.Logf("window name after the failed plan = %q (%t)", name, ok)
}

// TestPlanRunsIdenticallyOnBothTransports is the equivalence gate for plans.
//
// A plan is one of the switches a caller flips, so it has to mean the same
// thing whichever transport carries it. The results compared here are the whole
// point: only the cost is allowed to differ.
//
//libtmux:real-tmux
func TestPlanRunsIdenticallyOnBothTransports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	build := func(handle tmux.Server, name string) []string {
		t.Helper()
		window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr(name)})
		if err != nil {
			t.Fatal(err)
		}
		plan := tmux.NewPlan()
		pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
		plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
		plan.RenameWindow(window.Ref(), name+"-built")
		plan.DisplayMessage(pane, "#{window_name} #{pane_index}")

		result, err := plan.Run(ctx, handle)
		if err != nil {
			t.Fatalf("Run() over %v error = %v", handle.Engine(), err)
		}
		if !result.OK() {
			t.Fatalf("Run() over %v did not complete: %v", handle.Engine(), result.Err())
		}
		return result.Ops[3].Stdout
	}

	viaProcess := build(server, "process")
	viaControl := build(connected, "control")

	if len(viaProcess) != 1 || len(viaControl) != 1 {
		t.Fatalf("process = %q, control = %q", viaProcess, viaControl)
	}
	// The window names differ by design; the shape of the answer must not.
	if got, want := viaControl[0][len("control"):], viaProcess[0][len("process"):]; got != want {
		t.Errorf("plan answered %q over control and %q over processes", got, want)
	}
}

// TestPlanBuildsAWorkspaceInOnePass exercises the recorded operations the way a
// workspace builder uses them: a session, windows inside it, panes inside those,
// and commands typed into panes that did not exist when the plan was written.
//
// It is the coverage gate for the operation surface. Every step targets
// something an earlier step created, so a mistake in how a Ref resolves shows up
// as a tmux error rather than as a silently misplaced pane.
//
//libtmux:real-tmux
func TestPlanBuildsAWorkspaceInOnePass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	plan := tmux.NewPlan()
	session := plan.NewSession(tmux.NewSessionRequest{
		Name:       "planned-workspace",
		WindowName: "editor",
	})
	plan.RenameSession(session, "workspace")

	editor := plan.NewWindow(session, tmux.NewWindowRequest{Name: tmux.Ptr("editor")})
	left := plan.SplitPane(editor, tmux.SplitPaneRequest{Direction: tmux.PaneDirectionRight})
	plan.SelectLayout(editor, tmux.SelectLayoutRequest{Layout: "even-horizontal"})
	plan.SetPaneTitle(left, "logs")
	plan.SendKeys(left, tmux.SendKeysRequest{Command: tmux.Ptr("echo planned")})
	plan.ResizePane(left, tmux.ResizePaneRequest{Width: tmux.PaneCells(20)})
	plan.SelectPane(left, tmux.PaneSelectRequest{})

	shells := plan.NewWindow(session, tmux.NewWindowRequest{Name: tmux.Ptr("shells")})
	plan.SplitPane(shells, tmux.SplitPaneRequest{})
	plan.SelectLayout(shells, tmux.SelectLayoutRequest{Layout: "tiled"})
	plan.RenameWindow(shells, "shells-built")
	plan.SelectWindow(editor)
	plan.DisplayMessage(left, "#{pane_title} #{window_name}")

	dispatches := plan.Explain()
	t.Logf("%d operations in %d tmux invocations", plan.Len(), len(dispatches))

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		for index, op := range result.Ops {
			t.Logf("  %2d %-16s %-8s %v", index, op.Command, op.Status, op.Err)
		}
		t.Fatalf("Run() did not complete: %v", result.Err())
	}

	// Grouping must not have cost invocations rather than saved them.
	if len(dispatches) >= plan.Len() {
		t.Errorf("%d invocations for %d operations, want fewer", len(dispatches), plan.Len())
	}

	// The last step read back through refs created three steps earlier.
	answer := result.Ops[len(result.Ops)-1].Stdout
	if len(answer) != 1 || answer[0] != "logs editor" {
		t.Errorf("display-message = %q, want [\"logs editor\"]", answer)
	}

	built, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	names := make([]string, 0, len(built))
	for _, candidate := range built {
		name, _ := candidate.Name()
		names = append(names, name)
	}
	if !slices.Contains(names, "workspace") {
		t.Errorf("sessions = %q, want one named workspace", names)
	}
}

// TestPlanRecordsTheWiderSurface exercises the operations the workspace test
// does not: the ones naming two objects, the server-scoped ones, and the option
// and buffer writers. A plan that only ever names one object at a time would
// pass every other test here.
//
//libtmux:real-tmux
func TestPlanRecordsTheWiderSurface(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	session := sessions[0]

	plan := tmux.NewPlan()
	first := plan.NewWindow(session.Ref(), tmux.NewWindowRequest{Name: tmux.Ptr("first")})
	second := plan.NewWindow(session.Ref(), tmux.NewWindowRequest{Name: tmux.Ptr("second")})

	// Two windows the plan created, named in one command.
	plan.SwapWindow(first, second, true)

	// Panes, then a move that names two of them.
	left := plan.SplitPane(first, tmux.SplitPaneRequest{})
	right := plan.SplitPane(second, tmux.SplitPaneRequest{})
	plan.SwapPane(left, right, true, false)

	// Server-scoped writes, none of which name an object.
	plan.SetOption(tmux.Ref{}, tmux.SetPlanOptionRequest{
		Name: "@planned", Value: "yes", Global: true,
	})
	plan.SetBuffer("planned-buffer", "buffer contents")
	plan.SetEnvironment(session.Ref(), "PLANNED", "yes")
	plan.DisplayMessage(tmux.Ref{}, "#{@planned}")

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		for index, op := range result.Ops {
			t.Logf("  %2d %-16s %-8s %v", index, op.Command, op.Status, op.Err)
		}
		t.Fatalf("Run() did not complete: %v", result.Err())
	}

	// The untargeted read saw the untargeted write.
	answer := result.Ops[len(result.Ops)-1].Stdout
	if len(answer) != 1 || answer[0] != "yes" {
		t.Errorf("display-message = %q, want [\"yes\"]", answer)
	}

	value, ok, err := server.GlobalSessionScope().RawOption(ctx, "@planned")
	if err != nil || !ok || value != "yes" {
		t.Errorf("@planned = (%q, %t, %v)", value, ok, err)
	}
	// Read back from the session it was set on, not the server: the plan
	// targeted the session, and tmux keeps the two scopes apart.
	stored, ok, err := session.GetEnvironment(ctx, "PLANNED")
	if err != nil || !ok || stored.Value != "yes" {
		t.Errorf("PLANNED on the session = (%#v, %t, %v)", stored, ok, err)
	}
	buffer, err := server.ShowBuffer(ctx, tmux.Ptr("planned-buffer"))
	if err != nil || buffer != "buffer contents" {
		t.Errorf("buffer = (%q, %v)", buffer, err)
	}
}

// TestPlanCmdRecordsWhatHasNoRecorder gates the escape hatch.
//
// The recorders cover the commands this package wraps; Cmd covers the rest, and
// the point of it being part of a plan rather than a separate call is that a
// Ref still names what it acts on. Here it targets a pane the same plan created.
//
//libtmux:real-tmux
func TestPlanCmdRecordsWhatHasNoRecorder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatal(err)
	}

	plan := tmux.NewPlan()
	pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
	// No recorder wraps set-option -p; Cmd still reaches it, and still names
	// the pane the split above is going to create.
	plan.Cmd(pane, "set-option", "-p", "@raw", "reached")
	plan.CmdCapture(pane, "display-message", "-p", "#{@raw}")

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("Run() did not complete: %v", result.Err())
	}
	if got := result.Ops[2].Stdout; len(got) != 1 || got[0] != "reached" {
		t.Errorf("raw command through a forward ref = %q, want [\"reached\"]", got)
	}

	// The write shares a dispatch; the read cannot.
	reasons := make([]string, 0, 3)
	for _, dispatch := range plan.Explain() {
		reasons = append(reasons, dispatch.Reason)
	}
	if !slices.Equal(reasons, []string{"creates", "alone", "captures"}) {
		t.Errorf("dispatch reasons = %q", reasons)
	}
}

// TestPlannersAgreeOnResultsAndDifferOnCost is the gate on planners being
// interchangeable policy.
//
// A planner decides only how many times tmux is invoked. Running one plan
// through each must produce the same per-operation results, including the same
// captured output and the same created IDs, at a different dispatch count. If
// that ever stops holding, a planner is changing meaning rather than cost.
//
//libtmux:real-tmux
func TestPlannersAgreeOnResultsAndDifferOnCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}

	// Built fresh per planner, because running a plan changes the server.
	build := func() *tmux.Plan {
		window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
		if err != nil {
			t.Fatalf("NewWindow() error = %v", err)
		}
		plan := tmux.NewPlan()
		// The layout comes first so the split is followed only by operations
		// naming the pane it creates, which is what Marked folds and Folding
		// cannot.
		plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
		pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
		plan.SetPaneTitle(pane, "decorated")
		plan.Cmd(pane, "set-option", "-p", "@planner", "reached")
		plan.CmdCapture(pane, "display-message", "-p", "#{pane_title} #{@planner}")
		return plan
	}

	type outcome struct {
		dispatches int
		marked     int
		statuses   []tmux.OpStatus
		answer     string
	}
	outcomes := map[string]outcome{}
	for _, planner := range []struct {
		name  string
		value tmux.Planner
	}{
		{"Sequential", tmux.Sequential{}},
		{"Folding", tmux.Folding{}},
		{"Marked", tmux.Marked{}},
	} {
		plan := build()
		dispatches := plan.ExplainWith(planner.value)
		result, err := plan.RunWith(ctx, server, planner.value)
		if err != nil {
			t.Fatalf("%s: RunWith() error = %v", planner.name, err)
		}
		if !result.OK() {
			for index, op := range result.Ops {
				t.Logf("  %s %2d %-16s %-8s %v", planner.name, index, op.Command, op.Status, op.Err)
			}
			t.Fatalf("%s: RunWith() did not complete: %v", planner.name, result.Err())
		}
		statuses := make([]tmux.OpStatus, 0, len(result.Ops))
		for _, op := range result.Ops {
			statuses = append(statuses, op.Status)
		}
		answer := ""
		if got := result.Ops[len(result.Ops)-1].Stdout; len(got) == 1 {
			answer = got[0]
		}
		// The split is the second operation; its captured ID must survive the
		// fold, because the operations after it named the pane through it.
		if result.Ops[1].Created == "" {
			t.Errorf("%s: the split reported no pane ID", planner.name)
		}
		marked := 0
		for _, dispatch := range dispatches {
			if dispatch.Marked {
				marked++
			}
		}
		outcomes[planner.name] = outcome{len(dispatches), marked, statuses, answer}
		t.Logf("%-10s %d operations in %d tmux invocations, answer %q",
			planner.name, plan.Len(), len(dispatches), answer)
	}

	// Same meaning.
	for _, name := range []string{"Folding", "Marked"} {
		if !slices.Equal(outcomes[name].statuses, outcomes["Sequential"].statuses) {
			t.Errorf("%s statuses = %v, Sequential = %v",
				name, outcomes[name].statuses, outcomes["Sequential"].statuses)
		}
		if outcomes[name].answer != outcomes["Sequential"].answer {
			t.Errorf("%s answered %q, Sequential answered %q",
				name, outcomes[name].answer, outcomes["Sequential"].answer)
		}
	}
	if outcomes["Sequential"].answer != "decorated reached" {
		t.Errorf("answer = %q, want \"decorated reached\"", outcomes["Sequential"].answer)
	}

	// Different cost, strictly decreasing as the planner folds more.
	if outcomes["Sequential"].dispatches <= outcomes["Folding"].dispatches ||
		outcomes["Folding"].dispatches <= outcomes["Marked"].dispatches {
		t.Errorf("dispatches were %d/%d/%d for Sequential/Folding/Marked, want strictly fewer",
			outcomes["Sequential"].dispatches,
			outcomes["Folding"].dispatches,
			outcomes["Marked"].dispatches)
	}
	// And Marked got there the documented way, rather than by folding less.
	if outcomes["Marked"].marked != 1 {
		t.Errorf("Marked produced %d marked dispatches, want 1", outcomes["Marked"].marked)
	}
	if outcomes["Folding"].marked != 0 || outcomes["Sequential"].marked != 0 {
		t.Errorf("a planner other than Marked used the {marked} register")
	}
}

// TestPlanSplitsResultsPerOperation gates what a caller reads back from one
// merged tmux reply.
//
// tmux answers a command list with one exit status and one stdout, so the
// per-operation result is something this package reconstructs rather than
// something tmux reports. This checks all three statuses in one run, and that
// output lands on the operation that asked for it rather than on its
// neighbours.
//
//libtmux:real-tmux
func TestPlanSplitsResultsPerOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// Two reads with distinct output, then a failure, then work tmux never sees.
	plan := tmux.NewPlan()
	plan.CmdCapture(window.Ref(), "display-message", "-p", "FIRST")
	plan.CmdCapture(window.Ref(), "display-message", "-p", "SECOND")
	plan.SendKeys(tmux.PaneRef("%999"), tmux.SendKeysRequest{Command: tmux.Ptr("x")})
	plan.RenameWindow(window.Ref(), "never-ran")

	result, err := plan.Run(ctx, server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.OK() {
		t.Fatal("Run() reported a plan targeting a missing pane as complete")
	}

	// Output is attributed, not merged: each read got its own line and only
	// its own line, even though a folded reply would have carried both.
	if got := result.Ops[0].Stdout; len(got) != 1 || got[0] != "FIRST" {
		t.Errorf("first read Stdout = %q, want [\"FIRST\"]", got)
	}
	if got := result.Ops[1].Stdout; len(got) != 1 || got[0] != "SECOND" {
		t.Errorf("second read Stdout = %q, want [\"SECOND\"]", got)
	}

	// All three statuses, in one run.
	want := []tmux.OpStatus{
		tmux.OpComplete, // the reads ran
		tmux.OpComplete,
		tmux.OpFailed,  // tmux refused this one
		tmux.OpSkipped, // and never saw this one
	}
	for index, expected := range want {
		if got := result.Ops[index].Status; got != expected {
			t.Errorf("operation %d status = %v, want %v", index, got, expected)
		}
	}

	// The failure carries tmux's reason, and only the failed operation does.
	if result.Ops[2].Err == nil {
		t.Error("the failed operation carries no error")
	}
	if !errors.Is(result.Err(), tmux.ErrCommand) {
		t.Errorf("Err() = %v, want ErrCommand", result.Err())
	}
	for _, index := range []int{0, 1, 3} {
		if result.Ops[index].Err != nil {
			t.Errorf("operation %d carries an error it did not cause: %v",
				index, result.Ops[index].Err)
		}
	}

	// A skipped operation did not run: the window still has its old name.
	refreshed, err := window.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if name, _ := refreshed.Name(); name == "never-ran" {
		t.Error("an operation reported as skipped reached tmux")
	}
}

// TestPlannersAgreeWhenAnOperationNamesTwoObjects is the gate on the part of a
// planner's contract that no single planner can check on its own.
//
// A planner may change how many times tmux is invoked and nothing else. Marked
// is the one that rewrites an operation as it folds it, replacing the target
// with tmux's {marked} register, so it is the one that can break that promise
// -- and a command naming two objects is where it breaks quietly. tmux takes an
// empty second target as the current pane and reports success, so a dropped one
// is not an error anywhere: the swap simply happens between the wrong panes.
//
// Comparing what each planner left behind is therefore the only check that
// works. Asserting one planner's arguments would pass while the panes ended up
// the wrong way round.
//
//libtmux:real-tmux
func TestPlannersAgreeWhenAnOperationNamesTwoObjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// One swap, run through one planner, reported as the titles the panes ended
	// up carrying in index order.
	swapWith := func(planner tmux.Planner) []string {
		t.Helper()
		server := tmuxtest.NewServer(ctx, t).WithStrictErrors()
		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) == 0 {
			t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
		}
		window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
		if err != nil {
			t.Fatalf("NewWindow() error = %v", err)
		}
		existing, ok, err := window.ResolveActivePane(ctx)
		if err != nil || !ok {
			t.Fatalf("ResolveActivePane() = (%v, %v)", ok, err)
		}
		if _, err := existing.SetTitle(ctx, "alpha"); err != nil {
			t.Fatalf("SetTitle() error = %v", err)
		}

		// The split and the two operations naming its pane are what Marked
		// folds into one command list. The swap is the one that also names a
		// pane somewhere else.
		plan := tmux.NewPlan()
		created := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
		plan.SetPaneTitle(created, "beta")
		plan.SwapPane(created, existing.Ref(), true, false)

		result, err := plan.RunWith(ctx, server, planner)
		if err != nil {
			t.Fatalf("RunWith() error = %v", err)
		}
		if !result.OK() {
			t.Fatalf("RunWith() did not complete: %v", result.Err())
		}

		panes, err := window.SearchPanes(ctx, nil)
		if err != nil {
			t.Fatalf("SearchPanes() error = %v", err)
		}
		titles := make([]string, 0, len(panes))
		for _, pane := range panes {
			title, _ := pane.Formats().Raw("pane_title")
			titles = append(titles, title)
		}
		return titles
	}

	// Sequential sends every command on its own, so it is the reference: it
	// cannot fold and therefore cannot rewrite anything.
	want := swapWith(tmux.Sequential{})
	if !slices.Equal(want, []string{"beta", "alpha"}) {
		t.Fatalf("Sequential left the panes %v, want the swap to have happened", want)
	}
	for _, planner := range []struct {
		name  string
		value tmux.Planner
	}{
		{"Folding", tmux.Folding{}},
		{"Marked", tmux.Marked{}},
	} {
		if got := swapWith(planner.value); !slices.Equal(got, want) {
			t.Errorf("%s left the panes %v, Sequential left them %v",
				planner.name, got, want)
		}
	}
}

// TestPlanPreviewReportsWhatTmuxWouldRefuse gates the reason a plan is worth
// reading before it is sent.
//
// A plan is not atomic. An argument only rejected at the last step is rejected
// after every step before it has already changed tmux, so a preview that
// reported it the same way it reports a pane that does not exist yet would hide
// the one of the two that is a defect.
func TestPlanPreviewReportsWhatTmuxWouldRefuse(t *testing.T) {
	t.Parallel()

	session := tmux.SessionRef("$1")
	plan := tmux.NewPlan()
	plan.RenameSession(session, "fine")
	plan.RenameSession(session, "bad:name")

	preview, err := plan.Preview(tmux.Version{})
	if err == nil {
		t.Fatalf("Preview() accepted a name tmux would refuse: %q", preview)
	}
	if !strings.Contains(err.Error(), "step 1") {
		t.Errorf("Preview() error = %v, want it to name the step at fault", err)
	}
	if preview[0] == nil {
		t.Errorf("Preview() dropped the step it had already rendered: %q", preview)
	}
}

// countingPlanner groups exactly as told, including badly, which is what a
// caller's own planner is free to do by accident.
type countingPlanner struct{ dispatches []tmux.Dispatch }

// Plan returns the grouping this planner was built with.
func (p countingPlanner) Plan([]tmux.Op) []tmux.Dispatch { return p.dispatches }

// TestPlanRefusesAGroupingThatIsNotThePlan gates the other half of the Planner
// contract.
//
// A planner decides how many times tmux is invoked. It does not decide what
// runs, or in what order, and a caller writing their own can get that wrong.
// Every case here reached tmux or panicked before: an empty group and an index
// past the end indexed past the end of the plan's own results, and a grouping
// that covered nothing reported every operation skipped with no error at all.
func TestPlanRefusesAGroupingThatIsNotThePlan(t *testing.T) {
	t.Parallel()

	for _, malformed := range []struct {
		name       string
		dispatches []tmux.Dispatch
	}{
		{"a dispatch carrying nothing", []tmux.Dispatch{{Ops: nil}}},
		{"a step past the end", []tmux.Dispatch{{Ops: []int{7}}}},
		{"a negative step", []tmux.Dispatch{{Ops: []int{-1}}}},
		{"no steps at all", []tmux.Dispatch{}},
		{"the same step twice", []tmux.Dispatch{{Ops: []int{0}}, {Ops: []int{0}}}},
		{"steps out of order", []tmux.Dispatch{{Ops: []int{1}}, {Ops: []int{0}}}},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			t.Parallel()

			plan := tmux.NewPlan()
			plan.RenameWindow(tmux.WindowRef("@1"), "first")
			plan.RenameWindow(tmux.WindowRef("@1"), "second")

			// A server that was never started: reaching it would be the failure
			// this is checking for, so it must not be reachable.
			result, err := plan.RunWith(
				context.Background(),
				tmux.NewServer(tmux.ServerOptions{SocketName: "libtmux-golang-plan-unreachable"}),
				countingPlanner{dispatches: malformed.dispatches},
			)
			var refused *tmux.PlanError
			if !errors.As(err, &refused) {
				t.Fatalf("RunWith() error = %v, want a plan error", err)
			}
			for index, op := range result.Ops {
				if op.Status != tmux.OpSkipped {
					t.Errorf("operation %d = %v, want every one skipped", index, op.Status)
				}
			}
		})
	}
}

// markingPlanner marks whatever it is told to, including groups no planner in
// this package would make.
type markingPlanner struct{ ops []int }

// Plan returns one marked dispatch carrying the operations it was built with.
func (p markingPlanner) Plan([]tmux.Op) []tmux.Dispatch {
	return []tmux.Dispatch{{Ops: p.ops, Marked: true}}
}

// TestPlanRefusesAMarkedGroupItCannotReportSeparately gates the fail-closed
// rule on the path that used to skip it.
//
// A marked dispatch is a tmux command list like any other, so the same thing is
// true of it: tmux answers with one merged stdout and says nothing about which
// command produced what. Marking a read therefore lost that read's output and
// still reported it complete, which is exactly the result this package refuses
// to stand behind elsewhere.
func TestPlanRefusesAMarkedGroupItCannotReportSeparately(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		name string
		plan func() (*tmux.Plan, []int)
	}{
		{
			name: "a read riding with the creation",
			plan: func() (*tmux.Plan, []int) {
				plan := tmux.NewPlan()
				pane := plan.SplitPane(
					tmux.WindowRef("@1"), tmux.SplitPaneRequest{Attach: true})
				plan.DisplayMessage(pane, "#{pane_id}")
				return plan, []int{0, 1}
			},
		},
		{
			name: "nothing that leaves a pane to mark",
			plan: func() (*tmux.Plan, []int) {
				plan := tmux.NewPlan()
				plan.RenameWindow(tmux.WindowRef("@1"), "first")
				plan.RenameWindow(tmux.WindowRef("@1"), "second")
				return plan, []int{0, 1}
			},
		},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()

			plan, ops := refused.plan()
			// Reaching tmux would itself be the failure: this is refused while
			// the command list is being built, before anything is sent.
			result, err := plan.RunWith(
				context.Background(),
				tmux.NewServer(tmux.ServerOptions{SocketName: "libtmux-golang-plan-unreachable"}),
				markingPlanner{ops: ops},
			)
			var problem *tmux.PlanError
			if !errors.As(err, &problem) {
				t.Fatalf("RunWith() error = %v, want a plan error", err)
			}
			if result.Ops[1].Status != tmux.OpSkipped {
				t.Errorf("second operation = %v, want skipped", result.Ops[1].Status)
			}
		})
	}
}
