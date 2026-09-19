package tmux

import (
	"encoding/json"
	"maps"
)

// Records encode to JSON so a program can log, export, or answer with one
// without restating the model as a struct of its own. What a record holds is
// what tmux said when it was materialized, so that is what it encodes: its
// stable identity, its place in the hierarchy, and the format expansions
// verbatim under "formats".
//
// Nothing decodes. A record carries the server handle its follow-up commands
// run through, and a decoded one would have none, so it could name a pane it
// could never act on. Decode into a shape of your own where that is what you
// want.

// marshalFormats renders format expansions as tmux returned them. A record
// materialized with no fields encodes an empty object rather than null, so a
// consumer can index it without a nil check.
func marshalFormats(values formatValues) map[string]string {
	if values.values == nil {
		return map[string]string{}
	}
	return maps.Clone(values.values)
}

// MarshalJSON implements json.Marshaler.
func (v FormatValues) MarshalJSON() ([]byte, error) {
	return json.Marshal(marshalFormats(v.values))
}

// MarshalJSON implements json.Marshaler.
func (s Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID      SessionID         `json:"id"`
		Formats map[string]string `json:"formats"`
	}{ID: s.sessionID, Formats: marshalFormats(s.formats)})
}

// MarshalJSON implements json.Marshaler.
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

// MarshalJSON implements json.Marshaler.
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

// MarshalJSON implements json.Marshaler.
func (c Client) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    ClientName        `json:"name"`
		Formats map[string]string `json:"formats"`
	}{Name: c.clientName, Formats: marshalFormats(c.formats)})
}

// MarshalJSON implements json.Marshaler. A snapshot encodes only the kinds it
// listed, because a kind it never listed and a kind it listed and found empty
// are different answers, and an empty array would spell them the same.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	encoded := struct {
		Version  string    `json:"version"`
		Sessions []Session `json:"sessions,omitempty"`
		Windows  []Window  `json:"windows,omitempty"`
		Panes    []Pane    `json:"panes,omitempty"`
		Clients  []Client  `json:"clients,omitempty"`
	}{Version: s.Version().String()}
	if s.state == nil {
		return json.Marshal(encoded)
	}
	if s.state.listed.holds(listedSessions) {
		encoded.Sessions = s.Sessions()
	}
	if s.state.listed.holds(listedWindows) {
		encoded.Windows = s.Windows()
	}
	if s.state.listed.holds(listedPanes) {
		encoded.Panes = s.Panes()
	}
	if s.state.listed.holds(listedClients) {
		encoded.Clients = s.Clients()
	}
	return json.Marshal(encoded)
}
