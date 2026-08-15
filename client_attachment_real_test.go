//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmux_test

import (
	"context"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.client.Client.attached_pane
// libtmux:parity libtmux.client.Client.attached_session
// libtmux:parity libtmux.client.Client.attached_window
//
//libtmux:real-tmux
func TestClientResolveAttachmentTracksSwitchAndDetachAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	work := clientAdminSessionNamed(ctx, t, server, "work")
	beta, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "attachment-beta"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	sharedWindowID := tmux.WindowID(mustRealCommand(
		t, server, "display-message", "-p", "-t", "work:0", "#{window_id}",
	).Stdout[0])
	mustRealCommand(t, server, "link-window", "-s", sharedWindowID.String(), "-t", beta.ID().String()+":7")
	mustRealCommand(t, server, "select-window", "-t", beta.ID().String()+":7")

	control := tmuxtest.NewControlMode(context.Background(), t, server, work)
	client, err := server.Client(ctx, control.ClientName())
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}
	assertRealClientAttachment(t, client, work.ID(), 0, sharedWindowID)

	mustRealCommand(
		t,
		server,
		"switch-client",
		"-c",
		control.ClientName().String(),
		"-t",
		beta.ID().String(),
	)
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		attachment, resolveErr := client.ResolveAttachment(ctx)
		if resolveErr != nil {
			return false, resolveErr
		}
		session, ok := attachment.Session()
		return ok && session.ID() == beta.ID(), nil
	}); err != nil {
		t.Fatalf("wait for switched attachment: %v", err)
	}
	assertRealClientAttachment(t, client, beta.ID(), 7, sharedWindowID)

	target := control.ClientName()
	if err := server.DetachClient(ctx, tmux.DetachClientRequest{TargetClient: target}); err != nil {
		t.Fatalf("DetachClient() error = %v", err)
	}
	if err := control.Wait(ctx); err != nil {
		t.Fatalf("wait for detached control client: %v", err)
	}
	attachment, err := client.ResolveAttachment(ctx)
	if err != nil {
		t.Fatalf("ResolveAttachment() after detach error = %v", err)
	}
	if _, ok := attachment.Session(); ok {
		t.Fatal("ResolveAttachment() retained a session after detach")
	}
	if _, ok := attachment.Window(); ok {
		t.Fatal("ResolveAttachment() retained a window after detach")
	}
	if _, ok := attachment.Pane(); ok {
		t.Fatal("ResolveAttachment() retained a pane after detach")
	}
}

func assertRealClientAttachment(
	t *testing.T,
	client tmux.Client,
	wantSession tmux.SessionID,
	wantWindowIndex int,
	wantWindow tmux.WindowID,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attachment, err := client.ResolveAttachment(ctx)
	if err != nil {
		t.Fatalf("ResolveAttachment() error = %v", err)
	}
	session, ok := attachment.Session()
	if !ok || session.ID() != wantSession {
		t.Fatalf("Session() = (%#v, %t), want %s", session, ok, wantSession)
	}
	window, ok := attachment.Window()
	if !ok || window.SessionID() != wantSession || window.Index() != wantWindowIndex || window.ID() != wantWindow {
		t.Fatalf(
			"Window() = (%#v, %t), want %s:%d:%s",
			window,
			ok,
			wantSession,
			wantWindowIndex,
			wantWindow,
		)
	}
	pane, ok := attachment.Pane()
	if !ok || pane.SessionID() != wantSession || pane.WindowIndex() != wantWindowIndex || pane.WindowID() != wantWindow {
		t.Fatalf("Pane() = (%#v, %t), want pane in exact %s:%d:%s winlink", pane, ok, wantSession, wantWindowIndex, wantWindow)
	}
}
