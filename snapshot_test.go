package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

func TestSnapshotDecodeErrorRedactsMalformedValue(t *testing.T) {
	t.Parallel()

	const secret = "private-pane-command"
	decodeError := newSnapshotDecodeError(
		"pane", 0, "pane_current_command", secret, "invalid value",
	)
	if decodeError.Value != "[redacted]" {
		t.Fatalf("SnapshotDecodeError.Value = %q, want redacted", decodeError.Value)
	}
	if strings.Contains(decodeError.Error(), secret) {
		t.Fatalf("SnapshotDecodeError.Error() retained private value: %q", decodeError)
	}
	encoded, err := json.Marshal(decodeError)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("SnapshotDecodeError JSON retained private value: %s", encoded)
	}
}

func TestSnapshotDecodeErrorRecordIsOneBased(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	for _, test := range []struct {
		name    string
		records []formatValues
		want    int
	}{
		{
			name:    "first record",
			records: []formatValues{snapshotValues(t, version, "session_name", "missing-id")},
			want:    1,
		},
		{
			name: "second record",
			records: []formatValues{
				snapshotValues(t, version, "session_id", "$0"),
				snapshotValues(t, version, "session_name", "missing-id"),
			},
			want: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := newSnapshot(Server{}, version, snapshotRecords{sessions: test.records})
			var decodeError *SnapshotDecodeError
			if !errors.As(err, &decodeError) || decodeError.Record != test.want {
				t.Fatalf("SnapshotDecodeError = %#v, want record %d", err, test.want)
			}
		})
	}
}

