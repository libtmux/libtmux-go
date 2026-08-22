package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Reading a pane again should cost what changed, not what is there.
//
// A client watching a pane across turns reads the same screen every turn: the
// build output it read last turn is still on the screen this turn, and it pays
// for it again. capture_since answers with what the pane wrote since a cursor
// a previous call handed out, so a quiet pane costs nothing and a busy one
// costs its new lines.
//
// The cursor is opaque on purpose. It carries where tmux was when it was
// issued and a fingerprint of the rows there, and both are implementation:
// a client that parsed it would be depending on how this works rather than
// on what it promises, which is that passing it back returns what is new.
//
// tmux discards scrollback, so "what is new" is not always answerable. When
// the anchor has been trimmed away the reply is the visible screen with
// linesMissed set, which is honest about having lost the thread rather than
// silently returning a gap. A client that sees linesMissed knows its record of
// the pane is incomplete; one that never sees it knows its record is whole.

const (
	// cursorPrefix names the format so a cursor from another version, or from
	// somewhere else entirely, is refused rather than misread.
	cursorPrefix  = "capture-since-v2:"
	cursorVersion = 2
	// fingerprintRows is how many rows before the anchor a cursor records.
	// Enough that the run is unique on a screen of repeated prompts, few
	// enough that carrying it in every reply costs little.
	fingerprintRows = 12
	// stableReadAttempts bounds how often a read is retried when the pane
	// moved underneath it. A pane writing continuously would otherwise retry
	// forever; after this many tries the reply says the lines are incomplete.
	stableReadAttempts = 3
)

