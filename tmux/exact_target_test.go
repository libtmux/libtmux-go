package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A target names the record's one winlink as briefly as tmux allows: an index
// names whatever window holds it now, and a session lets set-option land on
// that session's current window once the id is gone.
func TestExactTargetsNameOneWinlinkAsBrieflyAsPossible(t *testing.T) {
	t.Parallel()

	linked := func(list string) formatValues {
		return formatValues{values: map[string]string{
			"session_name": "work", "window_linked_sessions_list": list,
		}}
	}
	tests := []struct {
		name    string
		formats formatValues
		window  string
		pane    string
	}{
		{name: "unmaterialized", window: "$2:@7", pane: "$2:.%11"},
		{name: "linked once", formats: linked("work"), window: "@7", pane: "$2:.%11"},
		{name: "linked elsewhere too", formats: linked("work,other"), window: "$2:@7", pane: "$2:.%11"},
		{name: "linked twice", formats: linked("other,work,work"), window: "$2:3", pane: "$2:3.%11"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			window, err := exactWindowTarget(Window{
				formats: test.formats, sessionID: "$2", windowID: "@7", windowIndex: 3,
			})
			if err != nil || window != test.window {
				t.Errorf("exactWindowTarget() = (%q, %v), want %q", window, err, test.window)
			}
			pane, err := exactPaneTarget(Pane{
				formats: test.formats, sessionID: "$2", windowID: "@7", windowIndex: 3, paneID: "%11",
			})
			if err != nil || pane != test.pane {
				t.Errorf("exactPaneTarget() = (%q, %v), want %q", pane, err, test.pane)
			}
		})
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
