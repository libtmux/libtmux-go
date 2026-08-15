package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func requireAssignable[T any](T) {}

// libtmux:parity libtmux.formats.CLIENT_FORMATS
// libtmux:parity libtmux.formats.PANE_FORMATS
// libtmux:parity libtmux.formats.SESSION_FORMATS
// libtmux:parity libtmux.formats.WINDOW_FORMATS
func TestScopedFormatSelectorSignaturesCompile(t *testing.T) {
	t.Parallel()

	var session tmux.Session
	var window tmux.Window
	var pane tmux.Pane
	var client tmux.Client
	var formats tmux.FormatValues

	requireAssignable[func() (string, bool)](session.Name)
	requireAssignable[func() (int, bool)](session.Attached)
	requireAssignable[func() (int, bool)](session.WindowCount)
	requireAssignable[func() (string, bool)](window.Name)
	requireAssignable[func() (bool, bool)](window.Active)
	requireAssignable[func() (int, bool)](window.PaneCount)
	requireAssignable[func() (int, bool)](window.LinkedSessionCount)
	requireAssignable[func() (string, bool)](pane.CurrentCommand)
	requireAssignable[func() (string, bool)](pane.Title)
	requireAssignable[func() (bool, bool)](pane.Piping)
	requireAssignable[func() (int, bool)](pane.ProcessPID)
	requireAssignable[func() (bool, bool)](client.ReadOnly)
	requireAssignable[func() (int, bool)](client.ProcessPID)
	requireAssignable[func() (int, bool)](client.ProcessUID)
	requireAssignable[func() (string, bool)](client.ProcessUser)
	requireAssignable[func() (time.Time, bool)](client.Created)
	requireAssignable[func() (string, bool)](client.TermFeatures)
	for _, position := range []func() (bool, bool){
		pane.AtTop, pane.AtBottom, pane.AtLeft, pane.AtRight,
	} {
		requireAssignable[func() (bool, bool)](position)
	}

	requireAssignable[func() tmux.FormatValues](session.Formats)
	requireAssignable[func() tmux.FormatValues](window.Formats)
	requireAssignable[func() tmux.FormatValues](pane.Formats)
	requireAssignable[func() tmux.FormatValues](client.Formats)
	requireAssignable[func(string) (string, bool)](formats.Raw)
	requireAssignable[func() (tmux.SessionID, bool)](formats.SessionID)
	requireAssignable[func() (tmux.WindowID, bool)](formats.WindowID)
	requireAssignable[func() (tmux.PaneID, bool)](formats.PaneID)
	requireAssignable[func() (tmux.ClientName, bool)](formats.ClientName)
	requireAssignable[func() (string, bool)](formats.SessionName)
	requireAssignable[func() (string, bool)](formats.WindowName)
	requireAssignable[func() (string, bool)](formats.PaneTitle)
	requireAssignable[func() (string, bool)](formats.ClientTermFeatures)
	requireAssignable[func() (time.Time, bool)](formats.ClientCreated)
	requireAssignable[func() (bool, bool)](formats.PaneActive)
	requireAssignable[func() (tmux.Version, bool)](formats.Version)
}

func TestMaterializedFormatMethodsDoNotStutterReceiver(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if ok && general.Tok == token.TYPE {
				for _, specification := range general.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok || !map[string]bool{"Session": true, "Window": true, "Pane": true, "Client": true}[typeSpec.Name.Name] {
						continue
					}
					structure, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							if name.IsExported() && strings.HasPrefix(name.Name, typeSpec.Name.Name) {
								t.Errorf("%s exports receiver-stuttering field %s.%s", entry.Name(), typeSpec.Name.Name, name.Name)
							}
						}
					}
				}
			}
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 || !function.Name.IsExported() {
				continue
			}
			receiverType := function.Recv.List[0].Type
			if pointer, ok := receiverType.(*ast.StarExpr); ok {
				receiverType = pointer.X
			}
			receiver, ok := receiverType.(*ast.Ident)
			if !ok || !map[string]bool{"Session": true, "Window": true, "Pane": true, "Client": true}[receiver.Name] {
				continue
			}
			if strings.HasPrefix(function.Name.Name, receiver.Name) {
				t.Errorf("%s exports receiver-stuttering method %s.%s", entry.Name(), receiver.Name, function.Name.Name)
			}
		}
	}
}

func TestPanePositionAccessorsComeFromFormatGenerator(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("pane_position.go"); !os.IsNotExist(err) {
		t.Fatalf("pane_position.go still exists; stat error = %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "format_generated.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"AtTop": true, "AtBottom": true, "AtLeft": true, "AtRight": true}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		receiver, ok := function.Recv.List[0].Type.(*ast.Ident)
		if ok && receiver.Name == "Pane" {
			delete(want, function.Name.Name)
		}
	}
	for name := range want {
		t.Errorf("format_generated.go is missing Pane.%s", name)
	}
}

func TestObsoleteFilterBooleanParserIsAbsent(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "filter.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "filterTmuxBool" {
			t.Error("filter.go retains filterTmuxBool after typed boolean accessors")
		}
	}
}
