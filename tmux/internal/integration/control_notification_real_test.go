//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestParseControlNotificationAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	lines, scanErrors := scanControlNotificationLines(ctx, control)

	const name = "control notification name"
	renamed, err := sessions[0].Rename(ctx, name)
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("control stream ended before session-renamed notification")
			}
			if !strings.HasPrefix(line, "%session-renamed ") {
				continue
			}
			notification, err := tmux.ParseControlNotification([]byte(line))
			if err != nil {
				t.Fatalf("ParseControlNotification() error = %v", err)
			}
			want := []string{renamed.ID().String(), name}
			if notification.Kind() != tmux.ControlNotificationSessionRenamed ||
				!slices.Equal(notification.Arguments(), want) {
				t.Fatalf(
					"parsed notification = (%q, %#v), want (%q, %#v)",
					notification.Kind(),
					notification.Arguments(),
					tmux.ControlNotificationSessionRenamed,
					want,
				)
			}
			return
		case err, ok := <-scanErrors:
			if !ok {
				scanErrors = nil
				continue
			}
			if err != nil {
				t.Fatalf("scan control stream: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("wait for %%session-renamed: %v", ctx.Err())
		}
	}
}

//libtmux:real-tmux
func TestNotificationStreamZeroOptionsRetainsStructureWithoutPaneOutput(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		FixedShell:     true,
		InitialSession: &tmux.NewSessionRequest{Command: "stty -echo; exec sh"},
	})
	// A ceiling rather than a delay: every wait below ends as soon as its
	// condition holds. One tight enough to be exceeded on a loaded machine
	// fails a test with nothing wrong with it.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, panes[0])
	stream, err := server.OpenNotifications(ctx, sessions[0], tmux.NotificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	const marker = "notification-stream-no-pane-output"
	sendRealPaneCommand(ctx, t, panes[0], "printf '\\n"+marker+"\\n'")
	waitForPaneCapture(ctx, t, panes[0], marker)
	if _, err := sessions[0].Rename(ctx, "renamed"); err != nil {
		t.Fatal(err)
	}

	for {
		notification, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if notification.Kind() == tmux.ControlNotificationOutput ||
			notification.Kind() == tmux.ControlNotificationExtendedOutput {
			t.Fatalf("zero options delivered %s", notification.Kind())
		}
		if notification.Kind() == tmux.ControlNotificationSessionRenamed {
			return
		}
	}
}

func scanControlNotificationLines(
	ctx context.Context,
	control *tmuxtest.ControlMode,
) (<-chan string, <-chan error) {
	lines := make(chan string, 16)
	errorsChannel := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(controlNotificationReader{ctx: ctx, control: control})
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			errorsChannel <- err
		}
		close(errorsChannel)
	}()
	return lines, errorsChannel
}

type controlNotificationReader struct {
	ctx     context.Context
	control *tmuxtest.ControlMode
}

func (r controlNotificationReader) Read(data []byte) (int, error) {
	return r.control.Read(r.ctx, data)
}

