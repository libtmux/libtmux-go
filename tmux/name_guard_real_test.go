package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Exactly tmux 3.7 rejects a colon or period in a window name and mishandles
// the command; the library refuses it there before any effect. Every other
// release leaves the name to tmux.
//
//libtmux:real-tmux
func TestExact37RefusesWindowNamesWithColonOrPeriod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exact := version.String() == "3.7"
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "anchor"})
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		result, err := server.Cmd(ctx, "list-windows", "-a", "-F", "#{window_id}")
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("list-windows = (%+v, %v)", result, err)
		}
		return len(result.Stdout)
	}
	before := count()

	for _, name := range []string{"a.b", "a:b"} {
		name := name
		refused := func(label string, err error) {
			t.Helper()
			var tooLow *tmux.VersionTooLowError
			if exact != errors.As(err, &tooLow) {
				t.Errorf("%s %q on %s: err = %v, want refusal exactly on 3.7", label, name, version, err)
			}
		}
		_, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: &name})
		refused("NewWindow", err)
		_, err = server.Cmd(ctx, "new-window", "-d", "-n", name)
		refused("raw new-window -n", err)
		_, err = server.Cmd(ctx, "new-session", "-d", "-s", "other", "-n", name)
		refused("raw new-session -n", err)
		window, err := session.ResolveActiveWindow(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = window.Rename(ctx, name)
		refused("Rename", err)
		_, err = server.Cmd(ctx, "rename-window", "-t", window.ID().String(), name)
		refused("raw rename-window", err)
		// A batch is checked like a single command.
		_, err = server.Cmd(ctx, "list-windows", ";", "rename-window", "-t", window.ID().String(), name)
		refused("batched rename-window", err)
	}
	if exact {
		if got := count(); got != before {
			t.Errorf("windows after refusals = %d, want unchanged %d", got, before)
		}
		for _, session := range []string{"other"} {
			if result, _ := server.Cmd(ctx, "has-session", "-t", "="+session); result.ExitCode == 0 {
				t.Errorf("session %q was created despite the refusal", session)
			}
		}
	}
	// A plain name is never refused.
	if _, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: ptr("plain")}); err != nil {
		t.Errorf("plain window name refused: %v", err)
	}
}

func ptr[T any](value T) *T { return &value }
