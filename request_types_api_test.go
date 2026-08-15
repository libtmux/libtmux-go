package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"testing"

	tmux "github.com/libtmux/libtmux-go"
)

// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:filter_expression:741088570e6e
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:target_client:9bd26a6f1edf
func TestRequestTargetAndFilterTypesCompile(_ *testing.T) {
	client := tmux.ClientName("client")
	pane := tmux.PaneID("%1")
	filter := tmux.TmuxFilter("#{session_attached}")

	_ = tmux.DisplayMessageRequest{TargetClient: client}
	_ = tmux.CopyModeRequest{SourcePane: pane}
	_ = tmux.ShowMessagesRequest{TargetClient: client}
	_ = tmux.RefreshClientRequest{TargetClient: client}
	_ = tmux.DetachClientRequest{TargetClient: client}
	_ = tmux.DetachAllClientsRequest{KeepClient: client}
	_ = tmux.SendKeysRequest{TargetClient: client}
	_ = tmux.ConfirmBeforeRequest{TargetClient: client}
	_ = tmux.CommandPromptRequest{TargetClient: client}
	_ = tmux.DisplayMenuRequest{TargetPane: pane, TargetClient: client}
	_ = tmux.DisplayPopupRequest{TargetClient: client}
	_ = tmux.RunShellRequest{TargetPane: pane}
	_ = tmux.IfShellRequest{TargetPane: pane}
	_ = tmux.ChooseTreeRequest{Filter: &filter}
}

func TestMigratedRequestFieldsHaveExactTypesAndNoObsoleteHelpers(t *testing.T) {
	t.Parallel()

	intType := reflect.TypeOf(int(0))
	clientNameType := reflect.TypeOf(tmux.ClientName(""))
	paneIDType := reflect.TypeOf(tmux.PaneID(""))
	valueFields := []requestFieldType{
		{tmux.NewSessionRequest{}, "Width", intType},
		{tmux.NewSessionRequest{}, "Height", intType},
		{tmux.ResizeWindowRequest{}, "Adjustment", intType},
		{tmux.ResizeWindowRequest{}, "Height", intType},
		{tmux.ResizeWindowRequest{}, "Width", intType},
		{tmux.ResizePaneRequest{}, "Adjustment", intType},
		{tmux.SendKeysRequest{}, "Repeat", intType},
		{tmux.SendKeysRequest{}, "TargetClient", clientNameType},
		{tmux.DisplayMessageRequest{}, "TargetClient", clientNameType},
		{tmux.CopyModeRequest{}, "SourcePane", paneIDType},
		{tmux.ShowMessagesRequest{}, "TargetClient", clientNameType},
		{tmux.RefreshClientRequest{}, "TargetClient", clientNameType},
		{tmux.DetachClientRequest{}, "TargetClient", clientNameType},
		{tmux.DetachAllClientsRequest{}, "KeepClient", clientNameType},
		{tmux.ConfirmBeforeRequest{}, "TargetClient", clientNameType},
		{tmux.CommandPromptRequest{}, "TargetClient", clientNameType},
		{tmux.DisplayMenuRequest{}, "TargetPane", paneIDType},
		{tmux.DisplayMenuRequest{}, "TargetClient", clientNameType},
		{tmux.DisplayPopupRequest{}, "TargetClient", clientNameType},
	}
	for _, field := range valueFields {
		assertRequestFieldType(t, field)
	}
	for _, field := range []requestFieldType{
		{tmux.UnbindKeyRequest{}, "Key", reflect.TypeOf((*string)(nil))},
		{tmux.ServerAccessRequest{}, "Allow", reflect.TypeOf((*string)(nil))},
		{tmux.ServerAccessRequest{}, "Deny", reflect.TypeOf((*string)(nil))},
		{tmux.PipePaneRequest{}, "Command", reflect.TypeOf((*string)(nil))},
	} {
		assertRequestFieldType(t, field)
	}

	obsoleteHelpers := map[string]struct{}{
		"externalPointer": {}, "filterPointer": {}, "intPointer": {},
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) < 4 || entry.Name()[len(entry.Name())-3:] != ".go" {
			continue
		}
		file, err := parser.ParseFile(fileSet, entry.Name(), nil, parser.AllErrors)
		if err != nil {
			t.Fatal(err)
		}
		if file.Name.Name != "tmux" && file.Name.Name != "tmux_test" {
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok {
				if _, found := obsoleteHelpers[function.Name.Name]; found {
					t.Errorf("%s declares obsolete pointer helper %s", entry.Name(), function.Name.Name)
				}
			}
		}
	}
}

type requestFieldType struct {
	request any
	field   string
	want    reflect.Type
}

func assertRequestFieldType(t *testing.T, field requestFieldType) {
	t.Helper()
	requestType := reflect.TypeOf(field.request)
	actual, found := requestType.FieldByName(field.field)
	if !found {
		t.Errorf("%s is missing %s", requestType, field.field)
		return
	}
	if actual.Type != field.want {
		t.Errorf("%s.%s type = %s, want %s", requestType, field.field, actual.Type, field.want)
	}
}
