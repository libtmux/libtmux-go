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

// eraseKeyNames are tmux key names that remove the character before the
// cursor, and killLineKeyNames those that discard the line. Both are modelled
// because correcting a typo is ordinary, and leaving either unmodelled puts
// the mask out of step with the line.
var eraseKeyNames = map[string]bool{"BSpace": true, "C-h": true, "DC": true}

var killLineKeyNames = map[string]bool{"C-u": true, "C-c": true}

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

// willType reports what a send_keys-style dispatch does to a pane's line:
// text is what it adds before tmux takes it, afterEnd is the line it leaves
// behind, and endsLine says whether the line it started on is gone by the
// end. The three are recorded at different moments. text has to be pending
// before tmux takes the keys, because the terminal's echo can reach a waiting
// client first. Ending a line may only be recorded once tmux has taken them,
// because until then the old line is still there and still needs masking -
// which is why a sequence that ends one adds nothing up front and leaves the
// old text guarding itself.
//
// Enter ends a line by submitting it and C-u by discarding it; the mask does
// not care which, only that what it was covering has gone.
//
// Masking works by removing the pending text from what the pane shows, so it
// holds only while that text is on the line verbatim. Typing, erasing, ending
// the line: those are modelled. A sequence carrying anything else - a cursor
// move, a completion - is not modelled at all, and reports nothing rather
// than a line tmux never drew, because a mask that is not on the line removes
// nothing and would take what was already tracked down with it. Such a
// sequence goes unmasked, so a wait can see the keys it typed.
func willType(keys []string, literal bool) (text, afterEnd string, endsLine bool) {
	if literal {
		// tmux writes literal bytes through, and the pane's line discipline
		// reads a newline among them as Enter, so literal text spanning
		// lines ends every line but its last.
		joined := strings.Join(keys, "")
		if cut := strings.LastIndexAny(joined, "\r\n"); cut >= 0 {
			return "", joined[cut+1:], true
		}
		return joined, "", false
	}
	var typed strings.Builder
	for _, key := range keys {
		switch {
		case submitKeyNames[key] || killLineKeyNames[key]:
			endsLine = true
			typed.Reset()
			continue
		case eraseKeyNames[key]:
			dropLastRune(&typed)
			continue
		case key == "Space":
			key = " "
		case !typesOneRune(key):
			return "", "", false
		}
		typed.WriteString(key)
	}
	if endsLine {
		return "", typed.String(), true
	}
	return typed.String(), "", false
}

// dropLastRune removes the final rune a builder holds, which is what a
// backspace does to the line.
func dropLastRune(builder *strings.Builder) {
	held := builder.String()
	if held == "" {
		return
	}
	_, width := utf8.DecodeLastRuneInString(held)
	builder.Reset()
	builder.WriteString(held[:len(held)-width])
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
