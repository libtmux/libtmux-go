package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// This package classifies a completed tmux failure by matching tmux's English
// diagnostics, because tmux offers nothing else. The wording moves between
// releases - "no space for new pane" became "no space for a new pane" at 3.7 -
// so the tables have to be checked against whatever tmux is running rather
// than against a fixture, which is what the compatibility matrix makes this
// run on every supported release.
//
//libtmux:real-tmux
func TestTmuxsOwnRefusalsStayClassified(t *testing.T) {
	t.Parallel()

	t.Run("a target that is gone", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		server := tmuxtest.NewServer(ctx, t)
		session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "gone"})
		if err != nil {
			t.Fatal(err)
		}
		window, err := session.NewWindow(ctx, tmux.NewWindowRequest{})
		if err != nil {
			t.Fatal(err)
		}
		pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if err := pane.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		err = pane.Kill(ctx)
		if !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("killing a pane twice = %v, want ErrNotFound", err)
		}
		if !errors.Is(err, tmux.ErrCommand) {
			t.Errorf("killing a pane twice = %v, want it to stay a command failure", err)
		}
	})

	t.Run("no server on the socket", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// A socket beside one the harness owns, so the path is short enough
		// for a Unix socket and no tmux has ever answered on it.
		running := tmuxtest.NewServer(ctx, t)
		server, err := tmux.NewServer(tmux.ServerOptions{
			SocketPath: running.SocketPath() + "-absent",
			ConfigFile: os.DevNull,
		})
		if err != nil {
			t.Fatal(err)
		}
		alive, err := server.IsAlive(ctx)
		if err != nil {
			t.Fatalf("IsAlive() error = %v, want an answer", err)
		}
		if alive {
			t.Error("IsAlive() = true for a socket no tmux has used")
		}
		if err := server.CheckAlive(ctx); !errors.Is(err, tmux.ErrNoServer) {
			t.Errorf("CheckAlive() = %v, want ErrNoServer", err)
		}
	})
}
