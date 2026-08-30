package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestConnectionCallPreservesZeroAndMultipleFrames(t *testing.T) {
	for _, test := range []struct {
		name   string
		frames []controlFrame
	}{
		{name: "zero frames"},
		{
			name: "multiple frames including error",
			frames: []controlFrame{
				{number: 41, rawStdout: []byte("one\n")},
				{number: 42, rawStdout: []byte("bad\n"), failed: true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, reader := newRequestLoopTestClient(t)
			connection := connectionWithTestClient(client)
			response := make(chan controlResponse, 1)
			go func() {
				results, err := connection.Call(context.Background(), "go-alias")
				response <- controlResponse{results: results, err: err}
			}()

			if got := readRequestLoopLine(t, reader); got != "'go-alias'" {
				t.Fatalf("command line = %q, want %q", got, "'go-alias'")
			}
			readRequestLoopFence(t, reader)
			completeControlRequest(client, test.frames...)

			select {
			case got := <-response:
				if got.err != nil {
					t.Fatalf("Call() error = %v", got.err)
				}
				if len(got.results) != len(test.frames) {
					t.Fatalf("Call() returned %d frames, want %d", len(got.results), len(test.frames))
				}
				for index, want := range test.frames {
					result := got.results[index]
					if !slices.Equal(result.Command, []string{"go-alias"}) ||
						!slices.Equal(result.RawStdout, want.rawStdout) ||
						result.Number != want.number || result.Failed != want.failed {
						t.Errorf("Call() frame %d = %#v, want command, payload, number, and failure preserved", index, result)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("Call() did not reach its reply fence")
			}
		})
	}
}

func TestConnectionCallTreatsASemicolonAsAnArgument(t *testing.T) {
	t.Parallel()

	client, reader := newRequestLoopTestClient(t)
	connection := connectionWithTestClient(client)
	response := make(chan error, 1)
	go func() {
		_, err := connection.Call(
			context.Background(), "display-message", "one", ";",
		)
		response <- err
	}()

	if got := readRequestLoopLine(t, reader); got != "'display-message' 'one' ';'" {
		t.Fatalf("command line = %q, want a quoted semicolon operand", got)
	}
	readRequestLoopFence(t, reader)
	completeControlRequest(client, controlFrame{})
	select {
	case err := <-response:
		if err != nil {
			t.Fatalf("Call() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not complete")
	}
}

func TestConnectionCallFailsAfterClose(t *testing.T) {
	t.Parallel()

	stopped := make(chan struct{})
	close(stopped)
	connection := &Connection{pool: &controlLanePool{stopped: stopped}}
	results, err := connection.Call(context.Background(), "display-message", "late")
	if results != nil || !errors.Is(err, ErrControlClosed) {
		t.Fatalf("Call() = (%#v, %v), want nil ErrControlClosed", results, err)
	}
}

func connectionWithTestClient(client *ControlClient) *Connection {
	free := make(chan *ControlClient, 1)
	free <- client
	return &Connection{pool: &controlLanePool{
		clients: []*ControlClient{client},
		free:    free,
		stopped: make(chan struct{}),
		drained: make(chan struct{}),
		live:    1,
	}}
}