// libtmux:parity libtmux.server.Server.formatter_prefix
// libtmux:parity libtmux.common.PaneDict
// libtmux:parity libtmux.common.SessionDict
// libtmux:parity libtmux.common.WindowDict
// libtmux:parity libtmux.pane.Pane.session
// libtmux:parity libtmux.pane.Pane.window
// libtmux:parity libtmux.window.Window.session
// libtmux:parity libtmux.neo.Obj
// libtmux:parity libtmux.neo.Obj.client_name
// libtmux:parity libtmux.neo.Obj.pane_id
// libtmux:parity libtmux.neo.Obj.pane_index
// libtmux:parity libtmux.neo.Obj.server
// libtmux:parity libtmux.neo.Obj.session_id
// libtmux:parity libtmux.neo.Obj.window_id
// libtmux:parity libtmux.neo.Obj.window_index
// libtmux:parity libtmux.neo.OutputRaw
// libtmux:parity libtmux.neo.OutputsRaw
// libtmux:parity libtmux.neo.parse_output
// libtmux:parity libtmux.neo.parse_output#parameter-branch:output:ede30dacf1d2
func TestSnapshotPreservesLinkedWinlinkAndPaneViews(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	windows := snapshot.Windows()
	if len(windows) != 3 {
		t.Fatalf("len(Windows()) = %d, want 3", len(windows))
	}
	linked := snapshot.WindowsByID(WindowID("@0"))
	if got := windowKeys(linked); !slices.Equal(got, []string{"$0:0", "$1:7"}) {
		t.Fatalf("WindowsByID(@0) = %v, want [$0:0 $1:7]", got)
	}
	if _, err := snapshot.WindowByID(WindowID("@0")); !errors.Is(err, ErrSnapshotAmbiguous) {
		t.Fatalf("WindowByID(@0) error = %v, want ErrSnapshotAmbiguous", err)
	}
	if _, err := snapshot.PaneByID(PaneID("%0")); !errors.Is(err, ErrSnapshotAmbiguous) {
		t.Fatalf("PaneByID(%%0) error = %v, want ErrSnapshotAmbiguous", err)
	}

	beta, err := snapshot.SessionByID(SessionID("$1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := windowKeys(beta.Windows()); !slices.Equal(got, []string{"$1:0", "$1:7"}) {
		t.Fatalf("beta.Windows() = %v, want [$1:0 $1:7]", got)
	}
	if got := paneKeys(beta.Panes()); !slices.Equal(got, []string{"$1:0:%1", "$1:7:%0"}) {
		t.Fatalf("beta.Panes() = %v, want [$1:0:%%1 $1:7:%%0]", got)
	}

	shared := linked[1]
	panes := shared.Panes()
	if got := paneKeys(panes); !slices.Equal(got, []string{"$1:7:%0"}) {
		t.Fatalf("linked window panes = %v, want [$1:7:%%0]", got)
	}
	parent, ok := panes[0].Window()
	if !ok || parent.sessionID != SessionID("$1") || parent.windowIndex != 7 {
		t.Fatalf("pane.Window() = (%#v, %t), want beta winlink 7", parent, ok)
	}
}

func TestSnapshotClientAttachmentUsesExactWinlink(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	client, err := snapshot.ClientByName(ClientName("/dev/pts/9"))
	if err != nil {
		t.Fatal(err)
	}
	session, ok := client.AttachedSession()
	if !ok || session.sessionID != SessionID("$1") {
		t.Fatalf("AttachedSession() = (%#v, %t), want $1", session, ok)
	}
	window, ok := client.AttachedWindow()
	if !ok || window.windowID != WindowID("@0") || window.windowIndex != 7 {
		t.Fatalf("AttachedWindow() = (%#v, %t), want @0 at index 7", window, ok)
	}
	pane, ok := client.AttachedPane()
	if !ok || pane.paneID != PaneID("%0") || pane.windowIndex != 7 {
		t.Fatalf("AttachedPane() = (%#v, %t), want %%0 at index 7", pane, ok)
	}

	detached, err := snapshot.ClientByName(ClientName("/dev/pts/10"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := detached.AttachedSession(); ok {
		t.Fatal("detached client has an attached session")
	}
}

func TestSnapshotAccessorsReturnFreshSlices(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	windows := snapshot.Windows()
	windows[0].windowIndex = 99
	windows = append(windows, Window{})
	if len(windows) != 4 {
		t.Fatalf("appended result length = %d, want 4", len(windows))
	}

	again := snapshot.Windows()
	if len(again) != 3 || again[0].windowIndex != 0 {
		t.Fatalf("Windows() after result mutation = %#v", again)
	}
	byID := snapshot.WindowsByID(WindowID("@0"))
	byID[0].windowIndex = 88
	if got := snapshot.WindowsByID(WindowID("@0"))[0].windowIndex; got != 0 {
		t.Fatalf("WindowsByID() reused result storage: index = %d", got)
	}
	fromSequence := slices.Collect(snapshot.WindowsSeq())
	fromSequence[0].windowIndex = 77
	if got := snapshot.Windows()[0].windowIndex; got != 0 {
		t.Fatalf("WindowsSeq() exposed snapshot storage: index = %d", got)
	}
}

func TestSnapshotAllowsRelationshipsLostBetweenListings(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	snapshot, err := newSnapshot(Server{}, version, snapshotRecords{
		panes: []formatValues{snapshotValues(t, version,
			"session_id", "$9",
			"window_id", "@9",
			"window_index", "4",
			"pane_id", "%9",
			"pane_index", "0",
		)},
	})
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	pane, err := snapshot.PaneByID(PaneID("%9"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pane.Window(); ok {
		t.Fatal("pane resolved a window absent from the observational snapshot")
	}
	if _, ok := pane.Session(); ok {
		t.Fatal("pane resolved a session absent from the observational snapshot")
	}
}

func TestSnapshotRetainsDanglingCrossScopeProjections(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	snapshot, err := newSnapshot(Server{}, version, snapshotRecords{
		sessions: []formatValues{snapshotValues(t, version,
			"session_id", "$0",
			"window_id", "@9",
			"pane_id", "%9",
		)},
		clients: []formatValues{snapshotValues(t, version,
			"client_name", "/dev/pts/9",
			"session_id", "$9",
			"window_id", "@9",
			"window_index", "4",
			"pane_id", "%9",
		)},
	})
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	session, err := snapshot.SessionByID(SessionID("$0"))
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := session.Formats().WindowID(); !ok || id != "@9" {
		t.Fatalf("Session.Formats().WindowID() = %q, %t, want @9, true", id, ok)
	}
	if id, ok := session.Formats().PaneID(); !ok || id != "%9" {
		t.Fatalf("Session.Formats().PaneID() = %q, %t, want %%9, true", id, ok)
	}
	client, err := snapshot.ClientByName(ClientName("/dev/pts/9"))
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := client.Formats().SessionID(); !ok || id != "$9" {
		t.Fatalf("Client.Formats().SessionID() = %q, %t, want $9, true", id, ok)
	}
	if _, ok := client.AttachedSession(); ok {
		t.Fatal("dangling client projection resolved an absent session")
	}
}

func TestSnapshotRejectsMalformedRequiredFields(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		records snapshotRecords
	}{
		{
			name: "missing session id",
			records: snapshotRecords{sessions: []formatValues{
				snapshotValues(t, version, "session_name", "orphan"),
			}},
		},
		{
			name: "invalid window index",
			records: snapshotRecords{windows: []formatValues{
				snapshotValues(t, version,
					"session_id", "$0",
					"window_id", "@0",
					"window_index", "not-an-index",
				),
			}},
		},
		{
			name: "empty pane id",
			records: snapshotRecords{panes: []formatValues{
				snapshotValues(t, version,
					"session_id", "$0",
					"window_id", "@0",
					"window_index", "0",
					"pane_id", "",
					"pane_index", "0",
				),
			}},
		},
		{
			name: "client window without session",
			records: snapshotRecords{clients: []formatValues{
				snapshotValues(t, version,
					"client_name", "/dev/pts/9",
					"window_id", "@0",
					"window_index", "0",
				),
			}},
		},
		{
			name: "client pane without session",
			records: snapshotRecords{clients: []formatValues{
				snapshotValues(t, version,
					"client_name", "/dev/pts/9",
					"pane_id", "%0",
				),
			}},
		},
		{
			name: "invalid session sigil",
			records: snapshotRecords{sessions: []formatValues{
				snapshotValues(t, version, "session_id", "oops"),
			}},
		},
		{
			name: "invalid window sigil",
			records: snapshotRecords{windows: []formatValues{
				snapshotValues(t, version,
					"session_id", "$0",
					"window_id", "$1",
					"window_index", "0",
				),
			}},
		},
		{
			name: "invalid pane number",
			records: snapshotRecords{panes: []formatValues{
				snapshotValues(t, version,
					"session_id", "$0",
					"window_id", "@0",
					"window_index", "0",
					"pane_id", "%-1",
					"pane_index", "0",
				),
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := newSnapshot(Server{}, version, test.records)
			if !errors.Is(err, ErrMalformedSnapshot) {
				t.Fatalf("newSnapshot() error = %v, want ErrMalformedSnapshot", err)
			}
		})
	}
}

func TestZeroSnapshotReturnsNonNilEmptySlices(t *testing.T) {
	t.Parallel()

	var snapshot Snapshot
	if snapshot.Sessions() == nil || snapshot.Windows() == nil ||
		snapshot.Panes() == nil || snapshot.Clients() == nil {
		t.Fatal("zero Snapshot returned a nil collection")
	}
	if _, err := snapshot.SessionByID(SessionID("$0")); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("SessionByID() error = %v, want ErrSnapshotNotFound", err)
	}
}

// libtmux:parity libtmux.neo.SCOPES_BY_LIST_CMD
// libtmux:parity libtmux.neo.fetch_objs
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:list_cmd:42a08906d3f8
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:list_extra_args:36ea1fcdc1b4
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:server:512dfb8018ab
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:server:794e7a25cfdb
// libtmux:parity libtmux.neo.get_output_format
// libtmux:parity libtmux.neo.get_output_format#parameter-branch:list_cmd:a798a4eda880
// libtmux:parity libtmux.neo.get_output_format#parameter-branch:tmux_version:c8853f8280c7
func TestServerSnapshotUsesVersionedFramedListings(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	identityFields := snapshotIdentityFields()
	sessionFields, err := formatFieldsFor("list-sessions", version)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(identityFields, snapshotRowValues(version, nil)),
			ExitCode:  0,
		}},
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(sessionFields, snapshotRowValues(version, map[string]string{
				"session_id": "$0", "session_name": "work",
			})),
			ExitCode: 0,
		}},
		{result: tmuxcmd.Result{RawStdout: nil, ExitCode: 0}},
		{result: tmuxcmd.Result{RawStdout: nil, ExitCode: 0}},
		{result: tmuxcmd.Result{RawStdout: nil, ExitCode: 0}},
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(identityFields, snapshotRowValues(version, nil)),
			ExitCode:  0,
		}},
	}}
	server := serverWithRunner(runner)

	snapshot, err := server.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Sessions(); len(got) != 1 || got[0].sessionID != SessionID("$0") {
		t.Fatalf("Snapshot().Sessions() = %#v, want session $0", got)
	}
	requests := runner.recordedRequests()
	if len(requests) != 6 {
		t.Fatalf("Snapshot() command count = %d, want two identity probes plus four listings", len(requests))
	}
	assertSnapshotRequest(t, requests[0], []string{
		"display-message", "-p", formatTemplate(identityFields),
	})
	assertSnapshotRequest(t, requests[1], []string{
		"list-sessions", "-F" + formatTemplate(sessionFields),
	})
	windowFields, err := formatFieldsFor("list-windows", version)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotRequest(t, requests[2], []string{
		"list-windows", "-a", "-F" + formatTemplate(windowFields),
	})
	paneFields, err := formatFieldsFor("list-panes", version)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotRequest(t, requests[3], []string{
		"list-panes", "-a", "-F" + formatTemplate(paneFields),
	})
	clientFields, err := formatFieldsFor("list-clients", version)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotRequest(t, requests[4], []string{
		"list-clients", "-F" + formatTemplate(clientFields),
	})
	assertSnapshotRequest(t, requests[5], []string{
		"display-message", "-p", formatTemplate(identityFields),
	})
}

