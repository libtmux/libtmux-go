package mcp

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
)

// pendingInput tracks, per pane, the text this process itself typed but has
// not submitted: the exact bytes send_keys or paste_text placed on a pane's
// current input line without a following Enter. wait_for_text uses it so a
// match whose only occurrence is that pending line - the caller's own
// unsubmitted input, however new the bytes look on the notification stream -
// is never reported as output the pane produced.
//
// Tracking is best-effort: a key this process cannot represent as literal
// text, and is not a recognized submit key, leaves pending unchanged rather
// than guessing at the line's new contents. That can go stale after such a
// key (a cursor move, for instance), but only toward over-masking a line
// that is no longer what pending says - a missed match, never a false one.
type pendingInput struct {
	mutex  sync.Mutex
	byPane map[tmux.PaneID]string
}

// submitKeyNames are tmux key names that submit a line the way Enter does.
var submitKeyNames = map[string]bool{
	"Enter":   true,
	"C-m":     true,
	"KPEnter": true,
}

// append adds text to pane's pending, unsubmitted input.
func (p *pendingInput) append(pane tmux.PaneID, text string) {
	if text == "" {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.byPane == nil {
		p.byPane = make(map[tmux.PaneID]string)
	}
	p.byPane[pane] += text
}

// clear reports pane's line as submitted: nothing on it is pending anymore.
func (p *pendingInput) clear(pane tmux.PaneID) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.byPane, pane)
}

// snapshot returns what is currently pending for pane.
func (p *pendingInput) snapshot(pane tmux.PaneID) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.byPane[pane]
}

// restore puts pane's pending back to text, for a dispatch that failed after
// pending was recorded.
func (p *pendingInput) restore(pane tmux.PaneID, text string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if text == "" {
		delete(p.byPane, pane)
		return
	}
	if p.byPane == nil {
		p.byPane = make(map[tmux.PaneID]string)
	}
	p.byPane[pane] = text
}

// record makes text pending on every pane the keys reach and returns a
// function putting each back as it was, for a dispatch that then fails. Empty
// text records nothing and returns a no-op.
//
// The snapshot each pane is restored to is read outside the lock that guards
// the pane, so a second record or clearAll for the same pane while this one
// is unresolved would lose one of them. Nothing does: processPaneInputs
// grants one caller at a time over any overlapping set of panes, and every
// caller here resolves the restore before releasing that lease. A caller
// reaching these without that lease reopens the race.
func (p *pendingInput) record(panes []string, text string) func() {
	if text == "" {
		return func() {}
	}
	previous := make(map[tmux.PaneID]string, len(panes))
	for _, pane := range panes {
		id := tmux.PaneID(pane)
		previous[id] = p.snapshot(id)
		p.append(id, text)
	}
	return func() {
		for id, was := range previous {
			p.restore(id, was)
		}
	}
}

// clearAll reports every pane's line as submitted.
func (p *pendingInput) clearAll(panes []string) {
	for _, pane := range panes {
		p.clear(tmux.PaneID(pane))
	}
}

// willType reports what a send_keys-style dispatch puts on the line: text is
// everything it types, afterSubmit is what it types past the last submit, and
// submits says whether it submitted at all. The three are recorded at
// different moments: text has to be pending before tmux takes it, because the
// terminal's echo can reach a waiting client first, while a submit and the
// line it starts may only be recorded once tmux has taken them, because until
// then the old line is still unsubmitted. Recording either the other way
// round turns a missed match into a false one.
//
// A key this cannot render as text - a cursor move, a control key, a
// backspace - leaves what is pending as it stands rather than guessing at the
// line. That over-masks, which is the direction this is allowed to be wrong
// in.
func willType(keys []string, literal bool) (text, afterSubmit string, submits bool) {
	if literal {
		joined := strings.Join(keys, "")
		return joined, "", false
	}
	var typed, sinceSubmit strings.Builder
	for _, key := range keys {
		switch {
		case submitKeyNames[key]:
			submits = true
			sinceSubmit.Reset()
			continue
		case key == "Space":
			key = " "
		case !typesOneRune(key):
			continue
		}
		typed.WriteString(key)
		sinceSubmit.WriteString(key)
	}
	if !submits {
		return typed.String(), "", false
	}
	return typed.String(), sinceSubmit.String(), true
}

// typesOneRune reports whether tmux types key as itself. A key named by one
// printable rune is that rune; every longer name is a key rather than text.
func typesOneRune(key string) bool {
	if utf8.RuneCountInString(key) != 1 {
		return false
	}
	character, _ := utf8.DecodeRuneInString(key)
	return unicode.IsPrint(character)
}
