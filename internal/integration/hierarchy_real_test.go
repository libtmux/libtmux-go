//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmuxtest"
)

// libtmux:parity libtmux.server.Server.clients
// libtmux:parity libtmux.server.Server.panes
// libtmux:parity libtmux.server.Server.sessions
// libtmux:parity libtmux.server.Server.windows
//
//libtmux:real-tmux
func TestServerHierarchyListsMaterializeRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	mustRealCommand(t, server, "new-session", "-d", "-s", "beta", "-n", "own")
	controlClient := startRealControlClient(t, server, "beta")
	_ = snapshotWithAttachedClient(t, server, controlClient)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(Sessions()) = %d, want 2", len(sessions))
	}
	windows, err := server.Windows(ctx)
	if err != nil {
		t.Fatalf("Windows() error = %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("len(Windows()) = %d, want 2", len(windows))
	}
	panes, err := server.Panes(ctx)
	if err != nil {
		t.Fatalf("Panes() error = %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("len(Panes()) = %d, want 2", len(panes))
	}
	clients, err := server.Clients(ctx)
	if err != nil {
		t.Fatalf("Clients() error = %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("len(Clients()) = %d, want 1", len(clients))
	}
}
