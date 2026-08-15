package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/doc/comment"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedOptionDocumentationIsCallerComplete(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}
	generated, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("generateOptions() error = %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "option_generated.go", generated, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated options: %v", err)
	}
	docs := generatedDocs(file)

	for _, surface := range generatedSurfaces(spec) {
		doc := docs.types[surface.TypeName]
		if !strings.HasPrefix(doc, surface.TypeName) {
			t.Errorf("%s documentation must begin with its identifier: %s", surface.TypeName, doc)
		}
		for _, want := range []string{
			surface.TypeName,
			"zero value",
			"immutable point-in-time",
			"may become stale",
			"[OptionValue.Get]",
			"[OptionValue.Origin]",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s documentation is missing %q: %s", surface.TypeName, want, doc)
			}
		}
	}
	for _, want := range []string{
		"[Server.Options]",
		"[Session.Options]",
		"[GlobalSessionScope.Options]",
		"[Window.Options]",
		"[GlobalWindowScope.Options]",
		"[Pane.Options]",
		"[GlobalSessionScope.Hooks]",
		"[Session.Hooks]",
		"[Window.Hooks]",
		"[GlobalWindowScope.Hooks]",
		"[Pane.Hooks]",
	} {
		if !strings.Contains(strings.Join(mapsValues(docs.types), "\n"), want) {
			t.Errorf("value-set documentation is missing acquisition link %q", want)
		}
	}

	for _, surface := range generatedSurfaces(spec) {
		for _, entry := range surface.Entries {
			key := surface.TypeName + "." + entry.GoName
			doc := docs.methods[key]
			if !strings.HasPrefix(doc, entry.GoName) {
				t.Errorf("%s.%s documentation must begin with its identifier: %s", surface.TypeName, entry.GoName, doc)
			}
			wantLead := entry.GoName + " returns the " + strconv.Quote(entry.Name) + " " + surface.Noun +
				" value as [OptionValue] with Go value shape OptionValue[" + entry.GoType + "]."
			if !strings.HasPrefix(doc, wantLead) {
				t.Errorf("%s documentation lead = %q, want prefix %q", key, doc, wantLead)
			}
			wantVariants := "Its scope-specific minimum tmux version and supported variants are " +
				documentedVariants(variantsForScope(entry, surface.Scope)) + "."
			if !strings.Contains(doc, wantVariants) {
				t.Errorf("%s documentation is missing exact variants %q: %s", key, wantVariants, doc)
			}
			signature := docs.signatures[key]
			wantResult := "OptionValue[" + entry.GoType + "]"
			if !slices.Equal(signature.results, []string{wantResult}) {
				t.Errorf("%s results = %v, want [%s]", key, signature.results, wantResult)
			}
			if len(signature.parameterTypes) != 0 {
				t.Errorf("%s parameter types = %v, want none", key, signature.parameterTypes)
			}
			for _, want := range []string{
				entry.GoName,
				strconv.Quote(entry.Name),
				"[OptionValue]",
				"OptionValue[" + entry.GoType + "]",
				"does not query tmux",
				"tmux",
				"scope",
				"style option",
			} {
				if !strings.Contains(doc, want) {
					t.Errorf("%s.%s documentation is missing %q: %s", surface.TypeName, entry.GoName, want, doc)
				}
			}
			if entry.Array && !strings.Contains(doc, "including gaps") {
				t.Errorf("%s.%s documentation omits sparse-array semantics: %s", surface.TypeName, entry.GoName, doc)
			}
			for _, fallback := range rawFallbacksForSurface(surface.TypeName) {
				if !strings.Contains(doc, fallback) {
					t.Errorf("%s.%s documentation is missing raw fallback %q: %s", surface.TypeName, entry.GoName, fallback, doc)
				}
			}
			for _, receiver := range surface.Setters {
				want := "[" + receiver + "." + setterName(entry) + "]"
				if !strings.Contains(doc, want) {
					t.Errorf("%s.%s documentation is missing setter link %q: %s", surface.TypeName, entry.GoName, want, doc)
				}
			}
			if len(surface.Setters) == 0 && strings.Contains(doc, "Set it with") {
				t.Errorf("%s.%s documentation claims a typed setter: %s", surface.TypeName, entry.GoName, doc)
			}
		}
	}
	for _, entry := range spec.Options {
		method := setterName(entry)
		for _, receiver := range setterReceivers(entry) {
			key := receiver + "." + method
			doc := docs.methods[key]
			if !strings.HasPrefix(doc, method) {
				t.Errorf("%s.%s documentation must begin with its identifier: %s", receiver, method, doc)
			}
			scope := setterScope(receiver)
			if !strings.Contains(doc, strconv.Quote(entry.Name)+" "+scope+" option") {
				t.Errorf("%s documentation does not identify exact %s scope: %s", key, scope, doc)
			}
			signature := docs.signatures[key]
			if got := signature.parameters["value"]; got != entry.GoType {
				t.Errorf("%s value parameter = %q, want %q", key, got, entry.GoType)
			}
			if want := []string{"context.Context", entry.GoType}; !slices.Equal(signature.parameterTypes, want) {
				t.Errorf("%s parameter types = %v, want %v", key, signature.parameterTypes, want)
			}
			valuesType, valuesSource := setterValueSurface(receiver)
			wants := []string{
				strconv.Quote(entry.Name),
				entry.GoType,
				"tmux " + variantsForScope(entry, scope)[0].Since,
				"[" + receiver + ".SetOption]",
				"[" + valuesType + "." + entry.GoName + "]",
				valuesSource,
			}
			if entry.Array {
				wants = append(wants, "replacement", "not atomic", "serialize", "UnsetOption")
			}
			for _, want := range wants {
				if !strings.Contains(doc, want) {
					t.Errorf("%s.%s documentation is missing %q: %s", receiver, method, want, doc)
				}
			}
		}
	}

	for _, test := range []struct {
		method string
		wants  []string
	}{
		{method: "PaneOptionValues.PaneActiveBorderStyle", wants: []string{"tmux 3.7", "style option"}},
		{method: "PaneOptionValues.PaneBorderFormat", wants: []string{"tmux 3.3"}},
		{method: "PaneOptionValues.PaneBorderStyle", wants: []string{"tmux 3.7", "style option"}},
		{method: "WindowOptionValues.PaneActiveBorderStyle", wants: []string{"tmux 3.2a"}},
		{method: "WindowOptionValues.PaneBorderFormat", wants: []string{"tmux 3.2a"}},
		{method: "WindowOptionValues.PaneBorderStyle", wants: []string{"tmux 3.2a"}},
		{method: "WindowOptionValues.AllowPassthrough", wants: []string{"FLAG", "CHOICE", "off", "on", "all", "tmux 3.3", "tmux 3.4"}},
		{method: "SessionHookValues.WindowLinked", wants: []string{"tmux 3.3", "including gaps"}},
		{method: "ServerHookValues.WindowLinked", wants: []string{"tmux 3.3", "including gaps"}},
	} {
		doc := docs.methods[test.method]
		for _, want := range test.wants {
			if !strings.Contains(doc, want) {
				t.Errorf("%s documentation is missing %q: %s", test.method, want, doc)
			}
		}
	}

	kindDoc := docs.types["ControlNotificationKind"]
	if !strings.HasPrefix(kindDoc, "ControlNotificationKind") {
		t.Errorf("ControlNotificationKind documentation must begin with its identifier: %s", kindDoc)
	}
	for _, want := range []string{"ControlNotificationKind", "zero value", "unknown", "[ParseControlNotification]"} {
		if !strings.Contains(kindDoc, want) {
			t.Errorf("ControlNotificationKind documentation is missing %q: %s", want, kindDoc)
		}
	}
	minimumDoc := docs.methods["ControlNotificationKind.MinimumVersion"]
	if !strings.HasPrefix(minimumDoc, "MinimumVersion") {
		t.Errorf("ControlNotificationKind.MinimumVersion documentation must begin with its identifier: %s", minimumDoc)
	}
	for _, want := range []string{"[Version]", "ok == false", "unknown"} {
		if !strings.Contains(minimumDoc, want) {
			t.Errorf("ControlNotificationKind.MinimumVersion documentation is missing %q: %s", want, minimumDoc)
		}
	}
	for _, entry := range spec.Notifications {
		constant := "ControlNotification" + entry.GoName
		doc := docs.values[constant]
		if !strings.HasPrefix(doc, constant) {
			t.Errorf("%s documentation must begin with its identifier: %s", constant, doc)
		}
		for _, want := range []string{"ControlNotification" + entry.GoName, entry.WireName, "tmux " + entry.Since, "ParseControlNotification"} {
			if !strings.Contains(doc, want) {
				t.Errorf("ControlNotification%s documentation is missing %q: %s", entry.GoName, want, doc)
			}
		}
		if strings.Contains(doc, "[ParseControlNotification]") {
			t.Errorf("%s documentation contains a declaration-context link: %s", constant, doc)
		}
		for _, label := range entry.PrefixLabels {
			if !strings.Contains(doc, "<"+label+">") {
				t.Errorf("%s documentation is missing prefix label %q: %s", constant, label, doc)
			}
		}
		switch entry.Tail {
		case "required":
			if !strings.Contains(doc, "<"+entry.TailLabel+">") {
				t.Errorf("%s documentation is missing required tail label %q: %s", constant, entry.TailLabel, doc)
			}
		case "optional":
			if !strings.Contains(doc, "or "+entry.WireName+" <"+entry.TailLabel+">") {
				t.Errorf("%s documentation is missing optional-tail grammar: %s", constant, doc)
			}
		case "colon":
			for _, want := range []string{
				"zero or more <reserved argument>",
				": <" + entry.TailLabel + ">",
				"prefix arguments, then zero or more reserved arguments, then the " + entry.TailLabel + " tail",
			} {
				if !strings.Contains(doc, want) {
					t.Errorf("%s documentation is missing colon-tail contract %q: %s", constant, want, doc)
				}
			}
		}
		if entry.AllowEmptyTail && !strings.Contains(doc, "tail may be empty") {
			t.Errorf("%s documentation omits empty-tail semantics: %s", constant, doc)
		}
	}
}

