package tmux

import "fmt"

// Equal reports whether both handles currently select the same socket path.
// It evaluates relative paths and environment-dependent named and default
// sockets against each handle's frozen binding.
func (s Server) Equal(other Server) bool {
	left, err := s.SocketSelection()
	if err != nil {
		return false
	}
	right, err := other.SocketSelection()
	return err == nil && left.Path == right.Path
}

// String returns a concise representation of the server selector.
func (s Server) String() string {
	if s.state == nil {
		return "Server(invalid)"
	}
	flag, value := effectiveSocketSelectorValues(
		s.state.config.socketPath,
		s.state.config.socketName,
	)
	switch flag {
	case "-S":
		return fmt.Sprintf("Server(socket_path=%s)", value)
	case "-L":
		return fmt.Sprintf("Server(socket_name=%s)", value)
	}
	return "Server(default)"
}

// Equal reports whether two session records carry the same stable ID.
func (s Session) Equal(other Session) bool { return s.sessionID == other.sessionID }

// String returns the session's stable ID and queried name.
func (s Session) String() string {
	if name, ok := s.Name(); ok {
		return fmt.Sprintf("Session(%s %s)", s.sessionID, name)
	}
	return fmt.Sprintf("Session(%s)", s.sessionID)
}

// Equal reports whether two window records carry the same stable window ID.
// It intentionally collapses linked-session views with different sessions or
// indexes; use SessionID, WindowID, and WindowIndex for exact view identity.
func (w Window) Equal(other Window) bool { return w.windowID == other.windowID }

// String returns the winlink identity and its materialized parent, when present.
func (w Window) String() string {
	name, _ := w.Name()
	identity := fmt.Sprintf("Window(%s %d:%s", w.windowID, w.windowIndex, name)
	if session, ok := w.Session(); ok {
		return identity + ", " + session.String() + ")"
	}
	return identity + ")"
}

// Equal reports whether two pane records carry the same stable pane ID. It
// intentionally collapses linked-session views; a pane's exact view identity
// includes SessionID, WindowID, WindowIndex, and PaneID.
func (p Pane) Equal(other Pane) bool { return p.paneID == other.paneID }

// String returns the pane identity and its materialized parent, when present.
func (p Pane) String() string {
	identity := fmt.Sprintf("Pane(%s", p.paneID)
	if window, ok := p.Window(); ok {
		return identity + " " + window.String() + ")"
	}
	return identity + ")"
}

// Equal reports whether two clients carry the same stable client name.
func (c Client) Equal(other Client) bool { return c.clientName == other.clientName }

// String returns the client's stable name.
func (c Client) String() string { return fmt.Sprintf("Client(%s)", c.clientName) }
