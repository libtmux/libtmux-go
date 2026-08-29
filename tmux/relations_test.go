package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// libtmux:parity libtmux.session.Session.active_pane
// libtmux:parity libtmux.session.Session.active_window
// libtmux:parity libtmux.exc.NoActiveWindow
// libtmux:parity libtmux.exc.NoActiveWindow.__init__
// libtmux:parity libtmux.exc.NoWindowsExist
// libtmux:parity libtmux.exc.NoWindowsExist.__init__
// libtmux:parity libtmux.window.Window.active_pane
// libtmux:parity libtmux.window.Window.linked_sessions
func TestSnapshotActiveRelationsAndLinkedSessions(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	beta, err := snapshot.SessionByID(SessionID("$1"))
	if err != nil {
		t.Fatal(err)
	}
	activeWindow, ok := beta.ActiveWindow()
	if !ok || activeWindow.windowID != WindowID("@0") || activeWindow.windowIndex != 7 {
		t.Fatalf("ActiveWindow() = (%#v, %t), want @0 at index 7", activeWindow, ok)
	}
	activePane, ok := beta.ActivePane()
	if !ok || activePane.paneID != PaneID("%0") || activePane.windowIndex != 7 {
		t.Fatalf("ActivePane() = (%#v, %t), want %%0 at index 7", activePane, ok)
	}
	windowPane, ok := activeWindow.ActivePane()
	if !ok || windowPane.paneID != activePane.paneID {
		t.Fatalf("Window.ActivePane() = (%#v, %t), want %%0", windowPane, ok)
	}

	linked, ok := snapshot.WindowsByID(WindowID("@0"))[0].LinkedSessions()
	if !ok {
		t.Fatal("snapshot window carries no linked sessions")
	}
	linkedIDs := make([]SessionID, len(linked))
	for index, session := range linked {
		linkedIDs[index] = session.sessionID
	}
	if want := []SessionID{"$0", "$1"}; !slices.Equal(linkedIDs, want) {
		t.Fatalf("LinkedSessions() = %v, want %v", linkedIDs, want)
	}
}

func TestZeroModelRelationsReturnCommaOKFalseAndFreshEmpty(t *testing.T) {
	t.Parallel()

	if _, ok := (Session{}).ActiveWindow(); ok {
		t.Fatal("zero Session has an active window")
	}
	if _, ok := (Session{}).ActivePane(); ok {
		t.Fatal("zero Session has an active pane")
	}
	if _, ok := (Window{}).ActivePane(); ok {
		t.Fatal("zero Window has an active pane")
	}
	if sessions, ok := (Window{}).LinkedSessions(); ok || sessions != nil {
		t.Fatalf("zero Window.LinkedSessions() = (%#v, %t), want (nil, false)", sessions, ok)
	}
	if windows, ok := (Session{}).Windows(); ok || windows != nil {
		t.Fatalf("zero Session.Windows() = (%#v, %t), want (nil, false)", windows, ok)
	}
	if panes, ok := (Session{}).Panes(); ok || panes != nil {
		t.Fatalf("zero Session.Panes() = (%#v, %t), want (nil, false)", panes, ok)
	}
	if panes, ok := (Window{}).Panes(); ok || panes != nil {
		t.Fatalf("zero Window.Panes() = (%#v, %t), want (nil, false)", panes, ok)
	}
}

