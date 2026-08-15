package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/tmux-python/libtmux/golang"
)

func TestMaterializedIdentityFieldsArePrivate(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "model.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantPrivate := map[string]map[string]bool{
		"Session": {"SessionID": true},
		"Window":  {"SessionID": true, "WindowID": true, "WindowIndex": true},
		"Pane": {
			"SessionID": true, "WindowID": true, "WindowIndex": true,
			"PaneID": true, "PaneIndex": true,
		},
		"Client": {"ClientName": true},
	}
	seen := make(map[string]bool, len(wantPrivate))

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			forbidden, ok := wantPrivate[typeSpec.Name.Name]
			if !ok {
				continue
			}
			seen[typeSpec.Name.Name] = true
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is not a struct", typeSpec.Name.Name)
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if forbidden[name.Name] && name.IsExported() {
						t.Errorf("%s.%s is exported identity storage", typeSpec.Name.Name, name.Name)
					}
				}
			}
		}
	}
	for model := range wantPrivate {
		if !seen[model] {
			t.Errorf("materialized model %s was not inspected", model)
		}
	}
}

// libtmux:parity libtmux.neo.Obj.client_name
// libtmux:parity libtmux.neo.Obj.pane_id
// libtmux:parity libtmux.neo.Obj.pane_index
// libtmux:parity libtmux.neo.Obj.session_id
// libtmux:parity libtmux.neo.Obj.window_id
// libtmux:parity libtmux.neo.Obj.window_index
func TestMaterializedIdentityMethodSignaturesCompile(t *testing.T) {
	t.Parallel()

	var session tmux.Session
	var window tmux.Window
	var pane tmux.Pane
	var client tmux.Client

	requireAssignable[func() tmux.SessionID](session.ID)
	requireAssignable[func() tmux.SessionID](window.SessionID)
	requireAssignable[func() tmux.WindowID](window.ID)
	requireAssignable[func() int](window.Index)
	requireAssignable[func() tmux.SessionID](pane.SessionID)
	requireAssignable[func() tmux.WindowID](pane.WindowID)
	requireAssignable[func() int](pane.WindowIndex)
	requireAssignable[func() tmux.PaneID](pane.ID)
	requireAssignable[func() int](pane.Index)
	requireAssignable[func() tmux.ClientName](client.Name)
}
