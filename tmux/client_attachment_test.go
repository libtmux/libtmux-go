package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.client.Client.attached_pane
// libtmux:parity libtmux.client.Client.attached_session
// libtmux:parity libtmux.client.Client.attached_window
func TestClientResolveAttachmentUsesOneStrictSnapshotAndSessionFocus(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := attachmentSnapshotRunner(t, version, snapshotRecords{
		sessions: []formatValues{
			snapshotValues(t, version, "session_id", "$1"),
			snapshotValues(t, version, "session_id", "$2"),
		},
		windows: []formatValues{
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@8", "window_index", "1",
				"window_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$2", "window_id", "@9", "window_index", "0",
				"window_active", "0",
			),
			snapshotValues(t, version,
				"session_id", "$2", "window_id", "@8", "window_index", "7",
				"window_active", "1",
			),
		},
		panes: []formatValues{
			snapshotValues(t, version,
				"session_id", "$1", "window_id", "@8", "window_index", "1",
				"pane_id", "%1", "pane_index", "0", "pane_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$2", "window_id", "@8", "window_index", "7",
				"pane_id", "%2", "pane_index", "0", "pane_active", "1",
			),
			snapshotValues(t, version,
				"session_id", "$2", "window_id", "@8", "window_index", "7",
				"pane_id", "%3", "pane_index", "1", "pane_active", "0",
			),
		},
		clients: []formatValues{
			snapshotValues(t, version,
				"client_name", "/dev/pts/9", "session_id", "$2",
				"window_id", "@8", "window_index", "7", "pane_id", "%3",
			),
		},
	})
	server := serverWithRunner(runner)
	client := Client{server: server, clientName: ClientName("/dev/pts/9")}

	attachment, err := client.ResolveAttachment(context.Background())
	if err != nil {
		t.Fatalf("ResolveAttachment() error = %v", err)
	}
	session, ok := attachment.Session()
	if !ok || session.sessionID != SessionID("$2") {
		t.Fatalf("Session() = (%#v, %t), want $2", session, ok)
	}
	window, ok := attachment.Window()
	if !ok || window.sessionID != SessionID("$2") || window.windowID != WindowID("@8") || window.windowIndex != 7 {
		t.Fatalf("Window() = (%#v, %t), want exact $2:7:@8 winlink", window, ok)
	}
	pane, ok := attachment.Pane()
	if !ok || pane.sessionID != SessionID("$2") || pane.windowIndex != 7 || pane.paneID != PaneID("%2") {
		t.Fatalf("Pane() = (%#v, %t), want session-active %%2 in $2:7", pane, ok)
	}

	// Accessors return copies, so callers cannot rewrite the attachment value.
	session.sessionID = SessionID("$99")
	window.windowIndex = 99
	pane.paneID = PaneID("%99")
	againSession, _ := attachment.Session()
	againWindow, _ := attachment.Window()
	againPane, _ := attachment.Pane()
	if againSession.sessionID != SessionID("$2") || againWindow.windowIndex != 7 || againPane.paneID != PaneID("%2") {
		t.Fatalf("accessor mutation changed attachment: (%#v, %#v, %#v)", againSession, againWindow, againPane)
	}

	requests := runner.recordedRequests()
	if len(requests) != 6 {
		t.Fatalf("ResolveAttachment() command count = %d, want one six-command Snapshot", len(requests))
	}
	identityFields := snapshotIdentityFields()
	assertSnapshotRequest(t, requests[0], []string{
		"display-message", "-p", formatTemplate(identityFields),
	})
	assertSnapshotRequest(t, requests[1], []string{
		"list-sessions", "-F" + formatTemplate(mustFormatFields(t, "list-sessions", version)),
	})
	assertSnapshotRequest(t, requests[2], []string{
		"list-windows", "-a", "-F" + formatTemplate(mustFormatFields(t, "list-windows", version)),
	})
	assertSnapshotRequest(t, requests[3], []string{
		"list-panes", "-a", "-F" + formatTemplate(mustFormatFields(t, "list-panes", version)),
	})
	assertSnapshotRequest(t, requests[4], []string{
		"list-clients", "-F" + formatTemplate(mustFormatFields(t, "list-clients", version)),
	})
	assertSnapshotRequest(t, requests[5], []string{
		"display-message", "-p", formatTemplate(identityFields),
	})
}