// libtmux:parity libtmux.pane.Pane.session
// libtmux:parity libtmux.pane.Pane.window
// libtmux:parity libtmux.session.Session.active_pane
// libtmux:parity libtmux.session.Session.active_window
// libtmux:parity libtmux.window.Window.active_pane
// libtmux:parity libtmux.window.Window.session
func TestLiveRelationshipResolversHydrateExactGraph(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	records := liveRelationshipRecords(t, version)
	newSubjects := func(t *testing.T) (Session, Window, Pane, *versionQueueRunner) {
		t.Helper()
		runner := attachmentSnapshotRunner(t, version, records)
		server := serverWithRunner(runner)
		return Session{server: server, sessionID: SessionID("$2")},
			Window{
				server: server, sessionID: SessionID("$2"),
				windowID: WindowID("@8"), windowIndex: 7,
			},
			Pane{
				server: server, sessionID: SessionID("$2"),
				windowID: WindowID("@8"), windowIndex: 7,
				paneID: PaneID("%2"), paneIndex: 0,
			}, runner
	}

	t.Run("session active window", func(t *testing.T) {
		session, _, _, runner := newSubjects(t)
		window, err := session.ResolveActiveWindow(context.Background())
		if err != nil {
			t.Fatalf("ResolveActiveWindow() error = %v", err)
		}
		if window.sessionID != SessionID("$2") || window.windowID != WindowID("@8") || window.windowIndex != 7 {
			t.Fatalf("ResolveActiveWindow() = %#v, want exact $2:7:@8 winlink", window)
		}
		if got := len(runner.recordedRequests()); got != 6 {
			t.Fatalf("ResolveActiveWindow() command count = %d, want one six-command Snapshot", got)
		}
	})

	t.Run("session active pane", func(t *testing.T) {
		session, _, _, _ := newSubjects(t)
		pane, ok, err := session.ResolveActivePane(context.Background())
		if err != nil {
			t.Fatalf("ResolveActivePane() error = %v", err)
		}
		if !ok || pane.sessionID != SessionID("$2") || pane.windowIndex != 7 || pane.paneID != PaneID("%2") {
			t.Fatalf("ResolveActivePane() = (%#v, %t), want exact $2:7:%%2 view", pane, ok)
		}
	})

	t.Run("window session", func(t *testing.T) {
		_, window, _, _ := newSubjects(t)
		session, err := window.ResolveSession(context.Background())
		if err != nil {
			t.Fatalf("ResolveSession() error = %v", err)
		}
		if session.sessionID != SessionID("$2") {
			t.Fatalf("ResolveSession() = %#v, want $2", session)
		}
	})

	t.Run("window active pane", func(t *testing.T) {
		_, window, _, _ := newSubjects(t)
		pane, ok, err := window.ResolveActivePane(context.Background())
		if err != nil {
			t.Fatalf("ResolveActivePane() error = %v", err)
		}
		if !ok || pane.sessionID != SessionID("$2") || pane.windowIndex != 7 || pane.paneID != PaneID("%2") {
			t.Fatalf("ResolveActivePane() = (%#v, %t), want exact $2:7:%%2 view", pane, ok)
		}
	})

	t.Run("pane window", func(t *testing.T) {
		_, _, pane, _ := newSubjects(t)
		window, err := pane.ResolveWindow(context.Background())
		if err != nil {
			t.Fatalf("ResolveWindow() error = %v", err)
		}
		if window.sessionID != SessionID("$2") || window.windowID != WindowID("@8") || window.windowIndex != 7 {
			t.Fatalf("ResolveWindow() = %#v, want exact $2:7:@8 winlink", window)
		}
	})

	t.Run("pane session", func(t *testing.T) {
		_, _, pane, _ := newSubjects(t)
		session, err := pane.ResolveSession(context.Background())
		if err != nil {
			t.Fatalf("ResolveSession() error = %v", err)
		}
		if session.sessionID != SessionID("$2") {
			t.Fatalf("ResolveSession() = %#v, want $2", session)
		}
	})
}

