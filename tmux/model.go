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
//
// # What is on here
//
// A Session is a record read at one moment,
// and the methods that act on tmux address the session by its stable
// [SessionID].
// They fall into the groups below.
//
// # Identity and refreshing
//
// A record does not update: [Session.Refresh] looks the session up again by its
// stable [SessionID] and returns a new record, leaving the receiver as it was.
// [SessionFromEnv] finds the session that holds the environment's pane.
// Equality compares the stable ID and the tmux server that produced the record.
//
//   - Reach: [SessionFromEnv], [Session.Refresh]
//   - Identity: [Session.ID], [Session.Server], [Session.Ref], [Session.Equal],
//     [Session.String], [Session.MarshalJSON]
//
// # Fields on the record
//
// The typed accessors read tmux's session formats from the record in hand and
// never query tmux.
// Each returns false as its second result when the value is absent, empty, or
// malformed, and some formats need tmux 3.6 or later.
// The raw expansions come back as a [FormatValues],
// where [FormatValues.Raw] tells an absent field from a present empty one.
//
//   - Name and time: [Session.Name], [Session.Path], [Session.Created],
//     [Session.Activity], [Session.LastAttached]
//   - Attached clients: [Session.Attached], [Session.AttachedList],
//     [Session.ManyAttached]
//   - Group: [Session.Group], [Session.Grouped], [Session.GroupSize],
//     [Session.GroupAttached], [Session.GroupAttachedList],
//     [Session.GroupList], [Session.GroupManyAttached]
//   - Windows: [Session.WindowCount], [Session.ActiveWindowIndex],
//     [Session.LastWindowIndex]
//   - Alerts: [Session.Alert], [Session.Alerts], [Session.ActivityFlag],
//     [Session.BellFlag], [Session.SilenceFlag]
//   - Other: [Session.Active], [Session.Marked], [Session.Stack],
//     [Session.Format]
//   - Raw: [Session.Formats]
//
// # Windows and panes
//
// A record from a snapshot holds its windows and panes, and reading them
// queries nothing;
// a record from a lookup, a refresh, or [Server.NewSession] carries no
// relations and reports false.
// The resolvers read live state and the search methods ask tmux,
// and a search takes a [TmuxFilter], where nil omits tmux's -f expression.
// A window made or selected here comes back freshly materialized,
// and selecting one is session-scoped:
// it makes no promise about clients attached to other sessions.
//
//   - Held by the record: [Session.Windows], [Session.Panes],
//     [Session.ActiveWindow], [Session.ActivePane]
//   - Read live: [Session.ResolveActiveWindow], [Session.ResolveActivePane]
//   - Search: [Session.SearchWindows], [Session.SearchPanes]
//   - Create: [Session.NewWindow]
//   - Select: [Session.SelectWindow], [Session.NextWindow],
//     [Session.PreviousWindow], [Session.LastWindow]
//
// # Running a command
//
// Run starts a command in a new window with a terminal, waits for it,
// and returns its exit status and the screen it left;
// a nonzero status is a result, not an error.
// Start returns once the command is under way,
// and if the [Running] it returns is never waited on,
// the window it created is never removed.
// Cmd instead sends a raw tmux subcommand targeted at the session.
//
// [Session.Run], [Session.Start], and [Session.Cmd]
//
// # Renaming and ending
//
// Rename returns a new record,
// and it rejects an invalid name before running tmux
// with an error matching [ErrInvalidRequest].
// Killing the session removes its winlinks and detaches ordinary attached
// clients, destroying any window left with no links;
// a window kill removes one window from every session it is linked into.
// KillWith can instead destroy every other session or only clear alerts.
// A completed kill is a [CommandError] only when it wrote to stderr,
// so a nonzero exit without stderr is ignored.
//
// [Session.Rename], [Session.Kill], [Session.KillWith], and
// [Session.KillWindow]
//
// # Options, hooks, and environment
//
// Typed reads include inherited values,
// and a failed read is returned rather than answered with zero values.
// A write by name reaches an option the typed setters do not cover,
// and each known option has a typed setter that [SessionOptionValues] pairs
// with its accessor.
// The environment is the session's own, apart from the server's global one:
// reading one variable reports false for a missing or hidden entry,
// and unsetting deletes an entry where removing marks it for removal from the
// environment of new processes.
//
//   - Read options: [Session.Options], [Session.RawOption]
//   - Write options: [Session.SetOption], [Session.AppendOption],
//     [Session.UnsetOption]
//   - Typed setters, for example: [Session.SetMouse], [Session.SetStatus],
//     [Session.SetHistoryLimit]
//   - Read hooks: [Session.Hooks], [Session.RawHook]
//   - Write hooks: [Session.SetHook], [Session.SetHooks], [Session.AppendHook],
//     [Session.UnsetHook], [Session.RunHook]
//   - Environment: [Session.ShowEnvironment], [Session.GetEnvironment],
//     [Session.SetEnvironment], [Session.UnsetEnvironment],
//     [Session.RemoveEnvironment]
//
// # Clients and connections
//
// Attach hands the caller's terminal to the session and blocks until detach or
// context cancellation.
// The other client methods act on every client attached to the session,
// except one that switches tmux's current client to it.
// Opening control mode needs tmux 3.7 and returns a [Connection] that the
// caller must close;
// a session already bound to a connection can open neither another nor a
// notification stream.
//
//   - Terminal: [Session.Attach]
//   - Every attached client: [Session.DetachClients], [Session.Lock]
//   - Current client: [Session.SwitchClient]
//   - Control mode: [Session.OpenControl], [Session.OpenNotifications]
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

