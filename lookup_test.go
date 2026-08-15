package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.child_id_attribute
// libtmux:parity libtmux.session.Session.from_session_id
// libtmux:parity libtmux.client.Client.from_client_name
// libtmux:parity libtmux.pane.Pane.from_pane_id
// libtmux:parity libtmux.window.Window.from_window_id
// libtmux:parity libtmux.neo.fetch_obj
// libtmux:parity libtmux.neo.fetch_obj#parameter-branch:list_cmd,list_extra_args,obj_id,obj_key,server:87dd2474d396
func TestServerPointLookupsUseScopedFramedListings(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name       string
		command    string
		extra      []string
		row        map[string]string
		lookup     func(Server) (string, string, Server, error)
		wantID     string
		wantFormat string
	}{
		{
			name:    "session",
			command: "list-sessions",
			row: map[string]string{
				"session_id": "$7", "session_name": "work",
			},
			lookup: func(server Server) (string, string, Server, error) {
				value, err := server.Session(context.Background(), SessionID("$7"))
				format, _ := value.Name()
				return value.sessionID.String(), format, value.Server(), err
			},
			wantID:     "$7",
			wantFormat: "work",
		},
		{
			name:    "window",
			command: "list-windows",
			extra:   []string{"-t", "@8"},
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "3",
				"window_name": "editor",
			},
			lookup: func(server Server) (string, string, Server, error) {
				value, err := server.Window(context.Background(), WindowID("@8"))
				format, _ := value.Name()
				return value.windowID.String(), format, value.Server(), err
			},
			wantID:     "@8",
			wantFormat: "editor",
		},
		{
			name:    "pane",
			command: "list-panes",
			extra:   []string{"-t", "%9"},
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "3",
				"pane_id": "%9", "pane_index": "1", "pane_title": "shell",
			},
			lookup: func(server Server) (string, string, Server, error) {
				value, err := server.Pane(context.Background(), PaneID("%9"))
				format, _ := value.Title()
				return value.paneID.String(), format, value.Server(), err
			},
			wantID:     "%9",
			wantFormat: "shell",
		},
		{
			name:    "client",
			command: "list-clients",
			row: map[string]string{
				"client_name": "/dev/pts/9", "client_termname": "xterm-256color",
			},
			lookup: func(server Server) (string, string, Server, error) {
				value, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				format, _ := value.TermName()
				return value.clientName.String(), format, value.Server(), err
			},
			wantID:     "/dev/pts/9",
			wantFormat: "xterm-256color",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, err := formatFieldsFor(test.command, version)
			if err != nil {
				t.Fatal(err)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{
					RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, test.row)),
					ExitCode:  0,
				}},
				liveIdentityResponse(version),
			}}
			server := serverWithRunner(runner).WithStrictErrors()

			id, format, producingServer, err := test.lookup(server)
			if err != nil {
				t.Fatalf("lookup error = %v", err)
			}
			if id != test.wantID || format != test.wantFormat {
				t.Fatalf("lookup = (%q, %q), want (%q, %q)", id, format, test.wantID, test.wantFormat)
			}
			if producingServer != server {
				t.Fatalf("lookup Server() = %#v, want original strict handle", producingServer)
			}

			requests := runner.recordedRequests()
			if len(requests) != 3 {
				t.Fatalf("lookup command count = %d, want opening, listing, closing", len(requests))
			}
			assertSnapshotRequest(t, requests[0], []string{
				"display-message", "-p", formatTemplate(snapshotIdentityFields()),
			})
			wantArguments := append([]string{test.command}, test.extra...)
			wantArguments = append(wantArguments, "-F"+formatTemplate(fields))
			assertSnapshotRequest(t, requests[1], wantArguments)
			assertSnapshotRequest(t, requests[2], []string{
				"display-message", "-p", formatTemplate(snapshotIdentityFields()),
			})
		})
	}
}