// libtmux:parity libtmux.exc.MultipleActiveWindows
// libtmux:parity libtmux.exc.MultipleActiveWindows.__init__
// libtmux:parity libtmux.exc.NoActiveWindow
// libtmux:parity libtmux.exc.NoActiveWindow.__init__
func TestLiveRelationshipResolversPreserveCardinalityAndOptionalPanes(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	activeWindow := snapshotValues(t, version,
		"session_id", "$2", "window_id", "@8", "window_index", "7",
		"window_active", "1",
	)
	sessionRow := snapshotValues(t, version, "session_id", "$2")

	t.Run("active window missing", func(t *testing.T) {
		runner := attachmentSnapshotRunner(t, version, snapshotRecords{
			sessions: []formatValues{sessionRow},
		})
		session := Session{server: serverWithRunner(runner), sessionID: SessionID("$2")}
		_, err := session.ResolveActiveWindow(context.Background())
		if !errors.Is(err, ErrSnapshotNotFound) {
			t.Fatalf("ResolveActiveWindow() error = %v, want ErrSnapshotNotFound", err)
		}
	})

	t.Run("active window ambiguous", func(t *testing.T) {
		otherActiveWindow := snapshotValues(t, version,
			"session_id", "$2", "window_id", "@9", "window_index", "8",
			"window_active", "1",
		)
		runner := attachmentSnapshotRunner(t, version, snapshotRecords{
			sessions: []formatValues{sessionRow},
			windows:  []formatValues{activeWindow, otherActiveWindow},
		})
		session := Session{server: serverWithRunner(runner), sessionID: SessionID("$2")}
		_, err := session.ResolveActiveWindow(context.Background())
		if !errors.Is(err, ErrSnapshotAmbiguous) {
			t.Fatalf("ResolveActiveWindow() error = %v, want ErrSnapshotAmbiguous", err)
		}
	})

	t.Run("window receiver missing", func(t *testing.T) {
		runner := attachmentSnapshotRunner(t, version, snapshotRecords{
			sessions: []formatValues{sessionRow},
		})
		window := Window{
			server: serverWithRunner(runner), sessionID: SessionID("$2"),
			windowID: WindowID("@8"), windowIndex: 7,
		}
		_, err := window.ResolveSession(context.Background())
		if !errors.Is(err, ErrSnapshotNotFound) {
			t.Fatalf("ResolveSession() error = %v, want ErrSnapshotNotFound", err)
		}
	})

	t.Run("window receiver ambiguous", func(t *testing.T) {
		runner := attachmentSnapshotRunner(t, version, snapshotRecords{
			sessions: []formatValues{sessionRow},
			windows:  []formatValues{activeWindow, activeWindow},
		})
		window := Window{
			server: serverWithRunner(runner), sessionID: SessionID("$2"),
			windowID: WindowID("@8"), windowIndex: 7,
		}
		_, err := window.ResolveSession(context.Background())
		if !errors.Is(err, ErrSnapshotAmbiguous) {
			t.Fatalf("ResolveSession() error = %v, want ErrSnapshotAmbiguous", err)
		}
	})

	t.Run("active pane is optional and ordered", func(t *testing.T) {
		firstActivePane := snapshotValues(t, version,
			"session_id", "$2", "window_id", "@8", "window_index", "7",
			"pane_id", "%2", "pane_index", "0", "pane_active", "1",
		)
		secondActivePane := snapshotValues(t, version,
			"session_id", "$2", "window_id", "@8", "window_index", "7",
			"pane_id", "%3", "pane_index", "1", "pane_active", "1",
		)
		records := snapshotRecords{
			sessions: []formatValues{sessionRow}, windows: []formatValues{activeWindow},
			panes: []formatValues{firstActivePane, secondActivePane},
		}
		runner := attachmentSnapshotRunner(t, version, records)
		window := Window{
			server: serverWithRunner(runner), sessionID: SessionID("$2"),
			windowID: WindowID("@8"), windowIndex: 7,
		}
		pane, ok, err := window.ResolveActivePane(context.Background())
		if err != nil || !ok || pane.paneID != PaneID("%2") {
			t.Fatalf("ResolveActivePane() = (%#v, %t, %v), want first active %%2", pane, ok, err)
		}

		runner = attachmentSnapshotRunner(t, version, snapshotRecords{
			sessions: []formatValues{sessionRow}, windows: []formatValues{activeWindow},
		})
		window.server = serverWithRunner(runner)
		pane, ok, err = window.ResolveActivePane(context.Background())
		if err != nil || ok {
			t.Fatalf("ResolveActivePane() = (%#v, %t, %v), want zero, false, nil", pane, ok, err)
		}
	})

	paneRow := snapshotValues(t, version,
		"session_id", "$2", "window_id", "@8", "window_index", "7",
		"pane_id", "%2", "pane_index", "0", "pane_active", "1",
	)
	tests := []struct {
		name    string
		records snapshotRecords
		resolve func(Server) error
		want    error
	}{
		{
			name: "session receiver missing",
			resolve: func(server Server) error {
				_, _, err := (Session{server: server, sessionID: SessionID("$2")}).ResolveActivePane(context.Background())
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name:    "session receiver ambiguous",
			records: snapshotRecords{sessions: []formatValues{sessionRow, sessionRow}},
			resolve: func(server Server) error {
				_, err := (Session{server: server, sessionID: SessionID("$2")}).ResolveActiveWindow(context.Background())
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name:    "window parent missing",
			records: snapshotRecords{windows: []formatValues{activeWindow}},
			resolve: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: SessionID("$2"),
					windowID: WindowID("@8"), windowIndex: 7,
				}).ResolveSession(context.Background())
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "window parent ambiguous",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow, sessionRow},
				windows:  []formatValues{activeWindow},
			},
			resolve: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: SessionID("$2"),
					windowID: WindowID("@8"), windowIndex: 7,
				}).ResolveSession(context.Background())
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "pane receiver missing",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow}, windows: []formatValues{activeWindow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveWindow(context.Background())
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "pane receiver ambiguous",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow}, windows: []formatValues{activeWindow},
				panes: []formatValues{paneRow, paneRow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveSession(context.Background())
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "pane parent window missing",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow}, panes: []formatValues{paneRow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveWindow(context.Background())
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "pane parent window ambiguous",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow}, windows: []formatValues{activeWindow, activeWindow},
				panes: []formatValues{paneRow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveWindow(context.Background())
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
		{
			name: "pane parent session missing",
			records: snapshotRecords{
				windows: []formatValues{activeWindow}, panes: []formatValues{paneRow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveSession(context.Background())
				return err
			},
			want: ErrSnapshotNotFound,
		},
		{
			name: "pane parent session ambiguous",
			records: snapshotRecords{
				sessions: []formatValues{sessionRow, sessionRow}, windows: []formatValues{activeWindow},
				panes: []formatValues{paneRow},
			},
			resolve: func(server Server) error {
				_, err := relationshipPane(server).ResolveSession(context.Background())
				return err
			},
			want: ErrSnapshotAmbiguous,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := attachmentSnapshotRunner(t, version, test.records)
			err := test.resolve(serverWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("resolver error = %v, want %v", err, test.want)
			}
		})
	}
}