// captureSinceInput asks for what a pane wrote since a cursor.
type captureSinceInput struct {
	// Cursor is what a previous capture_since returned. Empty starts a new
	// reading: the visible screen, and a cursor for next time.
	Cursor string `json:"cursor,omitempty" jsonschema:"the cursor a previous capture_since returned; empty starts a new reading"`
	// PaneID is the tmux pane id. It may be omitted when Cursor is given,
	// which already names the pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to read; the cursor already names it"`
	// SessionName picks the session when PaneID and Cursor are both empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to read when paneId is empty"`
	// MaxLines caps how many lines come back, keeping the last ones.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	// MaxBytes caps the reply's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

// captureSinceOutput carries what is new and where to continue from.
type captureSinceOutput struct {
	// PaneID is the pane that was read.
	PaneID string `json:"paneId"`
	// Cursor is what to pass to the next call to continue from here. Always
	// present, including when lines were missed, so a client can resume.
	Cursor string `json:"cursor"`
	// Lines are what the pane wrote since the cursor, or the visible screen
	// when there was no cursor or the old one was lost.
	Lines []string `json:"lines"`
	// LinesMissed reports that tmux no longer held everything since the
	// cursor, so what is returned is the visible screen and something between
	// the two readings was discarded.
	LinesMissed bool `json:"linesMissed"`
	// truncation reports what the bounds dropped from this reply, which is
	// separate from LinesMissed: bounds drop what this call chose not to send,
	// and a later call can still read on from the cursor.
	truncation
}

// captureCursor is a cursor's decoded contents.
type captureCursor struct {
	// The field names are one character because a cursor travels in every
	// reply and nothing reads it: it is base64 of this JSON behind a prefix,
	// and the only reader is decodeCursor.
	PaneID string `json:"p"`
	// PanePID identifies the process in the pane. A respawned pane keeps its
	// id, so without this a cursor would silently continue reading a different
	// program's output as though it were the same one's.
	PanePID     int `json:"i"`
	Version     int `json:"v"`
	HistorySize int `json:"h"`
	PaneHeight  int `json:"e"`
	AnchorAbs   int `json:"n"`
	// Leading are the rows immediately above the anchor, oldest first, which
	// is what finds the anchor again after tmux renumbers the grid.
	//
	// Above the anchor rather than at or below it, for two reasons. Below is
	// where a pane sitting at a prompt has nothing — blank rows match
	// everywhere and therefore nowhere. The anchor row itself is the one a
	// shell rewrites the instant anything is typed, so a run including it
	// stops matching precisely when the pane is being used.
	// Packed rather than a JSON array: every row would otherwise carry two
	// quotes and a comma, which on a screenful is more than the hashes.
	Leading packedHashes `json:"l,omitempty"`
	// AnchorHash is the anchor row as it was, which says whether the row has
	// been written over since and therefore whether it is new.
	AnchorHash string `json:"a,omitempty"`
	// BelowHashes are the rows after the anchor the last reply already
	// carried, so they are not sent twice.
	BelowHashes packedHashes `json:"b,omitempty"`
}

// hashWidth is how many characters one row's fingerprint takes, which is
// base64 of the eight bytes lineHash keeps.
const hashWidth = 11

// packedHashes is a run of fixed-width row fingerprints in one string, so the
// JSON carries no quote, comma, or bracket per row.
type packedHashes []string

func (h packedHashes) MarshalJSON() ([]byte, error) {
	return json.Marshal(strings.Join(h, ""))
}

func (h *packedHashes) UnmarshalJSON(raw []byte) error {
	var packed string
	if err := json.Unmarshal(raw, &packed); err != nil {
		return err
	}
	if len(packed)%hashWidth != 0 {
		return fmt.Errorf("a run of %d characters is not whole fingerprints", len(packed))
	}
	*h = nil
	for at := 0; at < len(packed); at += hashWidth {
		*h = append(*h, packed[at:at+hashWidth])
	}
	return nil
}

// paneState is where tmux's grid stood at one instant.
//
// The four values are read in one command because they only mean something
// together: a history size from one read and a cursor row from another
// describe no position the pane was ever in.
type paneState struct {
	pid         int
	historySize int
	height      int
	cursorY     int
	dead        bool
}

// paneStateFormat asks tmux for the whole state in one expansion.
//
// The fields are separated by a printable character rather than a tab. tmux
// rewrites control characters in format output for a client it does not
// believe is using UTF-8, which is any client whose environment names no
// UTF-8 locale — the environment an MCP client commonly starts a server with.
// A tab comes back as an underscore there, and every value in this reply is a
// number or a flag, so nothing here can contain the separator either way.
const paneStateFormat = "#{pane_pid}|#{history_size}|#{pane_height}|#{cursor_y}|#{pane_dead}"

// readPaneState asks tmux where the pane's grid stands.
func readPaneState(ctx context.Context, pane tmux.Pane) (paneState, error) {
	printed, err := pane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{
			Print:   true,
			Message: paneStateFormat,
		},
	})
	if err != nil {
		return paneState{}, err
	}
	if len(printed) == 0 {
		return paneState{}, fmt.Errorf("pane %s reported no state", pane.ID())
	}
	fields := strings.Split(printed[0], "|")
	if len(fields) != 5 {
		return paneState{}, fmt.Errorf("pane %s reported unreadable state %q", pane.ID(), printed[0])
	}
	numbers := make([]int, 4)
	for index := range numbers {
		value, err := strconv.Atoi(strings.TrimSpace(fields[index]))
		if err != nil {
			return paneState{}, fmt.Errorf(
				"pane %s reported unreadable state %q", pane.ID(), printed[0])
		}
		numbers[index] = value
	}
	return paneState{
		pid:         numbers[0],
		historySize: numbers[1],
		height:      numbers[2],
		cursorY:     numbers[3],
		// tmux writes the flag as 1 or 0, and as empty on versions that do not
		// set it for a live pane.
		dead: strings.TrimSpace(fields[4]) == "1",
	}, nil
}

