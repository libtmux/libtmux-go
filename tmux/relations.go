package tmux

import (
	"context"
	"strconv"
)

// ActiveWindow returns this session's sole active winlink from the materialized
// snapshot. It never queries tmux; use [Session.ResolveActiveWindow] for live
// state.
func (s Session) ActiveWindow() (Window, bool) {
	var active Window
	found := false
	for _, window := range s.Windows() {
		value, queried := window.Active()
		if !queried || !value {
			continue
		}
		if found {
			return Window{}, false
		}
		active = window
		found = true
	}
	return active, found
}

// ActivePane returns the active pane in this session's materialized active
// window. It never queries tmux; use [Session.ResolveActivePane] for live state.
func (s Session) ActivePane() (Pane, bool) {
	window, ok := s.ActiveWindow()
	if !ok {
		return Pane{}, false
	}
	return window.ActivePane()
}

// ActivePane returns the first active pane in this materialized winlink view.
// It never queries tmux; use [Window.ResolveActivePane] for live state.
func (w Window) ActivePane() (Pane, bool) {
	for _, pane := range w.Panes() {
		value, queried := pane.Active()
		if queried && value {
			return pane, true
		}
	}
	return Pane{}, false
}

// LinkedSessions returns newly allocated shallow copies of materialized sessions
// containing this window. It never queries tmux.
func (w Window) LinkedSessions() []Session {
	if w.snapshot == nil {
		return make([]Session, 0)
	}
	result := make([]Session, 0)
	seen := make(map[SessionID]struct{})
	for _, index := range w.snapshot.windowsByID[w.windowID] {
		sessionID := w.snapshot.windows[index].sessionID
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		session, ok := oneAt(w.snapshot.sessions, w.snapshot.sessionsByID[sessionID])
		if ok {
			result = append(result, session)
		}
	}
	return result
}

// ResolveActiveWindow snapshots live tmux state and returns this session's sole
// exact active winlink. It returns [SnapshotLookupError] cardinality errors.
// Canceling ctx stops this read-only snapshot wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (s Session) ResolveActiveWindow(ctx context.Context) (Window, error) {
	live, err := s.resolveLive(ctx)
	if err != nil {
		return Window{}, err
	}
	return requiredActiveWindow(live)
}

// ResolveActivePane snapshots live tmux state and returns the active pane in
// this session's exact active window. A missing active pane returns ok false.
// Canceling ctx stops this read-only snapshot wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (s Session) ResolveActivePane(ctx context.Context) (Pane, bool, error) {
	live, err := s.resolveLive(ctx)
	if err != nil {
		return Pane{}, false, err
	}
	window, err := requiredActiveWindow(live)
	if err != nil {
		return Pane{}, false, err
	}
	pane, ok := window.ActivePane()
	return pane, ok, nil
}

// ResolveSession snapshots live tmux state and returns this exact winlink's
// parent session. It returns [SnapshotLookupError] cardinality errors.
// Canceling ctx stops this read-only snapshot wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (w Window) ResolveSession(ctx context.Context) (Session, error) {
	live, err := w.resolveLive(ctx)
	if err != nil {
		return Session{}, err
	}
	return lookupSnapshotValue(
		live.snapshot.sessions,
		live.snapshot.sessionsByID[live.sessionID],
		"session",
		live.sessionID.String(),
	)
}

// ResolveActivePane snapshots live tmux state and returns the first active pane
// in this exact winlink view. A missing active pane returns ok false. Canceling
// ctx stops this read-only snapshot wait; errors.Is can detect context.Canceled
// or context.DeadlineExceeded as applicable.
func (w Window) ResolveActivePane(ctx context.Context) (Pane, bool, error) {
	live, err := w.resolveLive(ctx)
	if err != nil {
		return Pane{}, false, err
	}
	pane, ok := live.ActivePane()
	return pane, ok, nil
}

// ResolveWindow snapshots live tmux state and returns the exact winlink
// containing this pane view. It returns [SnapshotLookupError] cardinality errors.
// Canceling ctx stops this read-only snapshot wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (p Pane) ResolveWindow(ctx context.Context) (Window, error) {
	live, err := p.resolveLive(ctx)
	if err != nil {
		return Window{}, err
	}
	return live.resolveSnapshotWindow()
}

