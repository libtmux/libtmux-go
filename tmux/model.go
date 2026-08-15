package tmux

// SessionID is tmux's stable session identifier, including its $ sigil. The
// zero value is not a usable tmux target.
type SessionID string

// String returns the tmux identifier verbatim.
func (id SessionID) String() string { return string(id) }

// WindowID is tmux's stable window identifier, including its @ sigil. It does
// not distinguish linked-session views; the zero value is not a usable target.
type WindowID string

// String returns the tmux identifier verbatim.
func (id WindowID) String() string { return string(id) }

// PaneID is tmux's stable pane identifier, including its % sigil. It does not
// distinguish linked-session views; the zero value is not a usable target.
type PaneID string

// String returns the tmux identifier verbatim.
func (id PaneID) String() string { return string(id) }

// ClientName is tmux's stable client name, normally its terminal path. The
// zero value is not a usable tmux target.
type ClientName string

// String returns the tmux client name verbatim.
func (name ClientName) String() string { return string(name) }

// Session is one materialized tmux session record. It is normally returned by
// [Server.Snapshot], [Server.Session], or [Session.Refresh]. A zero Session is
// not a usable tmux target.
type Session struct {
	formats   formatValues
	server    Server
	snapshot  *snapshotState
	sessionID SessionID
}

// ID returns the stable tmux identity of this session.
func (s Session) ID() SessionID { return s.sessionID }

// Server returns the immutable configured handle that produced the session.
func (s Session) Server() Server { return s.server }

// WithServer returns a copy of the session whose operations run through server.
// It is the write half of [Session.Server] and queries tmux for nothing: a
// record holds its handle as a plain field, so moving one onto a handle that
// selected an [Engine] with [Server.WithEngine] costs a struct copy rather than
// a second lookup.
//
// It exists because a record keeps the handle that produced it. One obtained
// before an engine was selected keeps starting a tmux process for every command
// and reports no error while doing so, which is the failure this turns into a
// one-line fix.
//
// Nothing checks that server addresses the same tmux server, because nothing
// here talks to tmux. A record moved onto a handle with another socket resolves
// against whatever answers there and reports a missing target at its next
// command rather than at this call.
//
// [Session.Windows] and [Session.Panes] carry the handle of the record they are
// read from, so one move covers the relations reached through it.
func (s Session) WithServer(server Server) Session {
	s.server = server
	return s
}

// Windows returns newly allocated shallow copies of this snapshot record's
// winlink views. It never queries tmux, so it returns an empty slice unless the
// receiver was materialized from a snapshot. [Server.Snapshot] and the
// resolvers carry relations; a targeted point lookup, [Session.Refresh], and
// [Server.NewSession] do not. Use [Session.SearchWindows] with a nil filter for
// the session's current windows.
func (s Session) Windows() []Window {
	if s.snapshot == nil {
		return make([]Window, 0)
	}
	return boundTo(
		valuesAt(s.snapshot.windows, s.snapshot.windowsBySession[s.sessionID]),
		Window.WithServer,
		s.server,
	)
}

// Panes returns newly allocated shallow copies of this snapshot record's pane
// views. It never queries tmux, so it returns an empty slice unless the
// receiver was materialized from a snapshot. [Server.Snapshot] and the
// resolvers carry relations; a targeted point lookup, [Session.Refresh], and
// [Server.NewSession] do not. Use [Session.SearchPanes] with a nil filter for
// the session's current panes.
func (s Session) Panes() []Pane {
	if s.snapshot == nil {
		return make([]Pane, 0)
	}
	return boundTo(
		valuesAt(s.snapshot.panes, s.snapshot.panesBySession[s.sessionID]),
		Pane.WithServer,
		s.server,
	)
}

// Window is one materialized (session, index, window) winlink record. It is
// normally returned by [Server.Snapshot], [Server.Window], or [Window.Refresh].
// A zero Window is not a usable tmux target.
type Window struct {
	formats     formatValues
	server      Server
	snapshot    *snapshotState
	sessionID   SessionID
	windowID    WindowID
	windowIndex int
}

// SessionID returns the linked session containing this view.
func (w Window) SessionID() SessionID { return w.sessionID }

// ID returns the underlying stable tmux window identity.
func (w Window) ID() WindowID { return w.windowID }

// Index returns this window's index in its linked session. It returns -1 for
// a partial Window whose exact winlink has not been materialized.
func (w Window) Index() int { return w.windowIndex }

// Server returns the configured handle that produced the window.
func (w Window) Server() Server { return w.server }