func TestServerSnapshotNormalizesListFailureUnlessStrict(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	identity := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(snapshotIdentityFields(), snapshotRowValues(version, nil)),
		ExitCode:  0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: identity},
		{result: tmuxcmd.Result{Stderr: []string{"no server"}, ExitCode: 1}},
		{result: identity},
		{result: identity},
		{result: tmuxcmd.Result{Stderr: []string{"no server"}, ExitCode: 1}},
	}}
	server := serverWithRunner(runner)

	snapshot, err := server.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("lenient Snapshot() error = %v", err)
	}
	if snapshot.Sessions() == nil || len(snapshot.Sessions()) != 0 {
		t.Fatalf("lenient Snapshot().Sessions() = %#v, want non-nil empty", snapshot.Sessions())
	}

	_, err = server.WithStrictErrors().Snapshot(context.Background())
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("strict Snapshot() error = %v, want ErrCommand", err)
	}
}

func TestServerSnapshotNeverNormalizesContextError(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{err: context.Canceled}}}
	server := serverWithRunner(runner)
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot() error = %v, want context canceled", err)
	}
}

func TestServerSnapshotRejectsMalformedVersionInLenientMode(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(snapshotIdentityFields(), snapshotRowValues(version, map[string]string{
				"version": "not-a-version",
			})),
			ExitCode: 0,
		},
	}}}
	server := serverWithRunner(runner)
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("Snapshot() error = %v, want ErrInvalidVersion", err)
	}
}

