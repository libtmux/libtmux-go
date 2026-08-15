package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.search_panes
// libtmux:parity libtmux.server.Server.search_sessions
// libtmux:parity libtmux.server.Server.search_windows
// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:filter:dad5b2f428ff
// libtmux:parity libtmux.session.Session.panes
// libtmux:parity libtmux.session.Session.search_panes
// libtmux:parity libtmux.session.Session.search_windows
// libtmux:parity libtmux.session.Session.windows
// libtmux:parity libtmux.window.Window.panes
// libtmux:parity libtmux.window.Window.search_panes
func TestRawSearchMethodsPassFiltersAtEachScope(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	filter := TmuxFilter("#{==:1,1}")
	tests := []struct {
		name    string
		command string
		extra   []string
		row     map[string]string
		search  func(Server) ([]string, error)
		want    []string
	}{
		{
			name:    "server sessions",
			command: "list-sessions",
			row:     map[string]string{"session_id": "$1"},
			search: func(server Server) ([]string, error) {
				values, err := server.SearchSessions(context.Background(), &filter)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.sessionID.String()
				}
				return ids, err
			},
			want: []string{"$1"},
		},
		{
			name:    "server clients",
			command: "list-clients",
			row:     map[string]string{"client_name": "/dev/pts/7"},
			search: func(server Server) ([]string, error) {
				values, err := server.SearchClients(context.Background(), &filter)
				names := make([]string, len(values))
				for index, value := range values {
					names[index] = value.clientName.String()
				}
				return names, err
			},
			want: []string{"/dev/pts/7"},
		},
		{
			name:    "server windows",
			command: "list-windows",
			extra:   []string{"-a"},
			row: map[string]string{
				"session_id": "$1", "window_id": "@2", "window_index": "3",
			},
			search: func(server Server) ([]string, error) {
				values, err := server.SearchWindows(context.Background(), &filter)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.windowID.String()
				}
				return ids, err
			},
			want: []string{"@2"},
		},
		{
			name:    "server panes",
			command: "list-panes",
			extra:   []string{"-a"},
			row: map[string]string{
				"session_id": "$1", "window_id": "@2", "window_index": "3",
				"pane_id": "%4", "pane_index": "0",
			},
			search: func(server Server) ([]string, error) {
				values, err := server.SearchPanes(context.Background(), &filter)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.paneID.String()
				}
				return ids, err
			},
			want: []string{"%4"},
		},
		{
			name:    "session windows",
			command: "list-windows",
			extra:   []string{"-t", "$1"},
			row: map[string]string{
				"session_id": "$1", "window_id": "@2", "window_index": "3",
			},
			search: func(server Server) ([]string, error) {
				values, err := (Session{server: server, sessionID: "$1"}).SearchWindows(
					context.Background(), &filter,
				)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.windowID.String()
				}
				return ids, err
			},
			want: []string{"@2"},
		},
		{
			name:    "session panes",
			command: "list-panes",
			extra:   []string{"-s", "-t", "$1"},
			row: map[string]string{
				"session_id": "$1", "window_id": "@2", "window_index": "3",
				"pane_id": "%4", "pane_index": "0",
			},
			search: func(server Server) ([]string, error) {
				values, err := (Session{server: server, sessionID: "$1"}).SearchPanes(
					context.Background(), &filter,
				)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.paneID.String()
				}
				return ids, err
			},
			want: []string{"%4"},
		},
		{
			name:    "window panes",
			command: "list-panes",
			extra:   []string{"-t", "$1:0"},
			row: map[string]string{
				"session_id": "$1", "window_id": "@2", "window_index": "3",
				"pane_id": "%4", "pane_index": "0",
			},
			search: func(server Server) ([]string, error) {
				values, err := (Window{
					server: server, sessionID: "$1", windowID: "@2",
				}).SearchPanes(
					context.Background(), &filter,
				)
				ids := make([]string, len(values))
				for index, value := range values {
					ids[index] = value.paneID.String()
				}
				return ids, err
			},
			want: []string{"%4"},
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

			got, err := test.search(server)
			if err != nil {
				t.Fatalf("search error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("search ids = %#v, want %#v", got, test.want)
			}

			requests := runner.recordedRequests()
			if len(requests) != 3 {
				t.Fatalf("search command count = %d, want opening, listing, closing", len(requests))
			}
			assertSnapshotRequest(t, requests[0], []string{
				"display-message", "-p", formatTemplate(snapshotIdentityFields()),
			})
			wantArguments := append([]string{test.command}, test.extra...)
			wantArguments = append(
				wantArguments,
				"-f", string(filter),
				"-F"+formatTemplate(fields),
			)
			assertSnapshotRequest(t, requests[1], wantArguments)
			assertSnapshotRequest(t, requests[2], []string{
				"display-message", "-p", formatTemplate(snapshotIdentityFields()),
			})
		})
	}
}

