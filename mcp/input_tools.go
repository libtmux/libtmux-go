package mcp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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
	PaneID         string
	Synchronized   rawPaneFormat
	Dead           rawPaneFormat
	InputOff       rawPaneFormat
	InMode         rawPaneFormat
	CurrentCommand rawPaneFormat
	PID            rawPaneFormat
}

type clientAttentionSnapshotRow struct {
	Control     rawPaneFormat
	SessionID   rawPaneFormat
	WindowID    rawPaneFormat
	WindowIndex rawPaneFormat
	PaneID      rawPaneFormat
	Zoomed      rawPaneFormat
}

type paneInputPlacement struct {
	SessionID   string
	WindowID    string
	WindowIndex int
	PaneID      string
}

type paneInputMemberSignature struct {
	Placements     []paneInputPlacement
	Synchronized   string
	Dead           string
	InputOff       string
	InMode         string
	CurrentCommand rawPaneFormat
	PID            rawPaneFormat
}

type paneInputTransitionSignature struct {
	Source  paneInputMemberSignature
	Members []paneInputMemberSignature
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
	Identity      paneInputServerIdentity
	Caller        paneInputCaller
	Signature     paneInputTransitionSignature
}

type paneInputServerIdentity struct {
	endpoint        string
	endpointID      uint64
	serverPID       uint64
	serverStartTime uint64
}

type paneInputIdentity struct {
	endpointID      uint64
	serverPID       uint64
	serverStartTime uint64
	paneID          string
}

func (p paneInputPreflight) Identities() []paneInputIdentity {
	identities := make([]paneInputIdentity, 0, len(p.ConfiguredIDs))
	for _, paneID := range p.ConfiguredIDs {
		identities = append(identities, paneInputIdentity{
			endpointID: p.Identity.endpointID, serverPID: p.Identity.serverPID,
			serverStartTime: p.Identity.serverStartTime, paneID: paneID,
		})
	}
	return identities
}

func samePaneInputPreflight(initial, final paneInputPreflight) bool {
	return initial.Identity == final.Identity && initial.Caller == final.Caller &&
		slices.Equal(initial.ConfiguredIDs, final.ConfiguredIDs) &&
		samePaneInputMemberSignature(initial.Signature.Source, final.Signature.Source) &&
		samePaneInputMemberSignatures(initial.Signature.Members, final.Signature.Members)
}

func samePaneInputMemberSignature(left, right paneInputMemberSignature) bool {
	return slices.Equal(left.Placements, right.Placements) &&
		left.Synchronized == right.Synchronized && left.Dead == right.Dead &&
		left.InputOff == right.InputOff && left.InMode == right.InMode &&
		left.CurrentCommand == right.CurrentCommand && left.PID == right.PID
}

