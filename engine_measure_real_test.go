//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmux_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// engineWorkload runs one representative operation per label against server,
// letting the caller count what each one cost. Setup between operations is not
// recorded, so a row reports one operation rather than a scenario.
func engineWorkload(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	record func(label string, run func() error),
) {
	t.Helper()

	if _, err := server.Version(ctx); err != nil {
		t.Fatalf("warm the version cache: %v", err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	session := sessions[0]

	record("Server.Sessions", func() error {
		_, err := server.Sessions(ctx)
		return err
	})
	record("Server.Session (point lookup)", func() error {
		_, err := server.Session(ctx, session.ID())
		return err
	})
	record("Server.Snapshot", func() error {
		_, err := server.Snapshot(ctx)
		return err
	})
	record("Session.NewWindow", func() error {
		_, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr("measured")})
		return err
	})

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	window := snapshot.Windows()[len(snapshot.Windows())-1]
	pane := snapshot.Panes()[len(snapshot.Panes())-1]

	record("Server.Window (point lookup)", func() error {
		_, err := server.Window(ctx, window.ID())
		return err
	})
	record("Server.Pane (point lookup)", func() error {
		_, err := server.Pane(ctx, pane.ID())
		return err
	})
	record("Window.Rename", func() error {
		_, err := window.Rename(ctx, "measured-renamed")
		return err
	})
	record("Session.Rename", func() error {
		_, err := session.Rename(ctx, "measured-session")
		return err
	})

	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	record("Pane.Select", func() error {
		_, err := pane.Select(ctx, tmux.PaneSelectRequest{})
		return err
	})
	record("Pane.SendKeys", func() error {
		return pane.SendKeys(ctx, tmux.SendKeysRequest{Command: tmux.Ptr("true")})
	})
	record("Window.Options", func() error {
		_, err := window.Options(ctx)
		return err
	})
	record("Pane.CaptureBytes (byte-exact)", func() error {
		_, err := pane.CaptureBytes(ctx, tmux.CapturePaneRequest{})
		return err
	})
	record("Server.RefreshVersion (tmux -V)", func() error {
		_, err := server.RefreshVersion(ctx)
		return err
	})
}

// renameElapsed times a short sequential run of one operation. It reports an
// observation rather than a threshold: a wall clock on one machine is not a
// property this suite can assert.
func renameElapsed(ctx context.Context, t *testing.T, server tmux.Server) time.Duration {
	t.Helper()
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	window := snapshot.Windows()[len(snapshot.Windows())-1]
	started := time.Now()
	for iteration := range 30 {
		if _, err := window.Rename(ctx, fmt.Sprintf("timed-%d", iteration)); err != nil {
			t.Fatalf("Rename() error = %v", err)
		}
	}
	return time.Since(started).Round(time.Millisecond)
}

type engineMeasurement struct {
	label   string
	before  int
	after   int
	carried int
}

// TestEngineMeasurementTableAgainstRealTmux prints the process and control
// command counts MEASUREMENTS.md records. It fails when selecting the
// control-mode engine makes any operation start more tmux processes than it
// did before, which is the property the routing has to preserve.
//
//libtmux:real-tmux
func TestEngineMeasurementTableAgainstRealTmux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	order := make([]string, 0, 16)
	rows := make(map[string]*engineMeasurement, 16)
	row := func(label string) *engineMeasurement {
		measurement, found := rows[label]
		if !found {
			measurement = &engineMeasurement{label: label}
			rows[label] = measurement
			order = append(order, label)
		}
		return measurement
	}

	baseHarness := tmuxtest.NewServer(context.Background(), t)
	base, baseRunner := measuredServer(t, baseHarness)
	engineWorkload(ctx, t, base, func(label string, run func() error) {
		baseRunner.reset()
		if err := run(); err != nil {
			t.Fatalf("%s error = %v", label, err)
		}
		row(label).before = baseRunner.total()
	})

	connectedHarness := tmuxtest.NewServer(context.Background(), t)
	server, runner := measuredServer(t, connectedHarness)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	runner.reset()
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	setupProcesses := runner.total() + 1
	engine := &countingEngine{inner: client.Engine()}
	connected := server.WithEngine(engine)

	engineWorkload(ctx, t, connected, func(label string, run func() error) {
		runner.reset()
		engine.reset()
		if err := run(); err != nil {
			t.Fatalf("%s error = %v", label, err)
		}
		measurement := row(label)
		measurement.after = runner.total()
		measurement.carried = engine.total()
	})

	t.Logf("one-time control client setup: %d tmux processes", setupProcesses)
	t.Logf(
		"30 sequential Window.Rename: %s through processes, %s through the control engine",
		renameElapsed(ctx, t, base),
		renameElapsed(ctx, t, connected),
	)
	t.Log("| Operation | Processes before | Processes after | Control commands after |")
	t.Log("| --- | --- | --- | --- |")
	for _, label := range order {
		measurement := rows[label]
		t.Logf(
			"| `%s` | %d | %d | %d |",
			measurement.label,
			measurement.before,
			measurement.after,
			measurement.carried,
		)
		if measurement.after > measurement.before {
			t.Errorf(
				"%s started %d tmux processes with an engine and %d without",
				measurement.label,
				measurement.after,
				measurement.before,
			)
		}
	}
}