// captureSince reads what a pane wrote since a cursor.
func (t *tools) captureSince(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input captureSinceInput,
) (*mcp.CallToolResult, captureSinceOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, captureSinceOutput{}, err
	}

	var cursor *captureCursor
	if strings.TrimSpace(input.Cursor) != "" {
		decoded, err := decodeCursor(input.Cursor)
		if err != nil {
			return nil, captureSinceOutput{}, err
		}
		cursor = &decoded
	}

	paneID := input.PaneID
	if paneID == "" && cursor != nil {
		paneID = cursor.PaneID
	}
	pane, err := t.resolvePane(ctx, paneID, input.SessionName)
	if err != nil {
		return nil, captureSinceOutput{}, err
	}
	if cursor != nil && pane.ID().String() != cursor.PaneID {
		// Reading on from another pane's cursor would report that pane's
		// history as this one's, which is worse than refusing.
		return nil, captureSinceOutput{}, fmt.Errorf(
			"the cursor belongs to pane %s, not %s", cursor.PaneID, pane.ID())
	}

	read, err := t.readSince(ctx, pane, cursor)
	if err != nil {
		return nil, captureSinceOutput{}, err
	}
	kept, report := limits.apply(read.lines)
	return textResult(kept), captureSinceOutput{
		PaneID:      pane.ID().String(),
		Cursor:      encodeCursor(pane.ID().String(), read.state, read),
		Lines:       kept,
		LinesMissed: read.missed,
		truncation:  report,
	}, nil
}

// paneRead is one settled observation of a pane.
type paneRead struct {
	state paneState
	// anchorRows are the rows from the cursor row through the bottom of the
	// screen. What the next cursor records of them is which ones it already
	// reported, so they are not sent twice.
	anchorRows []string
	// leadingRows are the rows ending at the cursor row, which the next cursor
	// fingerprints. Recognising them is what lets a later call find the anchor
	// after tmux has renumbered the grid underneath it.
	leadingRows []string
	lines       []string
	missed      bool
}

// readSince reads either the visible screen or what came after a cursor.
func (t *tools) readSince(
	ctx context.Context,
	pane tmux.Pane,
	cursor *captureCursor,
) (paneRead, error) {
	if cursor == nil {
		return readVisible(ctx, pane, 0)
	}
	return readDelta(ctx, pane, *cursor)
}

// readVisible reads the whole visible screen and settles on a state for it.
//
// A pane writing while it is read gives a screen from one instant and a state
// from another, and the cursor built from the pair would anchor at a row the
// screen never had. So the state is read on both sides of the capture and the
// read is repeated until they agree.
func readVisible(ctx context.Context, pane tmux.Pane, expectPID int) (paneRead, error) {
	var last paneState
	for range stableReadAttempts {
		before, err := readPaneState(ctx, pane)
		if err != nil {
			return paneRead{}, err
		}
		if err := checkLifecycle(pane, before, expectPID); err != nil {
			return paneRead{}, err
		}
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return paneRead{}, err
		}
		anchor, leading, err := captureFingerprintRows(ctx, pane, before)
		if err != nil {
			return paneRead{}, err
		}
		after, err := readPaneState(ctx, pane)
		if err != nil {
			return paneRead{}, err
		}
		if err := checkLifecycle(pane, after, expectPID); err != nil {
			return paneRead{}, err
		}
		if before == after {
			return paneRead{
				state: after, anchorRows: anchor, leadingRows: leading, lines: lines,
			}, nil
		}
		last = after
	}
	// A pane that never settles still has to be answered. The reply is what it
	// shows now, marked as incomplete because the rows between this reading
	// and the last were not all seen.
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		return paneRead{}, err
	}
	anchor, leading, err := captureFingerprintRows(ctx, pane, last)
	if err != nil {
		return paneRead{}, err
	}
	return paneRead{
		state: last, anchorRows: anchor, leadingRows: leading,
		lines: lines, missed: true,
	}, nil
}

// readDelta reads the rows written since a cursor.
func readDelta(ctx context.Context, pane tmux.Pane, cursor captureCursor) (paneRead, error) {
	for range stableReadAttempts {
		before, err := readPaneState(ctx, pane)
		if err != nil {
			return paneRead{}, err
		}
		if err := checkLifecycle(pane, before, cursor.PanePID); err != nil {
			return paneRead{}, err
		}
		if anchorLost(cursor, before) {
			return missedRead(ctx, pane, cursor.PanePID)
		}

		rows, at, found, err := locateAnchor(ctx, pane, cursor, before)
		if err != nil {
			return paneRead{}, err
		}
		if !found {
			return missedRead(ctx, pane, cursor.PanePID)
		}
		anchor, leading, err := captureFingerprintRows(ctx, pane, before)
		if err != nil {
			return paneRead{}, err
		}
		after, err := readPaneState(ctx, pane)
		if err != nil {
			return paneRead{}, err
		}
		if err := checkLifecycle(pane, after, cursor.PanePID); err != nil {
			return paneRead{}, err
		}
		if before != after {
			continue
		}
		return paneRead{
			state:       after,
			anchorRows:  anchor,
			leadingRows: leading,
			// The blank rows below what the pane has written are the unused
			// part of the screen rather than output, and they are most of a
			// quiet pane's reply.
			lines: trimTrailingBlank(dropSeenRows(rows[at:], cursor)),
		}, nil
	}
	return missedRead(ctx, pane, cursor.PanePID)
}

