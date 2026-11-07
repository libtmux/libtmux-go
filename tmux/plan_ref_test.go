package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
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

func linkedPane(links string) Pane {
	return Pane{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": links,
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3, paneID: "%9",
	}
}

func linkedWindow(links string) Window {
	return Window{
		formats: formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": links,
		}},
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	}
}

// Window.SetOption and Window.SetHook name the window by id alone unless the
// caller asks for format expansion, which a plan cannot express, so the plan
// operations name the id in every linkage. A session-qualified id would let
// tmux apply the write to that session's current window (see
// windowOptionRuntimeScope).
func TestPlanWindowOptionAndHookTargetsMatchTheirMethods(t *testing.T) {
	t.Parallel()

	for _, links := range []string{"work", "work,work", "work,other"} {
		t.Run(links, func(t *testing.T) {
			t.Parallel()

			window := linkedWindow(links)
			runner := &recordingRunner{version: "3.6"}
			server := serverWithRunner(runner)
			window.server = server
			ctx := context.Background()
			if err := window.SetOption(ctx, "@thing", "v", SetOptionOptions{}); err != nil {
				t.Fatalf("SetOption() error = %v", err)
			}
			if err := window.SetHook(ctx, "pane-died", "true"); err != nil {
				t.Fatalf("SetHook() error = %v", err)
			}
			immediateOption := targetAfterFlag(t, runner.mentioning("set-option"))
			immediateHook := targetAfterFlag(t, runner.mentioning("set-hook"))

			plan := NewPlan()
			plan.SetOption(window.Ref(), SetPlanOptionRequest{Name: "@thing", Value: "v", Window: true})
			plan.SetHook(window.Ref(), "pane-died", "true", false)
			argv, err := plan.Preview(mustParseVersion(t, "3.6"))
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if got := argvTarget(t, argv[0]); got != immediateOption {
				t.Errorf("SetOption plan target = %q, immediate = %q", got, immediateOption)
			}
			if got := argvTarget(t, argv[1]); got != immediateHook {
				t.Errorf("SetHook plan target = %q, immediate = %q", got, immediateHook)
			}
		})
	}
}

func argvTarget(t *testing.T, argv []string) string {
	t.Helper()
	index := slices.Index(argv, "-t")
	if index < 0 || index+1 >= len(argv) {
		t.Fatalf("argv = %v has no -t", argv)
	}
	return argv[index+1]
}

func targetAfterFlag(t *testing.T, requests [][]string) string {
	t.Helper()
	if len(requests) != 1 {
		t.Fatalf("requests = %v, want exactly one", requests)
	}
	return argvTarget(t, requests[0])
}

// A pane in a window linked more than once is reached by its session, the
// winlink index and its id, the way every Pane method reaches it. The bare
// pane id would let tmux pick the window.
func TestPlanPaneOperationsMatchTheirMethodsTargets(t *testing.T) {
	t.Parallel()

	twice := linkedPane("work,work")
	once := linkedPane("work")
	cases := []struct {
		name string
		plan func(*Plan, Ref)
	}{
		{"SplitPane", func(p *Plan, r Ref) { p.SplitPane(r, SplitPaneRequest{}) }},
		{"SendKeys", func(p *Plan, r Ref) { p.SendKeys(r, SendKeysRequest{Command: Ptr("x"), SkipEnter: true}) }},
		{"SendKeysEnter", func(p *Plan, r Ref) { p.SendKeys(r, SendKeysRequest{Command: Ptr("x")}) }},
		{"DisplayMessage", func(p *Plan, r Ref) { p.DisplayMessage(r, "x") }},
		{"KillPane", func(p *Plan, r Ref) { p.KillPane(r) }},
		{"KillOtherPanes", func(p *Plan, r Ref) { p.KillOtherPanes(r) }},
		{"SelectPane", func(p *Plan, r Ref) { p.SelectPane(r, PaneSelectRequest{}) }},
		{"ResizePane", func(p *Plan, r Ref) { p.ResizePane(r, ResizePaneRequest{Height: PaneCells(5)}) }},
		{"SetPaneTitle", func(p *Plan, r Ref) { p.SetPaneTitle(r, "t") }},
		{"ClearHistory", func(p *Plan, r Ref) { p.ClearHistory(r, ClearHistoryRequest{}) }},
		{"SendPrefix", func(p *Plan, r Ref) { p.SendPrefix(r, PrefixKey(0)) }},
		{"PipePane", func(p *Plan, r Ref) { p.PipePane(r, PipePaneRequest{}) }},
		{"RespawnPane", func(p *Plan, r Ref) { p.RespawnPane(r, RespawnRequest{}) }},
		{"SwapPaneTarget", func(p *Plan, r Ref) { p.SwapPane(r, PaneRef("%1"), false, false) }},
		{"SwapPaneSource", func(p *Plan, r Ref) { p.SwapPane(PaneRef("%1"), r, false, false) }},
		{"JoinPaneTarget", func(p *Plan, r Ref) { p.JoinPane(r, PaneRef("%1"), false, false) }},
		{"JoinPaneSource", func(p *Plan, r Ref) { p.JoinPane(PaneRef("%1"), r, false, false) }},
		{"MovePaneTarget", func(p *Plan, r Ref) { p.MovePane(r, PaneRef("%1"), false, false) }},
		{"MovePaneSource", func(p *Plan, r Ref) { p.MovePane(PaneRef("%1"), r, false, false) }},
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
			if !slices.ContainsFunc(argv[0], func(a string) bool { return a == "$2:3.%9" }) {
				t.Errorf("linked twice: argv = %v, want the exact pane target $2:3.%%9", argv[0])
			}
			single := NewPlan()
			test.plan(single, once.Ref())
			argv, err = single.Preview(mustParseVersion(t, "3.6"))
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if !slices.ContainsFunc(argv[0], func(a string) bool { return a == "$2:.%9" }) {
				t.Errorf("linked once: argv = %v, want $2:.%%9", argv[0])
			}
		})
	}
}

