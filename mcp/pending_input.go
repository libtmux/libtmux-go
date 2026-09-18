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

// observeKeys updates pending for a send_keys-style dispatch: a literal
// sequence is unsubmitted text appended to the line; a non-literal sequence
// naming a submit key clears it, matching what tmux itself does with -l.
func (p *pendingInput) observeKeys(pane tmux.PaneID, keys []string, literal bool) {
	switch {
	case literal:
		p.append(pane, strings.Join(keys, ""))
	case containsSubmitKey(keys):
		p.clear(pane)
	}
}

func containsSubmitKey(keys []string) bool {
	for _, key := range keys {
		if submitKeyNames[key] {
			return true
		}
	}
	return false
}