// locateAnchor finds the anchor row and returns the grid it was found in.
//
// The cheap path reads from where the anchor would be if tmux had discarded
// nothing, starting far enough above it to include the fingerprint, and checks
// that the fingerprint is there. That is one capture and it is what happens
// whenever the pane has not reached its history limit.
//
// When it is not there, tmux has freed the oldest tenth of the history and
// shifted the rest up — every row's number changed at once, and tmux publishes
// no count of what it dropped, so the row cannot be renumbered arithmetically.
// The whole retained history is read and searched for the fingerprint instead.
func locateAnchor(
	ctx context.Context,
	pane tmux.Pane,
	cursor captureCursor,
	state paneState,
) (rows []string, at int, found bool, err error) {
	if len(cursor.Leading) == 0 {
		// A cursor from before this server recorded a fingerprint, or one for
		// a pane that had written nothing at all. Its position is all there
		// is, so it is used as-is rather than reporting a loss that may not
		// have happened.
		start := cursor.AnchorAbs - state.historySize
		rows, err = captureRawRows(ctx, pane, tmux.CapturePaneRequest{
			Start: tmux.CaptureLine(start),
		})
		return rows, 0, err == nil, err
	}

	lead := len(cursor.Leading)
	start := cursor.AnchorAbs - state.historySize - lead
	rows, err = captureRawRows(ctx, pane, tmux.CapturePaneRequest{
		Start: tmux.CaptureLine(start),
	})
	if err != nil {
		return nil, 0, false, err
	}
	if len(rows) > lead && matchesFingerprint(rows[:lead], cursor.Leading) {
		return rows, lead, true, nil
	}

	whole, err := captureRawRows(ctx, pane, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary,
	})
	if err != nil {
		return nil, 0, false, err
	}
	match := uniqueFingerprintMatch(whole, cursor.Leading)
	if match < 0 {
		return nil, 0, false, nil
	}
	return whole, match + lead, true, nil
}

// matchesFingerprint reports whether rows hash to the recorded run.
func matchesFingerprint(rows []string, hashes []string) bool {
	if len(rows) != len(hashes) {
		return false
	}
	for index, hash := range hashes {
		if lineHash(rows[index]) != hash {
			return false
		}
	}
	return true
}

// uniqueFingerprintMatch reports where a run of row hashes occurs exactly once.
//
// Exactly once matters. A pane showing a repeated prompt matches in many
// places, and resuming from one of them would send rows that only look like
// the right ones; saying the anchor was lost is the honest answer.
func uniqueFingerprintMatch(rows []string, fingerprint []string) int {
	if len(fingerprint) == 0 || len(rows) < len(fingerprint) {
		return -1
	}
	found := -1
	for index := 0; index+len(fingerprint) <= len(rows); index++ {
		if !matchesFingerprint(rows[index:index+len(fingerprint)], fingerprint) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = index
	}
	return found
}

