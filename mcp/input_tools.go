package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input tools distinguish a command plus Enter, a sequence of tmux key names,
// and literal text that tmux must not interpret as keys.

type rawPaneFormat struct {
	Value   string
	Present bool
}

type paneInputSnapshotRow struct {
	PaneID       string
	Synchronized rawPaneFormat
	Dead         rawPaneFormat
	InputOff     rawPaneFormat
	InMode       rawPaneFormat
}

type clientAttentionSnapshotRow struct {
	Control rawPaneFormat
	PaneID  rawPaneFormat
	Zoomed  rawPaneFormat
}

type paneInputMembershipKind uint8

const (
	paneInputConfigured paneInputMembershipKind = iota
	paneInputTargetOnly
)

type paneInputPreflight struct {
	Source        tmux.Pane
	Panes         []tmux.Pane
	ConfiguredIDs []string
}

func parseStrictPaneFlag(name string, raw rawPaneFormat) (bool, error) {
	if raw.Present {
		switch raw.Value {
		case "0":
			return false, nil
		case "1":
			return true, nil
		}
	}
	return false, fmt.Errorf("%s is unavailable or malformed", name)
}

func requireSafePaneMode(raw rawPaneFormat) error {
	if raw.Present && raw.Value == "0" {
		return nil
	}
	if raw.Present {
		mode, err := strconv.Atoi(raw.Value)
		if err == nil && mode > 0 {
			return errors.New("pane is in a mode")
		}
	}
	return errors.New("pane_in_mode is unavailable or malformed")
}

func configuredPaneInputMembership(sourceID string, rows []paneInputSnapshotRow) ([]string, error) {
	empty := []string{}
	var source *paneInputSnapshotRow
	for index := range rows {
		if rows[index].PaneID == sourceID {
			source = &rows[index]
			break
		}
	}
	if source == nil {
		return empty, fmt.Errorf("pane %s is absent from the fresh pane snapshot", sourceID)
	}

	synchronized, err := parseStrictPaneFlag("pane_synchronized", source.Synchronized)
	if err != nil {
		return empty, fmt.Errorf("pane %s: %w", sourceID, err)
	}
	configured := []paneInputSnapshotRow{*source}
	if synchronized {
		configured = configured[:0]
		for _, row := range rows {
			on, parseErr := parseStrictPaneFlag("pane_synchronized", row.Synchronized)
			if parseErr != nil {
				return empty, fmt.Errorf("pane %s: %w", row.PaneID, parseErr)
			}
			if on {
				configured = append(configured, row)
			}
		}
	}

	ids := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, row := range configured {
		dead, parseErr := parseStrictPaneFlag("pane_dead", row.Dead)
		if parseErr != nil {
			return empty, fmt.Errorf("pane %s: %w", row.PaneID, parseErr)
		}
		if dead {
			return empty, fmt.Errorf("pane %s has no process", row.PaneID)
		}
		inputOff, parseErr := parseStrictPaneFlag("pane_input_off", row.InputOff)
		if parseErr != nil {
			return empty, fmt.Errorf("pane %s: %w", row.PaneID, parseErr)
		}
		if inputOff {
			return empty, fmt.Errorf("pane %s has input disabled", row.PaneID)
		}
		if modeErr := requireSafePaneMode(row.InMode); modeErr != nil {
			return empty, fmt.Errorf("pane %s: %w", row.PaneID, modeErr)
		}
		if _, exists := seen[row.PaneID]; exists {
			continue
		}
		seen[row.PaneID] = struct{}{}
		ids = append(ids, row.PaneID)
	}
	slices.Sort(ids)
	return ids, nil
}