func TestServerSnapshotRejectsUnsupportedTmuxVersion(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(snapshotIdentityFields(), snapshotRowValues(version, map[string]string{
				"version": "3.1",
			})),
			ExitCode: 0,
		},
	}}}
	server := serverWithRunner(runner)
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("Snapshot() error = %v, want ErrVersionTooLow", err)
	}
}

func TestSnapshotIdentityAcceptsModernOpenBSDVersion(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	values := snapshotValues(t, version, "version", "openbsd-7.8")
	identity, err := decodeSnapshotIdentity("server", 0, values)
	if err != nil {
		t.Fatalf("decodeSnapshotIdentity() error = %v", err)
	}
	if got := identity.version.String(); got != "openbsd-7.8" {
		t.Fatalf("identity version = %q, want openbsd-7.8", got)
	}
	minimum := mustParseVersion(t, MinimumSupportedVersion)
	if identity.version.AtLeast(minimum) {
		t.Fatalf("unprobed OpenBSD identity reports capability at least %s", minimum)
	}
}

func TestServerSnapshotNormalizesVersionProbeFailure(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stderr: []string{"probe failed"}, ExitCode: 1},
	}}}
	server := serverWithRunner(runner)
	snapshot, err := server.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Sessions() == nil || len(snapshot.Sessions()) != 0 {
		t.Fatalf("Snapshot().Sessions() = %#v, want non-nil empty", snapshot.Sessions())
	}
}

