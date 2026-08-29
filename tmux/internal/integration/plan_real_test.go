package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Forward references let later steps target objects created earlier in the
// same run.
//
//libtmux:real-tmux
func TestPlanTargetsWhatItHasNotCreatedYet(t *testing.T) {
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

// Explain reports dispatch grouping without reaching tmux.
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

//libtmux:real-tmux
func TestPlanKeepsAnUnsentRenderFailureSkipped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	plan := tmux.NewPlan()
	session := plan.NewSession(tmux.NewSessionRequest{Name: "render-boundary"})
	plan.RenameSession(session, "bad:name")

	result, err := plan.Run(ctx, server)
	if err == nil {
		t.Fatal("Run() accepted an invalid forward-referenced request")
	}
	if result.Ops[0].Status != tmux.OpComplete {
		t.Errorf("creation status = %v, want complete", result.Ops[0].Status)
	}
	if result.Ops[1].Status != tmux.OpSkipped || result.Ops[1].Err != nil {
		t.Errorf("unsent rename = (%v, %v), want skipped without an operation error",
			result.Ops[1].Status, result.Ops[1].Err)
	}
}

// A zero Ref must not alias the plan's first step.
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

//libtmux:real-tmux
func TestPlanRefusesReferencesFromDifferentDaemons(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	firstServer := tmuxtest.NewServer(ctx, t)
	secondServer := tmuxtest.NewServer(ctx, t)

	firstSessions, err := firstServer.Sessions(ctx)
	if err != nil || len(firstSessions) != 1 {
		t.Fatalf("first Sessions() = (%d, %v), want one", len(firstSessions), err)
	}
	secondSessions, err := secondServer.Sessions(ctx)
	if err != nil || len(secondSessions) != 1 {
		t.Fatalf("second Sessions() = (%d, %v), want one", len(secondSessions), err)
	}

	plan := tmux.NewPlan()
	plan.RenameSession(firstSessions[0].Ref(), "must-not-run-first")
	plan.RenameSession(secondSessions[0].Ref(), "must-not-run-second")
	result, err := plan.Run(ctx, firstServer)
	var refused *tmux.PlanError
	if !errors.As(err, &refused) || refused.Step != 1 {
		t.Fatalf("Run() error = %v, want PlanError at step 1", err)
	}
	for index, op := range result.Ops {
		if op.Status != tmux.OpSkipped {
			t.Errorf("operation %d = %v, want skipped", index, op.Status)
		}
	}
	for index, session := range []tmux.Session{firstSessions[0], secondSessions[0]} {
		refreshed, refreshErr := session.Refresh(ctx)
		if refreshErr != nil {
			t.Fatalf("refresh session %d: %v", index, refreshErr)
		}
		if name, ok := refreshed.Name(); !ok || name != "work" {
			t.Errorf("session %d name = (%q, %t), want (work, true)", index, name, ok)
		}
	}
}

//libtmux:real-tmux
func TestPlanAtomicallyRejectsTheWrongServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	origin := tmuxtest.NewServer(ctx, t)
	other := tmuxtest.NewServer(ctx, t)

	sessions, err := origin.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	plan := tmux.NewPlan()
	plan.RenameSession(sessions[0].Ref(), "must-not-reach-other")
	result, err := plan.Run(ctx, other)
	if !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("Run() error = %v, want ErrDaemonReplaced", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Status != tmux.OpSkipped {
		t.Fatalf("operation results = %#v, want one skipped operation", result.Ops)
	}

	otherSessions, err := other.Sessions(ctx)
	if err != nil || len(otherSessions) != 1 {
		t.Fatalf("other Sessions() = (%d, %v), want one", len(otherSessions), err)
	}
	if name, ok := otherSessions[0].Name(); !ok || name != "work" {
		t.Fatalf("other session name = (%q, %t), want (work, true)", name, ok)
	}
}

// tmux does not attribute a grouped command-list failure to an individual
// operation, so every operation in the dispatch is indeterminate.
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
		result.Ops[3].Status,
	}
	want := []tmux.OpStatus{
		tmux.OpIndeterminate,
		tmux.OpIndeterminate,
		tmux.OpIndeterminate,
		tmux.OpIndeterminate,
	}
	if !slices.Equal(statuses, want) {
		t.Errorf("statuses = %v, want %v", statuses, want)
	}
	t.Logf("statuses = %v", statuses)

	name, ok := window.Name()
	t.Logf("window name after the failed plan = %q (%t)", name, ok)
}