// ResolveSession snapshots live tmux state and returns the parent session of
// this pane's exact winlink. It returns [SnapshotLookupError] cardinality errors.
// Canceling ctx stops this read-only snapshot wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (p Pane) ResolveSession(ctx context.Context) (Session, error) {
	live, err := p.resolveLive(ctx)
	if err != nil {
		return Session{}, err
	}
	window, err := live.resolveSnapshotWindow()
	if err != nil {
		return Session{}, err
	}
	return lookupSnapshotValue(
		live.snapshot.sessions,
		live.snapshot.sessionsByID[window.sessionID],
		"session",
		window.sessionID.String(),
	)
}

func (s Session) resolveLive(ctx context.Context) (Session, error) {
	if err := validateTypedTarget(
		"resolve-session", "SessionID", "session", s.sessionID.String(),
	); err != nil {
		return Session{}, err
	}
	snapshot, err := s.server.Snapshot(ctx)
	if err != nil {
		return Session{}, err
	}
	return snapshot.SessionByID(s.sessionID)
}

func (w Window) resolveLive(ctx context.Context) (Window, error) {
	identifier, err := validateWindowView(w)
	if err != nil {
		return Window{}, err
	}
	snapshot, err := w.server.Snapshot(ctx)
	if err != nil {
		return Window{}, err
	}
	return lookupSnapshotValue(
		snapshot.state.windows,
		snapshot.state.windowsByWinlink[winlinkKey{
			sessionID: w.sessionID,
			windowID:  w.windowID,
			index:     w.windowIndex,
		}],
		"window",
		identifier,
	)
}

func (p Pane) resolveLive(ctx context.Context) (Pane, error) {
	identifier, err := validatePaneView(p)
	if err != nil {
		return Pane{}, err
	}
	snapshot, err := p.server.Snapshot(ctx)
	if err != nil {
		return Pane{}, err
	}
	return lookupSnapshotValue(
		snapshot.state.panes,
		snapshot.state.panesByView[paneViewKey{
			winlinkKey: winlinkKey{
				sessionID: p.sessionID,
				windowID:  p.windowID,
				index:     p.windowIndex,
			},
			paneID: p.paneID,
		}],
		"pane",
		identifier,
	)
}

func (p Pane) resolveSnapshotWindow() (Window, error) {
	identifier := windowViewIdentifier(p.sessionID, p.windowID, p.windowIndex)
	key := winlinkKey{
		sessionID: p.sessionID,
		windowID:  p.windowID,
		index:     p.windowIndex,
	}
	return lookupSnapshotValue(
		p.snapshot.windows,
		p.snapshot.windowsByWinlink[key],
		"window",
		identifier,
	)
}

func requiredActiveWindow(session Session) (Window, error) {
	window, ok, err := attachmentActiveWindow(session)
	if err != nil {
		return Window{}, err
	}
	if !ok {
		return Window{}, &SnapshotLookupError{
			Object:     "active window",
			Identifier: session.sessionID.String(),
			Matches:    0,
		}
	}
	return window, nil
}

func validateWindowView(window Window) (string, error) {
	if err := validateTypedTarget(
		"resolve-window", "SessionID", "session", window.sessionID.String(),
	); err != nil {
		return "", err
	}
	if err := validateTypedTarget(
		"resolve-window", "WindowID", "window", window.windowID.String(),
	); err != nil {
		return "", err
	}
	if window.windowIndex < 0 {
		return "", invalidServerCommandRequest(
			"resolve-window",
			"WindowIndex",
			strconv.Itoa(window.windowIndex),
			"must be nonnegative",
		)
	}
	return windowViewIdentifier(window.sessionID, window.windowID, window.windowIndex), nil
}

func validatePaneView(pane Pane) (string, error) {
	if _, err := validateWindowView(Window{
		sessionID: pane.sessionID, windowID: pane.windowID, windowIndex: pane.windowIndex,
	}); err != nil {
		return "", err
	}
	if err := validateTypedTarget(
		"resolve-pane", "PaneID", "pane", pane.paneID.String(),
	); err != nil {
		return "", err
	}
	return paneViewIdentifier(pane), nil
}

func windowViewIdentifier(sessionID SessionID, windowID WindowID, index int) string {
	return sessionID.String() + ":" + strconv.Itoa(index) + ":" + windowID.String()
}

func paneViewIdentifier(pane Pane) string {
	return windowViewIdentifier(pane.sessionID, pane.windowID, pane.windowIndex) + "." + pane.paneID.String()
}