func TestClientResolveAttachmentAbsenceAndPartialStates(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name       string
		records    snapshotRecords
		hasSession bool
		hasWindow  bool
		hasPane    bool
	}{
		{name: "client disappeared"},
		{
			name: "client detached",
			records: snapshotRecords{clients: []formatValues{
				snapshotValues(t, version, "client_name", "/dev/pts/9"),
			}},
		},
		{
			name: "attached session stale",
			records: snapshotRecords{clients: []formatValues{
				snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$9"),
			}},
		},
		{
			name: "session only",
			records: snapshotRecords{
				sessions: []formatValues{snapshotValues(t, version, "session_id", "$2")},
				clients: []formatValues{
					snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$2"),
				},
			},
			hasSession: true,
		},
		{
			name: "session and window only",
			records: snapshotRecords{
				sessions: []formatValues{snapshotValues(t, version, "session_id", "$2")},
				windows: []formatValues{snapshotValues(t, version,
					"session_id", "$2", "window_id", "@8", "window_index", "7", "window_active", "1",
				)},
				clients: []formatValues{
					snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$2"),
				},
			},
			hasSession: true,
			hasWindow:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := attachmentSnapshotRunner(t, version, test.records)
			client := Client{server: serverWithRunner(runner), clientName: ClientName("/dev/pts/9")}
			attachment, err := client.ResolveAttachment(context.Background())
			if err != nil {
				t.Fatalf("ResolveAttachment() error = %v", err)
			}
			_, hasSession := attachment.Session()
			_, hasWindow := attachment.Window()
			_, hasPane := attachment.Pane()
			if hasSession != test.hasSession || hasWindow != test.hasWindow || hasPane != test.hasPane {
				t.Fatalf(
					"attachment presence = (%t, %t, %t), want (%t, %t, %t)",
					hasSession, hasWindow, hasPane, test.hasSession, test.hasWindow, test.hasPane,
				)
			}
		})
	}
}

// libtmux:parity libtmux.exc.MultipleActiveWindows
// libtmux:parity libtmux.exc.MultipleActiveWindows.__init__
func TestClientResolveAttachmentKeepsFailuresLoud(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		records snapshotRecords
		want    error
	}{
		{
			name: "duplicate client",
			records: snapshotRecords{clients: []formatValues{
				snapshotValues(t, version, "client_name", "/dev/pts/9"),
				snapshotValues(t, version, "client_name", "/dev/pts/9"),
			}},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "duplicate session",
			records: snapshotRecords{
				sessions: []formatValues{
					snapshotValues(t, version, "session_id", "$2"),
					snapshotValues(t, version, "session_id", "$2"),
				},
				clients: []formatValues{
					snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$2"),
				},
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "multiple active windows",
			records: snapshotRecords{
				sessions: []formatValues{snapshotValues(t, version, "session_id", "$2")},
				windows: []formatValues{
					snapshotValues(t, version,
						"session_id", "$2", "window_id", "@8", "window_index", "7", "window_active", "1",
					),
					snapshotValues(t, version,
						"session_id", "$2", "window_id", "@9", "window_index", "8", "window_active", "1",
					),
				},
				clients: []formatValues{
					snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$2"),
				},
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "multiple active panes",
			records: snapshotRecords{
				sessions: []formatValues{snapshotValues(t, version, "session_id", "$2")},
				windows: []formatValues{snapshotValues(t, version,
					"session_id", "$2", "window_id", "@8", "window_index", "7", "window_active", "1",
				)},
				panes: []formatValues{
					snapshotValues(t, version,
						"session_id", "$2", "window_id", "@8", "window_index", "7",
						"pane_id", "%1", "pane_index", "0", "pane_active", "1",
					),
					snapshotValues(t, version,
						"session_id", "$2", "window_id", "@8", "window_index", "7",
						"pane_id", "%2", "pane_index", "1", "pane_active", "1",
					),
				},
				clients: []formatValues{
					snapshotValues(t, version, "client_name", "/dev/pts/9", "session_id", "$2"),
				},
			},
			want: ErrSnapshotAmbiguous,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := attachmentSnapshotRunner(t, version, test.records)
			client := Client{server: serverWithRunner(runner), clientName: ClientName("/dev/pts/9")}
			attachment, err := client.ResolveAttachment(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("ResolveAttachment() = (%#v, %v), want %v", attachment, err, test.want)
			}
		})
	}
}