// A transport may change plan cost, not results.
//
//libtmux:real-tmux
func TestPlanRunsIdenticallyOnBothTransports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

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

// Every operation after session creation targets a forward reference.
//
//libtmux:real-tmux
func TestPlanBuildsAWorkspaceInOnePass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

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

//libtmux:real-tmux
func TestPlanRecordsTheWiderSurface(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

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

// Cmd resolves forward references like typed plan operations.
//
//libtmux:real-tmux
func TestPlanCmdRecordsWhatHasNoRecorder(t *testing.T) {
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

// Planners may change dispatch count, never per-operation results.
//
//libtmux:real-tmux
func TestPlannersAgreeOnResultsAndDifferOnCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

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
		// The layout is chainable; the split and read remain separate because
		// their output must be attributed exactly.
		plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
		pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{Attach: true})
		plan.SetPaneTitle(pane, "decorated")
		plan.Cmd(pane, "set-option", "-p", "@planner", "reached")
		plan.CmdCapture(pane, "display-message", "-p", "#{pane_title} #{@planner}")
		return plan
	}

	type outcome struct {
		dispatches int
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
		outcomes[planner.name] = outcome{len(dispatches), statuses, answer}
		t.Logf("%-10s %d operations in %d tmux invocations, answer %q",
			planner.name, plan.Len(), len(dispatches), answer)
	}

	// Same meaning.
	for _, name := range []string{"Folding"} {
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

	// Folding must use fewer dispatches than the sequential baseline.
	if outcomes["Sequential"].dispatches <= outcomes["Folding"].dispatches {
		t.Errorf("dispatches were %d/%d for Sequential/Folding, want fewer",
			outcomes["Sequential"].dispatches,
			outcomes["Folding"].dispatches)
	}
}

// tmux merges command-list replies; Plan must restore per-operation output and
// status.
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

	// The grouped send, Enter, and rename cannot be distinguished after a
	// nonzero exit.
	want := []tmux.OpStatus{
		tmux.OpComplete, // the reads ran
		tmux.OpComplete,
		tmux.OpIndeterminate,
		tmux.OpIndeterminate,
		tmux.OpIndeterminate,
	}
	for index, expected := range want {
		if got := result.Ops[index].Status; got != expected {
			t.Errorf("operation %d status = %v, want %v", index, got, expected)
		}
	}

	// Every indeterminate operation carries the grouped command's reason.
	for _, index := range []int{2, 3, 4} {
		if result.Ops[index].Err == nil {
			t.Errorf("indeterminate operation %d carries no error", index)
		}
	}
	if !errors.Is(result.Err(), tmux.ErrCommand) {
		t.Errorf("Err() = %v, want ErrCommand", result.Err())
	}
	for _, index := range []int{0, 1} {
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

//libtmux:real-tmux
func TestPlannersAgreeWhenAnOperationNamesTwoObjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// One swap, run through one planner, reported as the titles the panes ended
	// up carrying in index order.
	swapWith := func(planner tmux.Planner) []string {
		t.Helper()
		server := tmuxtest.NewServer(ctx, t)
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

		// The swap names both the new pane and one that already exists.
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
	} {
		if got := swapWith(planner.value); !slices.Equal(got, want) {
			t.Errorf("%s left the panes %v, Sequential left them %v",
				planner.name, got, want)
		}
	}
}

// Preview must reject invalid requests while leaving forward references
// unresolved; Run is not atomic.
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

// A Planner may group operations but cannot omit, reorder, duplicate, or
// invent them.
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
			server, err := tmux.NewServer(tmux.ServerOptions{
				SocketName: "libtmux-go-plan-unreachable",
			})
			if err != nil {
				t.Fatalf("NewServer() error = %v", err)
			}

			// A server that was never started: reaching it would be the failure
			// this is checking for, so it must not be reachable.
			result, err := plan.RunWith(
				context.Background(),
				server,
				countingPlanner{dispatches: malformed.dispatches},
			)
			if _, ok := errors.AsType[*tmux.PlanError](err); !ok {
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