func (t *tools) preflightPaneInput(
	ctx context.Context,
	id string,
	sessionName string,
	kind paneInputMembershipKind,
	tool string,
) (paneInputPreflight, error) {
	empty := paneInputPreflight{ConfiguredIDs: []string{}}
	resolved, err := t.resolvePane(ctx, id, sessionName)
	if err != nil {
		return empty, err
	}
	snapshot, err := t.tmux(ctx).Snapshot(ctx)
	if err != nil {
		return empty, fmt.Errorf("%s preflight pane and client snapshot: %w", tool, err)
	}
	panes := make([]tmux.Pane, 0)
	for _, pane := range snapshot.Panes() {
		if pane.SessionID() == resolved.SessionID() &&
			pane.WindowID() == resolved.WindowID() {
			panes = append(panes, pane)
		}
	}
	rows := make([]paneInputSnapshotRow, 0, len(panes))
	byID := make(map[string]tmux.Pane, len(panes))
	for _, pane := range panes {
		paneID := pane.ID().String()
		rows = append(rows, paneInputSnapshotRow{
			PaneID:       paneID,
			Synchronized: paneRawFormat(pane, "pane_synchronized"),
			Dead:         paneRawFormat(pane, "pane_dead"),
			InputOff:     paneRawFormat(pane, "pane_input_off"),
			InMode:       paneRawFormat(pane, "pane_in_mode"),
		})
		byID[paneID] = pane
	}

	sourceID := resolved.ID().String()
	ids := []string{}
	if kind == paneInputTargetOnly {
		var source *paneInputSnapshotRow
		for index := range rows {
			if rows[index].PaneID == sourceID {
				source = &rows[index]
				break
			}
		}
		if source == nil {
			return empty, fmt.Errorf("%s refused: pane %s is absent from the fresh pane snapshot", tool, sourceID)
		}
		dead, parseErr := parseStrictPaneFlag("pane_dead", source.Dead)
		if parseErr != nil {
			return empty, paneInputRefusal(tool, sourceID, parseErr)
		}
		if dead {
			return empty, paneInputRefusal(tool, sourceID, errors.New("pane has no process"))
		}
		inputOff, parseErr := parseStrictPaneFlag("pane_input_off", source.InputOff)
		if parseErr != nil {
			return empty, paneInputRefusal(tool, sourceID, parseErr)
		}
		if inputOff {
			return empty, paneInputRefusal(tool, sourceID, errors.New("pane has input disabled"))
		}
		if modeErr := requireSafePaneMode(source.InMode); modeErr != nil {
			return empty, paneInputRefusal(tool, sourceID, modeErr)
		}
		ids = append(ids, sourceID)
	} else {
		ids, err = configuredPaneInputMembership(sourceID, rows)
		if err != nil {
			return empty, fmt.Errorf("%s refused: %w; capture_pane reads text without changing pane mode", tool, err)
		}
	}
	attended, err := attendedPaneInputMembership(snapshot.Clients(), byID)
	if err != nil {
		return empty, fmt.Errorf("%s refused: %w", tool, err)
	}
	for _, paneID := range ids {
		if attended[paneID] {
			return empty, paneInputRefusal(tool, paneID, errors.New("pane is attended by a tmux client"))
		}
	}

	selected := make([]tmux.Pane, 0, len(ids))
	for _, paneID := range ids {
		pane, exists := byID[paneID]
		if !exists {
			return empty, fmt.Errorf("%s refused: pane %s disappeared from the preflight snapshot", tool, paneID)
		}
		selected = append(selected, pane)
	}
	return paneInputPreflight{
		Source: byID[sourceID], Panes: selected, ConfiguredIDs: ids,
	}, nil
}

func attendedPaneInputMembership(
	clients []tmux.Client,
	windowPanes map[string]tmux.Pane,
) (map[string]bool, error) {
	rows := make([]clientAttentionSnapshotRow, 0, len(clients))
	for _, client := range clients {
		rows = append(rows, clientAttentionSnapshotRow{
			Control: clientRawFormat(client, "client_control_mode"),
			PaneID:  clientRawFormat(client, "pane_id"),
			Zoomed:  clientRawFormat(client, "window_zoomed_flag"),
		})
	}
	windowPaneIDs := make(map[string]struct{}, len(windowPanes))
	for paneID := range windowPanes {
		windowPaneIDs[paneID] = struct{}{}
	}
	return attendedPaneIDs(rows, windowPaneIDs)
}

