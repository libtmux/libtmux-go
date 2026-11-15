package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// surfaceMember is an exported-or-not method or struct field of the tmux
// package, keyed in [memberSurface] as "tmux.Type.Member".
type surfaceMember struct {
	exported  bool
	generated bool
}

// memberSurface lists the methods and fields the package declares outside its
// tests. It reads every non-test file whatever its build constraints, so a
// name declared for one platform is still seen.
func memberSurface(t *testing.T) map[string]surfaceMember {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(documentationPackageDir(t), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	surface := map[string]surfaceMember{}
	add := func(name string, exported, generated bool) {
		previous := surface[name]
		surface[name] = surfaceMember{
			exported:  previous.exported || exported,
			generated: previous.generated || generated,
		}
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		generated := ast.IsGenerated(file)
		for _, declaration := range file.Decls {
			switch node := declaration.(type) {
			case *ast.FuncDecl:
				if node.Recv == nil || len(node.Recv.List) == 0 {
					continue
				}
				if receiver := receiverTypeName(node.Recv); receiver != "" {
					add("tmux."+receiver+"."+node.Name.Name,
						ast.IsExported(receiver) && ast.IsExported(node.Name.Name), generated)
				}
			case *ast.GenDecl:
				for _, specification := range node.Specs {
					spec, ok := specification.(*ast.TypeSpec)
					if !ok {
						continue
					}
					structure, ok := spec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							add("tmux."+spec.Name.Name+"."+name.Name,
								ast.IsExported(spec.Name.Name) && ast.IsExported(name.Name), generated)
						}
					}
				}
			}
		}
	}
	return surface
}

func TestGoSurfaceUsesUnstutteredOperationNames(t *testing.T) {
	t.Parallel()
	surface := memberSurface(t)
	for _, symbol := range []string{
		"tmux.Pane.Capture",
		"tmux.Pane.CaptureToBuffer",
		"tmux.Server.Start",
	} {
		if member, found := surface[symbol]; !found || !member.exported {
			t.Errorf("unstuttered operation is not exported as %s", symbol)
		}
	}
	for _, symbol := range []string{
		"tmux.Pane.CapturePane",
		"tmux.Pane.CapturePaneToBuffer",
		"tmux.Server.StartServer",
	} {
		if member, found := surface[symbol]; found && member.exported {
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
	checked := 0
	convicted := map[string]bool{}
	for symbol, member := range memberSurface(t) {
		if !member.exported || member.generated {
			continue
		}
		parts := strings.Split(symbol, ".")
		if len(parts) != 3 || !tmuxModelReceivers[parts[1]] {
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
