//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

//libtmux:real-tmux
func TestEnvironmentOperationsPreserveScopesAndStates(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	snapshot := mustRealSnapshot(t, server)
	sessions := snapshot.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("snapshot sessions = %d, want 1", len(sessions))
	}
	session := sessions[0]

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.SetEnvironment(ctx, "LIBTMUX_SCOPE", "global", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("Server.SetEnvironment() error = %v", err)
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_SCOPE", "session", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("Session.SetEnvironment() error = %v", err)
	}
	global, ok, err := server.GetEnvironment(ctx, "LIBTMUX_SCOPE")
	if err != nil || !ok || global != (tmux.EnvironmentValue{Value: "global"}) {
		t.Fatalf("global environment = (%#v, %t, %v)", global, ok, err)
	}
	local, ok, err := session.GetEnvironment(ctx, "LIBTMUX_SCOPE")
	if err != nil || !ok || local != (tmux.EnvironmentValue{Value: "session"}) {
		t.Fatalf("session environment = (%#v, %t, %v)", local, ok, err)
	}

	if err := session.SetEnvironment(ctx, "LIBTMUX_EMPTY", "", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("set empty environment error = %v", err)
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_EQUALS", "one=two", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("set equals environment error = %v", err)
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_FORMAT", "#{session_name}", tmux.SetEnvironmentOptions{ExpandFormat: true}); err != nil {
		t.Fatalf("set expanded environment error = %v", err)
	}
	if err := session.SetEnvironment(ctx, "LIBTMUX_HIDDEN", "secret", tmux.SetEnvironmentOptions{Hidden: true}); err != nil {
		t.Fatalf("set hidden environment error = %v", err)
	}
	if err := session.SetEnvironment(ctx, "-LIBTMUX_DASH", "literal", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("set dash-prefixed environment error = %v", err)
	}
	if err := session.RemoveEnvironment(ctx, "LIBTMUX_REMOVED"); err != nil {
		t.Fatalf("RemoveEnvironment() error = %v", err)
	}

	values, err := session.ShowEnvironment(ctx)
	if err != nil {
		t.Fatalf("ShowEnvironment() error = %v", err)
	}
	want := map[string]tmux.EnvironmentValue{
		"LIBTMUX_SCOPE":   {Value: "session"},
		"LIBTMUX_EMPTY":   {Value: ""},
		"LIBTMUX_EQUALS":  {Value: "one=two"},
		"LIBTMUX_FORMAT":  {Value: "work"},
		"LIBTMUX_REMOVED": {Removed: true},
		"-LIBTMUX_DASH":   {Value: "literal"},
	}
	for name, value := range want {
		if values[name] != value {
			t.Errorf("ShowEnvironment()[%q] = %#v, want %#v", name, values[name], value)
		}
	}
	if _, exists := values["LIBTMUX_HIDDEN"]; exists {
		t.Fatal("ShowEnvironment() exposed a hidden variable")
	}

	removed, ok, err := session.GetEnvironment(ctx, "LIBTMUX_REMOVED")
	if err != nil || !ok || !removed.Removed {
		t.Fatalf("GetEnvironment(removed) = (%#v, %t, %v), want removed entry", removed, ok, err)
	}
	if err := session.UnsetEnvironment(ctx, "LIBTMUX_EQUALS"); err != nil {
		t.Fatalf("UnsetEnvironment() error = %v", err)
	}
	if _, ok, err := session.GetEnvironment(ctx, "LIBTMUX_EQUALS"); err != nil || ok {
		t.Fatalf("GetEnvironment(unset) = (%t, %v), want false, nil", ok, err)
	}
	if value, ok, err := session.GetEnvironment(ctx, "-LIBTMUX_DASH"); err != nil || !ok || value.Value != "literal" {
		t.Fatalf("GetEnvironment(dash-prefixed) = (%#v, %t, %v)", value, ok, err)
	}
}
