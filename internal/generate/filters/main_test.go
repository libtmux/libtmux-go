package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFiltersIsDeterministicAndConcrete(t *testing.T) {
	t.Parallel()

	spec := generatorTestSpec()
	first, err := generateFilters(spec)
	if err != nil {
		t.Fatalf("generateFilters() error = %v", err)
	}
	second, err := generateFilters(spec)
	if err != nil {
		t.Fatalf("second generateFilters() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generateFilters() output changed between identical calls")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "filter_generated.go", first, parser.AllErrors); err != nil {
		t.Fatalf("generated Go does not parse: %v", err)
	}

	output := string(first)
	for _, declaration := range []string{
		"const FilterSchemaVersion = 1",
		"type SessionFilter struct",
		"type WindowFilter struct",
		"type PaneFilter struct",
		"type ClientFilter struct",
		"type WindowRel struct",
		"func (filter PaneFilter) Validate() error",
		"func ParsePaneLookup(lookup string, values ...string) (PaneFilter, error)",
		"func (filter PaneFilter) MarshalJSON() ([]byte, error)",
		"func (filter PaneFilter) Predicate() (func(*Pane) bool, error)",
		"func (filter *PaneFilter) UnmarshalJSON(data []byte) error",
	} {
		if !strings.Contains(output, declaration) {
			t.Errorf("generated output is missing %q", declaration)
		}
	}
	if strings.Contains(output, `"reflect"`) {
		t.Fatal("generated predicates import reflect")
	}
	for _, identityRead := range []string{
		"idValue := value.ID()",
		"nameValue := value.Name()",
	} {
		if !strings.Contains(output, identityRead) {
			t.Errorf("generated output is missing identity read %q", identityRead)
		}
	}
}

func TestCheckedInFilterSpecDefinesExactConstructors(t *testing.T) {
	t.Parallel()

	spec, err := readFilterSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]exactConstructorExpectation{
		"SessionIDIs":       {valueType: "SessionID", filterType: "SessionFilter", field: "ID"},
		"SessionNameIs":     {valueType: "string", filterType: "SessionFilter", field: "Name"},
		"SessionAttachedIs": {valueType: "bool", filterType: "SessionFilter", field: "Attached"},
		"WindowSessionIDIs": {valueType: "SessionID", filterType: "WindowFilter", field: "SessionID"},
		"WindowIDIs":        {valueType: "WindowID", filterType: "WindowFilter", field: "ID"},
		"WindowIndexIs":     {valueType: "int", filterType: "WindowFilter", field: "Index"},
		"WindowNameIs":      {valueType: "string", filterType: "WindowFilter", field: "Name"},
		"WindowActiveIs":    {valueType: "bool", filterType: "WindowFilter", field: "Active"},
		"PaneSessionIDIs":   {valueType: "SessionID", filterType: "PaneFilter", field: "SessionID"},
		"PaneWindowIDIs":    {valueType: "WindowID", filterType: "PaneFilter", field: "WindowID"},
		"PaneIDIs":          {valueType: "PaneID", filterType: "PaneFilter", field: "ID"},
		"PaneWindowIndexIs": {valueType: "int", filterType: "PaneFilter", field: "WindowIndex"},
		"PaneIndexIs":       {valueType: "int", filterType: "PaneFilter", field: "Index"},
		"PaneCommandIs":     {valueType: "string", filterType: "PaneFilter", field: "Command"},
		"PaneActiveIs":      {valueType: "bool", filterType: "PaneFilter", field: "Active"},
		"PaneTitleIs":       {valueType: "string", filterType: "PaneFilter", field: "Title"},
		"ClientNameIs":      {valueType: "ClientName", filterType: "ClientFilter", field: "Name"},
		"ClientReadOnlyIs":  {valueType: "bool", filterType: "ClientFilter", field: "ReadOnly"},
	}
	metadata := make(map[string]string, len(want))
	for _, model := range spec.Models {
		for _, field := range model.Fields {
			if field.ExactConstructor != "" {
				metadata[field.ExactConstructor] = model.Name + "." + field.Name
			}
		}
	}
	if len(metadata) != len(want) {
		t.Fatalf("exact constructor metadata count = %d, want %d: %#v", len(metadata), len(want), metadata)
	}
	for name := range want {
		if _, found := metadata[name]; !found {
			t.Errorf("exact constructor metadata is missing %s", name)
		}
	}

	generated, err := generateFilters(spec)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "filter_generated.go", generated, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	constructors := generatedExactConstructors(file)
	if len(constructors) != len(want) {
		t.Errorf("generated exact constructor count = %d, want %d", len(constructors), len(want))
	}
	for name, want := range want {
		constructor, found := constructors[name]
		if !found {
			t.Errorf("generated output is missing %s", name)
			continue
		}
		assertExactConstructor(t, fileSet, constructor, name, want)
	}
}

