package control_mode_subscribe_test

import (
	"context"
	"os"
	"testing"
	"time"

	tmux "github.com/libtmux/libtmux-go"
	"github.com/libtmux/libtmux-go/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestControlModeSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v), want one session", sessions, err)
	}
	control, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := control.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := sessions[0].Rename(ctx, "control-example"); err != nil {
		t.Fatal(err)
	}

	for notification, err := range control.Notifications(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		if notification.Kind() == tmux.ControlNotificationSessionRenamed {
			return
		}
	}
	t.Fatal("control stream ended before the rename it was watching for")
}
