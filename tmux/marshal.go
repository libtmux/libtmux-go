package tmux

import (
	"encoding/json"
	"maps"
)

// marshalFormats renders format expansions as tmux returned them. A record
// materialized with no fields encodes an empty object rather than null, so a
// consumer can index it without a nil check.
func marshalFormats(values formatValues) map[string]string {
	if values.values == nil {
		return map[string]string{}
	}
	return maps.Clone(values.values)
}

// MarshalJSON encodes the format expansions as tmux returned them. Nothing
// decodes: [json.Unmarshal] into this type reports no error and changes
// nothing, because every field is unexported.
func (v FormatValues) MarshalJSON() ([]byte, error) {
	return json.Marshal(marshalFormats(v.values))
}

// MarshalJSON encodes the session's identity and the format expansions tmux
// returned for it, under "formats". Nothing decodes: a decoded record would
// carry no [Server], so it could name a session it could never act on, and
// [json.Unmarshal] into this type reports no error and changes nothing.
func (s Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID      SessionID         `json:"id"`
		Formats map[string]string `json:"formats"`
	}{ID: s.sessionID, Formats: marshalFormats(s.formats)})
}

// MarshalJSON encodes the window's identity, its place in the hierarchy, and
// the format expansions tmux returned for it. Nothing decodes; see
// [Session.MarshalJSON].
func (w Window) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID          WindowID          `json:"id"`
		SessionID   SessionID         `json:"sessionId"`
		WindowIndex int               `json:"windowIndex"`
		Formats     map[string]string `json:"formats"`
	}{
		ID:          w.windowID,
		SessionID:   w.sessionID,
		WindowIndex: w.windowIndex,
		Formats:     marshalFormats(w.formats),
	})
}

// MarshalJSON encodes the pane's identity, its place in the hierarchy, and the
// format expansions tmux returned for it. Nothing decodes; see
// [Session.MarshalJSON].
func (p Pane) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID          PaneID            `json:"id"`
		SessionID   SessionID         `json:"sessionId"`
		WindowID    WindowID          `json:"windowId"`
		WindowIndex int               `json:"windowIndex"`
		Index       int               `json:"index"`
		Formats     map[string]string `json:"formats"`
	}{
		ID:          p.paneID,
		SessionID:   p.sessionID,
		WindowID:    p.windowID,
		WindowIndex: p.windowIndex,
		Index:       p.paneIndex,
		Formats:     marshalFormats(p.formats),
	})
}

// MarshalJSON encodes the client's name and the format expansions tmux
// returned for it. Nothing decodes; see [Session.MarshalJSON].
func (c Client) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    ClientName        `json:"name"`
		Formats map[string]string `json:"formats"`
	}{Name: c.clientName, Formats: marshalFormats(c.formats)})
}

// MarshalJSON encodes the snapshot's tmux version and the records it holds. A
// kind it listed encodes as an array, empty when tmux reported none; a kind it
// never listed is absent. Those are different answers, and one field cannot
// spell both, which is why the arrays are pointers here. Nothing decodes; see
// [Session.MarshalJSON].
func (s Snapshot) MarshalJSON() ([]byte, error) {
	encoded := struct {
		Version  string     `json:"version"`
		Sessions *[]Session `json:"sessions,omitempty"`
		Windows  *[]Window  `json:"windows,omitempty"`
		Panes    *[]Pane    `json:"panes,omitempty"`
		Clients  *[]Client  `json:"clients,omitempty"`
	}{Version: s.Version().String()}
	if s.state == nil {
		return json.Marshal(encoded)
	}
	if s.state.listed.holds(listedSessions) {
		listed := s.Sessions()
		encoded.Sessions = &listed
	}
	if s.state.listed.holds(listedWindows) {
		listed := s.Windows()
		encoded.Windows = &listed
	}
	if s.state.listed.holds(listedPanes) {
		listed := s.Panes()
		encoded.Panes = &listed
	}
	if s.state.listed.holds(listedClients) {
		listed := s.Clients()
		encoded.Clients = &listed
	}
	return json.Marshal(encoded)
}
