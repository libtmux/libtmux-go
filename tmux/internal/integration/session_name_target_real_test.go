//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Exact matching does not override tmux's $, @, and % identifier syntax, so a
// name resembling an identifier must never resolve to another object.
//
//libtmux:real-tmux
func TestASessionNamedLikeAnIdentifierDoesNotReachAnotherSession(t *testing.T) {
	t.Parallel()

	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		InitialSession: &tmux.NewSessionRequest{Name: "victim"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want the one session", len(sessions), err)
	}
	victim := sessions[0]
	identifier := victim.ID().String()

	// Nothing is named after the identifier, so the exact question has one
	// answer and it is no.
	exists, err := server.HasSession(ctx, tmux.HasSessionRequest{Target: identifier})
	if err != nil {
		t.Fatalf("HasSession(%q) error = %v", identifier, err)
	}
	if exists {
		t.Errorf("HasSession(%q) = true, but no session carries that name", identifier)
	}

	// tmux target syntax is still reachable, and there it does mean the
	// identifier.
	byTarget, err := server.HasSession(ctx, tmux.HasSessionRequest{
		Target: identifier, Pattern: true,
	})
	if err != nil {
		t.Fatalf("HasSession(%q, Pattern) error = %v", identifier, err)
	}
	if !byTarget {
		t.Errorf("HasSession(%q, Pattern) = false, want the identifier to resolve",
			identifier)
	}

	// The destructive shape: creating a session under that name must not take
	// the session the identifier points at.
	created, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: identifier, KillExisting: true,
	})
	if err != nil {
		t.Fatalf("NewSession(%q, KillExisting) error = %v", identifier, err)
	}
	if created.ID() == victim.ID() {
		t.Fatal("NewSession() returned the session it was supposed to leave alone")
	}
	survived, err := server.HasSession(ctx, tmux.HasSessionRequest{
		Target: "victim",
	})
	if err != nil {
		t.Fatalf("HasSession(victim) error = %v", err)
	}
	if !survived {
		t.Errorf("creating a session named %q killed the session holding that identifier",
			identifier)
	}
}

// TestReplacingASessionKillsTheOneItFound covers the other half of the same
// hazard: the session to replace is found by name, and killing it by that name
// sends the string back through tmux's ladder, where an identifier outranks it.
//
//libtmux:real-tmux
func TestReplacingASessionKillsTheOneItFound(t *testing.T) {
	t.Parallel()

	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		InitialSession: &tmux.NewSessionRequest{Name: "bystander"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want the one session", len(sessions), err)
	}
	bystander := sessions[0]
	identifier := bystander.ID().String()

	// A session whose name is the identifier of the other one.
	decoy, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: identifier})
	if err != nil {
		t.Fatalf("NewSession(%q) error = %v", identifier, err)
	}
	if decoy.ID() == bystander.ID() {
		t.Fatalf("the decoy is the bystander, so the test proves nothing")
	}

	replacement, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: identifier, KillExisting: true,
	})
	if err != nil {
		t.Fatalf("NewSession(%q, KillExisting) error = %v", identifier, err)
	}
	if replacement.ID() == bystander.ID() {
		t.Fatal("the replacement took over the bystander")
	}

	remaining, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	found := map[tmux.SessionID]bool{}
	for _, session := range remaining {
		found[session.ID()] = true
	}
	if !found[bystander.ID()] {
		t.Errorf("replacing the session named %q killed the session identified by it",
			identifier)
	}
	if found[decoy.ID()] {
		t.Errorf("the session actually named %q survived its own replacement", identifier)
	}
}
