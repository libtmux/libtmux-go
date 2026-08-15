package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// TestSelectLayoutRefusesWhatWouldKillTheServer is a data-loss gate.
//
// tmux 3.3a does not reject an unrecognised layout: it exits, and every session
// on that socket goes with it, including sessions belonging to somebody else.
// Every other supported version returns an error. The library therefore refuses
// the value itself, so the same typo costs the same error everywhere instead of
// a server on one version.
//
// The second session is the assertion. A test that only checked the returned
// error would pass on 3.3a while the server it was talking to no longer existed.
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
		"main-vertical-mirrored", // a real name, but not on every version
		"zzzz,80x24,0,0,0",       // checksum-shaped, but not hexadecimal
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