func setterScope(receiver string) string {
	return map[string]string{
		"Server": "server", "Session": "session", "Window": "window", "Pane": "pane",
		"GlobalSessionScope": "session", "GlobalWindowScope": "window",
	}[receiver]
}

func rawFallbacksForSurface(typeName string) []string {
	return map[string][]string{
		"ServerOptionValues":  {"[Server.RawOption]"},
		"SessionOptionValues": {"[Session.RawOption]", "[GlobalSessionScope.RawOption]"},
		"WindowOptionValues":  {"[Window.RawOption]", "[GlobalWindowScope.RawOption]"},
		"PaneOptionValues":    {"[Pane.RawOption]"},
		"ServerHookValues":    {"[GlobalSessionScope.RawHook]"},
		"SessionHookValues":   {"[Session.RawHook]"},
		"WindowHookValues":    {"[Window.RawHook]", "[GlobalWindowScope.RawHook]"},
		"PaneHookValues":      {"[Pane.RawHook]"},
	}[typeName]
}

func TestGeneratedNotificationGrammarRendersLiteralMetavariables(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}
	generated, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("generateOptions() error = %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "option_generated.go", generated, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated options: %v", err)
	}
	docs := generatedDocs(file)
	for _, entry := range spec.Notifications {
		doc := docs.values["ControlNotification"+entry.GoName]
		rendered := string(new(comment.Printer).HTML(new(comment.Parser).Parse(doc)))
		for _, label := range entry.PrefixLabels {
			want := "&lt;" + label + "&gt;"
			if !strings.Contains(rendered, want) {
				t.Errorf("%s rendered grammar is missing literal prefix metavariable %q: %s", entry.WireName, want, rendered)
			}
		}
		if entry.Tail != "none" {
			want := "&lt;" + entry.TailLabel + "&gt;"
			if !strings.Contains(rendered, want) {
				t.Errorf("%s rendered grammar is missing literal tail metavariable %q: %s", entry.WireName, want, rendered)
			}
		}
		if entry.Tail == "colon" && !strings.Contains(rendered, "zero or more &lt;reserved argument&gt;") {
			t.Errorf("%s rendered grammar is missing zero-or-more reserved arguments: %s", entry.WireName, rendered)
		}
	}
}