type exactConstructorExpectation struct {
	valueType  string
	filterType string
	field      string
}

func generatedExactConstructors(file *ast.File) map[string]*ast.FuncDecl {
	constructors := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasSuffix(function.Name.Name, "Is") {
			continue
		}
		constructors[function.Name.Name] = function
	}
	return constructors
}

func assertExactConstructor(
	t *testing.T,
	fileSet *token.FileSet,
	function *ast.FuncDecl,
	name string,
	want exactConstructorExpectation,
) {
	t.Helper()
	if function.Doc == nil || !strings.HasPrefix(function.Doc.Text(), name+" returns ") {
		t.Errorf("%s documentation is not identifier-led", name)
	}
	if function.Recv != nil || len(function.Type.Params.List) != 1 {
		t.Errorf("%s parameters = %#v, want one value parameter", name, function.Type.Params)
		return
	}
	parameter := function.Type.Params.List[0]
	if len(parameter.Names) != 1 || parameter.Names[0].Name != "value" ||
		expressionText(fileSet, parameter.Type) != want.valueType {
		t.Errorf("%s parameter = %s, want value %s", name, expressionText(fileSet, parameter), want.valueType)
	}
	if function.Type.Results == nil || len(function.Type.Results.List) != 1 ||
		len(function.Type.Results.List[0].Names) != 0 ||
		expressionText(fileSet, function.Type.Results.List[0].Type) != want.filterType {
		t.Errorf("%s return type = %#v, want %s", name, function.Type.Results, want.filterType)
	}
	if function.Body == nil || len(function.Body.List) != 1 {
		t.Errorf("%s body does not contain exactly one statement", name)
		return
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		t.Errorf("%s body = %#v, want one return", name, function.Body.List[0])
		return
	}
	literal, ok := returned.Results[0].(*ast.CompositeLit)
	if !ok || expressionText(fileSet, literal.Type) != want.filterType || len(literal.Elts) != 1 {
		t.Errorf("%s return = %s, want single-field %s literal", name, expressionText(fileSet, returned.Results[0]), want.filterType)
		return
	}
	pair, ok := literal.Elts[0].(*ast.KeyValueExpr)
	if !ok || expressionText(fileSet, pair.Key) != want.field ||
		expressionText(fileSet, pair.Value) != "Ptr(value)" {
		t.Errorf("%s return field = %s, want %s: Ptr(value)", name, expressionText(fileSet, literal.Elts[0]), want.field)
	}
}

func expressionText(fileSet *token.FileSet, expression ast.Node) string {
	var output bytes.Buffer
	if err := format.Node(&output, fileSet, expression); err != nil {
		return err.Error()
	}
	return output.String()
}

func TestValidateFilterSpecRejectsInvalidExactConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*filterSpec)
	}{
		{
			name: "missing",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].ExactConstructor = ""
			},
		},
		{
			name: "not exported",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].ExactConstructor = "sessionIDIs"
			},
		},
		{
			name: "non-exact field",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].Operators = []string{"in"}
			},
		},
		{
			name: "global duplicate",
			mutate: func(spec *filterSpec) {
				spec.Models[1].Fields[0].ExactConstructor = spec.Models[0].Fields[0].ExactConstructor
			},
		},
		{
			name: "generated declaration collision",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].ExactConstructor = "SessionFilter"
			},
		},
		{
			name: "handwritten declaration collision",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].ExactConstructor = "Ptr"
			},
		},
		{
			name: "uncovered handwritten declaration collision",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].ExactConstructor = "PaneCells"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := generatorTestSpec()
			test.mutate(&spec)
			if err := validateFilterSpec(spec); err == nil {
				t.Fatal("validateFilterSpec() error = nil, want exact-constructor failure")
			}
		})
	}
}