// WithServer returns a copy of the window whose operations run through server.
// It is the write half of [Window.Server] and queries tmux for nothing: a
// record holds its handle as a plain field, so moving one onto a handle that
// selected an [Engine] with [Server.WithEngine] costs a struct copy rather than
// a second lookup.
//
// It exists because a record keeps the handle that produced it. One obtained
// before an engine was selected keeps starting a tmux process for every command
// and reports no error while doing so, which is the failure this turns into a
// one-line fix.
//
// Nothing checks that server addresses the same tmux server, because nothing
// here talks to tmux. A record moved onto a handle with another socket resolves
// against whatever answers there and reports a missing target at its next
// command rather than at this call.
//
// [Window.Session] and [Window.Panes] carry the handle of the record they are
// read from, so one move covers the relations reached through it.
func (w Window) WithServer(server Server) Window {
	w.server = server
	return w
}

// Session returns this view's parent record when it remains in the same
// snapshot. It never queries tmux.
func (w Window) Session() (Session, bool) {
	if w.snapshot == nil {
		return Session{}, false
	}
	session, ok := oneAt(w.snapshot.sessions, w.snapshot.sessionsByID[w.sessionID])
	if !ok {
		return Session{}, false
	}
	return session.WithServer(w.server), true
}

// Panes returns newly allocated shallow copies of panes for this exact winlink.
// It never queries tmux, so it returns an empty slice unless the receiver was
// materialized from a snapshot. [Server.Snapshot], the resolvers, and
// [Session.NewWindow] carry relations; a targeted point lookup and
// [Window.Refresh] do not. Use [Window.SearchPanes] with a nil filter for the
// window's current panes.
func (w Window) Panes() []Pane {
	if w.snapshot == nil {
		return make([]Pane, 0)
	}
	key := winlinkKey{sessionID: w.sessionID, windowID: w.windowID, index: w.windowIndex}
	return boundTo(
		valuesAt(w.snapshot.panes, w.snapshot.panesByWinlink[key]),
		Pane.WithServer,
		w.server,
	)
}

// Pane is one materialized pane record within a specific winlink. It is
// normally returned by [Server.Snapshot], [Server.Pane], or [Pane.Refresh].
// A zero Pane is not a usable tmux target.
type Pane struct {
	formats     formatValues
	server      Server
	snapshot    *snapshotState
	sessionID   SessionID
	windowID    WindowID
	windowIndex int
	paneID      PaneID
	paneIndex   int
}

// SessionID returns the linked session containing this pane view.
func (p Pane) SessionID() SessionID { return p.sessionID }

// WindowID returns the underlying stable tmux window identity.
func (p Pane) WindowID() WindowID { return p.windowID }

// WindowIndex returns this pane view's window index in its linked session.
func (p Pane) WindowIndex() int { return p.windowIndex }

// ID returns the stable tmux pane identity.
func (p Pane) ID() PaneID { return p.paneID }

// Index returns the pane's index in its exact window view. Unlike the
// materialized format accessors it sits beside, it returns a plain int, because
// a pane record always carries the index tmux placed it at.
func (p Pane) Index() int { return p.paneIndex }

// Server returns the configured handle that produced the pane.
func (p Pane) Server() Server { return p.server }

// WithServer returns a copy of the pane whose operations run through server. It
// is the write half of [Pane.Server] and queries tmux for nothing: a record
// holds its handle as a plain field, so moving one onto a handle that selected
// an [Engine] with [Server.WithEngine] costs a struct copy rather than a second
// lookup.
//
// It exists because a record keeps the handle that produced it. One obtained
// before an engine was selected keeps starting a tmux process for every command
// and reports no error while doing so, which is the failure this turns into a
// one-line fix.
//
// Nothing checks that server addresses the same tmux server, because nothing
// here talks to tmux. A record moved onto a handle with another socket resolves
// against whatever answers there and reports a missing target at its next
// command rather than at this call.
//
// [Pane.Session] and [Pane.Window] carry the handle of the record they are read
// from, so one move covers the relations reached through it. [Pane.Capture] and
// [Pane.CaptureBytes] still start a process on any handle, because they promise
// tmux's own stdout bytes; [Pane.CaptureToFile] is the pane read that stays on
// the engine.
func (p Pane) WithServer(server Server) Pane {
	p.server = server
	return p
}

// Session returns this view's parent record when it remains in the same
// snapshot. It never queries tmux.
func (p Pane) Session() (Session, bool) {
	if p.snapshot == nil {
		return Session{}, false
	}
	session, ok := oneAt(p.snapshot.sessions, p.snapshot.sessionsByID[p.sessionID])
	if !ok {
		return Session{}, false
	}
	return session.WithServer(p.server), true
}