type generatedDocIndex struct {
	types      map[string]string
	values     map[string]string
	methods    map[string]string
	signatures map[string]generatedMethodSignature
}

type generatedMethodSignature struct {
	parameters     map[string]string
	parameterTypes []string
	results        []string
}

func generatedDocs(file *ast.File) generatedDocIndex {
	result := generatedDocIndex{
		types:      make(map[string]string),
		values:     make(map[string]string),
		methods:    make(map[string]string),
		signatures: make(map[string]generatedMethodSignature),
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					result.types[specification.Name.Name] = generatedDocComment(declaration.Doc, specification.Doc)
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						result.values[name.Name] = generatedDocComment(declaration.Doc, specification.Doc)
					}
				}
			}
		case *ast.FuncDecl:
			if declaration.Recv == nil || len(declaration.Recv.List) != 1 {
				continue
			}
			receiver, ok := declaration.Recv.List[0].Type.(*ast.Ident)
			if !ok {
				continue
			}
			key := receiver.Name + "." + declaration.Name.Name
			result.methods[key] = generatedDocComment(declaration.Doc)
			signature := generatedMethodSignature{parameters: make(map[string]string)}
			for _, field := range declaration.Type.Params.List {
				for range max(1, len(field.Names)) {
					signature.parameterTypes = append(signature.parameterTypes, types.ExprString(field.Type))
				}
				for _, name := range field.Names {
					signature.parameters[name.Name] = types.ExprString(field.Type)
				}
			}
			if declaration.Type.Results != nil {
				for _, field := range declaration.Type.Results.List {
					for range max(1, len(field.Names)) {
						signature.results = append(signature.results, types.ExprString(field.Type))
					}
				}
			}
			result.signatures[key] = signature
		}
	}
	return result
}

