package tmux_test

import (
	"strings"
	"testing"
	"unicode"

	"github.com/libtmux/libtmux-go/internal/goname"
)

// libtmux:parity libtmux.exc.DeprecatedError
// libtmux:parity libtmux.exc.DeprecatedError.__init__
// libtmux:parity libtmux.pane.Pane.__getitem__
// libtmux:parity libtmux.pane.Pane.get
// libtmux:parity libtmux.pane.Pane.resize_pane
// libtmux:parity libtmux.pane.Pane.select_pane
// libtmux:parity libtmux.pane.Pane.split_window
// libtmux:parity libtmux.server.Server.children
// libtmux:parity libtmux.server.Server.find_where
// libtmux:parity libtmux.server.Server.get_by_id
// libtmux:parity libtmux.server.Server.kill_server
// libtmux:parity libtmux.server.Server.list_sessions
// libtmux:parity libtmux.server.Server.where
// libtmux:parity libtmux.session.Session.__getitem__
// libtmux:parity libtmux.session.Session.attach_session
// libtmux:parity libtmux.session.Session.attached_pane
// libtmux:parity libtmux.session.Session.attached_window
// libtmux:parity libtmux.session.Session.children
// libtmux:parity libtmux.session.Session.find_where
// libtmux:parity libtmux.session.Session.get
// libtmux:parity libtmux.session.Session.get_by_id
// libtmux:parity libtmux.session.Session.kill_session
// libtmux:parity libtmux.session.Session.list_windows
// libtmux:parity libtmux.session.Session.where
// libtmux:parity libtmux.window.Window.__getitem__
// libtmux:parity libtmux.window.Window.attached_pane
// libtmux:parity libtmux.window.Window.children
// libtmux:parity libtmux.window.Window.find_where
// libtmux:parity libtmux.window.Window.get
// libtmux:parity libtmux.window.Window.get_by_id
// libtmux:parity libtmux.window.Window.kill_window
// libtmux:parity libtmux.window.Window.list_panes
// libtmux:parity libtmux.window.Window.select_window
// libtmux:parity libtmux.window.Window.split_window
// libtmux:parity libtmux.window.Window.where
func TestDeprecatedPythonAPIsStayOutOfGoSurface(t *testing.T) {
	t.Parallel()
	index, err := indexParityGoSymbols(".")
	if err != nil {
		t.Fatal(err)
	}
	for symbol, declaration := range index {
		if !declaration.Production {
			continue
		}
		if symbol == "tmux.ErrDeprecated" || strings.HasPrefix(symbol, "tmux.Deprecated") {
			t.Errorf("Python deprecation history is exported as %s", symbol)
		}
	}
}

// libtmux:parity libtmux#export:__author__
// libtmux:parity libtmux#export:__copyright__
// libtmux:parity libtmux#export:__description__
// libtmux:parity libtmux#export:__email__
// libtmux:parity libtmux#export:__license__
// libtmux:parity libtmux#export:__package_name__
// libtmux:parity libtmux#export:__title__
// libtmux:parity libtmux.__about__.__author__
// libtmux:parity libtmux.__about__.__copyright__
// libtmux:parity libtmux.__about__.__description__
// libtmux:parity libtmux.__about__.__docs__
// libtmux:parity libtmux.__about__.__email__
// libtmux:parity libtmux.__about__.__github__
// libtmux:parity libtmux.__about__.__license__
// libtmux:parity libtmux.__about__.__package_name__
// libtmux:parity libtmux.__about__.__pypi__
// libtmux:parity libtmux.__about__.__title__
// libtmux:parity libtmux.__about__.__tracker__
func TestPythonPackageMetadataStaysOutOfGoSurface(t *testing.T) {
	t.Parallel()
	index, err := indexParityGoSymbols(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"tmux.Metadata", "tmux.ProjectMetadata"} {
		if declaration, found := index[symbol]; found && declaration.Production {
			t.Errorf("Python package metadata is exported as %s", symbol)
		}
	}
}

