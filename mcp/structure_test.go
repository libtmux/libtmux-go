package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeysReachAPaneOnlyThroughTheDeliveryResolver is the gate under the
// delivery guard.
//
// The guard refuses a pane that cannot read a key, and it is reached by
// obtaining the pane rather than by remembering to call it. That only holds
// while every handler that types takes its pane from resolvePaneToDeliver: one
// that takes resolvePaneToWrite instead, and types anyway, is back to the
// defect this replaced, and nothing about the code would look wrong.
//
// The claim is about the source rather than about behaviour, so it stays true
// for tools nobody has written yet, which is what a behavioural test of the
// tools that exist cannot do.
func TestKeysReachAPaneOnlyThroughTheDeliveryResolver(t *testing.T) {
	// Ways of putting a keystroke into a pane. paste_buffer reaches tmux by a
	// different call than the rest, so both are named.
	delivers := map[string]bool{"SendKeys": true, "PasteBuffer": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			var types, resolves []string
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case delivers[selector.Sel.Name]:
					types = append(types, selector.Sel.Name)
				case strings.HasPrefix(selector.Sel.Name, "resolvePaneTo"):
					resolves = append(resolves, selector.Sel.Name)
				}
				return true
			})
			if len(types) == 0 {
				continue
			}
			checked++
			for _, resolved := range resolves {
				if resolved != "resolvePaneToDeliver" {
					t.Errorf("%s in %s calls %s and then %s: a handler that "+
						"types has to take its pane from resolvePaneToDeliver, "+
						"which is what refuses a pane that cannot read it",
						function.Name.Name, filepath.Base(name), resolved,
						strings.Join(types, " and "))
				}
			}
		}
	}
	// Without this the test passes when it matched nothing, which is what
	// renaming either delivery call would cause.
	if checked < 3 {
		t.Errorf("only %d functions were found to type into a pane; the shape "+
			"this looks for has moved", checked)
	}
}
