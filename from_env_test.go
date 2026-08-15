package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.from_env
// libtmux:parity libtmux._internal.env.TMUX
// libtmux:parity libtmux._internal.env.socket_path_from_env
// libtmux:parity libtmux._internal.env.socket_path_from_env#parameter-branch:env:0b9565d05bf8
// libtmux:parity libtmux._internal.env.socket_path_from_env#parameter-branch:env:536dade749bb
func TestNewServerFromEnvRightSplitsCommaSocketWithoutExecution(t *testing.T) {
	t.Setenv("PATH", "")

	server, err := NewServerFromEnv(map[string]string{
		"TMUX": "/tmp/with,comma/socket,not-a-pid,stale-session",
	})
	if err != nil {
		t.Fatalf("NewServerFromEnv() error = %v", err)
	}
	if got := server.SocketPath(); got != "/tmp/with,comma/socket" {
		t.Fatalf("NewServerFromEnv().SocketPath() = %q, want right-split socket", got)
	}
}

func TestNewServerFromEnvDoesNotValidateStalePIDOrSessionComponents(t *testing.T) {
	t.Parallel()

	server, err := NewServerFromEnv(map[string]string{"TMUX": "/sock,,"})
	if err != nil {
		t.Fatalf("NewServerFromEnv() error = %v", err)
	}
	if got := server.SocketPath(); got != "/sock" {
		t.Fatalf("NewServerFromEnv().SocketPath() = %q, want /sock", got)
	}
}

// libtmux:parity libtmux._internal.env.resolve_env
// libtmux:parity libtmux._internal.env.resolve_env#parameter-branch:env:a03c3102158d
func TestNewServerFromEnvDistinguishesNilFromEmptyEnvironment(t *testing.T) {
	t.Setenv("TMUX", "/tmp/process.sock,1,0")

	server, err := NewServerFromEnv(nil)
	if err != nil {
		t.Fatalf("NewServerFromEnv(nil) error = %v", err)
	}
	if got := server.SocketPath(); got != "/tmp/process.sock" {
		t.Fatalf("NewServerFromEnv(nil).SocketPath() = %q, want process environment", got)
	}
	if _, err := NewServerFromEnv(map[string]string{}); !errors.Is(err, ErrNotInsideTmux) {
		t.Fatalf("NewServerFromEnv(empty) error = %v, want ErrNotInsideTmux", err)
	}
}

// libtmux:parity libtmux.exc.NotInsideTmux
// libtmux:parity libtmux.exc.NotInsideTmux.__init__
// libtmux:parity libtmux.exc.NotInsideTmux.__init__#parameter-branch:variable:8ec7f3be64dc
func TestNewServerFromEnvRejectsMalformedValuesWithoutDisclosingThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "unset", env: map[string]string{}},
		{name: "empty", env: map[string]string{"TMUX": ""}},
		{name: "not a triple", env: map[string]string{"TMUX": "private-server-value"}},
		{name: "empty socket", env: map[string]string{"TMUX": ",private-pid,private-session"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewServerFromEnv(test.env)
			if !errors.Is(err, ErrNotInsideTmux) {
				t.Fatalf("NewServerFromEnv() error = %v, want ErrNotInsideTmux", err)
			}
			var envErr *FromEnvError
			if !errors.As(err, &envErr) || envErr.Variable != "TMUX" {
				t.Fatalf("NewServerFromEnv() error = %#v, want *FromEnvError for TMUX", err)
			}
			if test.name == "unset" && err.Error() != "tmux: not inside tmux: $TMUX is unset or empty" {
				t.Fatalf("NewServerFromEnv() error = %q, want Python-parity variable grammar", err)
			}
			for _, private := range []string{"private-server-value", "private-pid", "private-session"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("NewServerFromEnv() error disclosed environment value: %v", err)
				}
			}
		})
	}
}