func TestServerPointLookupsRedactUnrepresentableTypedTargets(t *testing.T) {
	t.Parallel()

	const secret = "private-point-target"
	tests := []struct {
		name   string
		lookup func(Server) error
	}{
		{
			name: "session",
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), SessionID("$7\x00"+secret))
				return err
			},
		},
		{
			name: "window",
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), WindowID("@8\x00"+secret))
				return err
			},
		},
		{
			name: "pane",
			lookup: func(server Server) error {
				_, err := server.Pane(context.Background(), PaneID("%9\x00"+secret))
				return err
			},
		},
		{
			name: "client",
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), ClientName("client\x00"+secret))
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.lookup(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidServerCommandRequest) || errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("point lookup error = %v, want only ErrInvalidServerCommandRequest", err)
			}
			var requestError *ServerCommandRequestError
			if !errors.As(err, &requestError) || requestError.Value != "[redacted]" {
				t.Fatalf("point lookup error = %#v, want redacted request error", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("point lookup runner calls = %d, want 0", calls)
			}
			assertErrorGraphRedacts(t, err, secret)
		})
	}
}

func TestServerPointLookupsChoosePythonBestWinlink(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		command string
		id      string
		rows    []map[string]string
		lookup  func(Server) (int, error)
		want    int
	}{
		{
			name:    "window active link wins",
			command: "list-windows",
			id:      "@8",
			rows: []map[string]string{
				{"session_id": "$7", "window_id": "@8", "window_index": "1", "window_active": "0"},
				{"session_id": "$7", "window_id": "@8", "window_index": "5", "window_active": "1"},
			},
			lookup: func(server Server) (int, error) {
				value, err := server.Window(context.Background(), WindowID("@8"))
				return value.windowIndex, err
			},
			want: 5,
		},
		{
			name:    "window lowest link wins without active match",
			command: "list-windows",
			id:      "@8",
			rows: []map[string]string{
				{"session_id": "$7", "window_id": "@8", "window_index": "5", "window_active": "0"},
				{"session_id": "$7", "window_id": "@99", "window_index": "0", "window_active": "1"},
				{"session_id": "$7", "window_id": "@8", "window_index": "1", "window_active": "0"},
			},
			lookup: func(server Server) (int, error) {
				value, err := server.Window(context.Background(), WindowID("@8"))
				return value.windowIndex, err
			},
			want: 1,
		},
		{
			name:    "pane active link wins",
			command: "list-panes",
			id:      "%9",
			rows: []map[string]string{
				{"session_id": "$7", "window_id": "@8", "window_index": "1", "window_active": "0", "pane_id": "%9", "pane_index": "0"},
				{"session_id": "$7", "window_id": "@8", "window_index": "5", "window_active": "1", "pane_id": "%9", "pane_index": "0"},
			},
			lookup: func(server Server) (int, error) {
				value, err := server.Pane(context.Background(), PaneID("%9"))
				return value.windowIndex, err
			},
			want: 5,
		},
		{
			name:    "pane lowest link wins without active match",
			command: "list-panes",
			id:      "%9",
			rows: []map[string]string{
				{"session_id": "$7", "window_id": "@8", "window_index": "5", "window_active": "0", "pane_id": "%9", "pane_index": "0"},
				{"session_id": "$7", "window_id": "@8", "window_index": "1", "window_active": "0", "pane_id": "%9", "pane_index": "0"},
			},
			lookup: func(server Server) (int, error) {
				value, err := server.Pane(context.Background(), PaneID("%9"))
				return value.windowIndex, err
			},
			want: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, err := formatFieldsFor(test.command, version)
			if err != nil {
				t.Fatal(err)
			}
			raw := make([]byte, 0)
			for _, row := range test.rows {
				raw = append(raw, framedSnapshotRecord(fields, snapshotRowValues(version, row))...)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{RawStdout: raw, ExitCode: 0}},
				liveIdentityResponse(version),
			}}

			got, err := test.lookup(serverWithRunner(runner))
			if err != nil {
				t.Fatalf("lookup error = %v", err)
			}
			if got != test.want {
				t.Fatalf("selected window index = %d, want %d", got, test.want)
			}
			requests := runner.recordedRequests()
			if len(requests) != 3 || !slices.Equal(requests[1].Arguments[:3], []string{test.command, "-t", test.id}) {
				t.Fatalf("targeted lookup requests = %#v", requests)
			}
		})
	}
}