// A record that cannot name its placement (here, no session) must not unlink
// or address anything by id instead: the operation that needs the placement
// refuses, and an operation that does not need it still works.
func TestPlanRefusesAPlacementTheRecordCannotName(t *testing.T) {
	t.Parallel()

	window := Window{windowID: "@7", windowIndex: 3}
	pane := Pane{paneID: "%9"}
	cases := []struct {
		name string
		plan func(*Plan)
	}{
		{"UnlinkWindow", func(p *Plan) { p.UnlinkWindow(window.Ref(), UnlinkWindowRequest{}) }},
		{"LinkWindowSource", func(p *Plan) { p.LinkWindow(SessionRef("$1"), window.Ref(), LinkWindowRequest{}) }},
		{"SelectPane", func(p *Plan) { p.SelectPane(pane.Ref(), PaneSelectRequest{}) }},
		{"KillPane", func(p *Plan) { p.KillPane(pane.Ref()) }},
		{"JoinPaneSource", func(p *Plan) { p.JoinPane(PaneRef("%1"), pane.Ref(), false, false) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan := NewPlan()
			test.plan(plan)
			_, err := plan.Preview(mustParseVersion(t, "3.6"))
			if _, ok := errors.AsType[*PlanError](err); !ok {
				t.Fatalf("Preview() error = %v, want PlanError", err)
			}
			runner := &recordingRunner{version: "3.6"}
			if _, err := plan.Run(context.Background(), serverWithRunner(runner)); err == nil {
				t.Fatal("Run() error = nil, want the placement refused")
			}
			if sent := runner.mentioning(plan.ops[0].name); len(sent) != 0 {
				t.Fatalf("dispatched %v, want nothing", sent)
			}
		})
	}

	plan := NewPlan()
	plan.KillWindow(window.Ref())
	if _, err := plan.Preview(mustParseVersion(t, "3.6")); err != nil {
		t.Fatalf("KillWindow Preview() error = %v, want the id-addressed op to work", err)
	}
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

// A Plan returned or copied by value keeps its own step refs, while another
// plan's refs stay refused. The owner is a token the copy shares.
func TestPlanCopyKeepsItsOwnStepRefs(t *testing.T) {
	t.Parallel()

	original := NewPlan()
	created := original.NewSession(NewSessionRequest{})
	original.RenameSession(created, "ok")
	copied := *original
	if _, err := copied.Preview(mustParseVersion(t, "3.6")); err != nil {
		t.Fatalf("copy Preview() error = %v, want its own refs accepted", err)
	}

	other := NewPlan()
	other.RenameSession(created, "foreign")
	_, err := other.Preview(mustParseVersion(t, "3.6"))
	if _, ok := errors.AsType[*PlanError](err); !ok {
		t.Fatalf("other plan Preview() error = %v, want PlanError", err)
	}
	if !strings.Contains(err.Error(), "another plan") {
		t.Fatalf("error = %v, want it to name another plan", err)
	}
}