func TestServerSnapshotNeverNormalizesOptionValidation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}, ExitCode: 0},
	}}}
	server := Server{state: &serverState{
		options: ServerOptions{Colors: ColorMode(16)},
		runner:  runner,
	}}
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, ErrUnknownColor) {
		t.Fatalf("Snapshot() error = %v, want ErrUnknownColor", err)
	}
}

func TestSnapshotRejectsRowsFromDifferentServerIdentities(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	_, err := newSnapshot(Server{}, version, snapshotRecords{
		sessions: []formatValues{
			snapshotValues(t, version, "session_id", "$0"),
			snapshotValues(t, version, "pid", "999", "session_id", "$1"),
		},
	})
	if !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("newSnapshot() error = %v, want ErrMalformedSnapshot", err)
	}
}

func TestServerSnapshotRejectsRestartAfterEmptyListings(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields := snapshotIdentityFields()
	opening := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, nil)),
		ExitCode:  0,
	}
	closing := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"pid": "999",
		})),
		ExitCode: 0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: opening},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: closing},
	}}
	server := serverWithRunner(runner)
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("Snapshot() error = %v, want ErrMalformedSnapshot", err)
	}
}

func TestServerSnapshotRejectsRestartAfterLenientListingFailure(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields := snapshotIdentityFields()
	opening := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, nil)),
		ExitCode:  0,
	}
	closing := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"pid": "999",
		})),
		ExitCode: 0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: opening},
		{result: tmuxcmd.Result{Stderr: []string{"listing failed"}, ExitCode: 1}},
		{result: closing},
	}}
	server := serverWithRunner(runner)
	if _, err := server.Snapshot(context.Background()); !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("Snapshot() error = %v, want ErrMalformedSnapshot", err)
	}
}

func TestStrictServerSnapshotReportsRestartAndListingFailure(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields := snapshotIdentityFields()
	opening := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, nil)),
		ExitCode:  0,
	}
	closing := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"pid": "999",
		})),
		ExitCode: 0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: opening},
		{result: tmuxcmd.Result{Stderr: []string{"listing failed"}, ExitCode: 1}},
		{result: closing},
	}}
	server := serverWithRunner(runner).WithStrictErrors()
	_, err := server.Snapshot(context.Background())
	if !errors.Is(err, ErrCommand) || !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("Snapshot() error = %v, want ErrCommand and ErrMalformedSnapshot", err)
	}
}

