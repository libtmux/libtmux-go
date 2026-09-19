package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Invalid layouts must never reach tmux 3.3a, which exits the server and destroys
// unrelated sessions instead of returning an error.
//
//libtmux:real-tmux
func TestSelectLayoutRefusesWhatWouldKillTheServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	bystander, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "bystander"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}

	for _, layout := range []string{
		"no-such-layout",
		"zzzz,80x24,0,0,0", // checksum-shaped, but not hexadecimal
		"garbage,,,",
		"32d2,80x24,0,0{}",
		"4a17,80x24,0,0{39x24,0,0,0,40x24,40,0[]}",
		"12f1,80x24,0,0{39x24,0,0,0,40x24,40,0,1",
		"89d5,80x24,0,0{39x24,0,0,0,40x24,40,0,1]",
		"ffff,80x24,0,0,0",
	} {
		t.Run(layout, func(t *testing.T) {
			err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: layout})
			if err == nil {
				t.Fatalf("SelectLayout(%q) was accepted", layout)
			}
			if !errors.Is(err, tmux.ErrInvalidServerCommandRequest) {
				t.Errorf("SelectLayout(%q) error = %v, want ErrInvalidServerCommandRequest", layout, err)
			}

			// The whole point: the server, and everything else on it, is alive.
			alive, err := server.IsAlive(ctx)
			if err != nil || !alive {
				t.Fatalf("the tmux server did not survive layout %q: (%t, %v)", layout, alive, err)
			}
			if _, err := bystander.Refresh(ctx); err != nil {
				t.Fatalf("an unrelated session did not survive layout %q: %v", layout, err)
			}
		})
	}

	// A real name tmux learned partway through the range is refused for a
	// different reason, and the reason has to be the one a caller can act on:
	// waiting for a newer tmux, not correcting a typo.
	t.Run("main-vertical-mirrored", func(t *testing.T) {
		version, err := server.Version(ctx)
		if err != nil {
			t.Fatalf("Version() error = %v", err)
		}
		mirrored, err := tmux.ParseVersion("3.5")
		if err != nil {
			t.Fatal(err)
		}
		err = window.SelectLayout(ctx, tmux.SelectLayoutRequest{
			Layout: "main-vertical-mirrored",
		})
		switch {
		case version.AtLeast(mirrored) && err != nil:
			t.Errorf("SelectLayout(main-vertical-mirrored) on tmux %s error = %v, "+
				"want it applied", version, err)
		case !version.AtLeast(mirrored) && !errors.Is(err, tmux.ErrVersionTooLow):
			t.Errorf("SelectLayout(main-vertical-mirrored) on tmux %s error = %v, "+
				"want ErrVersionTooLow", version, err)
		}
		// Either way, nothing may have reached a tmux that would exit on it.
		alive, err := server.IsAlive(ctx)
		if err != nil || !alive {
			t.Fatalf("the tmux server did not survive: (%t, %v)", alive, err)
		}
		if _, err := bystander.Refresh(ctx); err != nil {
			t.Fatalf("an unrelated session did not survive: %v", err)
		}
	})

	// What tmux does accept still works, including its own layout string.
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "tiled"}); err != nil {
		t.Fatalf("SelectLayout(tiled) error = %v", err)
	}
	refreshed, err := window.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	applied, ok := refreshed.Layout()
	if !ok || applied == "" {
		t.Fatalf("window reported no layout after tiled")
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: applied}); err != nil {
		t.Fatalf("tmux's own layout string %q was refused: %v", applied, err)
	}
}

//libtmux:real-tmux
func TestLayoutPreflightControlPlanRetainsKeeper(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	sessions, err := server.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	keeper := sessions[0]
	connection, err := keeper.OpenControl(t.Context(), tmux.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	bound := connection.Server()
	before, err := keeper.SearchWindows(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, planner := range []tmux.Planner{tmux.Sequential{}, tmux.Folding{}} {
		plan := tmux.NewPlan()
		window := plan.NewWindow(keeper.Ref(), tmux.NewWindowRequest{})
		plan.SelectLayout(window, tmux.SelectLayoutRequest{Layout: "32d2,80x24,0,0{}"})
		result, err := plan.RunWith(t.Context(), bound, planner)
		if !errors.Is(err, tmux.ErrInvalidServerCommandRequest) || result.Ops[0].Status != tmux.OpSkipped {
			t.Fatalf("control plan reached mutation: %+v %v", result, err)
		}
	}
	version, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	name := "main-h"
	if version.AtLeast(minimum) {
		name = "main-horizontal-m"
	}
	plan := tmux.NewPlan()
	plan.SelectLayout(before[0].Ref(), tmux.SelectLayoutRequest{Layout: name})
	result, err := plan.Run(t.Context(), bound)
	if err != nil || !result.OK() {
		t.Fatalf("control version-sensitive plan: %+v %v", result, err)
	}
	after, err := keeper.SearchWindows(t.Context(), nil)
	if err != nil || len(after) != len(before) || after[0].ID() != before[0].ID() {
		t.Fatalf("keeper windows changed: %v %v", after, err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bound.ValidateLayouts(t.Context(), func(yield func(string, int) bool) { yield(name, 1) }); !errors.Is(err, tmux.ErrControlClosed) {
		t.Fatalf("closed control connection error = %v, want ErrControlClosed", err)
	}
}

//libtmux:real-tmux
func TestLayoutPreflightBoundMissingDaemonPreservesFailure(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	sessions, err := server.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bound := sessions[0].Server()
	version, err := bound.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	name := "main-h"
	if version.AtLeast(minimum) {
		name = "main-horizontal-m"
	}
	if err := server.Kill(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := bound.ValidateLayouts(t.Context(), func(yield func(string, int) bool) { yield(name, 1) }); err == nil {
		t.Fatal("missing bound daemon was accepted using its retained version")
	}
}

//libtmux:real-tmux
func TestLayoutPreflightColdAndEmptyDaemon(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	version, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	name := "main-h"
	if version.AtLeast(minimum) {
		name = "main-horizontal-m"
	}
	input := func(yield func(string, int) bool) { yield(name, 1) }
	if err := server.ValidateLayouts(t.Context(), input); err != nil {
		t.Fatalf("cold endpoint: %v", err)
	}
	session, err := server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "empty-test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.Cmd(t.Context(), "set-option", "-s", "exit-empty", "off")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("retain empty daemon: %+v %v", result, err)
	}
	if err := session.Kill(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := server.ValidateLayouts(t.Context(), input); err != nil {
		t.Fatalf("live empty daemon: %v", err)
	}
}