// trimTrailingBlank drops the unwritten rows at the end of a capture.
func trimTrailingBlank(rows []string) []string {
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// missedRead answers with the visible screen when the thread was lost.
func missedRead(ctx context.Context, pane tmux.Pane, expectPID int) (paneRead, error) {
	read, err := readVisible(ctx, pane, expectPID)
	if err != nil {
		return paneRead{}, err
	}
	read.missed = true
	return read, nil
}

// captureAnchorRows reads the rows from the cursor row down, which the next
// cursor fingerprints. A cursor below the screen has none.
//
// The rows are read as bytes rather than as lines, because a decoded capture
// drops every trailing blank row and a pane sitting at a fresh prompt is
// blank from the cursor down. Decoding gave nothing to fingerprint, and a
// cursor with no fingerprint cannot be checked against the rows it claims to
// name — which is how a trimmed grid went unnoticed.
func captureFingerprintRows(
	ctx context.Context,
	pane tmux.Pane,
	state paneState,
) (anchor []string, leading []string, err error) {
	if state.cursorY < state.height {
		anchor, err = captureRawRows(ctx, pane, tmux.CapturePaneRequest{
			Start: tmux.CaptureLine(state.cursorY),
		})
		if err != nil {
			return nil, nil, err
		}
	}
	// The settled rows above the anchor, which may reach back into history
	// when the pane has written more than a screenful. A cursor at the very
	// top of a pane that has written nothing has none, and is then located by
	// its position alone.
	if state.cursorY > 0 {
		leading, err = captureRawRows(ctx, pane, tmux.CapturePaneRequest{
			Start: tmux.CaptureLine(max(state.cursorY-fingerprintRows, -state.historySize)),
			End:   tmux.CaptureLine(state.cursorY - 1),
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return anchor, leading, nil
}

// captureRawRows captures rows without losing the blank ones.
//
// tmux writes one newline per row, so the last one is a terminator rather than
// an empty row after it; everything else is a row, blank or not.
func captureRawRows(
	ctx context.Context,
	pane tmux.Pane,
	request tmux.CapturePaneRequest,
) ([]string, error) {
	raw, err := pane.CaptureBytes(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	rows := strings.Split(string(raw), "\n")
	if len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows, nil
}

// checkLifecycle refuses to keep reading a pane that is not the one the reading
// started on.
//
// A pane that was respawned holds a different program under the same id, and a
// cursor issued for the first one describes rows the second never wrote.
// Continuing would report one program's output as another's, which a caller
// has no way to notice.
func checkLifecycle(pane tmux.Pane, state paneState, expectPID int) error {
	if expectPID != 0 && state.pid != expectPID {
		return fmt.Errorf(
			"pane %s is running a different process than the cursor was issued for", pane.ID())
	}
	if state.dead && expectPID == 0 {
		return fmt.Errorf("pane %s has no running process", pane.ID())
	}
	return nil
}

// anchorLost reports whether tmux can still address the cursor's anchor.
func anchorLost(cursor captureCursor, state paneState) bool {
	// Below the last row there is: the pane was cleared, or the grid shrank
	// past where the cursor pointed.
	if cursor.AnchorAbs > state.historySize+state.height-1 {
		return true
	}
	// clear-history resets the grid to nothing, which loses every anchor
	// regardless of where it pointed.
	if state.historySize == 0 && cursor.HistorySize > 0 {
		return true
	}
	// History that shrank was trimmed, and rows before the anchor went with
	// it. A pane that grew taller also shows a smaller history without having
	// discarded anything, because rows moved from history onto the screen, so
	// the height is checked before concluding anything was lost.
	return state.historySize < cursor.HistorySize && state.height <= cursor.PaneHeight
}

// dropSeenRows removes the rows a cursor already reported.
//
// The anchor row is where the cursor sat, which is a row a program may still
// have been writing. It is dropped only when it is unchanged: a prompt that
// has since had a command typed onto it is new text on an old row, and a
// client that never saw it would be missing the line it most wants.
func dropSeenRows(rows []string, cursor captureCursor) []string {
	if len(rows) == 0 {
		return nil
	}
	anchorHash := cursor.AnchorHash
	kept := make([]string, 0, len(rows))
	if anchorHash == "" || lineHash(rows[0]) != anchorHash {
		kept = append(kept, rows[0])
	}
	tail := rows[1:]
	drop := 0
	for index, hash := range cursor.BelowHashes {
		if index >= len(tail) || lineHash(tail[index]) != hash {
			break
		}
		drop++
	}
	return append(kept, tail[drop:]...)
}

// lineHash fingerprints one row. Rows are compared by content because their
// numbers move: tmux renumbers the whole grid when it trims the oldest row.
func lineHash(line string) string {
	sum := sha256.Sum256([]byte(line))
	// Eight bytes of the digest, because the cursor travels in every reply and
	// carries one of these per row: a full digest made a cursor for a screen of
	// mostly blank rows cost more than sending that screen would have. These
	// are compared against the rows of one pane, so even a pane holding a
	// million rows is nowhere near an accidental collision.
	//
	// Base64 rather than hex, which is eleven characters for the same eight
	// bytes instead of sixteen.
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// encodeCursor renders a cursor as one opaque string.
func encodeCursor(paneID string, state paneState, read paneRead) string {
	cursor := captureCursor{
		Version:     cursorVersion,
		PaneID:      paneID,
		PanePID:     state.pid,
		HistorySize: state.historySize,
		PaneHeight:  state.height,
		AnchorAbs:   state.historySize + state.cursorY,
	}
	for _, row := range read.leadingRows {
		cursor.Leading = append(cursor.Leading, lineHash(row))
	}
	if len(read.anchorRows) > 0 {
		cursor.AnchorHash = lineHash(read.anchorRows[0])
	}
	if len(read.anchorRows) > 1 {
		cursor.BelowHashes = make([]string, 0, len(read.anchorRows)-1)
		for _, row := range read.anchorRows[1:] {
			cursor.BelowHashes = append(cursor.BelowHashes, lineHash(row))
		}
	}
	// Marshaling a value this package owns cannot fail, and there is nothing
	// useful to say if it somehow did: a cursor that could not be built is
	// reported as no cursor, which reads as a fresh start.
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeCursor reads a cursor back, refusing anything this server did not
// write. A malformed cursor is a failure rather than a fresh start: a client
// that corrupted one is not asking to read the pane from the top, and silently
// doing that would send it the whole screen as though it were new.
func decodeCursor(value string) (captureCursor, error) {
	rest, found := strings.CutPrefix(value, cursorPrefix)
	if !found {
		return captureCursor{}, errors.New("that is not a cursor this server issued")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return captureCursor{}, fmt.Errorf("the cursor is damaged: %w", err)
	}
	var cursor captureCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return captureCursor{}, fmt.Errorf("the cursor is damaged: %w", err)
	}
	if cursor.Version != cursorVersion {
		return captureCursor{}, fmt.Errorf(
			"the cursor is version %d, and this server issues version %d",
			cursor.Version, cursorVersion)
	}
	if cursor.PaneID == "" {
		return captureCursor{}, errors.New("the cursor names no pane")
	}
	return cursor, nil
}

// addCaptureTools advertises the tools that read a pane's text.
func addCaptureTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "capture_pane",
		Annotations: readOnly("Capture a tmux Pane"),
		Description: "Read what one pane holds: its visible screen, or its " +
			"scrollback too with includeHistory. The reply is bounded and says " +
			"what it dropped. Pass styles for a program that reports success or " +
			"failure in colour rather than in words, which a capture otherwise " +
			"strips. Use capture_since instead to read a pane repeatedly without " +
			"paying for the same screen each time.",
	}, t.capturePane)
	register(server, t, &mcp.Tool{
		Name:        "capture_since",
		Annotations: readOnly("Read What a Pane Wrote Since"),
		Description: "Read only what a pane wrote since the cursor a previous " +
			"call returned, and get a cursor for next time. Call it once with no " +
			"cursor to start. This is how to watch a pane across turns: a quiet " +
			"pane returns nothing rather than its whole screen again. " +
			"linesMissed reports that tmux discarded scrollback in between, so " +
			"the reply is the current screen rather than everything since.",
	}, t.captureSince)
	register(server, t, &mcp.Tool{
		Name:        "clear_pane",
		Annotations: settling("Clear a tmux Pane"),
		Description: "Clear a pane's screen, and its scrollback when asked. " +
			"Clearing what has already been read keeps later captures small.",
	}, t.clearPane)
	register(server, t, &mcp.Tool{
		Name:        "pipe_pane",
		Annotations: mutating("Pipe a Pane's Output"),
		Description: "Send everything a pane writes to a shell command as well " +
			"as to the screen, such as \"cat >> /tmp/build.log\". Use it for " +
			"output too large for scrollback, which is lost before anyone reads " +
			"it. Call again with no command to stop.",
	}, t.pipePane)
}