//libtmux:real-tmux
func TestNotificationStreamPauseAfterArmsTheServerHold(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	stream, err := server.OpenNotifications(ctx, sessions[0], tmux.NotificationOptions{
		IncludePaneOutput: true,
		PauseAfter:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	const marker = "pause-after-armed"
	sendRealPaneCommand(ctx, t, panes[0], "printf '"+marker+"\\n'")
	// tmux arms the hold on a client that has already attached, so whatever the
	// pane emitted before refresh-client landed is framed unextended and says
	// nothing about the arming. Only this command's own output does.
	for {
		notification, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, output, ok := notification.Output()
		if !ok || !bytes.Contains(output, []byte(marker)) {
			continue
		}
		if notification.Kind() == tmux.ControlNotificationOutput {
			t.Fatal("pane output arrived unextended, so the hold never reached tmux")
		}
		if notification.Kind() == tmux.ControlNotificationExtendedOutput {
			break
		}
	}
	if err := stream.ContinuePane(ctx, panes[0].ID()); err != nil {
		t.Fatalf("ContinuePane() error = %v", err)
	}
}

//libtmux:real-tmux
func TestNotificationStreamSubscribeReportsAFormatWhenItChanges(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	stream, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	if err := stream.Subscribe(ctx, tmux.SubscriptionRequest{
		Name:   "windows",
		Format: "#{session_windows}",
	}); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	// tmux reports the value it first evaluates, then each change; the
	// window opened here must show up as a change to two.
	if _, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
		t.Fatal(err)
	}

	for {
		notification, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		change, ok := notification.Subscription()
		if !ok {
			continue
		}
		if change.Name != "windows" {
			t.Fatalf("Subscription() name = %q, want windows", change.Name)
		}
		if change.Session != sessions[0].ID() {
			t.Fatalf("Subscription() session = %q, want %q", change.Session, sessions[0].ID())
		}
		if change.Window != "" || change.Pane != "" {
			t.Fatalf("session-scoped Subscription() carried window %q pane %q", change.Window, change.Pane)
		}
		if change.Value == "2" {
			return
		}
	}
}

//libtmux:real-tmux
func TestNotificationStreamUnsubscribeStopsAName(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	stream, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	request := tmux.SubscriptionRequest{Name: "name", Format: "#{session_name}"}
	if err := stream.Subscribe(ctx, request); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := stream.Unsubscribe(ctx, request.Name); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
	if err := stream.Subscribe(ctx, tmux.SubscriptionRequest{Name: "a:b", Format: "x"}); err == nil {
		t.Fatal("Subscribe() accepted a name containing the scope delimiter")
	}
}

// BacklogBytes sizes the bound on what a stream holds while nobody reads: a
// pane that prints well past it ends reads with the typed overflow error,
// reporting the limit that was asked for.
//
//libtmux:real-tmux
func TestNotificationStreamBacklogBytesBoundsWhatTheStreamHolds(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	const limit = 64 << 10
	stream, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{
		IncludePaneOutput: true,
		BacklogBytes:      limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// A second, promptly read stream says when the pane has finished writing,
	// so the first is read only after far more than its bound has arrived.
	watcher, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{
		IncludePaneOutput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	if _, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{
		Command: "sh -c 'yes xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx | head -c 1048576; echo finished-writing; sleep 60'",
	}); err != nil {
		t.Fatal(err)
	}
	var tail []byte
	for !bytes.Contains(tail, []byte("finished-writing")) {
		notification, err := watcher.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, output, ok := notification.Output(); ok {
			tail = append(tail[:0:0], append(tail[max(0, len(tail)-32):], output...)...)
		}
	}
	for {
		_, err := stream.Next(ctx)
		if err == nil {
			continue
		}
		overflow, ok := errors.AsType[*tmux.ControlNotificationOverflowError](err)
		if !ok || !errors.Is(err, tmux.ErrControlNotificationOverflow) {
			t.Fatalf("Next() error = %v, want the typed backlog overflow", err)
		}
		if overflow.LimitBytes != limit {
			t.Fatalf("overflow LimitBytes = %d, want %d", overflow.LimitBytes, limit)
		}
		if errors.Is(err, tmux.ErrControlClosed) {
			t.Fatalf("overflow %v matches ErrControlClosed; the process is still running", err)
		}
		return
	}
}

// One errors.Is(err, ErrControlClosed) covers a stream the caller closed and
// one whose server went away.
//
//libtmux:real-tmux
func TestNotificationStreamEndsMatchErrControlClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("closed", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
		}
		stream, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = stream.Next(ctx)
		if !errors.Is(err, tmux.ErrControlClosed) || !errors.Is(err, os.ErrClosed) ||
			errors.Is(err, tmux.ErrControlStreamLost) {
			t.Fatalf("Next() after Close() = %v, want ErrControlClosed and os.ErrClosed, no loss", err)
		}
	})
	t.Run("server exited", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
		}
		stream, err := sessions[0].OpenNotifications(ctx, tmux.NotificationOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		if err := server.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		for {
			_, err = stream.Next(ctx)
			if err != nil {
				break
			}
		}
		if !errors.Is(err, tmux.ErrControlClosed) || !errors.Is(err, tmux.ErrControlStreamLost) {
			t.Fatalf("Next() after the server exited = %v, want ErrControlClosed and ErrControlStreamLost", err)
		}
	})
}