func attendedPaneIDs(
	clients []clientAttentionSnapshotRow,
	windowPanes map[string]struct{},
) (map[string]bool, error) {
	attended := make(map[string]bool)
	for _, client := range clients {
		control, err := parseStrictPaneFlag("client_control_mode", client.Control)
		if err != nil {
			return nil, err
		}
		paneRaw := client.PaneID
		if !paneRaw.Present || !canonicalPaneID(paneRaw.Value) {
			return nil, errors.New("client pane_id is unavailable or malformed")
		}
		zoomed, err := parseStrictPaneFlag("window_zoomed_flag", client.Zoomed)
		if err != nil {
			return nil, err
		}
		if control {
			continue
		}
		if _, viewing := windowPanes[paneRaw.Value]; !viewing {
			continue
		}
		if zoomed {
			attended[paneRaw.Value] = true
			continue
		}
		for paneID := range windowPanes {
			attended[paneID] = true
		}
	}
	return attended, nil
}

func clientRawFormat(client tmux.Client, name string) rawPaneFormat {
	value, present := client.Formats().Raw(name)
	return rawPaneFormat{Value: value, Present: present}
}

func canonicalPaneID(value string) bool {
	if len(value) < 2 || value[0] != '%' {
		return false
	}
	id, err := strconv.ParseUint(value[1:], 10, 32)
	return err == nil && value == "%"+strconv.FormatUint(id, 10)
}

func paneRawFormat(pane tmux.Pane, name string) rawPaneFormat {
	value, present := pane.Formats().Raw(name)
	return rawPaneFormat{Value: value, Present: present}
}

func paneInputRefusal(tool, paneID string, cause error) error {
	return fmt.Errorf(
		"%s refused for pane %s: %w; capture_pane reads text without changing pane mode",
		tool, paneID, cause,
	)
}

// sendKeysBatchInput sends several keys to a pane in order.
type sendKeysBatchInput struct {
	// PaneID is the tmux pane id. Empty sends to the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to send to; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to send to when paneId is empty"`
	// Keys are tmux key names sent in order, such as ["C-c", "q", "Escape"].
	// Nothing is appended, so a client driving a program that reads lines adds
	// "Enter" itself.
	Keys []string `json:"keys" jsonschema:"tmux key names to send in order, such as [\"C-c\", \"q\", \"Enter\"]"`
	// Literal sends the keys as characters rather than looking their names up,
	// so "Escape" is those six letters.
	Literal bool `json:"literal,omitempty" jsonschema:"send the keys as characters rather than as tmux key names"`
}

// sendKeysBatchOutput reports the source target and accepted key count.
type sendKeysBatchOutput struct {
	// PaneID is the resolved source target.
	PaneID string `json:"paneId"`
	// Sent is how many keys tmux accepted when the call succeeds.
	Sent int `json:"sent"`
	// ResolvedPaneIDs is sorted configured preflight membership.
	ResolvedPaneIDs []string `json:"resolvedPaneIds"`
}

// sendKeysBatch sends a sequence of keys without pressing Enter.
//
// A program that reads keys rather than lines — an editor, a pager, a menu —
// is driven by key names in order, and send_keys cannot express that: it
// appends Enter, so every key would be its own line. This is what lets a
// client answer a prompt, quit a pager, or leave an editor.
func (t *tools) sendKeysBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input sendKeysBatchInput,
	tool string,
) (*mcp.CallToolResult, sendKeysBatchOutput, error) {
	output := sendKeysBatchOutput{ResolvedPaneIDs: []string{}}
	if len(input.Keys) == 0 {
		return nil, output, errors.New("keys is required")
	}
	for index, key := range input.Keys {
		if key == "" {
			return nil, output, fmt.Errorf("key %d is empty", index)
		}
	}
	preflight, err := t.preflightPaneInput(
		ctx, input.PaneID, input.SessionName, paneInputConfigured, tool,
	)
	if err != nil {
		return nil, output, err
	}
	output.PaneID = preflight.Source.ID().String()
	output.ResolvedPaneIDs = append([]string{}, preflight.ConfiguredIDs...)
	if err := t.confirmCallerInputPreflight(ctx, request, preflight, "sending keys"); err != nil {
		return nil, output, err
	}
	confirmedIDs := append([]string{}, preflight.ConfiguredIDs...)
	preflight, err = t.preflightPaneInput(
		ctx, output.PaneID, "", paneInputConfigured, tool,
	)
	if err != nil {
		return nil, output, err
	}
	output.ResolvedPaneIDs = append([]string{}, preflight.ConfiguredIDs...)
	if !slices.Equal(preflight.ConfiguredIDs, confirmedIDs) {
		return nil, output, fmt.Errorf(
			"%s refused: configured pane input membership changed from %v to %v before dispatch",
			tool, confirmedIDs, preflight.ConfiguredIDs,
		)
	}
	if err := t.runtime.deps.sendKeySequence(ctx, preflight.Source, tmux.SendKeySequenceRequest{
		Keys: input.Keys, Literal: input.Literal,
	}); err != nil {
		return nil, output, fmt.Errorf("sending keys: %w", err)
	}
	output.Sent = len(input.Keys)
	return nil, output, nil
}

