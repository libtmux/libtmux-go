package mcp

import (
	"strings"
	"sync"

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

// willType reports what a send_keys-style dispatch puts on the line, and
// whether it submits it. The two halves are recorded at different moments:
// text has to be pending before tmux takes it, because the terminal's echo
// can reach a waiting client first, and a submit may only be recorded once
// tmux has taken it, because until then the line is still unsubmitted.
// Recording either the other way round turns a missed match into a false one.
func willType(keys []string, literal bool) (text string, submits bool) {
	if literal {
		return strings.Join(keys, ""), false
	}
	return "", containsSubmitKey(keys)
}

func containsSubmitKey(keys []string) bool {
	for _, key := range keys {
		if submitKeyNames[key] {
			return true
		}
	}
	return false
}
