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
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
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
