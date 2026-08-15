package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExactTargetsIncludeWindowIndex(t *testing.T) {
	t.Parallel()

	window := Window{sessionID: "$2", windowID: "@7", windowIndex: 3}
	windowTarget, err := exactWindowTarget(window)
	if err != nil {
		t.Fatalf("exactWindowTarget() error = %v", err)
	}
	if windowTarget != "$2:3" {
		t.Fatalf("exactWindowTarget() = %q, want %q", windowTarget, "$2:3")
	}

	paneTarget, err := exactPaneTarget(Pane{
		sessionID: "$2", windowID: "@7", windowIndex: 3, paneID: "%11",
	})
	if err != nil {
		t.Fatalf("exactPaneTarget() error = %v", err)
	}
	if paneTarget != "$2:3.%11" {
		t.Fatalf("exactPaneTarget() = %q, want %q", paneTarget, "$2:3.%11")
	}
}

func TestExactTargetsRejectNegativeWindowIndexBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "window",
			run: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$2", windowID: "@7", windowIndex: -1,
				}).LastPane(context.Background(), LastPaneRequest{})
				return err
			},
		},
		{
			name: "pane",
			run: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$2", windowID: "@7", windowIndex: -1, paneID: "%11",
				}).Select(context.Background(), PaneSelectRequest{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
			}
		})
	}
}

func TestExactPaneTargetRejectsNULWithoutRetainingTarget(t *testing.T) {
	t.Parallel()

	_, err := exactPaneTarget(Pane{
		sessionID: "$2",
		windowID:  "@7",
		paneID:    "%11\x00secret",
	})
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("exactPaneTarget() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("exactPaneTarget() retained target in error: %v", err)
	}
}