func (s Session) withServer(server Server) Session {
	s.server = server
	return s
}

// Windows returns newly allocated shallow copies of this snapshot record's
// winlink views, and reports whether the receiver carries relations at all.
//
// It never queries tmux. [Server.Snapshot] and the resolvers carry relations; a
// targeted lookup, refresh, or creation reports false rather than an empty
// relation. Use [Session.SearchWindows] for current live windows.
func (s Session) Windows() ([]Window, bool) {
	if s.snapshot == nil || !s.snapshot.listed.holds(listedWindows) {
		return nil, false
	}
	return boundTo(
		valuesAt(s.snapshot.windows, s.snapshot.windowsBySession[s.sessionID]),
		Window.withServer,
		s.server,
	), true
}

// Panes returns newly allocated shallow copies of this snapshot record's pane
// views, and reports whether the receiver carries relations at all.
//
// It never queries tmux. [Server.Snapshot] and the resolvers carry relations; a
// targeted lookup, refresh, or creation reports false rather than an empty
// relation. Use [Session.SearchPanes] for current live panes.
func (s Session) Panes() ([]Pane, bool) {
	if s.snapshot == nil || !s.snapshot.listed.holds(listedPanes) {
		return nil, false
	}
	return boundTo(
		valuesAt(s.snapshot.panes, s.snapshot.panesBySession[s.sessionID]),
		Pane.withServer,
		s.server,
	), true
}

