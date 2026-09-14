package tmux

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func oneLayout(value string, panes int) iter.Seq2[string, int] {
	return func(yield func(string, int) bool) { yield(value, panes) }
}

func TestLayoutPreflightPreservesVersionProbeOutcomes(t *testing.T) {
	for _, test := range []struct {
		name, layout string
		response     versionResponse
		want         error
		cold         bool
	}{
		{"old daemon prefix", "main-h", versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}}, nil, false},
		{"new daemon ambiguity", "main-h", versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}}}, ErrInvalidServerCommandRequest, false},
		{"old daemon mirror", "main-horizontal-m", versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}}, ErrVersionTooLow, false},
		{"new daemon mirror", "main-horizontal-m", versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}}}, nil, false},
		{"missing socket", "main-h", versionResponse{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"error connecting to /socket (No such file or directory)"}}}, nil, true},
		{"exited endpoint", "main-h", versionResponse{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /socket"}}}, nil, true},
		{"permission", "main-h", versionResponse{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"error connecting to /socket (Permission denied)"}}}, ErrCommand, false},
		{"live failure", "main-h", versionResponse{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"server exited unexpectedly"}}}, ErrCommand, false},
		{"protocol", "main-h", versionResponse{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"protocol version mismatch"}}}, ErrCommand, false},
		{"malformed version", "main-h", versionResponse{result: tmuxcmd.Result{Stdout: []string{"unknown"}}}, ErrVersionQuery, false},
		{"canceled", "main-h", versionResponse{err: context.Canceled}, context.Canceled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &versionQueueRunner{responses: []versionResponse{
				test.response,
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}},
			}}
			err := serverWithRunner(runner).ValidateLayouts(t.Context(), oneLayout(test.layout, 1))
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateLayouts() = %v, want %v", err, test.want)
			}
			requests := runner.recordedRequests()
			assertRequestArguments(t, requests[0], []string{"display-message", "-p", "tmux #{version}"})
			wantCalls := 1
			if test.cold {
				wantCalls = 2
			}
			if len(requests) != wantCalls {
				t.Fatalf("calls = %d, want %d", len(requests), wantCalls)
			}
			if test.cold && !slices.Equal(requests[1].Arguments, []string{"-V"}) {
				t.Errorf("fallback request = %v", requests[1].Arguments)
			}
			if command, ok := errors.AsType[*CommandError](err); ok && !slices.Equal(command.Result.Stderr, test.response.result.Stderr) {
				t.Errorf("lost original diagnostics: %v", command)
			}
		})
	}
}

func TestLayoutPreflightDoesNotChangeClientVersionCache(t *testing.T) {
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}}},
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}},
	}}
	server := serverWithRunner(runner)
	before, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := server.ValidateLayouts(t.Context(), oneLayout("main-h", 1)); err != nil {
		t.Fatal(err)
	}
	after, err := server.Version(t.Context())
	if err != nil || before.String() != "3.7c" || after != before || runner.callCount() != 2 {
		t.Fatalf("client cache changed: %v %v %v calls=%d", before, after, err, runner.callCount())
	}
}

func TestLayoutPreflightValidatesCompleteSequenceBeforeIO(t *testing.T) {
	runner := &versionQueueRunner{}
	err := serverWithRunner(runner).ValidateLayouts(t.Context(), func(yield func(string, int) bool) {
		if yield("main-h", 1) {
			yield("b25d,80x24,0,0,0", 2)
		}
	})
	if !errors.Is(err, ErrInvalidServerCommandRequest) || runner.callCount() != 0 {
		t.Fatalf("late capacity error reached tmux: %v calls=%d", err, runner.callCount())
	}
	for _, input := range []iter.Seq2[string, int]{nil, oneLayout("", 0), oneLayout("even-h", 2)} {
		if err := (Server{}).ValidateLayouts(t.Context(), input); err != nil {
			t.Errorf("static validation required a server: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (Server{}).ValidateLayouts(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled empty input: %v", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	err = (Server{}).ValidateLayouts(ctx, func(yield func(string, int) bool) {
		yield("tiled", 1)
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation during iteration: %v", err)
	}
}

func TestLayoutPreflightPlanUsesRunLocalDaemonVersion(t *testing.T) {
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}}},
		{result: tmuxcmd.Result{}},
	}}
	plan := NewPlan()
	plan.SelectLayout(WindowRef("@1"), SelectLayoutRequest{Layout: "main-horizontal-m"})
	if _, err := plan.Preview(mustParseVersion(t, "3.7c")); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 0 {
		t.Fatal("preview reached tmux")
	}
	result, err := plan.Run(t.Context(), serverWithRunner(runner))
	if err != nil || !result.OK() || runner.callCount() != 2 {
		t.Fatalf("Run() = %+v %v calls=%d", result, err, runner.callCount())
	}
}