// Window returns this pane's exact winlink record when it remains in the same
// snapshot. It never queries tmux.
func (p Pane) Window() (Window, bool) {
	if p.snapshot == nil {
		return Window{}, false
	}
	key := winlinkKey{sessionID: p.sessionID, windowID: p.windowID, index: p.windowIndex}
	window, ok := oneAt(p.snapshot.windows, p.snapshot.windowsByWinlink[key])
	if !ok {
		return Window{}, false
	}
	return window.WithServer(p.server), true
}

// Client is one materialized tmux client record. It is normally returned by
// [Server.Snapshot], [Server.Client], or [Client.Refresh]. A zero Client is not
// a usable tmux target.
type Client struct {
	formats    formatValues
	server     Server
	snapshot   *snapshotState
	clientName ClientName
	attachment clientAttachment
}

type clientAttachment struct {
	sessionID   SessionID
	windowID    WindowID
	windowIndex int
	paneID      PaneID
	hasSession  bool
	hasWindow   bool
	hasPane     bool
}

// Server returns the configured handle that produced the client.
func (c Client) Server() Server { return c.server }

// WithServer returns a copy of the client whose operations run through server.
// It is the write half of [Client.Server] and queries tmux for nothing: a
// record holds its handle as a plain field, so moving one onto a handle that
// selected an [Engine] with [Server.WithEngine] costs a struct copy rather than
// a second lookup.
//
// It exists because a record keeps the handle that produced it. One obtained
// before an engine was selected keeps starting a tmux process for every command
// and reports no error while doing so, which is the failure this turns into a
// one-line fix.
//
// Nothing checks that server addresses the same tmux server, because nothing
// here talks to tmux. A record moved onto a handle with another socket resolves
// against whatever answers there and reports a missing target at its next
// command rather than at this call.
//
// [Client.AttachedSession], [Client.AttachedWindow], and [Client.AttachedPane]
// carry the handle of the record they are read from, so one move covers the
// relations reached through it.
func (c Client) WithServer(server Server) Client {
	c.server = server
	return c
}

// Name returns the stable tmux name of this client.
func (c Client) Name() ClientName { return c.clientName }

// AttachedSession returns the materialized attached session, if present in the
// same snapshot. It never queries tmux.
func (c Client) AttachedSession() (Session, bool) {
	if c.snapshot == nil || !c.attachment.hasSession {
		return Session{}, false
	}
	session, ok := oneAt(c.snapshot.sessions, c.snapshot.sessionsByID[c.attachment.sessionID])
	if !ok {
		return Session{}, false
	}
	return session.WithServer(c.server), true
}

// AttachedWindow returns the client's exact materialized winlink, if present
// in the same snapshot. It never queries tmux.
func (c Client) AttachedWindow() (Window, bool) {
	if c.snapshot == nil || !c.attachment.hasWindow {
		return Window{}, false
	}
	key := winlinkKey{
		sessionID: c.attachment.sessionID,
		windowID:  c.attachment.windowID,
		index:     c.attachment.windowIndex,
	}
	window, ok := oneAt(c.snapshot.windows, c.snapshot.windowsByWinlink[key])
	if !ok {
		return Window{}, false
	}
	return window.WithServer(c.server), true
}

// AttachedPane returns the client's exact materialized pane view, if present
// in the same snapshot. It never queries tmux.
func (c Client) AttachedPane() (Pane, bool) {
	if c.snapshot == nil || !c.attachment.hasPane {
		return Pane{}, false
	}
	key := paneViewKey{
		winlinkKey: winlinkKey{
			sessionID: c.attachment.sessionID,
			windowID:  c.attachment.windowID,
			index:     c.attachment.windowIndex,
		},
		paneID: c.attachment.paneID,
	}
	pane, ok := oneAt(c.snapshot.panes, c.snapshot.panesByView[key])
	if !ok {
		return Pane{}, false
	}
	return pane.WithServer(c.server), true
}

// boundTo moves every value onto server through the model's own WithServer
// method expression. Relation accessors read records out of a snapshot the
// handle that built it owns, so without this a record moved with WithServer
// would hand back children still bound to the handle it was moved off, which is
// the trap WithServer exists to close.
func boundTo[T any](values []T, bind func(T, Server) T, server Server) []T {
	for index := range values {
		values[index] = bind(values[index], server)
	}
	return values
}

func valuesAt[T any](values []T, indexes []int) []T {
	result := make([]T, len(indexes))
	for index, source := range indexes {
		result[index] = values[source]
	}
	return result
}

func oneAt[T any](values []T, indexes []int) (T, bool) {
	if len(indexes) != 1 {
		var zero T
		return zero, false
	}
	return values[indexes[0]], true
}