// libtmux:parity libtmux.exc.MultipleObjectsReturned
// libtmux:parity libtmux.exc.MultipleObjectsReturned.__init__
// libtmux:parity libtmux.exc.MultipleObjectsReturned.__init__#parameter-branch:args:fd422ab3a1ca
// libtmux:parity libtmux.exc.MultipleObjectsReturned.__init__#parameter-branch:count:f4f0d58a810a
// libtmux:parity libtmux.exc.MultipleObjectsReturned.__init__#parameter-branch:query:847ec4f232c8
// libtmux:parity libtmux.exc.ObjectDoesNotExist
// libtmux:parity libtmux.exc.ObjectDoesNotExist.__init__
// libtmux:parity libtmux.exc.ObjectDoesNotExist.__init__#parameter-branch:args:fd422ab3a1ca
// libtmux:parity libtmux.exc.ObjectDoesNotExist.__init__#parameter-branch:query:847ec4f232c8
// libtmux:parity libtmux.exc.PaneNotFound
// libtmux:parity libtmux.exc.PaneNotFound.__init__
// libtmux:parity libtmux.exc.PaneNotFound.__init__#parameter-branch:pane_id:a46e51340c77
// libtmux:parity libtmux.exc.TmuxObjectDoesNotExist
// libtmux:parity libtmux.exc.TmuxObjectDoesNotExist.__init__
// libtmux:parity libtmux.exc.TmuxObjectDoesNotExist.__init__#parameter-branch:list_cmd,list_extra_args,obj_id,obj_key:6c63cfb56ffd
// libtmux:parity libtmux.exc.TmuxObjectDoesNotExist.__init__#parameter-branch:list_extra_args:4ed34060ba84
func TestServerPointLookupCardinality(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		command string
		rows    []map[string]string
		lookup  func(Server) error
		want    error
	}{
		{
			name: "missing session", command: "list-sessions",
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), SessionID("$7"))
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "duplicate session", command: "list-sessions",
			rows: []map[string]string{
				{"session_id": "$7"}, {"session_id": "$7"},
			},
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), SessionID("$7"))
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "missing client", command: "list-clients",
			rows: []map[string]string{{"client_name": "/dev/pts/8"}},
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "duplicate client", command: "list-clients",
			rows: []map[string]string{
				{"client_name": "/dev/pts/9"}, {"client_name": "/dev/pts/9"},
			},
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "targeted window rows omit id", command: "list-windows",
			rows: []map[string]string{{"session_id": "$7", "window_id": "@8", "window_index": "1"}},
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), WindowID("@9"))
				return err
			},
			want: ErrSnapshotNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, err := formatFieldsFor(test.command, version)
			if err != nil {
				t.Fatal(err)
			}
			raw := make([]byte, 0)
			for _, row := range test.rows {
				raw = append(raw, framedSnapshotRecord(fields, snapshotRowValues(version, row))...)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{RawStdout: raw, ExitCode: 0}},
			}}
			if err := test.lookup(serverWithRunner(runner)); !errors.Is(err, test.want) {
				t.Fatalf("lookup error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServerPointLookupErrorPolicy(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	transportFailure := errors.New("test transport failure")
	tests := []struct {
		name     string
		response versionResponse
		lookup   func(Server) error
		want     error
		notWant  error
	}{
		{
			name:     "target not found",
			response: versionResponse{result: tmuxcmd.Result{Stderr: []string{"can't find window: @99"}, ExitCode: 1}},
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), WindowID("@99"))
				return err
			},
			want: ErrSnapshotNotFound, notWant: ErrCommand,
		},
		{
			name:     "dead server stays loud on lenient handle",
			response: versionResponse{result: tmuxcmd.Result{Stderr: []string{"no server running"}, ExitCode: 1}},
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), WindowID("@99"))
				return err
			},
			want: ErrCommand, notWant: ErrSnapshotNotFound,
		},
		{
			name:     "unscoped can't-find text remains command error",
			response: versionResponse{result: tmuxcmd.Result{Stderr: []string{"can't find session: $99"}, ExitCode: 1}},
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), SessionID("$99"))
				return err
			},
			want: ErrCommand, notWant: ErrSnapshotNotFound,
		},
		{
			name:     "context error",
			response: versionResponse{err: context.Canceled},
			lookup: func(server Server) error {
				_, err := server.Pane(context.Background(), PaneID("%99"))
				return err
			},
			want: context.Canceled,
		},
		{
			name:     "transport error",
			response: versionResponse{err: transportFailure},
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				return err
			},
			want: transportFailure,
		},
		{
			name:     "malformed format output",
			response: versionResponse{result: tmuxcmd.Result{RawStdout: []byte("not-framed\n"), ExitCode: 0}},
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				return err
			},
			want: ErrMalformedFormatOutput,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version), test.response,
			}}
			err := test.lookup(serverWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("lookup error = %v, want %v", err, test.want)
			}
			if test.notWant != nil && errors.Is(err, test.notWant) {
				t.Fatalf("lookup error = %v, must not match %v", err, test.notWant)
			}
		})
	}
}