func TestTMUXPackageDirectoryFindsModuleRootFromGenerator(t *testing.T) {
	t.Parallel()

	packageDirectory, err := tmuxPackageDirectoryFrom(".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(packageDirectory, "filter.go")); err != nil {
		t.Fatalf("tmux package directory %q has no filter.go: %v", packageDirectory, err)
	}
}

func TestTMUXPackageDirectoryRejectsAnotherModule(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module example.invalid/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tmuxPackageDirectoryFrom(directory); err == nil {
		t.Fatal("tmuxPackageDirectoryFrom() error = nil, want module-path failure")
	}
}

func TestDeclaredNamesExcludesMethods(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "methods.go", `
package tmux

type receiver struct{}

func (receiver) PaneCells() {}
func ParseVersion() {}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, declaration := range file.Decls {
		names = append(names, declaredNames(declaration)...)
	}
	if strings.Join(names, ",") != "receiver,ParseVersion" {
		t.Fatalf("declaredNames() = %v, want receiver and ParseVersion", names)
	}
}

func TestCheckedInFilterSourcesUseNonStutteringTypedMethods(t *testing.T) {
	t.Parallel()

	spec, err := readFilterSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	wantFormats := map[string]sourceSpec{
		"Session.Name":     {Kind: "format-string", Name: "Name"},
		"Session.Attached": {Kind: "format-int-bool", Name: "Attached"},
		"Window.Name":      {Kind: "format-string", Name: "Name"},
		"Window.Active":    {Kind: "format-bool", Name: "Active"},
		"Pane.Command":     {Kind: "format-string", Name: "CurrentCommand"},
		"Pane.Active":      {Kind: "format-bool", Name: "Active"},
		"Pane.Title":       {Kind: "format-string", Name: "Title"},
		"Client.ReadOnly":  {Kind: "format-bool", Name: "ReadOnly"},
	}
	wantIdentities := map[string]string{
		"Session.ID":       "ID",
		"Window.SessionID": "SessionID",
		"Window.ID":        "ID",
		"Window.Index":     "Index",
		"Pane.SessionID":   "SessionID",
		"Pane.WindowID":    "WindowID",
		"Pane.ID":          "ID",
		"Pane.WindowIndex": "WindowIndex",
		"Pane.Index":       "Index",
		"Client.Name":      "Name",
	}
	for _, model := range spec.Models {
		for _, field := range model.Fields {
			key := model.Name + "." + field.Name
			if want, found := wantFormats[key]; found && field.Source != want {
				t.Errorf("%s source = %#v, want %#v", key, field.Source, want)
			}
			if want, found := wantIdentities[key]; found {
				if field.Source != (sourceSpec{Kind: "method", Name: want}) {
					t.Errorf("%s identity source = %#v, want Task 2 method %s", key, field.Source, want)
				}
				delete(wantIdentities, key)
			}
		}
	}
	for key := range wantIdentities {
		t.Errorf("missing Task 2 identity filter source %s", key)
	}

	generated, err := generateFilters(spec)
	if err != nil {
		t.Fatal(err)
	}
	output := string(generated)
	for _, directRead := range []string{
		"nameValue, nameAvailable := value.Name()",
		"attachedRaw, attachedAvailable := value.Attached()",
		"attachedValue := attachedRaw != 0",
		"activeValue, activeAvailable := value.Active()",
		"commandValue, commandAvailable := value.CurrentCommand()",
		"titleValue, titleAvailable := value.Title()",
		"readOnlyValue, readOnlyAvailable := value.ReadOnly()",
	} {
		if !strings.Contains(output, directRead) {
			t.Errorf("generated filters are missing direct typed read %q", directRead)
		}
	}
	if strings.Contains(output, "filterTmuxBool") || strings.Contains(output, "Parsed :=") {
		t.Error("generated filters retain obsolete boolean-string parsing")
	}
}

func TestGeneratedFilterDocumentationIsCallerComplete(t *testing.T) {
	t.Parallel()

	spec, err := readFilterSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generateFilters(spec)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		"filter_generated.go",
		generated,
		parser.ParseComments,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, comments := range file.Comments {
		if strings.Contains(comments.Text(), "`") {
			t.Errorf("generated documentation contains literal Markdown backticks: %q", comments.Text())
		}
	}

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpecification.Type.(*ast.StructType)
			if !ok || (!strings.HasSuffix(typeSpecification.Name.Name, "Filter") &&
				!strings.HasSuffix(typeSpecification.Name.Name, "Rel")) {
				continue
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if field.Doc == nil || !strings.HasPrefix(field.Doc.Text(), name.Name+" ") {
						t.Errorf("%s.%s lacks an identifier-led doc comment", typeSpecification.Name.Name, name.Name)
					}
				}
			}
		}
	}

	assertGeneratedDocContains(t, file, "SessionFilter",
		"already-materialized", "never runs tmux", "zero", "non-nil",
		"AND", "AnyOf", "Not", "[SessionFilter.Predicate]", "[SessionFilter.MarshalJSON]",
		"validate automatically", "[SessionFilter.Validate]", "[SessionFilter.UnmarshalJSON]",
		"constructed directly",
		"[Session.Windows]")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "ID",
		"stable", "%", "nil pointer", "zero value")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "Index",
		"nonnegative", "nil pointer", "zero value")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "IndexIn",
		"nil slice", "non-nil empty slice", "invalid")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "IndexGT", "strictly greater")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "IndexGTE", "greater than or equal")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "IndexLT", "strictly less")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "IndexLTE", "less than or equal")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "CommandContains",
		"substring", "nil pointer", "non-nil pointer", "empty string")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "CommandRegex",
		"Go regular expression syntax", "empty string", "unset")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "AnyOf", "at least one", "ordinary criteria")
	assertGeneratedFieldDocContains(t, file, "PaneFilter", "Not", "excludes")
	assertGeneratedFieldDocContains(t, file, "SessionFilter", "Windows", "materialized", "Session.Windows")
	if documentation := generatedFieldDocumentation(file, "SessionFilter", "Windows"); strings.Contains(documentation, "[") {
		t.Errorf("SessionFilter.Windows documentation contains a declaration-context link: %q", documentation)
	}
	assertGeneratedDocContains(t, file, "WindowRel",
		"zero", "invalid", "existential", "exclusion", "universal",
		"vacuously true", "empty", "conjunctive")
	assertGeneratedFieldDocContains(t, file, "WindowRel", "Some", "existential")
	assertGeneratedFieldDocContains(t, file, "WindowRel", "Every", "universal", "vacuously true")
	assertGeneratedFieldDocContains(t, file, "WindowRel", "None", "excludes")

	assertGeneratedDocContains(t, file, "FilterSchemaVersion",
		"external", "JSON", "[Version]")
	for _, model := range []string{"Session", "Window", "Pane", "Client"} {
		assertGeneratedDocContains(t, file, "Parse"+model+"Lookup",
			"generated JSON relation names", "double underscores", "default operator is exact",
			"eq", "iexact", "contains", "icontains", "startswith", "istartswith",
			"endswith", "iendswith", "in", "nin", "regex", "iregex",
			"field-specific", "errors.Is(err,", "[ErrInvalidFilter]")
		lookupDocumentation := generatedDeclarationDoc(file, "Parse"+model+"Lookup")
		if strings.Contains(lookupDocumentation, "`") {
			t.Errorf("Parse%sLookup documentation contains literal Markdown backticks: %q", model, lookupDocumentation)
		}
		assertGeneratedDocContains(t, file, model+"Filter.Validate",
			"before", "errors.Is(err,", "[ErrInvalidFilter]")
		assertGeneratedDocContains(t, file, model+"Filter.MarshalJSON",
			"validates", "external", "FilterSchemaVersion", "errors.Is(err,", "[ErrInvalidFilter]")
		assertGeneratedDocContains(t, file, model+"Filter.Predicate",
			"[Snapshot]", "["+model+"]", "relationships already materialized on that candidate",
			"never runs tmux", "nil candidate", "false", "validates", "errors.Is(err,")
		assertGeneratedDocContains(t, file, model+"Filter.UnmarshalJSON",
			"unknown", "duplicate", "trailing", "clears", "partial", "decode and framing",
			"validates decoded criteria", "semantic validation",
			"errors.Is(err,", "[ErrInvalidFilter]")
	}
	for _, target := range []string{"Window", "Pane"} {
		assertGeneratedDocContains(t, file, target+"Rel.MarshalJSON",
			"validates", "external", "FilterSchemaVersion", "errors.Is(err,", "[ErrInvalidFilter]")
		assertGeneratedDocContains(t, file, target+"Rel.UnmarshalJSON",
			"unknown", "duplicate", "trailing", "clears", "partial", "external",
			"FilterSchemaVersion", "decode and framing", "validates decoded criteria",
			"semantic validation", "errors.Is(err,", "[ErrInvalidFilter]")
	}
	if documentation := generatedFieldDocumentation(file, "PaneFilter", "IDIn"); strings.Contains(documentation, "sigil to equal") {
		t.Errorf("PaneFilter.IDIn documentation has awkward grammar: %q", documentation)
	}
}

func TestReadFilterSpecRejectsBlankDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		blank  func(map[string]any)
		wanted string
	}{
		{
			name: "field",
			blank: func(spec map[string]any) {
				firstModel(spec)["fields"].([]any)[0].(map[string]any)["description"] = " \t"
			},
			wanted: "field \"ID\" description must not be blank",
		},
		{
			name: "relation",
			blank: func(spec map[string]any) {
				firstModel(spec)["relations"].([]any)[0].(map[string]any)["description"] = "\n"
			},
			wanted: "relation \"Windows\" description must not be blank",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := describedFilterSpecJSON(t)
			test.blank(spec)
			encoded, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			path := t.TempDir() + "/spec.json"
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = readFilterSpec(path)
			if err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("readFilterSpec() error = %v, want %q", err, test.wanted)
			}
		})
	}
}

func TestValidateFilterSpecRejectsOpenOrAmbiguousSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*filterSpec)
	}{
		{
			name: "duplicate model",
			mutate: func(spec *filterSpec) {
				spec.Models = append(spec.Models, spec.Models[0])
			},
		},
		{
			name: "unknown operator",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].Operators = append(
					spec.Models[0].Fields[0].Operators,
					"approximately",
				)
			},
		},
		{
			name: "missing relation target",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Relations[0].Target = "Buffer"
			},
		},
		{
			name: "format source without method",
			mutate: func(spec *filterSpec) {
				spec.Models[0].Fields[0].Source = sourceSpec{Kind: "format-string"}
			},
		},
		{
			name: "zero depth",
			mutate: func(spec *filterSpec) {
				spec.MaxDepth = 0
			},
		},
		{
			name: "minimum on non-integer field",
			mutate: func(spec *filterSpec) {
				minimum := 0
				spec.Models[0].Fields[0].Minimum = &minimum
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := generatorTestSpec()
			test.mutate(&spec)
			if err := validateFilterSpec(spec); err == nil {
				t.Fatal("validateFilterSpec() error = nil, want schema failure")
			}
		})
	}
}

func TestReadFilterSpecRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/spec.json"
	if err := os.WriteFile(path, []byte(`{"schema":1,"maxDepth":8,"models":[],"future":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFilterSpec(path); err == nil {
		t.Fatal("readFilterSpec() error = nil, want unknown-field failure")
	}
}

func TestGeneratedFiltersMatchCommittedOutput(t *testing.T) {
	t.Parallel()

	spec, err := readFilterSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generateFilters(spec)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../../filter_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("filter_generated.go is stale; run go generate ./...")
	}
}

func assertGeneratedDocContains(t *testing.T, file *ast.File, name string, wanted ...string) {
	t.Helper()

	documentation := generatedDeclarationDoc(file, name)
	if documentation == "" {
		t.Errorf("generated declaration %s lacks documentation", name)
		return
	}
	for _, fragment := range wanted {
		if !strings.Contains(documentation, fragment) {
			t.Errorf("generated declaration %s documentation %q lacks %q", name, documentation, fragment)
		}
	}
}

func generatedDeclarationDoc(file *ast.File, wanted string) string {
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			name := node.Name.Name
			if node.Recv != nil && len(node.Recv.List) == 1 {
				receiver := node.Recv.List[0].Type
				if pointer, ok := receiver.(*ast.StarExpr); ok {
					receiver = pointer.X
				}
				if identifier, ok := receiver.(*ast.Ident); ok {
					name = identifier.Name + "." + name
				}
			}
			if name == wanted && node.Doc != nil {
				return node.Doc.Text()
			}
		case *ast.GenDecl:
			for _, specification := range node.Specs {
				var name string
				var documentation *ast.CommentGroup
				switch typed := specification.(type) {
				case *ast.TypeSpec:
					name = typed.Name.Name
					documentation = typed.Doc
				case *ast.ValueSpec:
					if len(typed.Names) == 1 {
						name = typed.Names[0].Name
						documentation = typed.Doc
					}
				}
				if documentation == nil && len(node.Specs) == 1 {
					documentation = node.Doc
				}
				if name == wanted && documentation != nil {
					return documentation.Text()
				}
			}
		}
	}
	return ""
}

func assertGeneratedFieldDocContains(
	t *testing.T,
	file *ast.File,
	typeName string,
	fieldName string,
	wanted ...string,
) {
	t.Helper()

	documentation := generatedFieldDocumentation(file, typeName, fieldName)
	if documentation == "" {
		t.Errorf("generated field %s.%s was not found or lacks documentation", typeName, fieldName)
		return
	}
	for _, fragment := range wanted {
		if !strings.Contains(documentation, fragment) {
			t.Errorf("generated field %s.%s documentation %q lacks %q", typeName, fieldName, documentation, fragment)
		}
	}
}

func generatedFieldDocumentation(file *ast.File, typeName, fieldName string) string {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpecification.Name.Name != typeName {
				continue
			}
			structure, ok := typeSpecification.Type.(*ast.StructType)
			if !ok {
				return ""
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if name.Name != fieldName {
						continue
					}
					documentation := ""
					if field.Doc != nil {
						documentation = field.Doc.Text()
					}
					return documentation
				}
			}
		}
	}
	return ""
}

func describedFilterSpecJSON(t *testing.T) map[string]any {
	t.Helper()

	contents, err := os.ReadFile("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(contents, &spec); err != nil {
		t.Fatal(err)
	}
	for _, modelValue := range spec["models"].([]any) {
		model := modelValue.(map[string]any)
		for _, fieldValue := range model["fields"].([]any) {
			fieldValue.(map[string]any)["description"] = "documented field"
		}
		for _, relationValue := range model["relations"].([]any) {
			relationValue.(map[string]any)["description"] = "documented relation"
		}
	}
	return spec
}

func firstModel(spec map[string]any) map[string]any {
	return spec["models"].([]any)[0].(map[string]any)
}

func generatorTestSpec() filterSpec {
	return filterSpec{
		Schema:   1,
		MaxDepth: 8,
		Models: []modelSpec{
			{
				Name: "Session",
				Fields: []fieldSpec{
					{
						Name:             "ID",
						JSON:             "id",
						GoType:           "SessionID",
						Kind:             "string",
						Description:      "the stable session identifier, including its $ sigil",
						ExactConstructor: "SessionIDIs",
						Source:           sourceSpec{Kind: "method", Name: "ID"},
						Operators:        []string{"exact", "in"},
					},
				},
				Relations: []relationSpec{
					{Name: "Windows", JSON: "windows", Target: "Window", Cardinality: "many", Method: "Windows", Description: "the materialized windows returned by [Session.Windows]"},
				},
			},
			{
				Name: "Window",
				Fields: []fieldSpec{
					{
						Name:             "ID",
						JSON:             "id",
						GoType:           "WindowID",
						Kind:             "string",
						Description:      "the stable window identifier, including its @ sigil",
						ExactConstructor: "WindowIDIs",
						Source:           sourceSpec{Kind: "method", Name: "ID"},
						Operators:        []string{"exact", "in"},
					},
				},
			},
			{
				Name: "Pane",
				Fields: []fieldSpec{
					{
						Name:             "ID",
						JSON:             "id",
						GoType:           "PaneID",
						Kind:             "string",
						Description:      "the stable pane identifier, including its % sigil",
						ExactConstructor: "PaneIDIs",
						Source:           sourceSpec{Kind: "method", Name: "ID"},
						Operators:        []string{"exact", "in"},
					},
				},
			},
			{
				Name: "Client",
				Fields: []fieldSpec{
					{
						Name:             "Name",
						JSON:             "name",
						GoType:           "ClientName",
						Kind:             "string",
						Description:      "the stable client name",
						ExactConstructor: "ClientNameIs",
						Source:           sourceSpec{Kind: "method", Name: "Name"},
						Operators:        []string{"exact", "in", "contains", "regex"},
					},
				},
			},
		},
	}
}
