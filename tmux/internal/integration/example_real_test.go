package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/tmuxq"
)

func TestCreationMutationExampleAgainstRealTmux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "project", WindowName: "editor",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	windowName := "tests"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: &windowName, Attach: true,
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
	})
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	selected, err := pane.Select(ctx, tmux.PaneSelectRequest{})
	if err != nil {
		t.Fatalf("Pane.Select() error = %v", err)
	}
	if selected.SessionID() != session.ID() || selected.WindowID() != window.ID() ||
		selected.ID() != pane.ID() {
		t.Fatalf("selected pane = %#v, want exact %s:%s:%s view", selected,
			session.ID(), window.ID(), pane.ID())
	}

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	project, err := snapshot.SessionByID(session.ID())
	if err != nil {
		t.Fatalf("SessionByID() error = %v", err)
	}
	currentWindowID, ok := project.Formats().WindowID()
	if !ok || currentWindowID != window.ID() {
		t.Fatalf("current window = %q, want %s", currentWindowID, window.ID())
	}
	var exactWindow tmux.Window
	for _, candidate := range snapshot.WindowsByID(window.ID()) {
		if candidate.SessionID() == session.ID() {
			exactWindow = candidate
			break
		}
	}
	if exactWindow.ID() == "" {
		t.Fatalf("example window %s:%s is missing", session.ID(), window.ID())
	}
	activePaneID, ok := exactWindow.Formats().PaneID()
	if !ok || activePaneID != pane.ID() {
		t.Fatalf("active pane = %q, want %s", activePaneID, pane.ID())
	}
}

func TestPaneFilterPredicateExampleAgainstRealTmux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	windows := sessions[0].Windows()
	if len(windows) != 1 {
		t.Fatalf("session windows = %d, want 1", len(windows))
	}
	initialPanes := windows[0].Panes()
	if len(initialPanes) != 1 {
		t.Fatalf("window panes = %d, want 1", len(initialPanes))
	}
	created, err := windows[0].SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 2 {
		t.Fatalf("Panes() = (%#v, %v), want two panes", panes, err)
	}
	minimumIndex := initialPanes[0].Index()
	predicate, err := (tmux.PaneFilter{
		IDIn:    []tmux.PaneID{initialPanes[0].ID(), created.ID()},
		IndexGT: &minimumIndex,
	}).Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}

	matches := tmuxq.Where(panes, predicate)
	if len(matches) != 1 {
		t.Fatalf("Where() = %#v, want pane %s", matches, created.ID())
	}
	got := matches[0]
	if got.ID() != created.ID() || got.Index() != created.Index() ||
		got.SessionID() != sessions[0].ID() || got.WindowID() != windows[0].ID() {
		t.Fatalf(
			"Where() pane = %s:%s.%s index %d, want %s:%s.%s index %d",
			got.SessionID(), got.WindowID(), got.ID(), got.Index(),
			sessions[0].ID(), windows[0].ID(), created.ID(), created.Index(),
		)
	}
}

func TestDocumentedBlockingWorkflowsAgainstRealTmux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	result, err := server.Cmd(ctx, "display-message", "-p", "#{session_name}")
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 || result.Stdout[0] != "work" {
		t.Fatalf("Cmd() = (%#v, %v), want work", result, err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	windows := sessions[0].Windows()
	if len(windows) != 1 {
		t.Fatalf("session windows = %d, want 1", len(windows))
	}
	for _, window := range windows {
		if panes := window.Panes(); len(panes) != 1 {
			t.Fatalf("window %s panes = %d, want 1", window.ID(), len(panes))
		}
	}

	snapshot, err := server.Snapshot(ctx)
	if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Panes()) != 1 {
		t.Fatalf("Snapshot() = (%#v, %v), want one session and pane", snapshot, err)
	}
	for _, session := range snapshot.Sessions() {
		for _, pane := range session.Panes() {
			command, present := pane.CurrentCommand()
			if !present || command == "" {
				t.Fatalf("CurrentCommand() = (%q, %t), want a present command", command, present)
			}
		}
	}
	name := "work"
	predicate, err := (tmux.SessionFilter{Name: &name}).Predicate()
	if err != nil {
		t.Fatal(err)
	}
	if matches := tmuxq.Where(snapshot.Sessions(), predicate); len(matches) != 1 {
		t.Fatalf("local filter matched %d sessions, want 1", len(matches))
	}

	options, err := sessions[0].Options(ctx)
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	mouse := options.Mouse()
	if _, present := mouse.Get(); !present {
		t.Fatal("Options().Mouse() is absent")
	}
	if origin, present := mouse.Origin(); !present || origin != tmux.OptionOriginInherited {
		t.Fatalf("Options().Mouse().Origin() = (%v, %t), want inherited", origin, present)
	}
	liveFilter := tmux.TmuxFilter("#{==:#{session_name},work}")
	if matches, err := server.SearchSessions(ctx, &liveFilter); err != nil || len(matches) != 1 {
		t.Fatalf("SearchSessions() = (%#v, %v), want one match", matches, err)
	}

	ownedSession := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{})
	ownedWindow := tmuxtest.NewWindow(ctx, t, ownedSession, tmux.NewWindowRequest{})
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		panes, err := ownedWindow.SearchPanes(ctx, nil)
		return len(panes) == 1, err
	}); err != nil {
		t.Fatalf("WaitFor(window pane) error = %v", err)
	}

	lazy := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	_, err = lazy.Sessions(ctx)
	var commandError *tmux.CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("strict Sessions() error = %v, want *CommandError", err)
	}
	if commandError.Subcommand != "display-message" || commandError.Result.ExitCode == 0 {
		t.Fatalf("strict Sessions() CommandError = %#v, want display-message with nonzero exit", commandError)
	}
}
