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

// TestSelectLayoutAcceptsAUniquePresetPrefix pins GO2-1/D3 against a real
// server: "tile" applies exactly like "tiled" applies, on whatever tmux is on
// PATH, and an ambiguous prefix is refused client-side, naming its
// candidates, without ever reaching tmux or disturbing the server.
//
//libtmux:real-tmux
func TestSelectLayoutAcceptsAUniquePresetPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}

	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "even-vertical"}); err != nil {
		t.Fatalf("SelectLayout(even-vertical) error = %v", err)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "tile"}); err != nil {
		t.Fatalf("SelectLayout(tile) error = %v, want it applied like tiled", err)
	}
	fromPrefix, err := window.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	prefixLayout, ok := fromPrefix.Layout()
	if !ok || prefixLayout == "" {
		t.Fatal("window reported no layout after tile")
	}

	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "even-vertical"}); err != nil {
		t.Fatalf("SelectLayout(even-vertical) error = %v", err)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "tiled"}); err != nil {
		t.Fatalf("SelectLayout(tiled) error = %v", err)
	}
	fromFullName, err := window.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	fullNameLayout, ok := fromFullName.Layout()
	if !ok || fullNameLayout == "" {
		t.Fatal("window reported no layout after tiled")
	}
	if prefixLayout != fullNameLayout {
		t.Fatalf("SelectLayout(tile) produced %q, SelectLayout(tiled) produced %q, want them equal", prefixLayout, fullNameLayout)
	}

	err = window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "even-"})
	if !errors.Is(err, tmux.ErrInvalidServerCommandRequest) {
		t.Fatalf("SelectLayout(even-) error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if err == nil || !strings.Contains(err.Error(), "even-horizontal") || !strings.Contains(err.Error(), "even-vertical") {
		t.Fatalf("SelectLayout(even-) error = %v, want it to name both candidates", err)
	}
	alive, err := server.IsAlive(ctx)
	if err != nil || !alive {
		t.Fatalf("the tmux server did not survive an ambiguous prefix: (%t, %v)", alive, err)
	}
}