func TestRawSearchRejectsInvalidInputBeforeExecution(t *testing.T) {
	t.Parallel()

	badFilter := TmuxFilter("#{session_name}\x00secret")
	tests := []struct {
		name    string
		search  func(Server) error
		wantErr error
	}{
		{
			name: "filter",
			search: func(server Server) error {
				_, err := server.SearchSessions(context.Background(), &badFilter)
				return err
			},
			wantErr: ErrInvalidServerCommandRequest,
		},
		{
			name: "session target",
			search: func(server Server) error {
				_, err := (Session{server: server, sessionID: "not-a-session"}).SearchWindows(
					context.Background(), nil,
				)
				return err
			},
			wantErr: ErrInvalidTarget,
		},
		{
			name: "window target",
			search: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "not-a-window",
				}).SearchPanes(
					context.Background(), nil,
				)
				return err
			},
			wantErr: ErrInvalidTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{}
			err := test.search(serverWithRunner(runner))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("search error = %v, want %v", err, test.wantErr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
			}
		})
	}
}

// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:filter:dad5b2f428ff
func TestRawSearchDistinguishesNilAndEmptyFilters(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-sessions", version)
	if err != nil {
		t.Fatal(err)
	}
	empty := TmuxFilter("")
	for _, test := range []struct {
		name   string
		filter *TmuxFilter
		want   []string
	}{
		{name: "nil", want: []string{"list-sessions", "-F" + formatTemplate(fields)}},
		{
			name:   "empty",
			filter: &empty,
			want:   []string{"list-sessions", "-f", "", "-F" + formatTemplate(fields)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{ExitCode: 0}},
				liveIdentityResponse(version),
			}}
			values, err := serverWithRunner(runner).SearchSessions(
				context.Background(), test.filter,
			)
			if err != nil {
				t.Fatalf("SearchSessions() error = %v", err)
			}
			if values == nil || len(values) != 0 {
				t.Fatalf("SearchSessions() = %#v, want non-nil empty", values)
			}
			requests := runner.recordedRequests()
			if len(requests) != 3 {
				t.Fatalf("runner calls = %d, want 3", len(requests))
			}
			assertSnapshotRequest(t, requests[1], test.want)
		})
	}
}