func TestServerPointLookupRejectsIdentityChangeAfterSuccessfulListing(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		command string
		row     map[string]string
		lookup  func(Server) (string, error)
	}{
		{
			name: "session", command: "list-sessions",
			row: map[string]string{"session_id": "$7"},
			lookup: func(server Server) (string, error) {
				value, err := server.Session(context.Background(), SessionID("$7"))
				return value.sessionID.String(), err
			},
		},
		{
			name: "window", command: "list-windows",
			row: map[string]string{"session_id": "$7", "window_id": "@8", "window_index": "3"},
			lookup: func(server Server) (string, error) {
				value, err := server.Window(context.Background(), WindowID("@8"))
				return value.windowID.String(), err
			},
		},
		{
			name: "pane", command: "list-panes",
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "3",
				"pane_id": "%9", "pane_index": "1",
			},
			lookup: func(server Server) (string, error) {
				value, err := server.Pane(context.Background(), PaneID("%9"))
				return value.paneID.String(), err
			},
		},
		{
			name: "client", command: "list-clients",
			row: map[string]string{"client_name": "/dev/pts/9"},
			lookup: func(server Server) (string, error) {
				value, err := server.Client(context.Background(), ClientName("/dev/pts/9"))
				return value.clientName.String(), err
			},
		},
		{
			name: "session refresh", command: "list-sessions",
			row: map[string]string{"session_id": "$7"},
			lookup: func(server Server) (string, error) {
				value, err := (Session{server: server, sessionID: "$7"}).Refresh(context.Background())
				return value.sessionID.String(), err
			},
		},
		{
			name: "window refresh", command: "list-windows",
			row: map[string]string{"session_id": "$7", "window_id": "@8", "window_index": "3"},
			lookup: func(server Server) (string, error) {
				value, err := (Window{server: server, windowID: "@8"}).Refresh(context.Background())
				return value.windowID.String(), err
			},
		},
		{
			name: "pane refresh", command: "list-panes",
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "3",
				"pane_id": "%9", "pane_index": "1",
			},
			lookup: func(server Server) (string, error) {
				value, err := (Pane{server: server, paneID: "%9"}).Refresh(context.Background())
				return value.paneID.String(), err
			},
		},
		{
			name: "client refresh", command: "list-clients",
			row: map[string]string{"client_name": "/dev/pts/9"},
			lookup: func(server Server) (string, error) {
				value, err := (Client{server: server, clientName: "/dev/pts/9"}).Refresh(context.Background())
				return value.clientName.String(), err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, err := formatFieldsFor(test.command, version)
			if err != nil {
				t.Fatal(err)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{
					RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, test.row)),
					ExitCode:  0,
				}},
				changedLiveIdentityResponse(version),
			}}
			identifier, err := test.lookup(serverWithRunner(runner))
			if !errors.Is(err, ErrMalformedSnapshot) {
				t.Fatalf("lookup error = %v, want ErrMalformedSnapshot", err)
			}
			if identifier != "" {
				t.Fatalf("lookup returned stale identifier %q with restart error", identifier)
			}
			if errors.Is(err, ErrSnapshotNotFound) {
				t.Fatalf("lookup error = %v, must not return a stale or absent value", err)
			}
			if calls := runner.callCount(); calls != 3 {
				t.Fatalf("lookup command count = %d, want opening, listing, closing", calls)
			}
		})
	}
}