func TestLenientSnapshotClearsVersionWhenServerDisappearsAfterListingFailure(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	opening := tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(snapshotIdentityFields(), snapshotRowValues(version, nil)),
		ExitCode:  0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: opening},
		{result: tmuxcmd.Result{Stderr: []string{"listing failed"}, ExitCode: 1}},
		{result: tmuxcmd.Result{Stderr: []string{"no server"}, ExitCode: 1}},
	}}
	server := serverWithRunner(runner)
	snapshot, err := server.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Version().String(); got != "" {
		t.Fatalf("Snapshot().Version() = %q, want empty after server disappearance", got)
	}
}

func linkedSnapshot(t *testing.T) Snapshot {
	t.Helper()

	version := mustParseVersion(t, "3.7")
	snapshot, err := newSnapshot(Server{}, version, snapshotRecords{
		sessions: []formatValues{
			snapshotValues(t, version, "session_id", "$0", "session_name", "alpha"),
			snapshotValues(t, version, "session_id", "$1", "session_name", "beta"),
		},
		windows: []formatValues{
			snapshotValues(t, version,
				"session_id", "$0", "window_id", "@0", "window_index", "0",
				"window_name", "shared", "window_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@1", "window_index", "0",
				"window_name", "own", "window_active", "0",
			),
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@0", "window_index", "7",
				"window_name", "shared", "window_active", "1",
			),
		},
		panes: []formatValues{
			snapshotValues(t, version,
				"session_id", "$0", "window_id", "@0", "window_index", "0",
				"pane_id", "%0", "pane_index", "0", "pane_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@1", "window_index", "0",
				"pane_id", "%1", "pane_index", "0", "pane_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@0", "window_index", "7",
				"pane_id", "%0", "pane_index", "0", "pane_active", "1",
			),
		},
		clients: []formatValues{
			snapshotValues(t, version,
				"client_name", "/dev/pts/9", "session_id", "$1",
				"window_id", "@0", "window_index", "7", "pane_id", "%0",
			),
			snapshotValues(t, version, "client_name", "/dev/pts/10"),
		},
	})
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	return snapshot
}

func snapshotValues(t *testing.T, version Version, pairs ...string) formatValues {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("snapshotValues requires name/value pairs")
	}
	names := []string{"version", "pid", "start_time", "socket_path"}
	byName := snapshotRowValues(version, nil)
	for index := 0; index < len(pairs); index += 2 {
		name := pairs[index]
		if _, exists := byName[name]; !exists {
			names = append(names, name)
		}
		byName[name] = pairs[index+1]
	}
	fields := make([]formatField, len(names))
	values := make([]string, len(names))
	for index, name := range names {
		fields[index] = formatField{name: name}
		values[index] = byName[name]
	}
	result, err := newFormatValues(version, fields, values)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func snapshotRowValues(version Version, overrides map[string]string) map[string]string {
	values := map[string]string{
		"version":     version.String(),
		"pid":         "123",
		"start_time":  "456",
		"socket_path": "/tmp/libtmux-test.sock",
	}
	for name, value := range overrides {
		values[name] = value
	}
	return values
}

func windowKeys(windows []Window) []string {
	keys := make([]string, len(windows))
	for index, window := range windows {
		keys[index] = window.sessionID.String() + ":" + strconv.Itoa(window.windowIndex)
	}
	return keys
}

func paneKeys(panes []Pane) []string {
	keys := make([]string, len(panes))
	for index, pane := range panes {
		keys[index] = pane.sessionID.String() + ":" +
			strconv.Itoa(pane.windowIndex) + ":" + pane.paneID.String()
	}
	return keys
}

func framedSnapshotRecord(fields []formatField, values map[string]string) []byte {
	record := make([]string, 0, len(fields))
	for _, field := range fields {
		record = append(record, values[field.name])
	}
	return quotedFormatRecord(record)
}

func assertSnapshotRequest(t *testing.T, request tmuxcmd.Request, want []string) {
	t.Helper()
	if !slices.Equal(request.Arguments, want) {
		t.Fatalf("snapshot request = %#v, want %#v", request.Arguments, want)
	}
}