func generatedDocComment(groups ...*ast.CommentGroup) string {
	for _, group := range groups {
		if group == nil {
			continue
		}
		lines := make([]string, 0, len(group.List))
		for _, comment := range group.List {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func mapsValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

// pinnedSourceContractDigest locks the normalized options-table.c contract from
// tmux 3.2a, 3.3a, 3.4, 3.5, 3.6, and 3.7b.
const pinnedSourceContractDigest = "a5a8f2b5ff77f67b043df7756dee8c5c85f0a60bf850834d3edaa5d994218622"

var controlOnlyNotifications = []string{
	"client-detached-control", "client-session-changed-control", "config-error",
	"continue-control", "exit-control", "extended-output", "layout-change",
	"message-control", "output", "paste-buffer-changed", "paste-buffer-deleted",
	"pause-control", "session-changed-control", "session-renamed-control",
	"sessions-changed", "subscription-changed", "unlinked-window-add",
	"unlinked-window-close", "unlinked-window-renamed", "window-add",
	"window-close", "window-renamed-control",
}

func validSpec() optionSpec {
	return optionSpec{
		Schema:         2,
		FeatureFloor:   "3.2a",
		FeatureCeiling: "3.7",
		SourceTag:      "3.7b",
		Aliases: []aliasSpec{
			{Name: "clock-mode-color", Canonical: "clock-mode-colour"},
		},
		Options: []entrySpec{
			{
				Name:   "allow-passthrough",
				GoName: "AllowPassthrough",
				GoType: "AllowPassthrough",
				Variants: []variantSpec{
					{Since: "3.3", TmuxKind: "FLAG", Scopes: []string{"window", "pane"}},
					{
						Since:    "3.4",
						TmuxKind: "CHOICE",
						Scopes:   []string{"window", "pane"},
						Choices:  []string{"off", "on", "all"},
					},
				},
			},
			{
				Name:     "clock-mode-colour",
				GoName:   "ClockModeColour",
				GoType:   "string",
				Variants: []variantSpec{{Since: "3.2a", TmuxKind: "COLOUR", Scopes: []string{"window"}}},
			},
			{
				Name:     "command-alias",
				GoName:   "CommandAlias",
				GoType:   "SparseArray[string]",
				Array:    true,
				Variants: []variantSpec{{Since: "3.2a", TmuxKind: "STRING", Scopes: []string{"server"}}},
			},
			{
				Name:     "status-style",
				GoName:   "StatusStyle",
				GoType:   "string",
				Style:    true,
				Variants: []variantSpec{{Since: "3.2a", TmuxKind: "STRING", Scopes: []string{"session"}}},
			},
		},
		Hooks: []entrySpec{
			{
				Name:   "window-linked",
				GoName: "WindowLinked",
				GoType: "SparseArray[string]",
				Array:  true,
				Variants: []variantSpec{
					{Since: "3.2a", TmuxKind: "COMMAND", Scopes: []string{"window"}},
					{Since: "3.3", TmuxKind: "COMMAND", Scopes: []string{"session"}},
				},
			},
		},
		Notifications: []notificationSpec{
			{
				PythonField:     "continue_control",
				WireName:        "%continue",
				GoName:          "Continue",
				Since:           "3.2a",
				PrefixArguments: 1,
				PrefixLabels:    []string{"pane ID"},
				Tail:            "none",
			},
			{
				PythonField:     "message_control",
				WireName:        "%message",
				GoName:          "Message",
				Since:           "3.4",
				PrefixArguments: 0,
				Tail:            "required",
				TailLabel:       "message",
				AllowEmptyTail:  true,
			},
		},
	}
}

func TestGenerateOptionsIsDeterministicAndEmitsConcreteSurfaces(t *testing.T) {
	t.Parallel()

	spec := validSpec()
	first, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("generateOptions() error = %v", err)
	}
	second, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("second generateOptions() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generateOptions() output changed between identical calls")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "option_generated.go", first, parser.AllErrors); err != nil {
		t.Fatalf("generated Go does not parse: %v", err)
	}

	output := string(first)
	for _, declaration := range []string{
		"const (\n\tgeneratedOptionSpecSchema = 2\n\tgeneratedOptionSourceTag  = \"3.7b\"\n)",
		"type ControlNotificationKind string",
		"ControlNotificationContinue ControlNotificationKind = \"%continue\"",
		"ControlNotificationMessage ControlNotificationKind = \"%message\"",
		"var generatedControlNotificationDefinitions = [...]generatedControlNotificationDefinition",
		"func (k ControlNotificationKind) MinimumVersion() (Version, bool)",
		"type ServerOptionValues struct",
		"type SessionOptionValues struct",
		"type WindowOptionValues struct",
		"type PaneOptionValues struct",
		"type ServerHookValues struct",
		"// ServerHookValues is an immutable point-in-time view of known global session-scope hook values.",
		"type SessionHookValues struct",
		"type WindowHookValues struct",
		"type PaneHookValues struct",
		"func (v ServerOptionValues) CommandAlias() OptionValue[SparseArray[string]]",
		"func (v SessionOptionValues) StatusStyle() OptionValue[string]",
		"func (v WindowOptionValues) AllowPassthrough() OptionValue[AllowPassthrough]",
		"func (v PaneOptionValues) AllowPassthrough() OptionValue[AllowPassthrough]",
		"func (v SessionHookValues) WindowLinked() OptionValue[SparseArray[string]]",
		"func (v WindowHookValues) WindowLinked() OptionValue[SparseArray[string]]",
		"func newServerOptionValues(values []decodedOptionValue) ServerOptionValues",
		"result.commandAlias = optionValueFromDecoded(value.sparseStringValue, value.origin)",
		"func newSessionOptionValues(values []decodedOptionValue) SessionOptionValues",
		"result.statusStyle = optionValueFromDecoded(value.stringValue, value.origin)",
		"func newWindowOptionValues(values []decodedOptionValue) WindowOptionValues",
		"result.allowPassthrough = optionValueFromDecoded(AllowPassthrough(value.stringValue), value.origin)",
		"func newPaneOptionValues(values []decodedOptionValue) PaneOptionValues",
		"func newServerHookValues(values []decodedOptionValue) ServerHookValues",
		"func newSessionHookValues(values []decodedOptionValue) SessionHookValues",
		"result.windowLinked = optionValueFromDecoded(value.sparseStringValue, value.origin)",
		"func newWindowHookValues(values []decodedOptionValue) WindowHookValues",
		"func newPaneHookValues(values []decodedOptionValue) PaneHookValues",
		"var generatedOptionDefinitions = [...]generatedOptionDefinition",
		"var generatedHookDefinitions = [...]generatedOptionDefinition",
		"valueKind: generatedOptionValueKindString",
		"valueKind: generatedOptionValueKindSparseString",
	} {
		if !strings.Contains(output, declaration) {
			t.Errorf("generated output is missing %q", declaration)
		}
	}
	if strings.Contains(output, "ClockModeColor()") {
		t.Fatal("American alias generated a duplicate accessor")
	}
}

func TestGeneratedChoiceAndSetterInventory(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}
	generated, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("generateOptions() error = %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "option_generated.go", generated, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse generated options: %v", err)
	}

	choiceTypes := make(map[string]bool)
	choiceConstants := 0
	choiceGetters := 0
	scalarSetters := make(map[string]map[string]bool)
	arraySetters := make(map[string]map[string]bool)
	arraySetterOrder := make([]string, 0, 12)
	methods := make(map[string]bool)
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					if identifier, ok := specification.Type.(*ast.Ident); ok && identifier.Name == "string" {
						for _, entry := range spec.Options {
							if entry.GoType == entry.GoName && specification.Name.Name == entry.GoName {
								choiceTypes[entry.GoName] = true
							}
						}
					}
				case *ast.ValueSpec:
					identifier, ok := specification.Type.(*ast.Ident)
					if ok && choiceTypes[identifier.Name] {
						choiceConstants += len(specification.Names)
					}
				}
			}
		case *ast.FuncDecl:
			if declaration.Recv == nil || len(declaration.Recv.List) != 1 {
				continue
			}
			receiver, ok := declaration.Recv.List[0].Type.(*ast.Ident)
			if !ok {
				continue
			}
			key := receiver.Name + "." + declaration.Name.Name
			if methods[key] {
				t.Errorf("duplicate generated method %s", key)
			}
			methods[key] = true
			if declaration.Name.Name == "String" || declaration.Name.Name == "Valid" {
				continue
			}
			if len(declaration.Type.Results.List) == 1 {
				if indexed, ok := declaration.Type.Results.List[0].Type.(*ast.IndexExpr); ok {
					container, containerOK := indexed.X.(*ast.Ident)
					valueType, valueOK := indexed.Index.(*ast.Ident)
					if containerOK && valueOK && container.Name == "OptionValue" && choiceTypes[valueType.Name] {
						choiceGetters++
					}
				}
			}
			if isOptionSetterReceiver(receiver.Name) &&
				strings.HasPrefix(declaration.Name.Name, "Set") &&
				declaration.Name.Name != "SetOption" {
				wantReceiverName := map[string]string{
					"Server": "s", "Session": "s", "Window": "w", "Pane": "p",
					"GlobalSessionScope": "s", "GlobalWindowScope": "s",
				}[receiver.Name]
				if got := declaration.Recv.List[0].Names[0].Name; got != wantReceiverName {
					t.Errorf(
						"generated %s receiver name = %q, want %q",
						key,
						got,
						wantReceiverName,
					)
				}
				setters := scalarSetters
				if isArraySetterSignature(declaration) {
					setters = arraySetters
					arraySetterOrder = append(arraySetterOrder, key)
				}
				if setters[receiver.Name] == nil {
					setters[receiver.Name] = make(map[string]bool)
				}
				setters[receiver.Name][declaration.Name.Name] = true
			}
		}
	}

	if got := len(choiceTypes); got != 33 {
		t.Errorf("generated choice types = %d, want 33", got)
	}
	if choiceConstants != 136 {
		t.Errorf("generated choice constants = %d, want 136", choiceConstants)
	}
	if choiceGetters != 36 {
		t.Errorf("generated choice getter occurrences = %d, want 36", choiceGetters)
	}
	setterCount := 0
	for _, receiverSet := range scalarSetters {
		setterCount += len(receiverSet)
	}
	if setterCount != 288 {
		t.Errorf("generated scalar setters = %d, want 288", setterCount)
	}
	arrayDefinitionCount := 0
	arrayDefinitionScopePairs := 0
	for _, entry := range spec.Options {
		if !entry.Array {
			continue
		}
		arrayDefinitionCount++
		scopes := make(map[string]bool)
		for _, variant := range entry.Variants {
			for _, scope := range variant.Scopes {
				scopes[scope] = true
			}
		}
		arrayDefinitionScopePairs += len(scopes)
	}
	if arrayDefinitionCount != 8 {
		t.Errorf("array definitions = %d, want 8", arrayDefinitionCount)
	}
	if arrayDefinitionScopePairs != 9 {
		t.Errorf("array definition/scope pairs = %d, want 9", arrayDefinitionScopePairs)
	}
	arraySetterCount := 0
	for _, receiverSet := range arraySetters {
		arraySetterCount += len(receiverSet)
	}
	if arraySetterCount != 12 {
		t.Errorf("generated array setters = %d, want 12", arraySetterCount)
	}
	if setterCount+arraySetterCount != 300 {
		t.Errorf("generated setters = %d, want 300", setterCount+arraySetterCount)
	}
	wantArraySetterOrder := []string{
		"Server.SetCodepointWidths",
		"Server.SetCommandAlias",
		"Window.SetPaneColours",
		"GlobalWindowScope.SetPaneColours",
		"Pane.SetPaneColours",
		"Session.SetStatusFormat",
		"GlobalSessionScope.SetStatusFormat",
		"Server.SetTerminalFeatures",
		"Server.SetTerminalOverrides",
		"Session.SetUpdateEnvironment",
		"GlobalSessionScope.SetUpdateEnvironment",
		"Server.SetUserKeys",
	}
	if !slices.Equal(arraySetterOrder, wantArraySetterOrder) {
		t.Errorf("generated array setter order = %v, want %v", arraySetterOrder, wantArraySetterOrder)
	}
	for _, want := range []string{
		"ExtendedKeysFormatCSIU",
		"ExtendedKeysFormatXTerm",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated output is missing %s", want)
		}
	}
	for _, forbidden := range []string{"SetSetClipboard", "SetSetTitles", "SetSetTitlesString"} {
		if strings.Contains(string(generated), forbidden) {
			t.Errorf("generated output contains stuttering setter %s", forbidden)
		}
	}
	for _, want := range []string{"Server.SetClipboard", "Session.SetTitles", "Session.SetTitlesString"} {
		if !methods[want] {
			t.Errorf("generated output is missing %s", want)
		}
	}
	for _, entry := range spec.Options {
		setterName := "Set" + entry.GoName
		if strings.HasPrefix(entry.GoName, "Set") {
			setterName = entry.GoName
		}
		for _, receiver := range []string{"Server", "Session", "Window", "Pane", "GlobalSessionScope", "GlobalWindowScope"} {
			want := optionEntrySupportsSetterReceiver(entry, receiver)
			if entry.Array {
				if got := arraySetters[receiver][setterName]; got != want {
					t.Errorf("array %s.%s present = %t, want %t for %q", receiver, setterName, got, want, entry.Name)
				}
				continue
			}
			if got := scalarSetters[receiver][setterName]; got != want {
				t.Errorf("scalar %s.%s present = %t, want %t for %q", receiver, setterName, got, want, entry.Name)
			}
		}
	}
}

