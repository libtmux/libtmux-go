package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// Omitting an unsupported flag changes operation semantics while still
// reporting success. The default rejects before executing the command.
func TestUnsupportedFeaturesAreRefusedByDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     string
		operate     func(Server) error
		wantFeature string
		wantMinimum string
	}{
		{
			name:    "split-window empty",
			version: "3.6",
			operate: func(server Server) error {
				window := Window{server: server, sessionID: "$1", windowID: "@2", windowIndex: 0}
				_, err := window.SplitPane(context.Background(), SplitPaneRequest{Empty: true})
				return err
			},
			wantFeature: "empty",
			wantMinimum: "3.7",
		},
		{
			name:    "kill-session group",
			version: "3.6",
			operate: func(server Server) error {
				session := Session{server: server, sessionID: "$1"}
				return session.KillWith(context.Background(), SessionKillRequest{Group: true})
			},
			wantFeature: "group",
			wantMinimum: "3.7",
		},
		{
			name:    "run-shell args",
			version: "3.6",
			operate: func(server Server) error {
				_, err := server.RunShell(context.Background(), RunShellRequest{
					Command: "true",
					Args:    []string{"one"},
				})
				return err
			},
			wantFeature: "args",
			wantMinimum: "3.7",
		},
		{
			name:    "send-keys key_name",
			version: "3.3",
			operate: func(server Server) error {
				pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
				return pane.SendKeys(context.Background(), SendKeysRequest{
					Command: Ptr("Enter"),
					KeyName: true,
				})
			},
			wantFeature: "key_name",
			wantMinimum: "3.4",
		},
		{
			name:    "copy-mode page_down",
			version: "3.4",
			operate: func(server Server) error {
				pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
				return pane.CopyMode(context.Background(), CopyModeRequest{PageDown: true})
			},
			wantFeature: "page_down",
			wantMinimum: "3.5",
		},
		{
			name:    "capture-pane hyperlinks",
			version: "3.6",
			operate: func(server Server) error {
				pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
				_, err := pane.Capture(context.Background(), CapturePaneRequest{Hyperlinks: true})
				return err
			},
			wantFeature: "hyperlinks",
			wantMinimum: "3.7",
		},
		{
			name:    "display-message no_expand",
			version: "3.3",
			operate: func(server Server) error {
				_, err := server.DisplayMessage(context.Background(), DisplayMessageRequest{
					Message: "#{pane_id}", NoExpand: true,
				})
				return err
			},
			wantFeature: "no_expand",
			wantMinimum: "3.4",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
			}}
			err := test.operate(serverWithRunner(runner))

			var tooLow *VersionTooLowError
			if !errors.As(err, &tooLow) {
				t.Fatalf("operation error = %v, want a VersionTooLowError", err)
			}
			if tooLow.Feature != test.wantFeature {
				t.Fatalf("refused feature = %q, want %q", tooLow.Feature, test.wantFeature)
			}
			if got := tooLow.Minimum.String(); got != test.wantMinimum {
				t.Fatalf("required version = %q, want %q", got, test.wantMinimum)
			}
			if got := tooLow.Current.String(); got != test.version {
				t.Fatalf("installed version = %q, want %q", got, test.version)
			}
			// The refusal happens before tmux is asked to do anything, so the
			// version probe is all that ran.
			if calls := runner.callCount(); calls != 1 {
				t.Fatalf("runner calls = %d, want only the version probe", calls)
			}
		})
	}
}

// TestUnsupportedFeaturesDegradeOnRequest proves the switch that restores the
// reduced command, and that choosing it delivers what was dropped.
func TestUnsupportedFeaturesDegradeOnRequest(t *testing.T) {
	t.Parallel()

	warnings := make([]Warning, 0, 1)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported:    DegradeUnsupported,
		WarningHandler: func(warning Warning) { warnings = append(warnings, warning) },
	}, runner)

	session := Session{server: server, sessionID: "$1"}
	if err := session.KillWith(context.Background(), SessionKillRequest{Group: true}); err != nil {
		t.Fatalf("KillWith() error = %v", err)
	}
	if len(warnings) != 1 || warnings[0].Feature != "group" ||
		warnings[0].Kind != WarningUnsupportedFeature {
		t.Fatalf("warnings = %#v, want one group warning", warnings)
	}
	// The reduced command ran: kill-session without -g.
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want the probe and the reduced command", requests)
	}
	for _, argument := range requests[1].Arguments {
		if argument == "-g" {
			t.Fatalf("degraded kill-session kept -g: %#v", requests[1].Arguments)
		}
	}
}

// Records retain their materializing handle. A record created before a control
// pool therefore keeps using subprocesses and reports WarningControlPoolUnused.
func TestStaleRecordReportsPayingForProcesses(t *testing.T) {
	t.Parallel()

	var warnings []Warning
	stale := serverWithOptionsAndRunner(ServerOptions{
		WarningHandler: func(warning Warning) { warnings = append(warnings, warning) },
	}, &versionQueueRunner{})
	state := stale.connectionState()

	// No pool open: paying for a process is the documented default and is not
	// worth reporting.
	_, _ = stale.commandEngine(CommandServer)
	if len(warnings) != 0 {
		t.Fatalf("warnings with no pool open = %#v, want none", warnings)
	}

	// A pool opened on another handle sharing this tmux server.
	state.coordination().pools.Add(1)

	_, _ = stale.commandEngine(CommandServer)
	if len(warnings) != 1 || warnings[0].Kind != WarningControlPoolUnused {
		t.Fatalf("warnings with a pool open = %#v, want one unused-pool warning", warnings)
	}

	// A command that needs its own process needs one whatever is open.
	warnings = nil
	_, _ = stale.commandEngine(CommandProcess)
	if len(warnings) != 0 {
		t.Fatalf("warnings for a process command = %#v, want none", warnings)
	}

	// A handle that gave up its engine on purpose, for a read whose result is
	// tmux's exact stdout bytes, is not paying for anything it did not choose.
	_, _ = stale.withoutEngine().commandEngine(CommandServer)
	if len(warnings) != 0 {
		t.Fatalf("warnings for a deliberate process = %#v, want none", warnings)
	}
}

// TestDerivedHandleKeepsServerCoordination proves the handle NewSession returns
// shares the version cache with the one that created it. The two differ in
// their environment, not in the tmux they address, and re-probing costs a
// process for an answer already held.
func TestDerivedHandleKeepsServerCoordination(t *testing.T) {
	t.Parallel()

	original := serverWithOptionsAndRunner(ServerOptions{
		ProcessEnvironment: []string{"TMUX=/tmp/foreign,123,0", "KEEP=value"},
	}, &versionQueueRunner{})
	derived, err := newSessionCommandServer(original)
	if err != nil {
		t.Fatalf("newSessionCommandServer() error = %v", err)
	}

	if derived.connectionState() == original.connectionState() {
		t.Fatal("NewSession's handle shares the configuration it had to change")
	}
	if derived.connectionState().coordination() != original.connectionState().coordination() {
		t.Fatal("NewSession's handle started a second version cache for the same tmux")
	}
	if _, ok := lifecycleEnvironmentValue(
		derived.ProcessEnvironment(), "TMUX",
	); ok {
		t.Fatal("NewSession's handle kept TMUX in its environment")
	}
}