// libtmux:parity libtmux.neo.fetch_objs#parameter-branch:filter:dad5b2f428ff
func TestSearchClientsRequiresTmux34OnlyWhenFiltering(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.3a")
	minimum := mustParseVersion(t, "3.4")
	filter := TmuxFilter("#{==:1,1}")
	t.Run("filtered", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{
			liveIdentityResponse(version),
		}}
		_, err := serverWithRunner(runner).SearchClients(context.Background(), &filter)
		var tooLow *VersionTooLowError
		if !errors.As(err, &tooLow) || tooLow.Current.String() != version.String() ||
			tooLow.Minimum.String() != minimum.String() {
			t.Fatalf("SearchClients() error = %#v, want current %s minimum %s", err, version, minimum)
		}
		if runner.callCount() != 1 {
			t.Fatalf("runner calls = %d, want only opening identity probe", runner.callCount())
		}
	})

	t.Run("unfiltered", func(t *testing.T) {
		t.Parallel()
		fields, err := formatFieldsFor("list-clients", version)
		if err != nil {
			t.Fatal(err)
		}
		runner := &versionQueueRunner{responses: []versionResponse{
			liveIdentityResponse(version),
			{result: tmuxcmd.Result{
				RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
					"client_name": "/dev/pts/7",
				})),
				ExitCode: 0,
			}},
			liveIdentityResponse(version),
		}}
		clients, err := serverWithRunner(runner).SearchClients(context.Background(), nil)
		if err != nil || len(clients) != 1 || clients[0].clientName != "/dev/pts/7" {
			t.Fatalf("SearchClients(nil) = (%#v, %v), want one client", clients, err)
		}
	})
}

func TestScopedRawSearchDiscardsRowsOutsideReceiver(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-windows", version)
	if err != nil {
		t.Fatal(err)
	}
	output := append(
		framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$2", "window_id": "@7", "window_index": "0",
		})),
		framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$1", "window_id": "@8", "window_index": "1",
		}))...,
	)
	runner := &versionQueueRunner{responses: []versionResponse{
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{RawStdout: output, ExitCode: 0}},
		liveIdentityResponse(version),
	}}
	server := serverWithRunner(runner)
	values, err := (Session{server: server, sessionID: "$1"}).SearchWindows(
		context.Background(), nil,
	)
	if err != nil {
		t.Fatalf("SearchWindows() error = %v", err)
	}
	if len(values) != 1 || values[0].sessionID != "$1" || values[0].windowID != "@8" {
		t.Fatalf("SearchWindows() = %#v, want only $1:@8", values)
	}
}

func TestWindowSearchPanesDiscardsRowsOutsideExactWinlink(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	fields, err := formatFieldsFor("list-panes", version)
	if err != nil {
		t.Fatal(err)
	}
	output := append(
		framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$2", "window_id": "@7", "window_index": "4",
			"pane_id": "%9", "pane_index": "0",
		})),
		framedSnapshotRecord(fields, snapshotRowValues(version, map[string]string{
			"session_id": "$1", "window_id": "@7", "window_index": "3",
			"pane_id": "%9", "pane_index": "0",
		}))...,
	)
	runner := &versionQueueRunner{responses: []versionResponse{
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{RawStdout: output, ExitCode: 0}},
		liveIdentityResponse(version),
	}}
	server := serverWithRunner(runner)
	values, err := (Window{
		server: server, sessionID: "$1", windowID: "@7",
	}).SearchPanes(context.Background(), nil)
	if err != nil {
		t.Fatalf("SearchPanes() error = %v", err)
	}
	if len(values) != 1 || values[0].sessionID != "$1" ||
		values[0].windowID != "@7" || values[0].paneID != "%9" {
		t.Fatalf("SearchPanes() = %#v, want only $1:@7.%%9", values)
	}
	requests := runner.recordedRequests()
	assertSnapshotRequest(t, requests[1], []string{
		"list-panes", "-t", "$1:0", "-F" + formatTemplate(fields),
	})
}

func TestRawSearchListFailureIsLenientByDefaultAndStrictOnOptIn(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "lenient", true: "strict"}[strict], func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{
				liveIdentityResponse(version),
				{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"failed"}}},
				liveIdentityResponse(version),
			}}
			server := serverWithRunner(runner)
			if strict {
				server = server.WithStrictErrors()
			}

			values, err := server.SearchSessions(context.Background(), nil)
			if strict {
				var commandError *CommandError
				if !errors.As(err, &commandError) {
					t.Fatalf("SearchSessions() error = %v, want CommandError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SearchSessions() error = %v", err)
			}
			if values == nil || len(values) != 0 {
				t.Fatalf("SearchSessions() = %#v, want non-nil empty slice", values)
			}
		})
	}
}
