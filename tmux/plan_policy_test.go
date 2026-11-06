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