func TestServerPointLookupSurfacesClosingProbeFailures(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	transportFailure := errors.New("closing transport failure")
	tests := []struct {
		name    string
		closing versionResponse
		want    error
	}{
		{name: "context", closing: versionResponse{err: context.Canceled}, want: context.Canceled},
		{name: "transport", closing: versionResponse{err: transportFailure}, want: transportFailure},
	}
	fields, err := formatFieldsFor("list-sessions", version)
	if err != nil {
		t.Fatal(err)
	}
	listing := versionResponse{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$7",
		})),
		ExitCode: 0,
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				listing,
				test.closing,
			}}
			_, err := serverWithRunner(runner).Session(context.Background(), SessionID("$7"))
			if !errors.Is(err, test.want) {
				t.Fatalf("Session() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServerPointLookupDetectsRestartBeforeReportingAbsence(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	closing := versionResponse{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(
			snapshotIdentityFields(),
			snapshotRowValues(version, map[string]string{"pid": "999", "start_time": "1000"}),
		),
		ExitCode: 0,
	}}
	tests := []struct {
		name        string
		listing     versionResponse
		lookup      func(Server) error
		wantCommand bool
	}{
		{
			name:    "empty listing",
			listing: versionResponse{result: tmuxcmd.Result{ExitCode: 0}},
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), SessionID("$99"))
				return err
			},
		},
		{
			name: "targeted command miss",
			listing: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"can't find window: @99"}, ExitCode: 1,
			}},
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), WindowID("@99"))
				return err
			},
			wantCommand: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				test.listing,
				closing,
			}}
			err := test.lookup(serverWithRunner(runner))
			if !errors.Is(err, ErrMalformedSnapshot) {
				t.Fatalf("lookup error = %v, want ErrMalformedSnapshot", err)
			}
			if errors.Is(err, ErrSnapshotNotFound) {
				t.Fatalf("lookup error = %v, must not report stable absence", err)
			}
			if errors.Is(err, ErrCommand) != test.wantCommand {
				t.Fatalf("lookup ErrCommand match = %t, want %t: %v", errors.Is(err, ErrCommand), test.wantCommand, err)
			}
			if runner.callCount() != 3 {
				t.Fatalf("lookup command count = %d, want opening, listing, closing", runner.callCount())
			}
		})
	}
}

func TestServerPointLookupAbsenceClosingProbePolicy(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name    string
		closing versionResponse
		want    error
	}{
		{name: "same daemon", closing: liveIdentityResponse(version), want: ErrSnapshotNotFound},
		{
			name: "daemon unavailable",
			closing: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"no server running"}, ExitCode: 1,
			}},
			want: ErrSnapshotNotFound,
		},
		{name: "closing context canceled", closing: versionResponse{err: context.Canceled}, want: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{ExitCode: 0}},
				test.closing,
			}}
			_, err := serverWithRunner(runner).Session(context.Background(), SessionID("$99"))
			if !errors.Is(err, test.want) {
				t.Fatalf("Session() error = %v, want %v", err, test.want)
			}
			if runner.callCount() != 3 {
				t.Fatalf("lookup command count = %d, want opening, listing, closing", runner.callCount())
			}
		})
	}
}

func TestServerPointLookupRejectsInvalidStableTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		lookup func(Server) error
		want   error
	}{
		{
			name: "empty session",
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), "")
				return err
			},
			want: ErrMissingTarget,
		},
		{
			name: "session sigil",
			lookup: func(server Server) error {
				_, err := server.Session(context.Background(), "work")
				return err
			},
			want: ErrInvalidTarget,
		},
		{
			name: "window sigil",
			lookup: func(server Server) error {
				_, err := server.Window(context.Background(), "$1")
				return err
			},
			want: ErrInvalidTarget,
		},
		{
			name: "pane body",
			lookup: func(server Server) error {
				_, err := server.Pane(context.Background(), "%-1")
				return err
			},
			want: ErrInvalidTarget,
		},
		{
			name: "empty client",
			lookup: func(server Server) error {
				_, err := server.Client(context.Background(), "")
				return err
			},
			want: ErrMissingTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.lookup(serverWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("lookup error = %v, want %v", err, test.want)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("invalid lookup executed %d commands", calls)
			}
		})
	}
}

