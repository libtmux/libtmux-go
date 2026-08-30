package tmux

import (
	"context"
	"errors"
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