// libtmux:parity libtmux._internal.env.TMUX_PANE
// libtmux:parity libtmux._internal.env.pane_id_from_env
// libtmux:parity libtmux._internal.env.pane_id_from_env#parameter-branch:env:1437a3b4f272
func TestPaneFromEnvRejectsMalformedStableIDsWithoutExecutionOrDisclosure(t *testing.T) {
	t.Parallel()

	for _, paneID := range []string{"", "7", "private-pane-value"} {
		t.Run(paneID, func(t *testing.T) {
			t.Parallel()

			_, err := PaneFromEnv(context.Background(), map[string]string{
				"TMUX":      "/tmp/unused.sock,1,0",
				"TMUX_PANE": paneID,
			})
			if !errors.Is(err, ErrNotInsideTmux) {
				t.Fatalf("PaneFromEnv() error = %v, want ErrNotInsideTmux", err)
			}
			var envErr *FromEnvError
			if !errors.As(err, &envErr) || envErr.Variable != "TMUX_PANE" {
				t.Fatalf("PaneFromEnv() error = %#v, want *FromEnvError for TMUX_PANE", err)
			}
			if paneID == "" && err.Error() != "tmux: not inside tmux: $TMUX_PANE is unset or empty" {
				t.Fatalf("PaneFromEnv() error = %q, want Python-parity variable grammar", err)
			}
			if paneID != "" && strings.Contains(err.Error(), paneID) {
				t.Fatalf("PaneFromEnv() error disclosed TMUX_PANE: %v", err)
			}
		})
	}
}

// libtmux:parity libtmux.session.Session.from_env
// libtmux:parity libtmux.session.Session.from_env#parameter-branch:env:496823e938f7
// libtmux:parity libtmux.pane.Pane.from_env
// libtmux:parity libtmux.window.Window.from_env
// libtmux:parity libtmux.window.Window.from_env#parameter-branch:env:d6354f13d1ea
func TestDiscoverEnvironmentHierarchyUsesOneTargetedListingAndConnectsParents(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-panes", version)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
				"session_id": "$4", "session_name": "canonical",
				"window_id": "@8", "window_index": "3", "window_name": "editor",
				"pane_id": "%7", "pane_index": "2",
			})),
			ExitCode: 0,
		}},
		liveIdentityResponse(version),
	}}
	server := serverWithRunner(runner)

	pane, window, session, err := discoverEnvironmentHierarchy(
		context.Background(),
		server,
		PaneID("%7"),
	)
	if err != nil {
		t.Fatalf("discoverEnvironmentHierarchy() error = %v", err)
	}
	if pane.paneID != "%7" || pane.windowID != "@8" || pane.sessionID != "$4" {
		t.Fatalf("pane identity = %#v, want canonical hierarchy", pane)
	}
	if window.windowID != "@8" || window.sessionID != "$4" || session.sessionID != "$4" {
		t.Fatalf("parents = (%#v, %#v), want connected canonical hierarchy", window, session)
	}
	if parent, ok := pane.Window(); !ok || parent.windowID != window.windowID || parent.sessionID != window.sessionID {
		t.Fatalf("pane.Window() = (%#v, %t), want discovered window", parent, ok)
	}
	if parent, ok := pane.Session(); !ok || parent.sessionID != session.sessionID {
		t.Fatalf("pane.Session() = (%#v, %t), want discovered session", parent, ok)
	}
	if parent, ok := window.Session(); !ok || parent.sessionID != session.sessionID {
		t.Fatalf("window.Session() = (%#v, %t), want discovered session", parent, ok)
	}

	requests := runner.recordedRequests()
	if len(requests) != 3 {
		t.Fatalf("discovery command count = %d, want two identity probes and one listing", len(requests))
	}
	assertSnapshotRequest(t, requests[1], []string{
		"list-panes", "-t", "%7", "-F" + formatTemplate(fields),
	})
}

func TestDiscoverEnvironmentHierarchyRejectsServerChangeAfterListing(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-panes", version)
	if err != nil {
		t.Fatal(err)
	}
	changedIdentity := liveIdentityResponse(version)
	changedIdentity.result.RawStdout = framedSnapshotRecord(
		snapshotIdentityFields(),
		snapshotRowValues(version, map[string]string{"pid": "999", "start_time": "1000"}),
	)
	runner := &versionQueueRunner{responses: []versionResponse{
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
				"session_id": "$4", "window_id": "@8", "window_index": "3",
				"pane_id": "%7", "pane_index": "2",
			})),
			ExitCode: 0,
		}},
		changedIdentity,
	}}

	pane, window, session, err := discoverEnvironmentHierarchy(
		context.Background(),
		serverWithRunner(runner),
		PaneID("%7"),
	)
	if !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("discoverEnvironmentHierarchy() error = %v, want ErrMalformedSnapshot", err)
	}
	if pane.paneID != "" || window.windowID != "" || session.sessionID != "" {
		t.Fatalf("discovery returned stale hierarchy: (%#v, %#v, %#v)", pane, window, session)
	}
	if requests := runner.recordedRequests(); len(requests) != 3 || requests[1].Arguments[1] != "-t" {
		t.Fatalf("discovery requests = %#v, want one targeted listing between identity probes", requests)
	}
}