func samePaneInputMemberSignatures(left, right []paneInputMemberSignature) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !samePaneInputMemberSignature(left[index], right[index]) {
			return false
		}
	}
	return true
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
	snapshot, err := t.runtime.deps.snapshot(ctx, t.tmux(ctx))
	if err != nil {
		return empty, fmt.Errorf("%s preflight pane and client snapshot: %w", tool, err)
	}
	resolved, err := resolvePaneInputSource(snapshot, id, sessionName)
	if err != nil {
		return empty, err
	}
	panes := make([]tmux.Pane, 0)
	for _, pane := range snapshot.Panes() {
		if pane.WindowID() == resolved.WindowID() {
			panes = append(panes, pane)
		}
	}
	rows := make([]paneInputSnapshotRow, 0, len(panes))
	byID := make(map[string]tmux.Pane, len(panes))
	for _, pane := range panes {
		paneID := pane.ID().String()
		rows = append(rows, paneInputSnapshotRow{
			PaneID:         paneID,
			Synchronized:   paneRawFormat(pane, "pane_synchronized"),
			Dead:           paneRawFormat(pane, "pane_dead"),
			InputOff:       paneRawFormat(pane, "pane_input_off"),
			InMode:         paneRawFormat(pane, "pane_in_mode"),
			CurrentCommand: paneRawFormat(pane, "pane_current_command"),
			PID:            paneRawFormat(pane, "pane_pid"),
		})
		if _, exists := byID[paneID]; !exists {
			byID[paneID] = pane
		}
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
		if _, parseErr := parseStrictPaneFlag("pane_synchronized", source.Synchronized); parseErr != nil {
			return empty, paneInputRefusal(tool, sourceID, parseErr)
		}
		ids = append(ids, sourceID)
	} else {
		ids, err = configuredPaneInputMembership(sourceID, rows)
		if err != nil {
			return empty, fmt.Errorf("%s refused: %w; capture_pane reads text without changing pane mode", tool, err)
		}
	}
	attended, err := attendedPaneInputMembership(
		snapshot.Clients(), snapshot.Panes(), resolved.WindowID().String(),
	)
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
	byID[sourceID] = resolved
	sourceSignature, err := paneInputSignature(snapshot.PanesByID(resolved.ID()))
	if err != nil {
		return empty, paneInputRefusal(tool, sourceID, err)
	}
	members := make([]paneInputMemberSignature, 0, len(selected))
	for _, pane := range selected {
		signature, signatureErr := paneInputSignature(snapshot.PanesByID(pane.ID()))
		if signatureErr != nil {
			return empty, paneInputRefusal(tool, pane.ID().String(), signatureErr)
		}
		members = append(members, signature)
	}
	identity, err := paneInputIdentityForSnapshot(snapshot, byID[sourceID])
	if err != nil {
		return empty, fmt.Errorf("%s refused: %w", tool, err)
	}
	tmuxVariable, tmuxPresent := os.LookupEnv("TMUX")
	tmuxPane, panePresent := os.LookupEnv("TMUX_PANE")
	caller, err := parsePaneInputCaller(tmuxVariable, tmuxPresent, tmuxPane, panePresent)
	if err != nil {
		return empty, fmt.Errorf("%s refused: %w", tool, err)
	}
	callerPanes := make([]paneInputCallerPane, 0, len(snapshot.Panes()))
	for _, pane := range snapshot.Panes() {
		callerPanes = append(callerPanes, paneInputCallerPane{
			sessionID: pane.SessionID().String(), paneID: pane.ID().String(),
		})
	}
	caller, err = classifyPaneInputCaller(caller, identity, callerPanes)
	if err != nil {
		return empty, fmt.Errorf("%s refused: %w", tool, err)
	}
	return paneInputPreflight{
		Source: byID[sourceID], Panes: selected, ConfiguredIDs: ids,
		Identity: identity, Caller: caller,
		Signature: paneInputTransitionSignature{Source: sourceSignature, Members: members},
	}, nil
}

func paneInputSignature(
	panes []tmux.Pane,
) (paneInputMemberSignature, error) {
	if len(panes) == 0 {
		return paneInputMemberSignature{}, errors.New("pane is absent from its window placement")
	}
	panes = slices.Clone(panes)
	slices.SortFunc(panes, func(left, right tmux.Pane) int {
		return comparePaneInputPlacement(paneInputPlacementFor(left), paneInputPlacementFor(right))
	})

	var signature paneInputMemberSignature
	for index, pane := range panes {
		placement := paneInputPlacementFor(pane)
		if !canonicalPaneInputID(placement.SessionID, '$') ||
			!canonicalPaneInputID(placement.WindowID, '@') ||
			placement.WindowIndex < 0 || !canonicalPaneID(placement.PaneID) {
			return paneInputMemberSignature{}, errors.New("pane placement is unavailable or malformed")
		}
		if index > 0 {
			previous := paneInputPlacementFor(panes[index-1])
			if placement == previous {
				return paneInputMemberSignature{}, errors.New("pane placement is duplicated")
			}
			if placement.PaneID != signature.Placements[0].PaneID ||
				placement.WindowID != signature.Placements[0].WindowID {
				return paneInputMemberSignature{}, errors.New("pane linked placement is inconsistent")
			}
		}

		row := paneInputSnapshotRow{
			PaneID:         placement.PaneID,
			Synchronized:   paneRawFormat(pane, "pane_synchronized"),
			Dead:           paneRawFormat(pane, "pane_dead"),
			InputOff:       paneRawFormat(pane, "pane_input_off"),
			InMode:         paneRawFormat(pane, "pane_in_mode"),
			CurrentCommand: paneRawFormat(pane, "pane_current_command"),
			PID:            paneRawFormat(pane, "pane_pid"),
		}
		if _, err := parseStrictPaneFlag("pane_synchronized", row.Synchronized); err != nil {
			return paneInputMemberSignature{}, err
		}
		if _, err := parseStrictPaneFlag("pane_dead", row.Dead); err != nil {
			return paneInputMemberSignature{}, err
		}
		if _, err := parseStrictPaneFlag("pane_input_off", row.InputOff); err != nil {
			return paneInputMemberSignature{}, err
		}
		if err := requireSafePaneMode(row.InMode); err != nil {
			return paneInputMemberSignature{}, err
		}
		if !row.PID.Present {
			return paneInputMemberSignature{}, errors.New("pane_pid is unavailable")
		}
		if _, err := parseCanonicalPaneInputNumber(row.PID.Value, false); err != nil {
			return paneInputMemberSignature{}, fmt.Errorf("pane_pid is malformed: %w", err)
		}
		observed := paneInputMemberSignature{
			Synchronized: row.Synchronized.Value, Dead: row.Dead.Value,
			InputOff: row.InputOff.Value, InMode: row.InMode.Value,
			CurrentCommand: row.CurrentCommand, PID: row.PID,
		}
		if index == 0 {
			signature = observed
		} else if signature.Synchronized != observed.Synchronized ||
			signature.Dead != observed.Dead || signature.InputOff != observed.InputOff ||
			signature.InMode != observed.InMode ||
			signature.CurrentCommand != observed.CurrentCommand ||
			signature.PID != observed.PID {
			return paneInputMemberSignature{}, errors.New("pane linked state is inconsistent")
		}
		signature.Placements = append(signature.Placements, placement)
	}
	return signature, nil
}

