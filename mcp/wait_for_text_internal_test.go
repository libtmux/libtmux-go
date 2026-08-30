package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

type queuedPaneNotifications struct {
	items []tmux.ControlNotification
}

func (q *queuedPaneNotifications) NextNotification(
	ctx context.Context,
) (tmux.ControlNotification, error) {
	if len(q.items) != 0 {
		next := q.items[0]
		q.items = q.items[1:]
		return next, nil
	}
	<-ctx.Done()
	return tmux.ControlNotification{}, ctx.Err()
}

func TestWaitForTextRejectsNegativeDurations(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "negative-wait-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(t.Context(), target, nil)
	registry := &tools{runtime: runtime}
	for name, input := range map[string]waitForTextInput{
		"idle":    {IdleSeconds: -1},
		"timeout": {TimeoutSeconds: -1},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := registry.waitForText(t.Context(), nil, input)
			if err == nil || !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("waitForText() error = %v, want negative-duration rejection", err)
			}
		})
	}
}

func TestWaitForTextSchemaRejectsNegativeDurations(t *testing.T) {
	schema, err := jsonschema.For[waitForTextInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	constrain("wait_for_text", schema)
	for _, name := range []string{"idleSeconds", "timeoutSeconds"} {
		property := schema.Properties[name]
		if property == nil {
			t.Fatalf("wait_for_text schema has no %s property", name)
		}
		if property.Minimum == nil || *property.Minimum != 0 {
			t.Fatalf("wait_for_text %s minimum = %v, want 0", name, property.Minimum)
		}
	}
}

func TestWaitForTextReportsItsInternalMatchWindowLoss(t *testing.T) {
	payload := strings.Repeat("x", waitBufferMax+100)
	notification, err := tmux.ParseControlNotification([]byte("%output %1 " + payload))
	if err != nil {
		t.Fatal(err)
	}
	watched := watchPane(
		t.Context(),
		&queuedPaneNotifications{items: []tmux.ControlNotification{notification}},
		tmux.PaneID("%1"),
		nil,
		nil,
		time.Millisecond,
	)
	if watched.err != nil || watched.outcome != outcomeIdle || watched.matched != "" {
		t.Fatalf("watchPane() = (%q, %q, %q, %v)",
			watched.written, watched.outcome, watched.matched, watched.err)
	}
	output := waitForTextOutput{}
	_, finished, err := finishWait(
		&output,
		watched.outcome,
		watched.matched,
		false,
		splitWritten(watched.written),
		bounds{lines: ceilingMaxLines, bytes: ceilingMaxBytes},
		watched.truncation,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !finished.Truncated || finished.TruncatedBytes < 100 {
		t.Fatalf("wait output truncation = %+v, want at least 100 internally dropped bytes", finished)
	}
}

func TestPaneWaitSurfacesAnIndependentDeadline(t *testing.T) {
	watched := watchPane(
		t.Context(),
		failingPaneObservation{err: context.DeadlineExceeded},
		tmux.PaneID("%1"),
		nil,
		nil,
		time.Minute,
	)
	if watched.outcome != "" || !errors.Is(watched.err, context.DeadlineExceeded) {
		t.Fatalf("watchPane() = (%q, %v), want independent deadline error",
			watched.outcome, watched.err)
	}
}

//libtmux:real-tmux
func TestWaitForTextTimeoutIncludesPaneSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "wait-text-setup-budget"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance := mustInternalMCPServer(t, target)
	command, err := instance.runtime.command(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const gate = "wait-text-setup-gate"
	held := make(chan error, 1)
	go func() {
		held <- command.WaitFor(ctx, tmux.WaitForRequest{Channel: gate})
	}()
	waitForWaitTextLaneBlock(ctx, t, command)
	release := func() {
		if err := target.WaitFor(ctx, tmux.WaitForRequest{
			Channel: gate, Mode: tmux.WaitForModeSignal,
		}); err != nil {
			t.Error(err)
			return
		}
		if err := <-held; err != nil {
			t.Error(err)
		}
	}
	defer release()

	started := time.Now()
	_, output, err := instance.tools.waitForText(ctx, nil, waitForTextInput{
		PaneID: pane.ID().String(), Patterns: []string{"NEVER"},
		SinceEntry: true, TimeoutSeconds: 1,
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if output.Outcome != outcomeTimeout || output.EffectiveTimeoutSeconds != 1 {
		t.Fatalf("waitForText() = %+v, want one-second timeout", output)
	}
	if elapsed < 750*time.Millisecond || elapsed > 1500*time.Millisecond ||
		output.ElapsedSeconds < 0.75 || output.ElapsedSeconds > 1.5 {
		t.Fatalf("wait elapsed = %v (reported %.3fs), want whole-operation one-second budget",
			elapsed, output.ElapsedSeconds)
	}
}

//libtmux:real-tmux
func TestInstanceCloseJoinsTimedOutPaneObservations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "wait-text-close-owner"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance := mustInternalMCPServer(t, target)
	for range 2 {
		_, output, err := instance.tools.waitForText(ctx, nil, waitForTextInput{
			PaneID: pane.ID().String(), SinceEntry: true, TimeoutSeconds: 1,
		})
		if err != nil || output.Outcome != outcomeTimeout {
			t.Fatalf("timed wait = (%+v, %v)", output, err)
		}
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	clients, err := target.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("Instance.Close() left %d tmux client(s)", len(clients))
	}
}

func waitForWaitTextLaneBlock(ctx context.Context, t *testing.T, command tmux.Server) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		_, err := command.Cmd(probeCtx, "display-message", "-p", "lane-free")
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			t.Fatalf("probe command lane: %v", err)
		}
		select {
		case <-deadline.C:
			t.Fatal("command lane did not block")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
