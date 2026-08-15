package tmux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

func TestReadOnlyOperationsPreserveContextCancellation(t *testing.T) {
	t.Parallel()

	operations := []struct {
		name string
		call func(Server, context.Context) error
	}{
		{
			name: "server session",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Session(ctx, "$1")
				return err
			},
		},
		{
			name: "server window",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Window(ctx, "@1")
				return err
			},
		},
		{
			name: "server pane",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Pane(ctx, "%1")
				return err
			},
		},
		{
			name: "server client",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Client(ctx, "/dev/pts/1")
				return err
			},
		},
		{
			name: "session refresh",
			call: func(server Server, ctx context.Context) error {
				_, err := (Session{server: server, sessionID: "$1"}).Refresh(ctx)
				return err
			},
		},
		{
			name: "window refresh",
			call: func(server Server, ctx context.Context) error {
				_, err := (Window{server: server, windowID: "@1"}).Refresh(ctx)
				return err
			},
		},
		{
			name: "pane refresh",
			call: func(server Server, ctx context.Context) error {
				_, err := (Pane{server: server, paneID: "%1"}).Refresh(ctx)
				return err
			},
		},
		{
			name: "client refresh",
			call: func(server Server, ctx context.Context) error {
				_, err := (Client{server: server, clientName: "/dev/pts/1"}).Refresh(ctx)
				return err
			},
		},
		{
			name: "session resolve active window",
			call: func(server Server, ctx context.Context) error {
				_, err := (Session{server: server, sessionID: "$1"}).ResolveActiveWindow(ctx)
				return err
			},
		},
		{
			name: "session resolve active pane",
			call: func(server Server, ctx context.Context) error {
				_, _, err := (Session{server: server, sessionID: "$1"}).ResolveActivePane(ctx)
				return err
			},
		},
		{
			name: "window resolve session",
			call: func(server Server, ctx context.Context) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "@1", windowIndex: 0,
				}).ResolveSession(ctx)
				return err
			},
		},
		{
			name: "window resolve active pane",
			call: func(server Server, ctx context.Context) error {
				_, _, err := (Window{
					server: server, sessionID: "$1", windowID: "@1", windowIndex: 0,
				}).ResolveActivePane(ctx)
				return err
			},
		},
		{
			name: "pane resolve window",
			call: func(server Server, ctx context.Context) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@1", windowIndex: 0, paneID: "%1",
				}).ResolveWindow(ctx)
				return err
			},
		},
		{
			name: "pane resolve session",
			call: func(server Server, ctx context.Context) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@1", windowIndex: 0, paneID: "%1",
				}).ResolveSession(ctx)
				return err
			},
		},
		{
			name: "server sessions",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Sessions(ctx)
				return err
			},
		},
		{
			name: "server windows",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Windows(ctx)
				return err
			},
		},
		{
			name: "server panes",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Panes(ctx)
				return err
			},
		},
		{
			name: "server clients",
			call: func(server Server, ctx context.Context) error {
				_, err := server.Clients(ctx)
				return err
			},
		},
	}
	contexts := []struct {
		name string
		new  func(t *testing.T) (context.Context, error)
	}{
		{
			name: "canceled",
			new: func(t *testing.T) (context.Context, error) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, context.Canceled
			},
		},
		{
			name: "deadline exceeded",
			new: func(t *testing.T) (context.Context, error) {
				t.Helper()
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				<-ctx.Done()
				return ctx, context.DeadlineExceeded
			},
		},
	}

	for _, operation := range operations {
		for _, contextCase := range contexts {
			t.Run(operation.name+"/"+contextCase.name, func(t *testing.T) {
				ctx, want := contextCase.new(t)
				runner := &contextBoundRunner{}
				err := operation.call(serverWithRunner(runner).WithStrictErrors(), ctx)
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want errors.Is(_, %v)", err, want)
				}
				if runner.calls != 1 {
					t.Fatalf("runner calls = %d, want 1", runner.calls)
				}
			})
		}
	}
}

type contextBoundRunner struct {
	calls int
}

func (r *contextBoundRunner) Run(ctx context.Context, _ tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.calls++
	return tmuxcmd.Result{}, ctx.Err()
}