func paneInputPlacementFor(pane tmux.Pane) paneInputPlacement {
	return paneInputPlacement{
		SessionID: pane.SessionID().String(), WindowID: pane.WindowID().String(),
		WindowIndex: pane.WindowIndex(), PaneID: pane.ID().String(),
	}
}

func comparePaneInputPlacement(left, right paneInputPlacement) int {
	if order := cmp.Compare(left.SessionID, right.SessionID); order != 0 {
		return order
	}
	if order := cmp.Compare(left.WindowID, right.WindowID); order != 0 {
		return order
	}
	if order := cmp.Compare(left.WindowIndex, right.WindowIndex); order != 0 {
		return order
	}
	return cmp.Compare(left.PaneID, right.PaneID)
}

func resolvePaneInputSource(
	snapshot tmux.Snapshot,
	id string,
	sessionName string,
) (tmux.Pane, error) {
	if wanted := strings.TrimSpace(id); wanted != "" {
		if !canonicalPaneID(wanted) {
			return tmux.Pane{}, fmt.Errorf("pane id %q is not canonical", wanted)
		}
		panes := snapshot.PanesByID(tmux.PaneID(wanted))
		if len(panes) == 0 {
			pane, err := snapshot.PaneByID(tmux.PaneID(wanted))
			return pane, notFound(err, "pane", wanted, "list_panes")
		}
		slices.SortFunc(panes, func(left, right tmux.Pane) int {
			return comparePaneInputPlacement(paneInputPlacementFor(left), paneInputPlacementFor(right))
		})
		return panes[0], nil
	}

	sessions := snapshot.Sessions()
	var session tmux.Session
	if wanted := strings.TrimSpace(sessionName); wanted != "" {
		found := false
		for _, candidate := range sessions {
			name, present := candidate.Name()
			if !present {
				return tmux.Pane{}, errors.New("session_name is unavailable")
			}
			if name == wanted {
				session, found = candidate, true
				break
			}
		}
		if !found {
			return tmux.Pane{}, missing{fmt.Errorf(
				"no session named %q on this tmux server; list_sessions reports the sessions that exist",
				wanted,
			)}
		}
	} else {
		switch len(sessions) {
		case 0:
			return tmux.Pane{}, errors.New("the tmux server has no sessions")
		case 1:
			session = sessions[0]
		default:
			return tmux.Pane{}, fmt.Errorf(
				"the tmux server has %d sessions, so sessionName is required", len(sessions),
			)
		}
	}
	pane, present := session.ActivePane()
	if !present {
		return tmux.Pane{}, fmt.Errorf("session %s has no unambiguous active pane", session.ID())
	}
	return pane, nil
}

func paneInputIdentityForSnapshot(
	snapshot tmux.Snapshot,
	source tmux.Pane,
) (paneInputServerIdentity, error) {
	selection, err := snapshot.Server().SocketSelection()
	if err != nil {
		return paneInputServerIdentity{}, fmt.Errorf("resolve pane input endpoint: %w", err)
	}
	endpoint, err := filepath.EvalSymlinks(selection.Path)
	if err != nil {
		return paneInputServerIdentity{}, fmt.Errorf("resolve pane input endpoint: %w", err)
	}
	if !filepath.IsAbs(endpoint) {
		return paneInputServerIdentity{}, errors.New("resolved pane input endpoint is not absolute")
	}
	endpointID, err := processPaneInputEndpoints.identify(endpoint)
	if err != nil {
		return paneInputServerIdentity{}, fmt.Errorf("identify pane input endpoint: %w", err)
	}
	pidRaw, pidPresent := source.Formats().Raw("pid")
	pid, err := parseCanonicalPaneInputNumber(pidRaw, false)
	if err != nil || !pidPresent {
		return paneInputServerIdentity{}, errors.New("server pid is unavailable or malformed")
	}
	startRaw, startPresent := source.Formats().Raw("start_time")
	startTime, err := parseCanonicalPaneInputNumber(startRaw, false)
	if err != nil || !startPresent {
		return paneInputServerIdentity{}, errors.New("server start_time is unavailable or malformed")
	}
	return paneInputServerIdentity{
		endpoint: endpoint, endpointID: endpointID,
		serverPID: pid, serverStartTime: startTime,
	}, nil
}

