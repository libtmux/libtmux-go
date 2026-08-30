//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bufio"
	"context"
	"errors"
	"io"
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
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	stream, err := server.OpenNotifications(ctx, sessions[0], tmux.NotificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	const marker = "notification-stream-no-pane-output"
	sendRealPaneCommand(ctx, t, panes[0], "printf '"+marker+"\\n'")
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

	sendRealPaneCommand(ctx, t, panes[0], "printf 'pause-after-armed\\n'")
	for {
		notification, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// tmux frames pane output as extended output only for a client that
		// armed the hold, so this is the arming reaching the server.
		if notification.Kind() == tmux.ControlNotificationExtendedOutput {
			break
		}
		if notification.Kind() == tmux.ControlNotificationOutput {
			t.Fatal("pane output arrived unextended, so the hold never reached tmux")
		}
	}
	if err := stream.ContinuePane(ctx, panes[0].ID()); err != nil {
		t.Fatalf("ContinuePane() error = %v", err)
	}
}
