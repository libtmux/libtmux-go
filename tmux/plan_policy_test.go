package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestPlanRunPolicyDoesNotChangePreview(t *testing.T) {
	t.Parallel()

	plan := NewPlan()
	plan.SplitPane(WindowRef("@2"), SplitPaneRequest{Empty: true})
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{Stdout: []string{"%3"}}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported: DegradeUnsupported,
	}, runner)

	result, err := plan.Run(context.Background(), server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("Run() result = %#v, want all operations complete", result)
	}

	_, err = plan.Preview(mustParseVersion(t, "3.6"))
	var tooLow *VersionTooLowError
	if !errors.As(err, &tooLow) {
		t.Fatalf("Preview() error = %v, want VersionTooLowError", err)
	}
	if tooLow.Feature != "empty" {
		t.Fatalf("Preview() refused feature = %q, want empty", tooLow.Feature)
	}
}

func TestPlanDegradationReportsWarning(t *testing.T) {
	t.Parallel()

	warnings := make([]Warning, 0, 1)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{Stdout: []string{"%3"}}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported: DegradeUnsupported,
		WarningHandler: func(warning Warning) {
			warnings = append(warnings, warning)
		},
	}, runner)
	plan := NewPlan()
	plan.SplitPane(WindowRef("@2"), SplitPaneRequest{Empty: true})

	result, err := plan.Run(context.Background(), server)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("Run() result = %#v, want all operations complete", result)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one", warnings)
	}
	if warnings[0].Kind != WarningUnsupportedFeature || warnings[0].Feature != "empty" {
		t.Fatalf("warning = %#v, want one unsupported empty warning", warnings[0])
	}
}

func TestPlanMarksStartedSubprocessCancellationIndeterminate(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		err: &tmuxcmd.OutcomeUnknownError{Err: context.Canceled},
	}}}
	server := serverWithRunner(runner)
	plan := NewPlan()
	plan.Cmd(Ref{}, "display-message", "started")

	result, err := plan.Run(context.Background(), server)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("Run() error = %v, want outcome-unknown context cancellation", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Status != OpIndeterminate {
		t.Fatalf("Run() result = %#v, want one indeterminate operation", result)
	}
	if !errors.Is(result.Ops[0].Err, ErrOutcomeUnknown) {
		t.Fatalf("operation error = %v, want ErrOutcomeUnknown", result.Ops[0].Err)
	}
}

// A planned request refuses or reports exactly what the same request does when
// issued directly, for every plan builder that shares a renderer with warnings.
func TestPlanAppliesUnsupportedPolicyLikeImmediateCalls(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		feature   string
		dropped   string
		plan      func(*Plan)
		immediate func(Server) error
	}{
		{
			name: "send-keys", feature: "key_name", dropped: "-K",
			plan: func(plan *Plan) {
				plan.SendKeys(PaneRef("%9"), SendKeysRequest{
					KeyName: true, Command: Ptr("C-c"), SkipEnter: true,
				})
			},
			immediate: func(server Server) error {
				return paneWithExactTestTarget(server).SendKeys(context.Background(), SendKeysRequest{
					KeyName: true, Command: Ptr("C-c"), SkipEnter: true,
				})
			},
		},
		{
			name: "clear-history", feature: "reset_hyperlinks", dropped: "-H",
			plan: func(plan *Plan) {
				plan.ClearHistory(PaneRef("%9"), ClearHistoryRequest{ResetHyperlinks: true})
			},
			immediate: func(server Server) error {
				return paneWithExactTestTarget(server).ClearHistory(
					context.Background(), ClearHistoryRequest{ResetHyperlinks: true})
			},
		},
	}
	run := func(policy UnsupportedPolicy, call func(Server) error) ([]Warning, []string, error) {
		var warnings []Warning
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}},
			{result: tmuxcmd.Result{}},
		}}
		server := serverWithOptionsAndRunner(ServerOptions{
			Unsupported:    policy,
			WarningHandler: func(warning Warning) { warnings = append(warnings, warning) },
		}, runner)
		err := call(server)
		var argv []string
		for _, request := range runner.recordedRequests() {
			argv = append(argv, request.Arguments...)
		}
		return warnings, argv, err
	}
	for _, test := range cases {
		t.Run(test.name+"/fail", func(t *testing.T) {
			t.Parallel()

			plan := NewPlan()
			test.plan(plan)
			_, argv, err := run(FailUnsupported, func(server Server) error {
				result, err := plan.Run(context.Background(), server)
				_ = result
				return err
			})
			var tooLow *VersionTooLowError
			if !errors.As(err, &tooLow) || tooLow.Feature != test.feature {
				t.Fatalf("Run() error = %v, want VersionTooLowError for %s", err, test.feature)
			}
			if slices.Contains(argv, test.dropped) {
				t.Fatalf("argv = %v, want no %s dispatched", argv, test.dropped)
			}
			_, _, immediateErr := run(FailUnsupported, test.immediate)
			if !errors.As(immediateErr, &tooLow) || tooLow.Feature != test.feature {
				t.Fatalf("immediate error = %v, want the same VersionTooLowError", immediateErr)
			}
		})
		t.Run(test.name+"/degrade", func(t *testing.T) {
			t.Parallel()

			plan := NewPlan()
			test.plan(plan)
			warnings, argv, err := run(DegradeUnsupported, func(server Server) error {
				_, err := plan.Run(context.Background(), server)
				return err
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(warnings) != 1 || warnings[0].Feature != test.feature {
				t.Fatalf("warnings = %#v, want one for %s", warnings, test.feature)
			}
			if slices.Contains(argv, test.dropped) {
				t.Fatalf("argv = %v, want %s dropped", argv, test.dropped)
			}
			immediateWarnings, _, _ := run(DegradeUnsupported, test.immediate)
			if len(immediateWarnings) != 1 || immediateWarnings[0].Feature != test.feature {
				t.Fatalf("immediate warnings = %#v, want the same single warning", immediateWarnings)
			}
		})
	}
}

// Every plan builder that shares an argument renderer which can warn goes
// through the one place that applies policy: it is the operation's own return
// value, not something each builder remembers to call.
func TestPlanSplitPaneStillAppliesPolicy(t *testing.T) {
	t.Parallel()

	plan := NewPlan()
	plan.SplitPane(WindowRef("@2"), SplitPaneRequest{Empty: true})
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
	}}
	_, err := plan.Run(context.Background(), serverWithRunner(runner))
	if _, ok := errors.AsType[*VersionTooLowError](err); !ok {
		t.Fatalf("Run() error = %v, want VersionTooLowError", err)
	}
}