func isOptionSetterReceiver(receiver string) bool {
	return slices.Contains([]string{
		"Server", "Session", "Window", "Pane",
		"GlobalSessionScope", "GlobalWindowScope",
	}, receiver)
}

func isArraySetterSignature(declaration *ast.FuncDecl) bool {
	parameters := declaration.Type.Params.List
	results := declaration.Type.Results.List
	if len(parameters) != 2 || len(results) != 2 {
		return false
	}
	contextType, contextOK := parameters[0].Type.(*ast.SelectorExpr)
	contextPackage, packageOK := contextType.X.(*ast.Ident)
	arrayType, arrayOK := parameters[1].Type.(*ast.IndexExpr)
	arrayName, arrayNameOK := arrayType.X.(*ast.Ident)
	arrayValue, arrayValueOK := arrayType.Index.(*ast.Ident)
	resultType, resultOK := results[0].Type.(*ast.Ident)
	errorType, errorOK := results[1].Type.(*ast.Ident)
	return contextOK && packageOK && contextPackage.Name == "context" &&
		contextType.Sel.Name == "Context" && arrayOK && arrayNameOK &&
		arrayName.Name == "SparseArray" && arrayValueOK && arrayValue.Name == "string" &&
		resultOK && resultType.Name == "SetArrayResult" && errorOK && errorType.Name == "error"
}