func attendedPaneInputMembership(
	clients []tmux.Client,
	panes []tmux.Pane,
	targetWindowID string,
) (map[string]bool, error) {
	rows := make([]clientAttentionSnapshotRow, 0, len(clients))
	for _, client := range clients {
		rows = append(rows, clientAttentionSnapshotRow{
			Control:     clientRawFormat(client, "client_control_mode"),
			SessionID:   clientRawFormat(client, "session_id"),
			WindowID:    clientRawFormat(client, "window_id"),
			WindowIndex: clientRawFormat(client, "window_index"),
			PaneID:      clientRawFormat(client, "pane_id"),
			Zoomed:      clientRawFormat(client, "window_zoomed_flag"),
		})
	}
	placements := make([]paneInputPlacement, 0, len(panes))
	for _, pane := range panes {
		placements = append(placements, paneInputPlacement{
			SessionID: pane.SessionID().String(), WindowID: pane.WindowID().String(),
			WindowIndex: pane.WindowIndex(), PaneID: pane.ID().String(),
		})
	}
	return attendedPaneIDs(rows, placements, targetWindowID)
}

func attendedPaneIDs(
	clients []clientAttentionSnapshotRow,
	panes []paneInputPlacement,
	targetWindowID string,
) (map[string]bool, error) {
	if !canonicalPaneInputID(targetWindowID, '@') {
		return nil, errors.New("target window_id is unavailable or malformed")
	}
	for _, pane := range panes {
		if !canonicalPaneInputID(pane.SessionID, '$') ||
			!canonicalPaneInputID(pane.WindowID, '@') ||
			pane.WindowIndex < 0 || !canonicalPaneID(pane.PaneID) {
			return nil, errors.New("pane placement is unavailable or malformed")
		}
	}
	attended := make(map[string]bool)
	for _, client := range clients {
		control, err := parseStrictPaneFlag("client_control_mode", client.Control)
		if err != nil {
			return nil, err
		}
		if control {
			continue
		}
		placement, err := clientPaneInputPlacement(client)
		if err != nil {
			return nil, err
		}
		zoomed, err := parseStrictPaneFlag("window_zoomed_flag", client.Zoomed)
		if err != nil {
			return nil, err
		}
		matches := 0
		for _, pane := range panes {
			if pane == placement {
				matches++
			}
		}
		if matches != 1 {
			return nil, errors.New("client pane placement is unavailable or inconsistent")
		}
		if placement.WindowID != targetWindowID {
			continue
		}
		if zoomed {
			attended[placement.PaneID] = true
			continue
		}
		for _, pane := range panes {
			if pane.WindowID == targetWindowID {
				attended[pane.PaneID] = true
			}
		}
	}
	return attended, nil
}

func clientPaneInputPlacement(row clientAttentionSnapshotRow) (paneInputPlacement, error) {
	if !row.SessionID.Present || !canonicalPaneInputID(row.SessionID.Value, '$') ||
		!row.WindowID.Present || !canonicalPaneInputID(row.WindowID.Value, '@') ||
		!row.PaneID.Present || !canonicalPaneID(row.PaneID.Value) {
		return paneInputPlacement{}, errors.New("client pane placement is unavailable or malformed")
	}
	index, err := strconv.Atoi(row.WindowIndex.Value)
	if !row.WindowIndex.Present || err != nil || index < 0 ||
		row.WindowIndex.Value != strconv.Itoa(index) {
		return paneInputPlacement{}, errors.New("client window_index is unavailable or malformed")
	}
	return paneInputPlacement{
		SessionID: row.SessionID.Value, WindowID: row.WindowID.Value,
		WindowIndex: index, PaneID: row.PaneID.Value,
	}, nil
}

