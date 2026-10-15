package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// A caller that stops waiting on a command leaves its lane busy until tmux
// finishes it. A later caller must take a lane that is free, not queue behind
// that one.
//
//libtmux:real-tmux
func TestConnectionLeavesABusyLaneOutUntilItIsFree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{Lanes: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()

	// wait-for blocks its lane until the channel is signalled, which the test
	// does itself at the end, so nothing is left running when it returns.
	const channel = "libtmux-busy-lane"
	held, cancelHeld := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = connection.Call(held, "wait-for", channel)
	cancelHeld()
	if !errors.Is(err, tmux.ErrOutcomeUnknown) {
		t.Fatalf("Call(wait-for) after its deadline = %v, want ErrOutcomeUnknown", err)
	}
	// Twice in a row: the first takes the free lane and returns it, and the
	// second is the one that used to be handed the busy lane.
	for attempt := range 2 {
		quick, cancelQuick := context.WithTimeout(ctx, 2*time.Second)
		_, err := connection.Call(quick, "display-message", "-p", "x")
		cancelQuick()
		if err != nil {
			t.Fatalf("call %d while one lane is busy = %v, want the free lane", attempt, err)
		}
	}
	if _, err := server.Cmd(ctx, "wait-for", "-S", channel); err != nil {
		t.Fatal(err)
	}
}

// tmux 3.6 moves the lanes of a destroyed session to another one. A command
// that opens a client of its own must follow, not attach to the dead session.
//
//libtmux:real-tmux
func TestConnectionBlockingCommandSurvivesItsSessionBeingKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	if !version.AtLeast(minimum) {
		t.Skip("lanes move to another session on tmux 3.6 and later")
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{Lanes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "survivor"}); err != nil {
		t.Fatal(err)
	}
	if err := sessions[0].Kill(ctx); err != nil {
		t.Fatal(err)
	}

	bound := connection.Server()
	if _, err := bound.RunShell(ctx, tmux.RunShellRequest{Command: "true"}); err != nil {
		t.Fatalf("RunShell() after the attached session was killed = %v", err)
	}
	// A signal sent before the wait is remembered, so the wait returns at once.
	if _, err := server.Cmd(ctx, "wait-for", "-S", "libtmux-survivor"); err != nil {
		t.Fatal(err)
	}
	if err := bound.WaitFor(ctx, tmux.WaitForRequest{Channel: "libtmux-survivor"}); err != nil {
		t.Fatalf("WaitFor() after the attached session was killed = %v", err)
	}
}
