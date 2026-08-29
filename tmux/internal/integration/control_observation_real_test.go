//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestPaneObservationLinearizesBaselineAndOutput(t *testing.T) {
	initial := tmux.NewSessionRequest{Name: "work"}
	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		InitialSession: &initial,
		FixedShell:     true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%d, %v), want one", len(panes), err)
	}
	const before = "OBSERVATION-BEFORE"
	writePaneMarker(ctx, t, panes[0],
		"printf '%s\\n' '"+before+"' '%end 1 2 3'")
	waitForPaneMarker(ctx, t, panes[0], before)

	const handoff = "OBSERVATION-HANDOFF"
	hook := "run-shell \"printf '" + handoff + "\\n' > #{pane_tty}\""
	if err := sessions[0].SetHook(ctx, "after-capture-pane", hook); err != nil {
		t.Fatal(err)
	}
	observation, err := panes[0].OpenObservation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observation.Close() })
	if err := sessions[0].UnsetHook(ctx, "after-capture-pane"); err != nil {
		t.Fatal(err)
	}
	if baseline := strings.Join(observation.Baseline(), "\n"); !strings.Contains(baseline, before) ||
		!strings.Contains(baseline, "%end 1 2 3") {
		t.Fatalf("baseline does not contain %q: %q", before, baseline)
	} else if strings.Contains(baseline, handoff) {
		t.Fatalf("baseline crossed the capture boundary: %q", baseline)
	}
	bufferFormat := "#{buffer_name}"
	buffers, err := server.ListBuffers(ctx, tmux.ListBuffersRequest{Format: &bufferFormat})
	if err != nil {
		t.Fatal(err)
	}
	for _, buffer := range buffers {
		if strings.HasPrefix(buffer, "libtmux-go-observe-") {
			t.Fatalf("pane observation left global buffer %q", buffer)
		}
	}

	var output strings.Builder
	for !strings.Contains(output.String(), handoff) {
		notification, nextErr := observation.NextNotification(ctx)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		paneID, data, ok := notification.Output()
		if !ok || paneID != panes[0].ID() {
			continue
		}
		output.Write(data)
		if strings.Contains(output.String(), before) {
			t.Fatalf("post-boundary output repeated baseline data: %q", output.String())
		}
	}
}

//libtmux:real-tmux
func TestPaneObservationOwnsLinkedSessionAndDetectsUnlink(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	owner, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "observation-owner", WindowName: "observed",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = snapshot.SessionByID(owner.ID())
	if err != nil {
		t.Fatal(err)
	}
	ownerWindows, windowsOK := owner.Windows()
	ownerPanes, panesOK := owner.Panes()
	if !windowsOK || !panesOK || len(ownerWindows) != 1 || len(ownerPanes) != 1 {
		t.Fatalf(
			"owner relations = (%d windows/%t, %d panes/%t), want one each",
			len(ownerWindows), windowsOK, len(ownerPanes), panesOK,
		)
	}

	otherSessionObservation, err := ownerPanes[0].OpenObservation(ctx)
	if err != nil {
		t.Fatalf("OpenObservation(other session) error = %v", err)
	}
	if err := otherSessionObservation.Close(); err != nil {
		t.Fatal(err)
	}

	if err := ownerWindows[0].Link(ctx, tmux.LinkWindowRequest{
		TargetSession: sessions[0].ID(), Detach: true,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linkedSession, err := snapshot.SessionByID(sessions[0].ID())
	if err != nil {
		t.Fatal(err)
	}
	linkedWindows, windowsOK := linkedSession.Windows()
	linkedPanes, panesOK := linkedSession.Panes()
	if !windowsOK || !panesOK {
		t.Fatal("linked session snapshot omitted relations")
	}
	var linkedWindow tmux.Window
	var linkedPane tmux.Pane
	for _, window := range linkedWindows {
		if window.ID() == ownerWindows[0].ID() {
			linkedWindow = window
		}
	}
	for _, pane := range linkedPanes {
		if pane.ID() == ownerPanes[0].ID() {
			linkedPane = pane
		}
	}
	if linkedWindow.ID() == "" || linkedPane.ID() == "" {
		t.Fatal("linked window or pane view was not materialized")
	}
	observation, err := linkedPane.OpenObservation(ctx)
	if err != nil {
		t.Fatalf("OpenObservation(linked pane) error = %v", err)
	}
	t.Cleanup(func() { _ = observation.Close() })
	if err := linkedWindow.Unlink(ctx, tmux.UnlinkWindowRequest{}); err != nil {
		t.Fatal(err)
	}
	for {
		_, err = observation.NextNotification(ctx)
		if errors.Is(err, tmux.ErrPaneObservationLost) {
			break
		}
		if err != nil {
			t.Fatalf("NextNotification() error = %v, want ErrPaneObservationLost", err)
		}
	}
	retryCtx, retryCancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer retryCancel()
	if _, err := observation.NextNotification(retryCtx); !errors.Is(err, tmux.ErrPaneObservationLost) {
		t.Fatalf("second NextNotification() error = %v, want terminal ErrPaneObservationLost", err)
	}
}

func writePaneMarker(ctx context.Context, t *testing.T, pane tmux.Pane, command string) {
	t.Helper()
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func waitForPaneMarker(
	ctx context.Context,
	t *testing.T,
	pane tmux.Pane,
	marker string,
) {
	t.Helper()
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		return strings.Contains(strings.Join(lines, "\n"), marker), err
	}); err != nil {
		t.Fatal(err)
	}
}