func relationshipPane(server Server) Pane {
	return Pane{
		server: server, sessionID: SessionID("$2"),
		windowID: WindowID("@8"), windowIndex: 7,
		paneID: PaneID("%2"), paneIndex: 0,
	}
}

func liveRelationshipRecords(t *testing.T, version Version) snapshotRecords {
	t.Helper()
	return snapshotRecords{
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
		},
	}
}

// tmux cannot have a window without panes or a session without windows.
// Therefore an empty relation means it was not materialized, not that it has
// no children.
func TestRelationsDistinguishUnknownFromEmpty(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	rows := snapshotRecords{
		sessions: []formatValues{
			snapshotValues(t, version, "session_id", "$0", "session_name", "alpha"),
		},
		windows: []formatValues{
			snapshotValues(t, version,
				"session_id", "$0", "window_id", "@0", "window_index", "0",
				"window_name", "shell", "window_active", "1",
			),
		},
		panes: []formatValues{
			snapshotValues(t, version,
				"session_id", "$0", "window_id", "@0", "window_index", "0",
				"pane_id", "%0", "pane_index", "0", "pane_active", "1",
			),
		},
	}

	full, err := newSnapshot(Server{}, version, rows)
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	// The same rows, listed the way a targeted window lookup lists them.
	partial, err := newSnapshotWithIdentity(
		Server{}, version, snapshotRecords{windows: rows.windows}, listedWindows, nil,
	)
	if err != nil {
		t.Fatalf("newSnapshotWithIdentity() error = %v", err)
	}

	fullWindow, err := full.WindowByID(WindowID("@0"))
	if err != nil {
		t.Fatal(err)
	}
	partialWindow, err := partial.WindowByID(WindowID("@0"))
	if err != nil {
		t.Fatal(err)
	}

	if panes, ok := fullWindow.Panes(); !ok || len(panes) != 1 {
		t.Fatalf("listed window Panes() = (%d, %t), want (1, true)", len(panes), ok)
	}
	if panes, ok := partialWindow.Panes(); ok || panes != nil {
		t.Fatalf("unlisted window Panes() = (%#v, %t), want (nil, false)", panes, ok)
	}
	if sessions, ok := partialWindow.LinkedSessions(); ok || sessions != nil {
		t.Fatalf("unlisted window LinkedSessions() = (%#v, %t), want (nil, false)", sessions, ok)
	}
	// ActivePane reads Panes, so it reports absence rather than picking one out
	// of a relation it does not hold.
	if pane, ok := partialWindow.ActivePane(); ok {
		t.Fatalf("unlisted window ActivePane() = (%#v, true), want false", pane)
	}

	// A filter naming a relation cannot match a record that does not carry it,
	// which is what the to-one branch has always done with its found result.
	predicate, err := (WindowFilter{
		Panes: &PaneRel{Some: &PaneFilter{}},
	}).Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}
	if !predicate(&fullWindow) {
		t.Fatal("listed window did not match a relation filter")
	}
	if predicate(&partialWindow) {
		t.Fatal("unlisted window matched a relation filter it cannot answer")
	}
}