func optionEntrySupportsSetterReceiver(entry entrySpec, receiver string) bool {
	scope := map[string]string{
		"Server": "server", "Session": "session", "Window": "window",
		"Pane": "pane", "GlobalSessionScope": "session",
		"GlobalWindowScope": "window",
	}[receiver]
	for _, variant := range entry.Variants {
		if slices.Contains(variant.Scopes, scope) {
			return true
		}
	}
	return false
}

func TestCheckedInGeneratedOptionsMatchSpec(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}
	want, err := generateOptions(spec)
	if err != nil {
		t.Fatalf("generateOptions() error = %v", err)
	}
	path := filepath.Join("..", "..", "..", "option_generated.go")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in output: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("option_generated.go is stale; run go generate ./...")
	}
}

func TestValidateOptionSpecRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*optionSpec)
	}{
		{name: "schema", mutate: func(spec *optionSpec) { spec.Schema = 1 }},
		{name: "floor", mutate: func(spec *optionSpec) { spec.FeatureFloor = "next" }},
		{name: "ceiling before floor", mutate: func(spec *optionSpec) { spec.FeatureCeiling = "3.1" }},
		{name: "source before ceiling", mutate: func(spec *optionSpec) { spec.SourceTag = "3.6" }},
		{name: "unsorted aliases", mutate: func(spec *optionSpec) {
			spec.Aliases = append(spec.Aliases, aliasSpec{Name: "backspace-key", Canonical: "clock-mode-colour"})
		}},
		{name: "alias target", mutate: func(spec *optionSpec) { spec.Aliases[0].Canonical = "missing" }},
		{name: "option casing", mutate: func(spec *optionSpec) { spec.Options[0].Name = "Allow-Passthrough" }},
		{name: "option ordering", mutate: func(spec *optionSpec) { spec.Options[0], spec.Options[1] = spec.Options[1], spec.Options[0] }},
		{name: "Go name", mutate: func(spec *optionSpec) { spec.Options[0].GoName = "allowPassthrough" }},
		{name: "Go name collision", mutate: func(spec *optionSpec) { spec.Options[1].GoName = spec.Options[0].GoName }},
		{name: "missing variant", mutate: func(spec *optionSpec) { spec.Options[0].Variants = nil }},
		{name: "variant ordering", mutate: func(spec *optionSpec) { spec.Options[0].Variants[1].Since = "3.2a" }},
		{name: "unknown kind", mutate: func(spec *optionSpec) { spec.Options[0].Variants[0].TmuxKind = "MYSTERY" }},
		{name: "unknown scope", mutate: func(spec *optionSpec) { spec.Options[0].Variants[0].Scopes[0] = "client" }},
		{name: "scope ordering", mutate: func(spec *optionSpec) { spec.Options[0].Variants[0].Scopes = []string{"pane", "window"} }},
		{name: "duplicate scope", mutate: func(spec *optionSpec) { spec.Options[0].Variants[0].Scopes = []string{"window", "window"} }},
		{name: "choice without values", mutate: func(spec *optionSpec) { spec.Options[0].Variants[1].Choices = nil }},
		{name: "choices on flag", mutate: func(spec *optionSpec) { spec.Options[0].Variants[0].Choices = []string{"off"} }},
		{name: "duplicate choice", mutate: func(spec *optionSpec) { spec.Options[0].Variants[1].Choices = []string{"off", "off"} }},
		{name: "choice type", mutate: func(spec *optionSpec) { spec.Options[0].GoType = "string" }},
		{name: "choice name on scalar", mutate: func(spec *optionSpec) {
			spec.Options[1].ChoiceNames = map[string]string{"blue": "Blue"}
		}},
		{name: "choice name unknown value", mutate: func(spec *optionSpec) {
			spec.Options[0].ChoiceNames = map[string]string{"future": "Future"}
		}},
		{name: "choice name invalid suffix", mutate: func(spec *optionSpec) {
			spec.Options[0].ChoiceNames = map[string]string{"all": "all"}
		}},
		{name: "choice constant collision", mutate: func(spec *optionSpec) {
			spec.Options[0].ChoiceNames = map[string]string{"off": "Same", "on": "Same"}
		}},
		{name: "setter collision", mutate: func(spec *optionSpec) {
			spec.Options[0].GoName = "Option"
			spec.Options[0].GoType = "Option"
		}},
		{name: "number type", mutate: func(spec *optionSpec) {
			spec.Options[1].Variants[0].TmuxKind = "NUMBER"
		}},
		{name: "array type", mutate: func(spec *optionSpec) { spec.Options[2].GoType = "string" }},
		{name: "style kind", mutate: func(spec *optionSpec) { spec.Options[3].Variants[0].TmuxKind = "COLOUR" }},
		{name: "hook scalar", mutate: func(spec *optionSpec) { spec.Hooks[0].Array = false }},
		{name: "hook kind", mutate: func(spec *optionSpec) { spec.Hooks[0].Variants[0].TmuxKind = "STRING" }},
		{name: "missing notifications", mutate: func(spec *optionSpec) { spec.Notifications = nil }},
		{name: "notification ordering", mutate: func(spec *optionSpec) {
			spec.Notifications[0], spec.Notifications[1] = spec.Notifications[1], spec.Notifications[0]
		}},
		{name: "notification Python field", mutate: func(spec *optionSpec) {
			spec.Notifications[0].PythonField = "Continue-Control"
		}},
		{name: "notification Python field collision", mutate: func(spec *optionSpec) {
			spec.Notifications[1].PythonField = spec.Notifications[0].PythonField
		}},
		{name: "notification wire name", mutate: func(spec *optionSpec) {
			spec.Notifications[0].WireName = "continue"
		}},
		{name: "notification wire collision", mutate: func(spec *optionSpec) {
			spec.Notifications[1].WireName = spec.Notifications[0].WireName
		}},
		{name: "notification Go name", mutate: func(spec *optionSpec) {
			spec.Notifications[0].GoName = "continue"
		}},
		{name: "notification Go collision", mutate: func(spec *optionSpec) {
			spec.Notifications[1].GoName = spec.Notifications[0].GoName
		}},
		{name: "notification version", mutate: func(spec *optionSpec) {
			spec.Notifications[0].Since = "3.8"
		}},
		{name: "notification prefix", mutate: func(spec *optionSpec) {
			spec.Notifications[0].PrefixArguments = -1
		}},
		{name: "notification prefix labels", mutate: func(spec *optionSpec) {
			spec.Notifications[0].PrefixLabels = nil
		}},
		{name: "notification blank prefix label", mutate: func(spec *optionSpec) {
			spec.Notifications[0].PrefixLabels[0] = " "
		}},
		{name: "notification tail", mutate: func(spec *optionSpec) {
			spec.Notifications[0].Tail = "mystery"
		}},
		{name: "notification missing required tail label", mutate: func(spec *optionSpec) {
			spec.Notifications[1].TailLabel = ""
		}},
		{name: "notification tail label without tail", mutate: func(spec *optionSpec) {
			spec.Notifications[0].TailLabel = "pane output"
		}},
		{name: "empty fixed tail", mutate: func(spec *optionSpec) {
			spec.Notifications[0].AllowEmptyTail = true
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := validSpec()
			test.mutate(&spec)
			if err := validateOptionSpec(spec); err == nil {
				t.Fatal("validateOptionSpec() error = nil, want validation failure")
			}
		})
	}
}

