package tmux

import (
	"context"
	"errors"
)

// ClientAttachment is one immutable view of a client's live attachment.
// Session, Window, and Pane return values from the same observational Snapshot.
type ClientAttachment struct {
	session    Session
	window     Window
	pane       Pane
	hasSession bool
	hasWindow  bool
	hasPane    bool
}

// Session returns the attached session, if it remained live in the snapshot.
func (a ClientAttachment) Session() (Session, bool) {
	return a.session, a.hasSession
}

// Window returns the attached session's active winlink, if present.
func (a ClientAttachment) Window() (Window, bool) {
	return a.window, a.hasWindow
}

// Pane returns the attached session's active window's active pane, if present.
func (a ClientAttachment) Pane() (Pane, bool) {
	return a.pane, a.hasPane
}

// ResolveAttachment re-reads the client's live attached hierarchy.
//
// One resolution materializes one strict Snapshot. Snapshot collection is a
// multi-command observational read, so concurrent tmux changes may produce a
// partial hierarchy. A missing client, detached client, or stale session
// returns an empty attachment without an error. Other collection, decoding,
// and cardinality errors remain visible.
func (c Client) ResolveAttachment(ctx context.Context) (ClientAttachment, error) {
	if err := validateTypedTarget(
		"resolve-attachment", "ClientName", "client", c.clientName.String(),
	); err != nil {
		return ClientAttachment{}, err
	}
	snapshot, err := c.server.WithStrictErrors().Snapshot(ctx)
	if err != nil {
		return ClientAttachment{}, err
	}
	live, err := snapshot.ClientByName(c.clientName)
	if errors.Is(err, ErrSnapshotNotFound) {
		return ClientAttachment{}, nil
	}
	if err != nil {
		return ClientAttachment{}, err
	}
	if !live.attachment.hasSession {
		return ClientAttachment{}, nil
	}

	session, err := snapshot.SessionByID(live.attachment.sessionID)
	if errors.Is(err, ErrSnapshotNotFound) {
		return ClientAttachment{}, nil
	}
	if err != nil {
		return ClientAttachment{}, err
	}
	attachment := ClientAttachment{session: session, hasSession: true}

	window, found, err := attachmentActiveWindow(session)
	if err != nil {
		return ClientAttachment{}, err
	}
	if !found {
		return attachment, nil
	}
	attachment.window = window
	attachment.hasWindow = true

	pane, found, err := attachmentActivePane(window)
	if err != nil {
		return ClientAttachment{}, err
	}
	if !found {
		return attachment, nil
	}
	attachment.pane = pane
	attachment.hasPane = true
	return attachment, nil
}

func attachmentActiveWindow(session Session) (Window, bool, error) {
	var active Window
	matches := 0
	for _, window := range session.Windows() {
		value, queried := window.Active()
		if !queried || !value {
			continue
		}
		active = window
		matches++
	}
	if matches > 1 {
		return Window{}, false, &SnapshotLookupError{
			Object:     "active window",
			Identifier: session.sessionID.String(),
			Matches:    matches,
		}
	}
	return active, matches == 1, nil
}

func attachmentActivePane(window Window) (Pane, bool, error) {
	var active Pane
	matches := 0
	for _, pane := range window.Panes() {
		value, queried := pane.Active()
		if !queried || !value {
			continue
		}
		active = pane
		matches++
	}
	if matches > 1 {
		return Pane{}, false, &SnapshotLookupError{
			Object:     "active pane",
			Identifier: window.windowID.String(),
			Matches:    matches,
		}
	}
	return active, matches == 1, nil
}