// pasteSequence names each staged buffer apart from the last, so two pastes at
// once do not overwrite one another's text before either is delivered.
var pasteSequence atomic.Int64

// pasteTextInput delivers text into a pane as text.
type pasteTextInput struct {
	// PaneID is the tmux pane id. Empty pastes into the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to paste into; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to paste into when paneId is empty"`
	// Text is delivered exactly, with no key names read.
	Text string `json:"text" jsonschema:"the text to deliver, taken literally"`
	// Bracket wraps the paste in the codes that tell a program the text was
	// pasted rather than typed, which stops an editor auto-indenting it into
	// a staircase. It is on unless a caller turns it off, and programs that
	// did not ask for bracketed paste never see the codes.
	Bracket *bool `json:"bracket,omitempty" jsonschema:"mark the text as pasted so an editor does not auto-indent it; on by default"`
	// Enter presses Enter after the text, which is what turns a pasted command
	// into a command that runs.
	Enter bool `json:"enter,omitempty" jsonschema:"press Enter after the text"`
}

// pasteTextOutput reports the source target and accepted paste byte count.
type pasteTextOutput struct {
	// PaneID is the resolved source target.
	PaneID string `json:"paneId"`
	// Bytes is how many text bytes tmux accepted for paste.
	Bytes int `json:"bytes"`
}

// pasteText stages text in a per-call buffer so tmux cannot interpret key
// names, then removes the buffer after delivery.
func (t *tools) pasteText(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input pasteTextInput,
) (*mcp.CallToolResult, pasteTextOutput, error) {
	output := pasteTextOutput{}
	if input.Text == "" {
		return nil, output, errors.New("text is required")
	}
	preflight, err := t.preflightPaneInput(
		ctx, input.PaneID, input.SessionName, paneInputTargetOnly, "paste_text",
	)
	if err != nil {
		return nil, output, err
	}
	pane := preflight.Source
	output.PaneID = pane.ID().String()
	if err := t.confirmCallerInputPreflight(ctx, request, preflight, "pasting text"); err != nil {
		return nil, output, err
	}

	server := t.tmux(ctx)
	name := "libtmux-mcp-paste-" + strconv.FormatInt(pasteSequence.Add(1), 10)
	contents := input.Text
	if input.Enter {
		contents += "\n"
	}
	if err := t.runtime.deps.setBuffer(ctx, server, tmux.SetBufferRequest{
		Data: contents,
		Name: &name,
	}); err != nil {
		return nil, output, err
	}
	preflight, err = t.preflightPaneInput(
		ctx, output.PaneID, "", paneInputTargetOnly, "paste_text",
	)
	if err != nil {
		return nil, output, errors.Join(err, server.DeleteBuffer(ctx, &name))
	}
	pane = preflight.Source
	// Deleted with the paste rather than left behind: tmux keeps buffers until
	// something drops them, and a client pasting repeatedly would fill a
	// person's buffer list with text they never copied.
	bracket := true
	if input.Bracket != nil {
		bracket = *input.Bracket
	}
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName:  &name,
		DeleteAfter: true,
		Bracket:     bracket,
	}); err != nil {
		cleanupErr := server.DeleteBuffer(ctx, &name)
		return nil, output, errors.Join(err, cleanupErr)
	}
	output.Bytes = len(input.Text)
	return nil, output, nil
}

// addInputTools advertises the tools that put something into a pane.