// Window is one materialized (session, index, window) winlink record. It is
// normally returned by [Server.Snapshot], [Server.Window], or [Window.Refresh].
// A zero Window is not a usable tmux target.
//
// # What is on here
//
// A window is one session's link to a tmux window, so the same [WindowID] can
// appear in several sessions with different indexes.
// Most methods address that exact link, and the result of one that changes the
// window is a fresh record that may name another linked session.
// Whatever is not one window's job, such as lookups by ID or the server's
// buffers, lives on [Server] and [Session].
//
// # Fields on the record
//
// The typed accessors read tmux's window formats from the record in hand and
// never query tmux.
// Each returns false as its second result when the value is absent, empty, or
// malformed.
// The raw expansions come back as a [FormatValues].
// [Window.Refresh] returns a new record read live by stable ID, and it does
// not keep the linked-session view the receiver had.
//
//   - Identity: [Window.ID], [Window.Index], [Window.SessionID],
//     [Window.Server], [Window.Ref], [Window.Equal], [Window.String]
//   - Name and state: [Window.Name], [Window.Active], [Window.Flags],
//     [Window.Layout], [Window.ZoomedFlag], [Window.PaneCount]
//   - Size: [Window.Width], [Window.Height]
//   - Alerts: [Window.ActivityFlag], [Window.BellFlag], [Window.SilenceFlag]
//   - Raw: [Window.Formats]
//   - Live: [Window.Refresh]
//
// # Panes and sessions
//
// A record from a snapshot holds its panes and its session, and reading them
// queries nothing;
// a record from a lookup or a refresh carries no relations and reports false.
// The resolvers and the search method read live state, and a search takes a
// [TmuxFilter], where nil omits tmux's -f expression.
// Selecting a pane here is scoped to this window's link and does not select the
// window in its session.
//
//   - Held by the record: [Window.Panes], [Window.ActivePane],
//     [Window.Session], [Window.LinkedSessions]
//   - Read live: [Window.ResolveActivePane], [Window.ResolveSession]
//   - Search: [Window.SearchPanes]
//   - Select a pane: [Window.SelectPane], [Window.LastPane]
//
// # Splitting and arranging
//
// A split makes a tiled pane and a new pane makes a floating one;
// both return a [Pane] materialized in this window, and a failure after tmux
// reports the pane ID returns the partial result with the error and does not
// roll the creation back.
// A layout change alters pane geometry without selecting the window or
// refreshing the receiver, so take a new snapshot to read the result;
// rotating and resizing return a fresh [Window].
//
//   - Create a pane: [Window.SplitPane], [Window.NewPane]
//   - Layout: [Window.SelectLayout], [Window.NextLayout],
//     [Window.PreviousLayout]
//   - Arrange: [Window.Rotate], [Window.Resize]
//
// # Moving and linking
//
// Linking adds the window to another session and keeps the source;
// moving takes this link out and places it in the target;
// swapping exchanges two links.
// Unlinking a window's only link is rejected unless the request sets KillIfLast,
// which destroys the window.
// A transport or context error on a call that returns nothing can be
// delivery-ambiguous, and nothing is rolled back.
//
//   - Link: [Window.Link], [Window.Unlink]
//   - Move: [Window.Move], [Window.Swap]
//   - Create beside this one: [Window.NewWindow]
//
// # Renaming and ending
//
// Rename, Select, Respawn and Resize return a fresh record, and if the command
// succeeds but the refresh fails they return the receiver with that error.
// Killing the window destroys its panes and every link to it, and a session left
// with no windows is destroyed;
// KillOthers keeps only this link's window in its session.
// A completed kill is a [CommandError] only when it wrote to stderr,
// so a nonzero exit without stderr is ignored.
//
//   - Change: [Window.Rename], [Window.Select], [Window.Respawn]
//   - End: [Window.Kill], [Window.KillOthers]
//
// # Messages and raw commands
//
// DisplayMessage formats a message at this window's target and can print it
// back, and a completed stderr reaches the [WarningHandler] rather than
// becoming an error.
// Cmd sends any tmux subcommand targeted at the window's stable ID.
//
// [Window.DisplayMessage] and [Window.Cmd]
//
// # Options and hooks
//
// Typed reads include inherited values,
// and a failed read is returned rather than answered with zero values.
// A write by name reaches an option the typed setters do not cover,
// and each known option has a typed setter that [WindowOptionValues] pairs with
// its accessor.
//
//   - Read options: [Window.Options], [Window.RawOption]
//   - Write by name: [Window.SetOption], [Window.AppendOption],
//     [Window.UnsetOption]
//   - Typed setters, for example: [Window.SetAutomaticRename],
//     [Window.SetSynchronizePanes], [Window.SetMainPaneWidth]
//   - Read hooks: [Window.Hooks], [Window.RawHook]
//   - Write hooks: [Window.SetHook], [Window.SetHooks], [Window.AppendHook],
//     [Window.UnsetHook], [Window.RunHook]
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