func TestClientResolveAttachmentKeepsSnapshotBoundaryErrorsLoud(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	lowVersion := mustParseVersion(t, "3.1")
	tests := []struct {
		name          string
		responses     []versionResponse
		want          error
		wantTransport bool
	}{
		{
			name:      "context",
			responses: []versionResponse{{err: context.Canceled}},
			want:      context.Canceled,
		},
		{
			name:          "transport",
			responses:     []versionResponse{{err: errors.New("exec failed")}},
			wantTransport: true,
		},
		{
			name:      "version",
			responses: []versionResponse{liveIdentityResponse(lowVersion)},
			want:      ErrVersionTooLow,
		},
		{
			name: "malformed",
			responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{
					RawStdout: framedSnapshotRecord(
						mustFormatFields(t, "list-sessions", version),
						snapshotRowValues(version, map[string]string{"session_id": "not-an-id"}),
					),
					ExitCode: 0,
				}},
				{result: tmuxcmd.Result{ExitCode: 0}},
				{result: tmuxcmd.Result{ExitCode: 0}},
				{result: tmuxcmd.Result{ExitCode: 0}},
				liveIdentityResponse(version),
			},
			want: ErrMalformedSnapshot,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: test.responses}
			client := Client{server: serverWithRunner(runner), clientName: ClientName("/dev/pts/9")}
			attachment, err := client.ResolveAttachment(context.Background())
			var transportError *commandTransportError
			if test.wantTransport && !errors.As(err, &transportError) {
				t.Fatalf("ResolveAttachment() = (%#v, %v), want transport error", attachment, err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("ResolveAttachment() = (%#v, %v), want %v", attachment, err, test.want)
			}
		})
	}
}

func attachmentSnapshotRunner(
	t *testing.T,
	version Version,
	records snapshotRecords,
) *versionQueueRunner {
	t.Helper()
	responses := []versionResponse{liveIdentityResponse(version)}
	for _, listing := range []struct {
		command string
		rows    []formatValues
	}{
		{command: "list-sessions", rows: records.sessions},
		{command: "list-windows", rows: records.windows},
		{command: "list-panes", rows: records.panes},
		{command: "list-clients", rows: records.clients},
	} {
		fields := mustFormatFields(t, listing.command, version)
		raw := make([]byte, 0)
		for _, row := range listing.rows {
			values := snapshotRowValues(version, nil)
			for _, field := range fields {
				if value, ok := row.get(field.name); ok {
					values[field.name] = value
				}
			}
			raw = append(raw, framedSnapshotRecord(fields, values)...)
		}
		responses = append(responses, versionResponse{result: tmuxcmd.Result{
			RawStdout: raw,
			ExitCode:  0,
		}})
	}
	responses = append(responses, liveIdentityResponse(version))
	return &versionQueueRunner{responses: responses}
}

func mustFormatFields(t *testing.T, command string, version Version) []formatField {
	t.Helper()
	fields, err := formatFieldsFor(command, version)
	if err != nil {
		t.Fatal(err)
	}
	return fields
}