func TestPlanRefusesDeferredRefFromAnotherPlanBeforeDispatch(t *testing.T) {
	t.Parallel()

	first := NewPlan()
	foreign := first.NewSession(NewSessionRequest{})
	second := NewPlan()
	second.NewSession(NewSessionRequest{})
	second.RenameSession(foreign, "renamed")

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"$99"}}},
		{result: tmuxcmd.Result{}},
	}}
	result, err := second.Run(context.Background(), serverWithRunner(runner))
	var planErr *PlanError
	if !errors.As(err, &planErr) || planErr.Step != 1 {
		t.Fatalf("Run() error = %v, want PlanError at step 1", err)
	}
	if got := runner.recordedRequests(); len(got) != 0 {
		t.Fatalf("dispatched %d requests, want none", len(got))
	}
	if result.OK() {
		t.Fatalf("Run() result = %#v, want nothing complete", result)
	}
	if _, err := second.Preview(mustParseVersion(t, "3.6")); !errors.As(err, &planErr) {
		t.Fatalf("Preview() error = %v, want PlanError", err)
	}
}

func TestPlanKeepsMaterializedRefsReusableAcrossPlans(t *testing.T) {
	t.Parallel()

	window := Window{sessionID: "$2", windowID: "@7", windowIndex: 3}
	ref := window.Ref()
	for range 2 {
		plan := NewPlan()
		plan.SplitPane(ref, SplitPaneRequest{})
		plan.KillWindow(ref)
		if _, err := plan.Preview(mustParseVersion(t, "3.6")); err != nil {
			t.Fatalf("Preview() error = %v", err)
		}
	}
	own := NewPlan()
	created := own.NewSession(NewSessionRequest{})
	own.RenameSession(created, "ok")
	if _, err := own.Preview(mustParseVersion(t, "3.6")); err != nil {
		t.Fatalf("Preview() own deferred ref error = %v", err)
	}
}

func TestPlanUnlinkWindowNamesTheLinkedView(t *testing.T) {
	t.Parallel()

	linkedTwice := Window{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": "other,work,work",
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	}
	plan := NewPlan()
	plan.UnlinkWindow(linkedTwice.Ref(), UnlinkWindowRequest{KillIfLast: true})
	argv, err := plan.Preview(mustParseVersion(t, "3.6"))
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	want := []string{"unlink-window", "-k", "-t", "$2:3"}
	if !slices.Equal(argv[0], want) {
		t.Fatalf("Preview() = %v, want %v", argv[0], want)
	}
}

func TestPlanPlacementOperationsNameTheLinkedView(t *testing.T) {
	t.Parallel()

	twice := Window{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": "work,work",
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	}
	plan := NewPlan()
	plan.LinkWindow(SessionRef("$4"), twice.Ref(), LinkWindowRequest{})
	plan.MoveWindow(SessionRef("$4"), twice.Ref(), MoveWindowRequest{})
	plan.SwapWindow(twice.Ref(), twice.Ref(), false)
	plan.KillWindow(twice.Ref())
	argv, err := plan.Preview(mustParseVersion(t, "3.6"))
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	for index, want := range []string{"$2:3", "$2:3", "$2:3", "@7"} {
		if !slices.Contains(argv[index], want) {
			t.Errorf("step %d argv = %v, want it to name %s", index, argv[index], want)
		}
	}
}

