package tmux

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestControlClientTracksSessionChangeAfterNotificationOverflow(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })
	client := &ControlClient{
		session: Session{sessionID: "$1"},
		stdout: io.NopCloser(strings.NewReader(strings.Join([]string{
			"%output %1 " + strings.Repeat("x", 32),
			"%session-changed $2 current",
			"%session-changed invalid ignored",
			"%client-session-changed /dev/pts/9 $3 other",
		}, "\n") + "\n")),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       make(chan struct{}),
		readDone:      make(chan struct{}),
	}

	client.readStream()
	if _, err := queue.next(context.Background(), 0); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("notification queue error = %v, want overflow", err)
	}
	client.stateMu.Lock()
	current := client.currentSessionID
	client.stateMu.Unlock()
	if current != "$2" {
		t.Fatalf("tracked session = %s, want $2", current)
	}
	if client.Session().ID() != "$1" {
		t.Fatalf("startup Session() = %s, want immutable $1", client.Session().ID())
	}
}

func TestControlClientReconnectSessionCrossesReplyFenceBeforeSampling(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-sessions", version)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$2", "session_name": "current",
		})),
		ExitCode: 0,
	}}}}
	server := serverWithRunner(runner).withDaemon(snapshotServerIdentity{
		version: version, pid: "123", startTime: "456", socketPath: "/tmp/libtmux-test.sock",
	})
	client, reader := newRequestLoopTestClient(t)
	client.server = server
	client.session = Session{server: server.withoutDaemon(), sessionID: "$1"}
	client.currentSessionID = "$1"

	result := make(chan reconnectSessionResult, 1)
	go func() {
		session, err := client.reconnectSession(context.Background())
		result <- reconnectSessionResult{session: session, err: err}
	}()

	readRequestLoopFence(t, reader)
	client.trackSessionChange([]byte("%session-changed $2 current"))
	completeControlRequest(client)

	select {
	case got := <-result:
		name, named := got.session.Name()
		if got.err != nil || got.session.ID() != "$2" || !named || name != "current" ||
			got.session.Server().daemon == nil {
			t.Fatalf("reconnectSession() = (%#v, %v), want materialized current session", got.session, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnectSession() did not cross its reply fence")
	}
}

func TestControlClientReconnectCancellationAfterFenceIsNotIndeterminate(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.reconnectSession(ctx)
		result <- err
	}()

	readRequestLoopFence(t, reader)
	cancel()
	var reconnectErr error
	select {
	case reconnectErr = <-result:
	case <-time.After(time.Second):
		t.Fatal("reconnectSession() did not return after cancellation")
	}
	completeControlRequest(client)
	if !errors.Is(reconnectErr, context.Canceled) || errors.Is(reconnectErr, ErrOutcomeUnknown) {
		t.Fatalf("reconnectSession() error = %v, want plain context cancellation", reconnectErr)
	}
}

func TestControlClientReconnectWaitsForCleanTerminalReader(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("reader failed")
	tests := []struct {
		name    string
		readErr error
		wantID  SessionID
	}{
		{name: "clean", wantID: "$2"},
		{name: "reader failure", readErr: readFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requestDone := make(chan struct{})
			close(requestDone)
			client := &ControlClient{
				session: Session{sessionID: "$2"}, currentSessionID: "$1",
				requests: make(chan *controlRequest), requestDone: requestDone,
				stopRequests: make(chan struct{}), closing: make(chan struct{}),
				readDone: make(chan struct{}),
			}
			ctx := &reconnectWaitContext{
				Context: context.Background(), waiting: make(chan struct{}),
			}
			result := make(chan reconnectSessionResult, 1)
			go func() {
				session, err := client.reconnectSession(ctx)
				result <- reconnectSessionResult{session: session, err: err}
			}()

			<-ctx.waiting
			client.stateMu.Lock()
			client.currentSessionID = "$2"
			client.readErr = test.readErr
			client.stateMu.Unlock()
			close(client.readDone)
			got := <-result
			if got.session.ID() != test.wantID || !errors.Is(got.err, test.readErr) {
				t.Fatalf("reconnectSession() = (%#v, %v), want ID %s, error %v",
					got.session, got.err, test.wantID, test.readErr)
			}
		})
	}
}

func TestControlClientReconnectRejectsRequestedShutdown(t *testing.T) {
	t.Parallel()

	readDone := make(chan struct{})
	close(readDone)
	client := &ControlClient{
		session: Session{sessionID: "$1"}, currentSessionID: "$1",
		readDone: readDone, closing: make(chan struct{}),
	}
	client.closeRequested.Store(true)

	if _, err := client.reconnectSession(context.Background()); !errors.Is(err, ErrControlClosed) {
		t.Fatalf("reconnectSession() error = %v, want ErrControlClosed", err)
	}
}

type reconnectSessionResult struct {
	session Session
	err     error
}

type reconnectWaitContext struct {
	context.Context
	doneCalls int
	waiting   chan struct{}
}

func (c *reconnectWaitContext) Done() <-chan struct{} {
	c.doneCalls++
	if c.doneCalls == 2 {
		close(c.waiting)
	}
	return nil
}