func clientRawFormat(client tmux.Client, name string) rawPaneFormat {
	value, present := client.Formats().Raw(name)
	return rawPaneFormat{Value: value, Present: present}
}

func canonicalPaneID(value string) bool {
	return canonicalPaneInputID(value, '%')
}

func canonicalPaneInputID(value string, prefix byte) bool {
	if len(value) < 2 || value[0] != prefix {
		return false
	}
	id, err := strconv.ParseUint(value[1:], 10, 32)
	return err == nil && value == string(prefix)+strconv.FormatUint(id, 10)
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
	lease, err := processPaneInputs.acquire(
		preflight.Identities(), paneInputReservationInput, tool,
	)
	if err != nil {
		return nil, output, err
	}
	defer processPaneInputs.release(lease)
	if err := t.confirmCallerInputPreflight(ctx, request, preflight, "sending keys"); err != nil {
		return nil, output, err
	}
	initial := preflight
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
	if !samePaneInputPreflight(initial, preflight) {
		return nil, output, fmt.Errorf(
			"%s refused: pane input state, placement, route, caller, or membership changed before dispatch",
			tool,
		)
	}
	if !processPaneInputs.owns(lease, preflight.Identities()) {
		return nil, output, fmt.Errorf(
			"%s refused: its pane input reservation changed before dispatch", tool,
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
	initial, err := t.preflightPaneInput(
		ctx, input.PaneID, input.SessionName, paneInputTargetOnly, "paste_text",
	)
	if err != nil {
		return nil, output, err
	}
	pane := initial.Source
	output.PaneID = pane.ID().String()
	lease, err := processPaneInputs.acquire(
		initial.Identities(), paneInputReservationInput, "paste_text",
	)
	if err != nil {
		return nil, output, err
	}
	defer processPaneInputs.release(lease)
	if err := t.confirmCallerInputPreflight(ctx, request, initial, "pasting text"); err != nil {
		return nil, output, err
	}
	if input.Text == "" && !input.Enter {
		return nil, output, nil
	}

	server := initial.Source.Server()
	name := "libtmux-mcp-paste-" + strconv.FormatInt(pasteSequence.Add(1), 10)
	contents := input.Text
	if input.Enter {
		contents += "\n"
	}
	// Bracketed paste tells the terminal that what follows is pasted, so it is
	// inserted rather than acted on -- which is exactly what a newline carried
	// with the text must not be. Bracketing a paste that asks for Enter leaves
	// the command typed at the prompt in every shell that honours the markers,
	// which is most of them. The buffer is what stops tmux interpreting a key
	// name in the text; the markers are not, so dropping them for this case
	// costs nothing and keeps the delivery to one paste at the target.
	bracket := !input.Enter
	if input.Bracket != nil {
		bracket = *input.Bracket
	}
	if err := t.runtime.deps.setBuffer(ctx, server, tmux.SetBufferRequest{
		Data: contents,
		Name: &name,
	}); err != nil {
		return nil, output, errors.Join(err, t.deletePasteBuffer(server, name))
	}
	final, err := t.preflightPaneInput(
		ctx, output.PaneID, "", paneInputTargetOnly, "paste_text",
	)
	if err != nil {
		return nil, output, errors.Join(err, t.deletePasteBuffer(server, name))
	}
	if !samePaneInputPreflight(initial, final) {
		return nil, output, errors.Join(
			errors.New("paste_text refused: pane input state, placement, route, caller, or membership changed before dispatch"),
			t.deletePasteBuffer(server, name),
		)
	}
	pane = final.Source
	if !processPaneInputs.owns(lease, final.Identities()) {
		return nil, output, errors.Join(
			errors.New("paste_text refused: its pane input reservation changed before dispatch"),
			t.deletePasteBuffer(server, name),
		)
	}
	// Deleted with the paste rather than left behind: tmux keeps buffers until
	// something drops them, and a client pasting repeatedly would fill a
	// person's buffer list with text they never copied.
	if err := t.runtime.deps.pasteBuffer(ctx, pane, tmux.PasteBufferRequest{
		BufferName:  &name,
		DeleteAfter: true,
		Bracket:     bracket,
	}); err != nil {
		cleanupErr := t.deletePasteBuffer(server, name)
		return nil, output, errors.Join(err, cleanupErr)
	}
	output.Bytes = len(input.Text)
	return nil, output, nil
}

func (t *tools) deletePasteBuffer(server tmux.Server, name string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.runtime.ctx), 2*time.Second)
	defer cancel()
	return server.DeleteBuffer(cleanupCtx, &name)
}

// addInputTools advertises the tools that put something into a pane.