func TestScalarSetterCollisionReservationsMatchExistingReceivers(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Height", "Width"} {
		paneEntry := entrySpec{
			Name:   strings.ToLower(name),
			GoName: name,
			GoType: "bool",
			Variants: []variantSpec{{
				Since: "3.2a", TmuxKind: "FLAG", Scopes: []string{"pane"},
			}},
		}
		if err := validateSetterMethods([]entrySpec{paneEntry}); err == nil {
			t.Errorf("Pane.Set%s collision was accepted", name)
		}

		windowEntry := paneEntry
		windowEntry.Variants = []variantSpec{{
			Since: "3.2a", TmuxKind: "FLAG", Scopes: []string{"window"},
		}}
		if err := validateSetterMethods([]entrySpec{windowEntry}); err != nil {
			t.Errorf("Window.Set%s candidate was rejected: %v", name, err)
		}
	}
}

func TestSetterCollisionValidationIncludesArrays(t *testing.T) {
	t.Parallel()

	arrayEntry := entrySpec{
		Name: "option", GoName: "Option", GoType: "SparseArray[string]", Array: true,
		Variants: []variantSpec{{
			Since: "3.2a", TmuxKind: "STRING", Scopes: []string{"server"},
		}},
	}
	if err := validateSetterMethods([]entrySpec{arrayEntry}); err == nil {
		t.Fatal("Server.SetOption array collision was accepted")
	}

	arrayEntry.Name = "command-alias"
	arrayEntry.GoName = "CommandAlias"
	scalarEntry := entrySpec{
		Name: "scalar-alias", GoName: "SetCommandAlias", GoType: "string",
		Variants: []variantSpec{{
			Since: "3.2a", TmuxKind: "STRING", Scopes: []string{"server"},
		}},
	}
	if err := validateSetterMethods([]entrySpec{arrayEntry, scalarEntry}); err == nil {
		t.Fatal("scalar and array Server.SetCommandAlias collision was accepted")
	}
}

func TestReadOptionSpecRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	t.Parallel()

	valid := `{"schema":2,"featureFloor":"3.2a","featureCeiling":"3.7","sourceTag":"3.7b","aliases":[],"options":[{"name":"buffer-limit","goName":"BufferLimit","goType":"int64","array":false,"style":false,"variants":[{"since":"3.2a","tmuxKind":"NUMBER","scopes":["server"],"choices":[]}]}],"hooks":[{"name":"after-bind-key","goName":"AfterBindKey","goType":"SparseArray[string]","array":true,"style":false,"variants":[{"since":"3.2a","tmuxKind":"COMMAND","scopes":["session"],"choices":[]}]}],"notifications":[{"pythonField":"continue_control","wireName":"%continue","goName":"Continue","since":"3.2a","prefixArguments":1,"prefixLabels":["pane ID"],"tail":"none","tailLabel":"","allowEmptyTail":false}]}`
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "whitespace", content: valid + " \n\t"},
		{name: "unknown", content: strings.Replace(valid, `"schema":2`, `"schema":2,"unknown":true`, 1), wantErr: true},
		{name: "second object", content: valid + `{}`, wantErr: true},
		{name: "closing bracket", content: valid + `]`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "spec.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readOptionSpec(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("readOptionSpec() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestCheckedInSpecMatchesPinnedTmuxInventory(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}

	type counts struct {
		unique  int
		server  int
		session int
		window  int
		pane    int
	}
	versions := []string{"3.2a", "3.3", "3.4", "3.5", "3.6", "3.7"}
	wantOptions := []counts{
		{unique: 104, server: 17, session: 47, window: 40, pane: 6},
		{unique: 116, server: 18, session: 47, window: 51, pane: 13},
		{unique: 121, server: 18, session: 48, window: 55, pane: 13},
		{unique: 124, server: 20, session: 48, window: 56, pane: 14},
		{unique: 142, server: 24, session: 51, window: 67, pane: 16},
		{unique: 153, server: 25, session: 54, window: 74, pane: 19},
	}
	wantHooks := []counts{
		{unique: 61, session: 49, window: 12, pane: 7},
		{unique: 65, session: 54, window: 11, pane: 7},
		{unique: 65, session: 54, window: 11, pane: 7},
		{unique: 66, session: 55, window: 11, pane: 7},
		{unique: 68, session: 57, window: 11, pane: 7},
		{unique: 68, session: 57, window: 11, pane: 7},
	}
	for index, version := range versions {
		if got := inventoryAt(spec.Options, version); got != wantOptions[index] {
			t.Errorf("options at %s = %+v, want %+v", version, got, wantOptions[index])
		}
		if got := inventoryAt(spec.Hooks, version); got != wantHooks[index] {
			t.Errorf("hooks at %s = %+v, want %+v", version, got, wantHooks[index])
		}
	}

	if got := unionInventory(spec.Options); got != (counts{unique: 153, server: 25, session: 54, window: 74, pane: 19}) {
		t.Errorf("option union = %+v", got)
	}
	if got := unionInventory(spec.Hooks); got != (counts{unique: 68, session: 57, window: 13, pane: 7}) {
		t.Errorf("hook union = %+v", got)
	}

	wantAliases := []aliasSpec{
		{Name: "clock-mode-color", Canonical: "clock-mode-colour"},
		{Name: "cursor-color", Canonical: "cursor-colour"},
		{Name: "display-panes-active-color", Canonical: "display-panes-active-colour"},
		{Name: "display-panes-color", Canonical: "display-panes-colour"},
		{Name: "pane-colors", Canonical: "pane-colours"},
		{Name: "prompt-cursor-color", Canonical: "prompt-cursor-colour"},
	}
	if !slices.Equal(spec.Aliases, wantAliases) {
		t.Errorf("aliases = %#v, want %#v", spec.Aliases, wantAliases)
	}

	hookNames := make(map[string]struct{}, len(spec.Hooks))
	for _, hook := range spec.Hooks {
		hookNames[hook.Name] = struct{}{}
	}
	for _, name := range controlOnlyNotifications {
		if _, found := hookNames[name]; found {
			t.Errorf("control notification %q is present as a hook", name)
		}
	}
}

func TestCheckedInSpecMatchesExactSourceContract(t *testing.T) {
	t.Parallel()

	spec, err := readOptionSpec("spec.json")
	if err != nil {
		t.Fatalf("readOptionSpec(spec.json) error = %v", err)
	}
	payload, err := json.Marshal(struct {
		Spec                     optionSpec `json:"spec"`
		ControlOnlyNotifications []string   `json:"controlOnlyNotifications"`
	}{
		Spec:                     spec,
		ControlOnlyNotifications: controlOnlyNotifications,
	})
	if err != nil {
		t.Fatalf("marshal source contract: %v", err)
	}
	digest := sha256.Sum256(payload)
	got := hex.EncodeToString(digest[:])
	if got != pinnedSourceContractDigest {
		t.Fatalf("source contract digest = %s, want %s", got, pinnedSourceContractDigest)
	}
}

func inventoryAt(entries []entrySpec, version string) (result struct {
	unique  int
	server  int
	session int
	window  int
	pane    int
},
) {
	order := map[string]int{"3.2a": 0, "3.3": 1, "3.4": 2, "3.5": 3, "3.6": 4, "3.7": 5}
	for _, entry := range entries {
		var active *variantSpec
		for index := range entry.Variants {
			variant := &entry.Variants[index]
			if order[variant.Since] <= order[version] {
				active = variant
			}
		}
		if active == nil {
			continue
		}
		result.unique++
		for _, scope := range active.Scopes {
			switch scope {
			case "server":
				result.server++
			case "session":
				result.session++
			case "window":
				result.window++
			case "pane":
				result.pane++
			}
		}
	}
	return result
}

func unionInventory(entries []entrySpec) (result struct {
	unique  int
	server  int
	session int
	window  int
	pane    int
},
) {
	result.unique = len(entries)
	for _, entry := range entries {
		scopes := make(map[string]struct{})
		for _, variant := range entry.Variants {
			for _, scope := range variant.Scopes {
				scopes[scope] = struct{}{}
			}
		}
		for scope := range scopes {
			switch scope {
			case "server":
				result.server++
			case "session":
				result.session++
			case "window":
				result.window++
			case "pane":
				result.pane++
			}
		}
	}
	return result
}
