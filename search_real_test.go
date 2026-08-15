//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.server.Server.search_panes
// libtmux:parity libtmux.server.Server.search_sessions
// libtmux:parity libtmux.server.Server.search_windows
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:filter:dad5b2f428ff
// libtmux:parity libtmux.session.Session.panes
// libtmux:parity libtmux.session.Session.search_panes
// libtmux:parity libtmux.session.Session.search_windows
// libtmux:parity libtmux.session.Session.windows
//
// libtmux:parity libtmux.window.Window.panes
// libtmux:parity libtmux.window.Window.search_panes
//
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:list_extra_args:36ea1fcdc1b4
//
//libtmux:real-tmux
func TestRawSearchMethodsFilterRealTmuxListings(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	mustRealCommand(t, server, "new-window", "-d", "-t", "work", "-n", "needle")
	mustRealCommand(t, server, "split-window", "-d", "-t", "work:needle")
	mustRealCommand(t, server, "new-session", "-d", "-s", "other", "-n", "other")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionFilter := tmux.TmuxFilter("#{==:#{session_name},work}")
	sessions, err := server.SearchSessions(ctx, &sessionFilter)
	if err != nil {
		t.Fatalf("SearchSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("SearchSessions() len = %d, want 1", len(sessions))
	}
	if name, ok := sessions[0].Name(); !ok || name != "work" {
		t.Fatalf("SearchSessions() name = (%q, %v), want work", name, ok)
	}

	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	clientFilter := tmux.TmuxFilter(
		"#{==:#{client_name}," + control.ClientName().String() + "}",
	)
	clients, err := server.SearchClients(ctx, &clientFilter)
	version, versionErr := server.Version(ctx)
	if versionErr != nil {
		t.Fatalf("Version() error = %v", versionErr)
	}
	minimum34, parseErr := tmux.ParseVersion("3.4")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if !version.AtLeast(minimum34) {
		if !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("SearchClients() error = %v, want ErrVersionTooLow on tmux %s", err, version)
		}
	} else if err != nil || len(clients) != 1 || clients[0].Name() != control.ClientName() {
		t.Fatalf("SearchClients() = (%#v, %v), want %q", clients, err, control.ClientName())
	}

	windowFilter := tmux.TmuxFilter("#{==:#{window_name},needle}")
	windows, err := server.SearchWindows(ctx, &windowFilter)
	if err != nil {
		t.Fatalf("SearchWindows() error = %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("SearchWindows() len = %d, want 1", len(windows))
	}
	sessionWindows, err := sessions[0].SearchWindows(ctx, &windowFilter)
	if err != nil || len(sessionWindows) != 1 || sessionWindows[0].ID() != windows[0].ID() {
		t.Fatalf("Session.SearchWindows() = (%#v, %v), want matching needle window", sessionWindows, err)
	}

	paneFilter := tmux.TmuxFilter("#{==:#{window_name},needle}")
	panes, err := server.SearchPanes(ctx, &paneFilter)
	if err != nil {
		t.Fatalf("SearchPanes() error = %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("SearchPanes() len = %d, want 2", len(panes))
	}
	sessionPanes, err := sessions[0].SearchPanes(ctx, &paneFilter)
	if err != nil || len(sessionPanes) != 2 {
		t.Fatalf("Session.SearchPanes() = (%#v, %v), want 2 panes", sessionPanes, err)
	}
	windowPanes, err := windows[0].SearchPanes(ctx, nil)
	if err != nil || len(windowPanes) != 2 {
		t.Fatalf("Window.SearchPanes() = (%#v, %v), want 2 panes", windowPanes, err)
	}

	malformed := tmux.TmuxFilter("#{")
	empty, err := server.SearchSessions(ctx, &malformed)
	if err != nil {
		t.Fatalf("SearchSessions(malformed) error = %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("SearchSessions(malformed) = %#v, want non-nil empty", empty)
	}
}