// libtmux:parity libtmux.session.Session.refresh
// libtmux:parity libtmux.client.Client.refresh
// libtmux:parity libtmux.client.Client.server
// libtmux:parity libtmux.pane.Pane.refresh
// libtmux:parity libtmux.pane.Pane.server
// libtmux:parity libtmux.window.Window.refresh
// libtmux:parity libtmux.window.Window.server
func TestModelRefreshReturnsNewLiveValue(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name     string
		command  string
		row      map[string]string
		refresh  func(Server) (string, string, Server, bool, error)
		wantID   string
		wantName string
	}{
		{
			name: "session", command: "list-sessions",
			row: map[string]string{"session_id": "$7", "session_name": "fresh-session"},
			refresh: func(server Server) (string, string, Server, bool, error) {
				stale := Session{server: server, sessionID: SessionID("$7")}
				fresh, err := stale.Refresh(context.Background())
				name, ok := fresh.Name()
				_, staleHasName := stale.Name()
				return fresh.sessionID.String(), name, fresh.Server(), ok && !staleHasName, err
			},
			wantID: "$7", wantName: "fresh-session",
		},
		{
			name: "window", command: "list-windows",
			row: map[string]string{"session_id": "$7", "window_id": "@8", "window_index": "3", "window_name": "fresh-window"},
			refresh: func(server Server) (string, string, Server, bool, error) {
				stale := Window{server: server, windowID: WindowID("@8")}
				fresh, err := stale.Refresh(context.Background())
				name, ok := fresh.Name()
				_, staleHasName := stale.Name()
				return fresh.windowID.String(), name, fresh.Server(), ok && !staleHasName, err
			},
			wantID: "@8", wantName: "fresh-window",
		},
		{
			name: "pane", command: "list-panes",
			row: map[string]string{"session_id": "$7", "window_id": "@8", "window_index": "3", "pane_id": "%9", "pane_index": "1", "pane_title": "fresh-pane"},
			refresh: func(server Server) (string, string, Server, bool, error) {
				stale := Pane{server: server, paneID: PaneID("%9")}
				fresh, err := stale.Refresh(context.Background())
				name, ok := fresh.Title()
				_, staleHasName := stale.Title()
				return fresh.paneID.String(), name, fresh.Server(), ok && !staleHasName, err
			},
			wantID: "%9", wantName: "fresh-pane",
		},
		{
			name: "client", command: "list-clients",
			row: map[string]string{"client_name": "/dev/pts/9", "client_termname": "fresh-client"},
			refresh: func(server Server) (string, string, Server, bool, error) {
				stale := Client{server: server, clientName: ClientName("/dev/pts/9")}
				fresh, err := stale.Refresh(context.Background())
				name, ok := fresh.TermName()
				_, staleHasName := stale.TermName()
				return fresh.clientName.String(), name, fresh.Server(), ok && !staleHasName, err
			},
			wantID: "/dev/pts/9", wantName: "fresh-client",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, err := formatFieldsFor(test.command, version)
			if err != nil {
				t.Fatal(err)
			}
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{
					RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, test.row)),
					ExitCode:  0,
				}},
				liveIdentityResponse(version),
			}}
			server := serverWithRunner(runner).WithStrictErrors()

			id, name, producingServer, receiverUnchanged, err := test.refresh(server)
			if err != nil {
				t.Fatalf("Refresh() error = %v", err)
			}
			if id != test.wantID || name != test.wantName || !receiverUnchanged {
				t.Fatalf("Refresh() = (%q, %q, unchanged %t), want (%q, %q, true)", id, name, receiverUnchanged, test.wantID, test.wantName)
			}
			if producingServer != server {
				t.Fatalf("Refresh().Server() = %#v, want original strict handle", producingServer)
			}
		})
	}
}

func liveIdentityResponse(version Version) versionResponse {
	return versionResponse{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(snapshotIdentityFields(), snapshotRowValues(version, nil)),
		ExitCode:  0,
	}}
}

func changedLiveIdentityResponse(version Version) versionResponse {
	return versionResponse{result: tmuxcmd.Result{
		RawStdout: framedSnapshotRecord(
			snapshotIdentityFields(),
			snapshotRowValues(version, map[string]string{"pid": "999", "start_time": "1000"}),
		),
		ExitCode: 0,
	}}
}