func (w Window) withServer(server Server) Window {
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
	return session.withServer(w.server), true
}

// Panes returns newly allocated shallow copies of panes for this exact winlink,
// and reports whether the receiver carries relations at all.
//
// It never queries tmux. [Server.Snapshot], the resolvers, and
// [Session.NewWindow] carry relations; a targeted lookup or refresh reports
// false instead. Use [Window.SearchPanes] for current live panes.
func (w Window) Panes() ([]Pane, bool) {
	if w.snapshot == nil || !w.snapshot.listed.holds(listedPanes) {
		return nil, false
	}
	key := winlinkKey{sessionID: w.sessionID, windowID: w.windowID, index: w.windowIndex}
	return boundTo(
		valuesAt(w.snapshot.panes, w.snapshot.panesByWinlink[key]),
		Pane.withServer,
		w.server,
	), true
}

// Pane is one materialized pane record within a specific winlink. It is
// normally returned by [Server.Snapshot], [Server.Pane], or [Pane.Refresh].
// A zero Pane is not a usable tmux target.
//
// # What is on here
//
// A pane is one pane in one session's link to a window, so the same [PaneID]
// can appear under several linked windows.
// Most methods address that exact view, and a method that changes the pane
// returns a record refreshed by [PaneID], which can carry a different linked
// session or window.
// Lookups by ID and the server's buffers live on [Server].
//
// # Fields on the record
//
// The typed accessors read tmux's pane formats from the record in hand and never
// query tmux.
// Each returns false as its second result when the value is absent, empty, or
// malformed.
// The raw expansions come back as a [FormatValues].
// [Pane.Refresh] returns a new record read live by stable ID.
//
//   - Identity: [Pane.ID], [Pane.Index], [Pane.SessionID], [Pane.WindowID],
//     [Pane.WindowIndex], [Pane.Server], [Pane.Ref], [Pane.Equal],
//     [Pane.String]
//   - Process: [Pane.CurrentCommand], [Pane.CurrentPath], [Pane.ProcessPID],
//     [Pane.TTY], [Pane.StartCommand]
//   - Dead panes: [Pane.Dead], [Pane.DeadStatus], [Pane.DeadSignal],
//     [Pane.DeadTime]
//   - Geometry: [Pane.Width], [Pane.Height], [Pane.Left], [Pane.Top],
//     [Pane.Right], [Pane.Bottom]
//   - State: [Pane.Active], [Pane.Title], [Pane.InMode], [Pane.Marked],
//     [Pane.ZoomedFlag]
//   - Cursor: [Pane.CursorX], [Pane.CursorY]
//   - Raw: [Pane.Formats]
//   - Live: [Pane.Refresh]
//
// # Window and session
//
// A record from a snapshot holds its window and session, and reading them
// queries nothing;
// a record from a lookup or a refresh carries no relations and reports false.
// The resolvers snapshot live state and fail with an error that matches
// [ErrNotFound] when nothing is found.
//
//   - Held by the record: [Pane.Window], [Pane.Session]
//   - Read live: [Pane.ResolveWindow], [Pane.ResolveSession]
//
// # Sending input
//
// The keys go to tmux, not to a shell: the pane's program interprets what
// arrives, and a call returns once tmux has taken the input.
// A completed command is a redacted [CommandError] only when it wrote to
// stderr, and a transport or context error is delivery-ambiguous.
// Clear types the word reset and Enter as a person would, where Reset acts on
// the pane itself.
//
//   - Keys: [Pane.SendKeys], [Pane.SendKeySequence], [Pane.Enter],
//     [Pane.SendPrefix]
//   - As a writer: [Pane.Writer]
//   - Paste: [Pane.PasteBuffer]
//   - Clear: [Pane.Clear], [Pane.Reset], [Pane.ClearHistory]
//
// # Reading output
//
// A capture reads the screen at one point in time, not a stream, and the zero
// request reads the visible screen.
// The printed forms need a subprocess, so a [Pane] bound to a [Connection]
// returns [ErrConnectionRequiresProcess] for them, and [Pane.CaptureToFile]
// stages through a tmux buffer to run over the connection.
// A text wait polls the visible screen and matches text already there, so match
// what a command prints, not the command typed.
//
//   - Capture: [Pane.Capture], [Pane.CaptureBytes]
//   - Capture elsewhere: [Pane.CaptureTo], [Pane.CaptureToFile],
//     [Pane.CaptureToBuffer]
//   - Wait for output: [Pane.WaitForText]
//   - Observe: [Pane.OpenObservation]
//   - Pipe: [Pane.Pipe]
//
// # Splitting and arranging
//
// A split makes a tiled pane beside this one and a new pane makes a floating
// one, both returned as a [Pane] materialized in this window.
// Moving and joining relocate the pane, and removing a window's last pane
// destroys the window.
// Resizing and retitling refresh by [PaneID] and return the original receiver
// with the error if only the refresh fails.
//
//   - Create: [Pane.Split], [Pane.NewPane]
//   - Relocate: [Pane.Move], [Pane.Join], [Pane.BreakPane], [Pane.Swap]
//   - Size: [Pane.Resize], [Pane.SetWidth], [Pane.SetHeight]
//   - Title: [Pane.SetTitle]
//   - Focus: [Pane.Select]
//
// # Modes and overlays
//
// Copy mode and the choosers act on this pane's target.
// A window search needs an attached client, and a popup draws on a client's
// screen, waits until it closes, and exposes no handle to its process.
//
//   - Copy mode: [Pane.CopyMode]
//   - Choosers: [Pane.ChooseTree], [Pane.ChooseBuffer], [Pane.ChooseClient],
//     [Pane.FindWindow]
//   - Overlays: [Pane.DisplayPopup], [Pane.DisplayPanes], [Pane.DisplayMessage]
//
// # Respawning and ending
//
// Respawn restarts the pane's process and refreshes by [PaneID].
// Killing the last pane destroys its window, and a session left with no
// windows is destroyed;
// KillOthers keeps only this pane in its window and never destroys a window.
// Only stderr makes a completed kill a [CommandError].
//
// [Pane.Respawn], [Pane.Kill], and [Pane.KillOthers]
//
// # Options and hooks
//
// Typed reads include inherited values,
// and a failed read is returned rather than answered with zero values.
// A write by name reaches an option the typed setters do not cover.
// Cmd sends any tmux subcommand targeted at the pane's stable ID.
//
//   - Read options: [Pane.Options], [Pane.RawOption]
//   - Write by name: [Pane.SetOption], [Pane.AppendOption], [Pane.UnsetOption]
//   - Typed setters, for example: [Pane.SetWindowStyle],
//     [Pane.SetSynchronizePanes], [Pane.SetRemainOnExit]
//   - Read hooks: [Pane.Hooks], [Pane.RawHook]
//   - Write hooks: [Pane.SetHook], [Pane.SetHooks], [Pane.AppendHook],
//     [Pane.UnsetHook], [Pane.RunHook]
//   - Raw command: [Pane.Cmd]
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

func (p Pane) withServer(server Server) Pane {
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
	return session.withServer(p.server), true
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
	return window.withServer(p.server), true
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
	return session.withServer(c.server), true
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
	return window.withServer(c.server), true
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
	return pane.withServer(c.server), true
}

// boundTo keeps relations on the server handle selected by the parent record.
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