func TestGoSurfaceUsesUnstutteredOperationNames(t *testing.T) {
	t.Parallel()
	index, err := indexParityGoSymbols(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{
		"tmux.Pane.Capture",
		"tmux.Pane.CaptureToBuffer",
		"tmux.Server.Start",
	} {
		declaration, found := index[symbol]
		if !found || !declaration.Production {
			t.Errorf("unstuttered operation is not exported as %s", symbol)
		}
	}
	for _, symbol := range []string{
		"tmux.Pane.CapturePane",
		"tmux.Pane.CapturePaneToBuffer",
		"tmux.Server.StartServer",
	} {
		if declaration, found := index[symbol]; found && declaration.Production {
			t.Errorf("stuttering operation remains exported as %s", symbol)
		}
	}
}

// tmuxModelReceivers are the public model types whose exported methods follow
// the receiver-noun rule in DESIGN.md. Each type is named after the tmux noun
// it models, so the guard derives the singular and plural forms from the type
// name instead of carrying a second spelling that could drift from it.
var tmuxModelReceivers = map[string]bool{
	"Client":  true,
	"Pane":    true,
	"Server":  true,
	"Session": true,
	"Window":  true,
}

// stutteringGoNameExceptions records exported operations that keep their
// receiver's noun even though the receiver-noun rule convicts them. Each key
// needs a reason. The guard also rejects an entry that no longer names a
// convicted operation, so a renamed or removed method cannot leave standing
// permission behind for a future name to inherit.
var stutteringGoNameExceptions = map[string]string{
	"tmux.Pane.BreakPane": "the noun names the operand moved out of its window rather than the " +
		"returned Window, and Pane.Break would read as Go's control-flow keyword",
	"tmux.Server.LockServer": "Server.Lock would read as sync.Locker acquisition on a handle " +
		"documented safe for concurrent method calls, and the noun distinguishes the operation " +
		"from Server.LockClient",
	"tmux.Server.ServerAccess": "access is the object the command lists or mutates rather than a " +
		"verb, so Server.Access would name no operation",
}

// goNameWords splits a Go identifier into its CamelCase words, keeping a run of
// capitals such as ID or UTF8 attached to the letters that belong with it. The
// receiver-noun rule compares whole words, so a noun spelled inside a longer
// word is not a repetition: Window.SplitPane names a pane, not a window.
func goNameWords(name string) []string {
	runes := []rune(name)
	words := make([]string, 0, len(runes))
	start := 0
	for index := 1; index < len(runes); index++ {
		if !unicode.IsUpper(runes[index]) {
			continue
		}
		following := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		if unicode.IsUpper(runes[index-1]) && !following {
			continue
		}
		words = append(words, string(runes[start:index]))
		start = index
	}
	return append(words, string(runes[start:]))
}

// repeatsReceiverNoun reports whether method repeats its own receiver's noun
// outside the two cases DESIGN.md retains it: a New<Noun> operation creating
// another object of the receiver's kind, and a trailing plural naming the
// receiver's siblings. Every other word matching the receiver noun, singular
// or plural, is a repetition the caller already reads from the receiver.
func repeatsReceiverNoun(receiver, method string) bool {
	words := goNameWords(method)
	for index, word := range words {
		switch word {
		case receiver:
			if index == 1 && words[0] == "New" {
				continue
			}
			return true
		case receiver + "s":
			if index == len(words)-1 {
				continue
			}
			return true
		}
	}
	return false
}

// TestGoSurfaceDropsReceiverNounFromOperationNames holds the whole handwritten
// model surface to the receiver-noun rule, so a new tmux command wrapper cannot
// adopt a repeated noun without recording the decision. Generated option, hook,
// and format members are excluded: their names come from the tmux option and
// format specifications, where window-status-style must stay distinguishable
// from the session-scope status-style.
func TestGoSurfaceDropsReceiverNounFromOperationNames(t *testing.T) {
	t.Parallel()
	index, err := indexParityGoSymbols(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	convicted := map[string]bool{}
	for symbol, declaration := range index {
		if !declaration.Production || !declaration.Exported || declaration.Generated {
			continue
		}
		parts := strings.Split(symbol, ".")
		if len(parts) != 3 || parts[0] != "tmux" || !tmuxModelReceivers[parts[1]] {
			continue
		}
		checked++
		if !repeatsReceiverNoun(parts[1], parts[2]) {
			continue
		}
		convicted[symbol] = true
		if reason, allowed := stutteringGoNameExceptions[symbol]; allowed {
			t.Logf("allowed receiver noun in %s: %s", symbol, reason)
			continue
		}
		t.Errorf("%s repeats the receiver noun %q", symbol, parts[1])
	}
	if checked == 0 {
		t.Fatal("no exported model operation was checked for a repeated receiver noun")
	}
	for symbol := range stutteringGoNameExceptions {
		if !convicted[symbol] {
			t.Errorf("exception %s no longer names a convicted operation", symbol)
		}
	}
}

// omittedGoNameExceptions records derived Go spellings that a Go-native API may
// claim even though they collide with an omitted Python name. A Go operation
// that genuinely belongs on its receiver is not a reintroduced Python API. Each
// key needs a reason; the set is empty because no collision exists today.
var omittedGoNameExceptions = map[string]string{}

// omittedGoCandidate derives the Go spelling a Python symbol would reappear
// under, applying the same [goname.Exported] convention the format generator
// uses so the guard cannot drift from the names this module actually produces.
// It returns an empty string for dunder members, which have no Go spelling.
//
// A class member becomes a receiver-scoped candidate, so an unrelated operation
// on another receiver is not mistaken for a reintroduction: omitting
// libtmux.session.Session.kill_session guards tmux.Session.KillSession and
// leaves the module's own tmux.Server.KillSession alone.
func omittedGoCandidate(id string) string {
	path, found := strings.CutPrefix(id, "libtmux.")
	if !found {
		return ""
	}
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return ""
	}
	member := parts[len(parts)-1]
	if strings.HasPrefix(member, "__") {
		return ""
	}
	name := goname.Exported(member)
	if name == "" {
		return ""
	}
	if owner := parts[len(parts)-2]; owner != "" && owner[0] >= 'A' && owner[0] <= 'Z' {
		return "tmux." + owner + "." + name
	}
	return "tmux." + name
}

func TestOmittedPythonAPIsStayOutOfGoSurface(t *testing.T) {
	t.Parallel()
	manifest, err := decodeParityManifest(parityManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	index, err := indexParityGoSymbols(".")
	if err != nil {
		t.Fatal(err)
	}
	guarded := 0
	for _, entry := range manifest.Entries {
		if entry.Translation != "deprecated-python-omission" {
			continue
		}
		candidate := omittedGoCandidate(entry.ID)
		if candidate == "" {
			continue
		}
		if reason, allowed := omittedGoNameExceptions[candidate]; allowed {
			t.Logf("allowed collision for %s: %s", candidate, reason)
			continue
		}
		guarded++
		if declaration, found := index[candidate]; found && declaration.Production {
			t.Errorf("omitted Python API %s is exported as %s", entry.ID, candidate)
		}
	}
	if guarded == 0 {
		t.Fatal("no omitted Python API produced a guarded Go candidate")
	}
}
