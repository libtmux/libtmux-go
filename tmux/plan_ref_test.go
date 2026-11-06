package tmux

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// recordingRunner answers the version probe as the version it carries and
// succeeds on everything else, keeping every request for the test to read.
type recordingRunner struct {
	version string
	mu      sync.Mutex
	seen    []tmuxcmd.Request
}

func (r *recordingRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.mu.Lock()
	r.seen = append(r.seen, request)
	r.mu.Unlock()
	if slices.Contains(request.Arguments, "-V") {
		return tmuxcmd.Result{Stdout: []string{"tmux " + r.version}}, nil
	}
	return tmuxcmd.Result{}, nil
}

// mentioning returns the recorded requests whose arguments contain word.
func (r *recordingRunner) mentioning(word string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found [][]string
	for _, request := range r.seen {
		if slices.Contains(request.Arguments, word) {
			found = append(found, request.Arguments)
		}
	}
	return found
}

// The unsupported policy covers every operation before the first is sent, so
// a plan never refuses after it has already changed tmux.
func TestPlanAppliesPolicyBeforeDispatchingAnything(t *testing.T) {
	t.Parallel()

	build := func() *Plan {
		plan := NewPlan()
		pane := plan.SplitPane(WindowRef("@2"), SplitPaneRequest{})
		plan.SendKeys(pane, SendKeysRequest{KeyName: true, Command: Ptr("C-c"), SkipEnter: true})
		return plan
	}
	runner := &recordingRunner{version: "3.3a"}
	_, err := build().Run(context.Background(), serverWithRunner(runner))
	var tooLow *VersionTooLowError
	if !errors.As(err, &tooLow) {
		t.Fatalf("Run() error = %v, want VersionTooLowError", err)
	}
	if sent := runner.mentioning("split-window"); len(sent) != 0 {
		t.Fatalf("split-window dispatched before the refusal: %v", sent)
	}

	_, err = build().Preview(mustParseVersion(t, "3.3a"))
	if !errors.As(err, &tooLow) {
		t.Fatalf("Preview() error = %v, want VersionTooLowError for the deferred step", err)
	}
}

// Under degrade the policy pass reports nothing itself: each warning reaches
// the handler once, when its operation is rendered for dispatch.
func TestPlanPolicyPassReportsEachWarningOnce(t *testing.T) {
	t.Parallel()

	var warnings []Warning
	runner := &recordingRunner{version: "3.3a"}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported:    DegradeUnsupported,
		WarningHandler: func(w Warning) { warnings = append(warnings, w) },
	}, runner)
	plan := NewPlan()
	plan.SendKeys(PaneRef("%9"), SendKeysRequest{KeyName: true, Command: Ptr("C-c"), SkipEnter: true})
	if _, err := plan.Run(context.Background(), server); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one", warnings)
	}
}