// A plan operation addresses a linked window the way the matching Window
// method does: by the exact winlink where the method uses one, and by the
// window id where the method does (Window.Kill removes every link).
func TestPlanWindowOperationsMatchTheirMethodsTargets(t *testing.T) {
	t.Parallel()

	twice := Window{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": "work,work",
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	}
	once := Window{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": "work",
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	}
	layout := SelectLayoutRequest{Layout: "tiled"}
	cases := []struct {
		name string
		want string
		plan func(*Plan, Ref)
	}{
		{"KillWindow", "@7", func(p *Plan, ref Ref) { p.KillWindow(ref) }},
		{"KillOtherWindows", "$2:3", func(p *Plan, ref Ref) { p.KillOtherWindows(ref) }},
		{"RenameWindow", "$2:3", func(p *Plan, ref Ref) { p.RenameWindow(ref, "n") }},
		{"SelectWindow", "$2:3", func(p *Plan, ref Ref) { p.SelectWindow(ref) }},
		{"SelectLayout", "$2:3", func(p *Plan, ref Ref) { p.SelectLayout(ref, layout) }},
		{"NextLayout", "$2:3", func(p *Plan, ref Ref) { p.NextLayout(ref) }},
		{"PreviousLayout", "$2:3", func(p *Plan, ref Ref) { p.PreviousLayout(ref) }},
		{"ResizeWindow", "$2:3", func(p *Plan, ref Ref) {
			p.ResizeWindow(ref, ResizeWindowRequest{Width: 80})
		}},
		{"RotateWindow", "$2:3", func(p *Plan, ref Ref) { p.RotateWindow(ref, RotateWindowRequest{}) }},
		{"RespawnWindow", "$2:3", func(p *Plan, ref Ref) { p.RespawnWindow(ref, RespawnRequest{}) }},
		{"LastPane", "$2:3", func(p *Plan, ref Ref) { p.LastPane(ref) }},
		{"NewWindow", "$2:3", func(p *Plan, ref Ref) { p.NewWindow(ref, NewWindowRequest{}) }},
		{"SplitPane", "$2:3", func(p *Plan, ref Ref) { p.SplitPane(ref, SplitPaneRequest{}) }},
		{"DisplayMessage", "$2:3", func(p *Plan, ref Ref) { p.DisplayMessage(ref, "x") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan := NewPlan()
			test.plan(plan, twice.Ref())
			argv, err := plan.Preview(mustParseVersion(t, "3.6"))
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if !slices.Contains(argv[0], test.want) {
				t.Errorf("linked twice: argv = %v, want it to name %s", argv[0], test.want)
			}
			single := NewPlan()
			test.plan(single, once.Ref())
			argv, err = single.Preview(mustParseVersion(t, "3.6"))
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if !slices.Contains(argv[0], "@7") {
				t.Errorf("linked once: argv = %v, want the window id", argv[0])
			}
		})
	}
}

// A deferred Ref belongs to the plan that returned it, through a token the Plan
// carries, so a plan returned by value (q := *p) keeps accepting its own refs
// while a different plan still refuses them (TestPlanCopyKeepsItsOwnStepRefs
// and the foreign-ref test above). A zero-value Plan owns the refs it hands
// out.
func TestPlanDeferredRefBelongsToTheValueThatCreatedIt(t *testing.T) {
	t.Parallel()

	original := NewPlan()
	created := original.NewSession(NewSessionRequest{})
	original.RenameSession(created, "ok")
	copied := *original
	runner := &versionQueueRunner{}
	server := serverWithOptionsAndRunner(ServerOptions{}, runner)
	if _, err := copied.Preview(mustParseVersion(t, "3.6")); err != nil {
		t.Fatalf("copy Preview() error = %v, want its own refs accepted", err)
	}
	foreign := NewPlan()
	foreign.RenameSession(created, "foreign")
	if _, err := foreign.Run(context.Background(), server); err == nil {
		t.Fatal("foreign Run() error = nil, want the ref refused")
	}
	if len(runner.recordedRequests()) != 0 {
		t.Fatalf("foreign plan dispatched %v, want nothing before the refusal", runner.recordedRequests())
	}

	var zero Plan
	own := zero.NewSession(NewSessionRequest{})
	zero.RenameSession(own, "ok")
	if _, err := zero.Preview(mustParseVersion(t, "3.6")); err != nil {
		t.Fatalf("zero-value Plan Preview() error = %v, want its own refs accepted", err)
	}
}