func TestDiscoverEnvironmentHierarchySurfacesClosingProbeFailures(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-panes", version)
	if err != nil {
		t.Fatal(err)
	}
	listing := versionResponse{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$4", "window_id": "@8", "window_index": "3",
			"pane_id": "%7", "pane_index": "2",
		})),
		ExitCode: 0,
	}}
	transportFailure := errors.New("closing transport failure")
	tests := []struct {
		name    string
		closing versionResponse
		want    error
	}{
		{
			name: "unavailable",
			closing: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"no server running"}, ExitCode: 1,
			}},
			want: ErrCommand,
		},
		{name: "transport", closing: versionResponse{err: transportFailure}, want: transportFailure},
		{name: "context", closing: versionResponse{err: context.Canceled}, want: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				listing,
				test.closing,
			}}
			_, _, _, err := discoverEnvironmentHierarchy(
				context.Background(),
				serverWithRunner(runner),
				PaneID("%7"),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("discoverEnvironmentHierarchy() error = %v, want %v", err, test.want)
			}
		})
	}
}

// libtmux:parity libtmux._internal.env.pane_id_from_env#parameter-branch:env:22987a3707d3
func TestPaneEnvironmentAcceptsAnyNonemptyPercentTargetAndLetsTmuxClassifyIt(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	for _, rawPaneID := range []string{"%abc", "%", "%-1"} {
		t.Run(rawPaneID, func(t *testing.T) {
			t.Parallel()

			paneID, err := paneIDFromEnvironment(map[string]string{"TMUX_PANE": rawPaneID})
			if err != nil {
				t.Fatalf("paneIDFromEnvironment() error = %v", err)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{Stderr: []string{"can't find pane"}, ExitCode: 1}},
				liveIdentityResponse(version),
			}}
			_, _, _, err = discoverEnvironmentHierarchy(
				context.Background(),
				serverWithRunner(runner),
				paneID,
			)
			if !errors.Is(err, ErrSnapshotNotFound) || errors.Is(err, ErrNotInsideTmux) || errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("discovery error = %v, want tmux-classified ErrSnapshotNotFound", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 3 || !strings.Contains(strings.Join(requests[1].Arguments, " "), "-t "+rawPaneID) {
				t.Fatalf("discovery requests = %#v, want targeted lookup for environment value", requests)
			}
		})
	}
}

func TestHierarchyFromNilEnvironmentSnapshotsProcessEntriesOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	_, _, _, err := hierarchyFromEnvironmentWithProcess(
		context.Background(),
		nil,
		func() []string {
			calls++
			return []string{
				"TMUX=/tmp/process.sock,1,0",
				"TMUX_PANE=not-a-pane",
			}
		},
	)
	if !errors.Is(err, ErrNotInsideTmux) {
		t.Fatalf("hierarchyFromEnvironmentWithProcess() error = %v, want pane environment error", err)
	}
	if calls != 1 {
		t.Fatalf("process environment reads = %d, want one snapshot", calls)
	}
}

func TestDiscoverEnvironmentHierarchyDistinguishesMissingPaneFromDeadServer(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name      string
		responses []versionResponse
		want      error
		reject    error
	}{
		{
			name: "missing pane on live server",
			responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{Stderr: []string{"can't find pane: %99"}, ExitCode: 1}},
				liveIdentityResponse(version),
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "dead before opening probe",
			responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"no server running"}, ExitCode: 1,
			}}},
			want:   ErrCommand,
			reject: ErrSnapshotNotFound,
		},
		{
			name:      "context remains visible",
			responses: []versionResponse{{err: context.Canceled}},
			want:      context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: test.responses}
			_, _, _, err := discoverEnvironmentHierarchy(
				context.Background(),
				serverWithRunner(runner),
				PaneID("%99"),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("discoverEnvironmentHierarchy() error = %v, want %v", err, test.want)
			}
			if test.reject != nil && errors.Is(err, test.reject) {
				t.Fatalf("discoverEnvironmentHierarchy() error = %v, must not match %v", err, test.reject)
			}
		})
	}
}
