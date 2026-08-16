// Command filters generates the typed tmux snapshot filters.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var jsonNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

var goDocumentationLinkPattern = regexp.MustCompile(`\[[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\]`)

var expectedModels = [...]string{"Session", "Window", "Pane", "Client"}

const tmuxModulePath = "github.com/libtmux/libtmux-go"

// tmuxPackageDirName is the directory the tmux package occupies inside its
// module, which is named after the package rather than the repository.
const tmuxPackageDirName = "tmux"

type filterSpec struct {
	Schema   int         `json:"schema"`
	MaxDepth int         `json:"maxDepth"`
	Models   []modelSpec `json:"models"`
}

type modelSpec struct {
	Name      string         `json:"name"`
	Fields    []fieldSpec    `json:"fields"`
	Relations []relationSpec `json:"relations,omitempty"`
}

type fieldSpec struct {
	Name             string     `json:"name"`
	JSON             string     `json:"json"`
	GoType           string     `json:"goType"`
	Kind             string     `json:"kind"`
	Description      string     `json:"description"`
	ExactConstructor string     `json:"exactConstructor,omitempty"`
	Minimum          *int       `json:"minimum,omitempty"`
	Source           sourceSpec `json:"source"`
	Operators        []string   `json:"operators"`
}

type sourceSpec struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type relationSpec struct {
	Name        string `json:"name"`
	JSON        string `json:"json"`
	Target      string `json:"target"`
	Cardinality string `json:"cardinality"`
	Method      string `json:"method"`
	Description string `json:"description"`
}

type generatedField struct {
	GoName   string
	JSONName string
	GoType   string
	Operator string
}

func main() {
	specPath := flag.String("spec", "internal/generate/filters/spec.json", "filter specification")
	outputPath := flag.String("output", "filter_generated.go", "generated Go output")
	flag.Parse()

	spec, err := readFilterSpec(*specPath)
	if err != nil {
		fatal(err)
	}
	output, err := generateFilters(spec)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*outputPath, output, 0o644); err != nil {
		fatal(fmt.Errorf("write generated filters: %w", err))
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func readFilterSpec(path string) (filterSpec, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return filterSpec{}, fmt.Errorf("read filter spec: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var spec filterSpec
	if err := decoder.Decode(&spec); err != nil {
		return filterSpec{}, fmt.Errorf("decode filter spec: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return filterSpec{}, errors.New("decode filter spec: trailing JSON value")
		}
		return filterSpec{}, fmt.Errorf("decode filter spec trailing value: %w", err)
	}
	if err := validateFilterSpec(spec); err != nil {
		return filterSpec{}, err
	}
	return spec, nil
}

func validateFilterSpec(spec filterSpec) error {
	if spec.Schema != 1 {
		return fmt.Errorf("filter spec has unsupported schema %d", spec.Schema)
	}
	if spec.MaxDepth <= 0 {
		return errors.New("filter spec maxDepth must be positive")
	}
	if len(spec.Models) != len(expectedModels) {
		return fmt.Errorf("filter spec must define exactly %d models", len(expectedModels))
	}

	models := make(map[string]modelSpec, len(spec.Models))
	for index, model := range spec.Models {
		if model.Name != expectedModels[index] {
			return fmt.Errorf(
				"filter model %d is %q, want %q",
				index,
				model.Name,
				expectedModels[index],
			)
		}
		if _, found := models[model.Name]; found {
			return fmt.Errorf("duplicate filter model %q", model.Name)
		}
		models[model.Name] = model
		if len(model.Fields) == 0 {
			return fmt.Errorf("filter model %q has no fields", model.Name)
		}
	}

	for _, model := range spec.Models {
		if err := validateModelSpec(model, models); err != nil {
			return err
		}
	}
	reserved, err := filterDeclarationNames(spec)
	if err != nil {
		return err
	}
	constructors := make(map[string]string)
	for _, model := range spec.Models {
		for _, field := range model.Fields {
			name := field.ExactConstructor
			if name == "" {
				continue
			}
			location := model.Name + "." + field.Name
			if declaration, found := reserved[name]; found {
				return fmt.Errorf(
					"filter exact constructor %q for %s collides with %s",
					name,
					location,
					declaration,
				)
			}
			if previous, found := constructors[name]; found {
				return fmt.Errorf(
					"filter exact constructor %q is shared by %s and %s",
					name,
					previous,
					location,
				)
			}
			constructors[name] = location
		}
	}
	return nil
}

func validateModelSpec(model modelSpec, models map[string]modelSpec) error {
	seenGo := map[string]string{"AnyOf": "composition", "Not": "composition"}
	seenJSON := map[string]string{"anyOf": "composition", "not": "composition"}
	for _, field := range model.Fields {
		if err := validateFieldSpec(model.Name, field); err != nil {
			return err
		}
		for _, generated := range generatedFields(field) {
			if previous, found := seenGo[generated.GoName]; found {
				return fmt.Errorf(
					"filter model %q fields %q and %q produce Go field %q",
					model.Name,
					previous,
					field.Name,
					generated.GoName,
				)
			}
			seenGo[generated.GoName] = field.Name
			if previous, found := seenJSON[generated.JSONName]; found {
				return fmt.Errorf(
					"filter model %q fields %q and %q produce JSON field %q",
					model.Name,
					previous,
					field.Name,
					generated.JSONName,
				)
			}
			seenJSON[generated.JSONName] = field.Name
		}
	}

	for _, relation := range model.Relations {
		if strings.TrimSpace(relation.Description) == "" {
			return fmt.Errorf("filter model %q relation %q description must not be blank", model.Name, relation.Name)
		}
		if !token.IsIdentifier(relation.Name) || !token.IsExported(relation.Name) {
			return fmt.Errorf("filter model %q has invalid relation name %q", model.Name, relation.Name)
		}
		if !jsonNamePattern.MatchString(relation.JSON) {
			return fmt.Errorf("filter model %q relation %q has invalid JSON name %q", model.Name, relation.Name, relation.JSON)
		}
		if _, found := models[relation.Target]; !found {
			return fmt.Errorf("filter model %q relation %q has missing target %q", model.Name, relation.Name, relation.Target)
		}
		if relation.Cardinality != "one" && relation.Cardinality != "many" {
			return fmt.Errorf("filter model %q relation %q has invalid cardinality %q", model.Name, relation.Name, relation.Cardinality)
		}
		if !token.IsIdentifier(relation.Method) || !token.IsExported(relation.Method) {
			return fmt.Errorf("filter model %q relation %q has invalid method %q", model.Name, relation.Name, relation.Method)
		}
		if previous, found := seenGo[relation.Name]; found {
			return fmt.Errorf("filter model %q relation %q collides with %q", model.Name, relation.Name, previous)
		}
		seenGo[relation.Name] = relation.Name
		if previous, found := seenJSON[relation.JSON]; found {
			return fmt.Errorf("filter model %q relation JSON %q collides with %q", model.Name, relation.JSON, previous)
		}
		seenJSON[relation.JSON] = relation.Name
	}
	return nil
}

func validateFieldSpec(model string, field fieldSpec) error {
	if strings.TrimSpace(field.Description) == "" {
		return fmt.Errorf("filter model %q field %q description must not be blank", model, field.Name)
	}
	if !token.IsIdentifier(field.Name) || !token.IsExported(field.Name) {
		return fmt.Errorf("filter model %q has invalid field name %q", model, field.Name)
	}
	if !jsonNamePattern.MatchString(field.JSON) {
		return fmt.Errorf("filter model %q field %q has invalid JSON name %q", model, field.Name, field.JSON)
	}
	if !token.IsIdentifier(field.GoType) {
		return fmt.Errorf("filter model %q field %q has invalid Go type %q", model, field.Name, field.GoType)
	}
	validTypes := map[string]map[string]bool{
		"string": {
			"string": true, "SessionID": true, "WindowID": true,
			"PaneID": true, "ClientName": true,
		},
		"int":  {"int": true},
		"bool": {"bool": true},
	}
	allowedTypes, found := validTypes[field.Kind]
	if !found || !allowedTypes[field.GoType] {
		return fmt.Errorf("filter model %q field %q has incompatible kind %q and Go type %q", model, field.Name, field.Kind, field.GoType)
	}
	if field.Minimum != nil && field.Kind != "int" {
		return fmt.Errorf("filter model %q field %q has a minimum for non-integer kind %q", model, field.Name, field.Kind)
	}
	if !token.IsIdentifier(field.Source.Name) || !token.IsExported(field.Source.Name) {
		return fmt.Errorf("filter model %q field %q has invalid source name %q", model, field.Name, field.Source.Name)
	}
	switch field.Source.Kind {
	case "field":
	case "method":
	case "field-string":
		if field.Kind != "string" || field.GoType != "string" {
			return fmt.Errorf("filter model %q field %q field-string source requires string", model, field.Name)
		}
	case "format-string":
		if field.Kind != "string" || field.GoType != "string" {
			return fmt.Errorf("filter model %q field %q format-string source requires string", model, field.Name)
		}
	case "format-bool":
		if field.Kind != "bool" || field.GoType != "bool" {
			return fmt.Errorf("filter model %q field %q format-bool source requires bool", model, field.Name)
		}
	case "format-int-bool":
		if field.Kind != "bool" || field.GoType != "bool" {
			return fmt.Errorf("filter model %q field %q format-int-bool source requires bool", model, field.Name)
		}
	default:
		return fmt.Errorf("filter model %q field %q has invalid source kind %q", model, field.Name, field.Source.Kind)
	}

	allowedOperators := map[string]map[string]bool{
		"string": {"exact": true, "in": true, "contains": true, "regex": true},
		"int":    {"exact": true, "in": true, "gt": true, "gte": true, "lt": true, "lte": true},
		"bool":   {"exact": true},
	}[field.Kind]
	if len(field.Operators) == 0 {
		return fmt.Errorf("filter model %q field %q has no operators", model, field.Name)
	}
	seen := make(map[string]struct{}, len(field.Operators))
	for _, operator := range field.Operators {
		if !allowedOperators[operator] {
			return fmt.Errorf("filter model %q field %q has invalid operator %q", model, field.Name, operator)
		}
		if _, found := seen[operator]; found {
			return fmt.Errorf("filter model %q field %q repeats operator %q", model, field.Name, operator)
		}
		seen[operator] = struct{}{}
	}
	_, hasExact := seen["exact"]
	if hasExact && field.ExactConstructor == "" {
		return fmt.Errorf("filter model %q field %q exact constructor must not be blank", model, field.Name)
	}
	if !hasExact && field.ExactConstructor != "" {
		return fmt.Errorf("filter model %q field %q has an exact constructor without the exact operator", model, field.Name)
	}
	if field.ExactConstructor != "" &&
		(!token.IsIdentifier(field.ExactConstructor) || !token.IsExported(field.ExactConstructor)) {
		return fmt.Errorf(
			"filter model %q field %q has invalid exact constructor %q",
			model,
			field.Name,
			field.ExactConstructor,
		)
	}
	return nil
}

func generateFilters(spec filterSpec) ([]byte, error) {
	if err := validateFilterSpec(spec); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n\n")
	output.WriteString("package tmux\n\n")
	output.WriteString("import (\n")
	output.WriteString("\t\"encoding/json\"\n")
	if specHasOperator(spec, "regex") {
		output.WriteString("\t\"regexp\"\n")
	}
	if specHasOperator(spec, "contains") {
		output.WriteString("\t\"strings\"\n")
	}
	output.WriteString(")\n\n")
	output.WriteString("// FilterSchemaVersion identifies external metadata for the generated filter JSON wire schema.\n")
	output.WriteString("// It is not embedded in JSON and is independent of the tmux [Version].\n")
	fmt.Fprintf(&output, "const FilterSchemaVersion = %d\n\n", spec.Schema)
	fmt.Fprintf(&output, "const maxFilterDepth = %d\n\n", spec.MaxDepth)

	for _, model := range spec.Models {
		renderFilterStruct(&output, model)
	}
	for _, model := range spec.Models {
		renderExactConstructors(&output, model)
	}
	for _, target := range manyRelationTargets(spec) {
		renderRelationStruct(&output, target)
	}
	for _, model := range spec.Models {
		renderFilterEquality(&output, model)
	}
	for _, target := range manyRelationTargets(spec) {
		renderRelationEquality(&output, target)
	}
	renderLookupParsers(&output, spec)
	renderValidationState(&output, spec)
	for _, model := range spec.Models {
		renderValidation(&output, model)
	}
	for _, model := range spec.Models {
		renderFilterMarshal(&output, model)
	}
	for _, target := range manyRelationTargets(spec) {
		renderRelationMarshal(&output, target)
	}
	for _, model := range spec.Models {
		renderCompiler(&output, model)
	}
	for _, model := range spec.Models {
		renderFilterUnmarshal(&output, model)
	}
	for _, target := range manyRelationTargets(spec) {
		renderRelationUnmarshal(&output, target)
	}

	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated filters: %w\n%s", err, output.Bytes())
	}
	return formatted, nil
}

func renderExactConstructors(output *bytes.Buffer, model modelSpec) {
	for _, field := range model.Fields {
		if field.ExactConstructor == "" {
			continue
		}
		description := stripDocumentationLinks(
			strings.TrimSuffix(strings.TrimSpace(field.Description), "."),
		)
		fmt.Fprintf(
			output,
			"// %s returns a [%sFilter] that exactly matches %s. It sets no other criteria and does not validate value.\n",
			field.ExactConstructor,
			model.Name,
			description,
		)
		fmt.Fprintf(
			output,
			"func %s(value %s) %sFilter {\n",
			field.ExactConstructor,
			field.GoType,
			model.Name,
		)
		fmt.Fprintf(output, "\treturn %sFilter{%s: Ptr(value)}\n", model.Name, field.Name)
		output.WriteString("}\n\n")
	}
}

func filterDeclarationNames(spec filterSpec) (map[string]string, error) {
	names, err := handwrittenDeclarationNames()
	if err != nil {
		return nil, err
	}
	names["FilterSchemaVersion"] = "generated declaration"
	for _, model := range spec.Models {
		names[model.Name+"Filter"] = "generated declaration"
		names["Parse"+model.Name+"Lookup"] = "generated declaration"
	}
	for _, target := range manyRelationTargets(spec) {
		names[target+"Rel"] = "generated declaration"
	}
	return names, nil
}

func handwrittenDeclarationNames() (map[string]string, error) {
	packageDirectory, err := tmuxPackageDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		return nil, fmt.Errorf("read tmux package declarations: %w", err)
	}
	names := make(map[string]string)
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		fileName := entry.Name()
		if entry.IsDir() || filepath.Ext(fileName) != ".go" ||
			strings.HasSuffix(fileName, "_test.go") || fileName == "filter_generated.go" {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(packageDirectory, fileName), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse tmux declaration file %s: %w", fileName, err)
		}
		if file.Name.Name != "tmux" {
			continue
		}
		for _, declaration := range file.Decls {
			for _, declaredName := range declaredNames(declaration) {
				if token.IsExported(declaredName) {
					names[declaredName] = "handwritten declaration in " + fileName
				}
			}
		}
	}
	return names, nil
}

func tmuxPackageDirectory() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get generator working directory: %w", err)
	}
	return tmuxPackageDirectoryFrom(directory)
}

func tmuxPackageDirectoryFrom(startDirectory string) (string, error) {
	directory, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("make generator directory absolute: %w", err)
	}
	for {
		contents, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err == nil {
			modulePath := goModulePath(contents)
			if modulePath != tmuxModulePath {
				return "", fmt.Errorf(
					"generator module %q at %s, want %q",
					modulePath,
					directory,
					tmuxModulePath,
				)
			}
			// The module root is not the package directory: the package sits
			// one level below it, in a directory named after itself.
			return filepath.Join(directory, tmuxPackageDirName), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read generator go.mod: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("find %s module root from %s", tmuxModulePath, startDirectory)
		}
		directory = parent
	}
}

func goModulePath(contents []byte) string {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func declaredNames(declaration ast.Decl) []string {
	switch declaration := declaration.(type) {
	case *ast.FuncDecl:
		if declaration.Recv != nil {
			return nil
		}
		return []string{declaration.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, specification := range declaration.Specs {
			switch specification := specification.(type) {
			case *ast.TypeSpec:
				names = append(names, specification.Name.Name)
			case *ast.ValueSpec:
				for _, name := range specification.Names {
					names = append(names, name.Name)
				}
			}
		}
		return names
	default:
		return nil
	}
}

func renderFilterStruct(output *bytes.Buffer, model modelSpec) {
	fmt.Fprintf(output, "// %sFilter evaluates already-materialized [%s] values and never runs tmux.\n", model.Name, model.Name)
	output.WriteString("// Its zero value matches every non-nil candidate. Ordinary field and relation criteria are ANDed.\n")
	output.WriteString("// AnyOf additionally requires at least one branch to match; Not excludes a match.\n")
	if links := filterDocumentationLinks(model); len(links) != 0 {
		fmt.Fprintf(output, "// Field and relation criteria correspond to %s.\n", joinDocumentationLinks(links))
	}
	fmt.Fprintf(output, "// [%sFilter.Predicate], [%sFilter.MarshalJSON], and [%sFilter.UnmarshalJSON] validate automatically.\n", model.Name, model.Name, model.Name)
	fmt.Fprintf(output, "// Use [%sFilter.Validate] to check a filter constructed directly.\n", model.Name)
	fmt.Fprintf(output, "type %sFilter struct {\n", model.Name)
	for _, field := range model.Fields {
		for _, generated := range generatedFields(field) {
			fmt.Fprintf(output, "\t// %s\n", generatedFieldDoc(field, generated))
			fmt.Fprintf(
				output,
				"\t%s %s `json:%s`\n",
				generated.GoName,
				generated.GoType,
				strconv.Quote(generated.JSONName+",omitempty"),
			)
		}
	}
	output.WriteString("\t// AnyOf additionally requires at least one branch to match after ordinary criteria match. A nil slice leaves AnyOf unset; a non-nil empty slice is invalid.\n")
	fmt.Fprintf(output, "\tAnyOf []%sFilter `json:\"anyOf,omitempty\"`\n", model.Name)
	output.WriteString("\t// Not excludes a candidate when its nested filter matches. A nil pointer leaves Not unset.\n")
	fmt.Fprintf(output, "\tNot *%sFilter `json:\"not,omitempty\"`\n", model.Name)
	for _, relation := range model.Relations {
		typeName := "*" + relation.Target + "Filter"
		if relation.Cardinality == "many" {
			typeName = "*" + relation.Target + "Rel"
		}
		fmt.Fprintf(
			output,
			"\t// %s traverses %s. A nil pointer leaves the relation criterion unset.\n",
			relation.Name,
			stripDocumentationLinks(relation.Description),
		)
		fmt.Fprintf(output, "\t%s %s `json:%s`\n", relation.Name, typeName, strconv.Quote(relation.JSON+",omitempty"))
	}
	output.WriteString("}\n\n")
}

func renderRelationStruct(output *bytes.Buffer, target string) {
	fmt.Fprintf(output, "// %sRel applies quantifiers to a materialized [%s] relation.\n", target, target)
	output.WriteString("// Its zero value is invalid. Some is existential, None is exclusion, and Every is universal and vacuously true for an empty relation.\n")
	output.WriteString("// All enabled quantifiers are conjunctive.\n")
	fmt.Fprintf(output, "type %sRel struct {\n", target)
	output.WriteString("\t// Some requires an existential match: at least one related value must match.\n")
	fmt.Fprintf(output, "\tSome *%sFilter `json:\"some,omitempty\"`\n", target)
	output.WriteString("\t// Every requires a universal match and is vacuously true for an empty relation.\n")
	fmt.Fprintf(output, "\tEvery *%sFilter `json:\"every,omitempty\"`\n", target)
	output.WriteString("\t// None excludes the candidate when any related value matches.\n")
	fmt.Fprintf(output, "\tNone *%sFilter `json:\"none,omitempty\"`\n", target)
	output.WriteString("}\n\n")
}

func renderFilterEquality(output *bytes.Buffer, model modelSpec) {
	fmt.Fprintf(output, "func (filter *%sFilter) isZero() bool {\n", model.Name)
	parts := make([]string, 0, len(model.Fields)*2+len(model.Relations)+2)
	for _, field := range model.Fields {
		for _, generated := range generatedFields(field) {
			if generated.GoType == "string" {
				parts = append(parts, "filter."+generated.GoName+` == ""`)
			} else {
				parts = append(parts, "filter."+generated.GoName+" == nil")
			}
		}
	}
	parts = append(parts, "filter.AnyOf == nil", "filter.Not == nil")
	for _, relation := range model.Relations {
		parts = append(parts, "filter."+relation.Name+" == nil")
	}
	fmt.Fprintf(output, "\treturn %s\n", strings.Join(parts, " &&\n\t\t"))
	output.WriteString("}\n\n")

	fmt.Fprintf(
		output,
		"func filter%sEqual(left, right *%sFilter) bool {\n",
		model.Name,
		model.Name,
	)
	output.WriteString("\tif left == nil || right == nil {\n")
	output.WriteString("\t\treturn left == nil && right == nil\n")
	output.WriteString("\t}\n")
	for _, field := range model.Fields {
		for _, generated := range generatedFields(field) {
			switch {
			case strings.HasPrefix(generated.GoType, "*"):
				fmt.Fprintf(
					output,
					"\tif !filterPointersEqual(left.%s, right.%s) {\n",
					generated.GoName,
					generated.GoName,
				)
			case strings.HasPrefix(generated.GoType, "[]"):
				fmt.Fprintf(
					output,
					"\tif !filterSlicesEqual(left.%s, right.%s) {\n",
					generated.GoName,
					generated.GoName,
				)
			default:
				fmt.Fprintf(
					output,
					"\tif left.%s != right.%s {\n",
					generated.GoName,
					generated.GoName,
				)
			}
			output.WriteString("\t\treturn false\n")
			output.WriteString("\t}\n")
		}
	}
	output.WriteString("\tif (left.AnyOf == nil) != (right.AnyOf == nil) || len(left.AnyOf) != len(right.AnyOf) {\n")
	output.WriteString("\t\treturn false\n")
	output.WriteString("\t}\n")
	output.WriteString("\tfor index := range left.AnyOf {\n")
	fmt.Fprintf(
		output,
		"\t\tif !filter%sEqual(&left.AnyOf[index], &right.AnyOf[index]) {\n",
		model.Name,
	)
	output.WriteString("\t\t\treturn false\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(
		output,
		"\tif !filter%sEqual(left.Not, right.Not) {\n",
		model.Name,
	)
	output.WriteString("\t\treturn false\n")
	output.WriteString("\t}\n")
	for _, relation := range model.Relations {
		function := "filter" + relation.Target + "Equal"
		if relation.Cardinality == "many" {
			function = "filter" + relation.Target + "RelEqual"
		}
		fmt.Fprintf(
			output,
			"\tif !%s(left.%s, right.%s) {\n",
			function,
			relation.Name,
			relation.Name,
		)
		output.WriteString("\t\treturn false\n")
		output.WriteString("\t}\n")
	}
	output.WriteString("\treturn true\n")
	output.WriteString("}\n\n")
}

func renderRelationEquality(output *bytes.Buffer, target string) {
	fmt.Fprintf(
		output,
		"func filter%sRelEqual(left, right *%sRel) bool {\n",
		target,
		target,
	)
	output.WriteString("\tif left == nil || right == nil {\n")
	output.WriteString("\t\treturn left == nil && right == nil\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(
		output,
		"\treturn filter%sEqual(left.Some, right.Some) &&\n",
		target,
	)
	fmt.Fprintf(
		output,
		"\t\tfilter%sEqual(left.Every, right.Every) &&\n",
		target,
	)
	fmt.Fprintf(
		output,
		"\t\tfilter%sEqual(left.None, right.None)\n",
		target,
	)
	output.WriteString("}\n\n")
}

func renderLookupParsers(output *bytes.Buffer, spec filterSpec) {
	for _, model := range spec.Models {
		modelName := strings.ToLower(model.Name)
		fmt.Fprintf(output, "// Parse%sLookup converts a lookup path into a concrete %s filter.\n", model.Name, modelName)
		output.WriteString("// Paths traverse generated JSON relation names and separate segments with double underscores.\n")
		output.WriteString("// The default operator is exact. Accepted suffixes are eq, exact, iexact, contains, icontains, startswith, istartswith, endswith, iendswith, in, nin, regex, and iregex; availability is field-specific.\n")
		output.WriteString("// The eq suffix aliases exact, nin negates in, scalar operators require one value, and in and nin require one or more.\n")
		output.WriteString("// Invalid paths, operators, values, or results return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
		fmt.Fprintf(
			output,
			"func Parse%sLookup(lookup string, values ...string) (%sFilter, error) {\n",
			model.Name,
			model.Name,
		)
		output.WriteString("\tpath, operator, err := splitFilterLookup(lookup)\n")
		output.WriteString("\tif err != nil {\n")
		fmt.Fprintf(output, "\t\treturn %sFilter{}, err\n", model.Name)
		output.WriteString("\t}\n")
		fmt.Fprintf(
			output,
			"\tfilter, negated, err := parse%sLookup(path, operator, values)\n",
			model.Name,
		)
		output.WriteString("\tif err != nil {\n")
		fmt.Fprintf(output, "\t\treturn %sFilter{}, err\n", model.Name)
		output.WriteString("\t}\n")
		output.WriteString("\tif negated {\n")
		output.WriteString("\t\tpositive := filter\n")
		fmt.Fprintf(output, "\t\tfilter = %sFilter{Not: &positive}\n", model.Name)
		output.WriteString("\t}\n")
		output.WriteString("\tif err := filter.Validate(); err != nil {\n")
		fmt.Fprintf(output, "\t\treturn %sFilter{}, err\n", model.Name)
		output.WriteString("\t}\n")
		output.WriteString("\treturn filter, nil\n")
		output.WriteString("}\n\n")
	}
	for _, model := range spec.Models {
		renderLookupParser(output, model)
	}
}

func renderLookupParser(output *bytes.Buffer, model modelSpec) {
	modelName := strings.ToLower(model.Name)
	fmt.Fprintf(
		output,
		"func parse%sLookup(path []string, operator string, values []string) (%sFilter, bool, error) {\n",
		model.Name,
		model.Name,
	)
	output.WriteString("\tif len(path) == 0 {\n")
	fmt.Fprintf(
		output,
		"\t\treturn %sFilter{}, false, invalidFilter(%s)\n",
		model.Name,
		strconv.Quote(modelName+" lookup path must name a field"),
	)
	output.WriteString("\t}\n")
	output.WriteString("\tswitch path[0] {\n")
	for _, field := range model.Fields {
		fmt.Fprintf(output, "\tcase %s:\n", strconv.Quote(field.JSON))
		output.WriteString("\t\tif len(path) != 1 {\n")
		fmt.Fprintf(
			output,
			"\t\t\treturn %sFilter{}, false, invalidFilter(%s)\n",
			model.Name,
			strconv.Quote(modelName+" lookup field "+field.JSON+" does not accept a nested path"),
		)
		output.WriteString("\t\t}\n")
		renderLookupField(output, model, field)
	}
	for _, relation := range model.Relations {
		fmt.Fprintf(output, "\tcase %s:\n", strconv.Quote(relation.JSON))
		output.WriteString("\t\tif len(path) == 1 {\n")
		fmt.Fprintf(
			output,
			"\t\t\treturn %sFilter{}, false, invalidFilter(%s)\n",
			model.Name,
			strconv.Quote(modelName+" lookup relation "+relation.JSON+" requires a nested field"),
		)
		output.WriteString("\t\t}\n")
		fmt.Fprintf(
			output,
			"\t\trelated, negated, err := parse%sLookup(path[1:], operator, values)\n",
			relation.Target,
		)
		output.WriteString("\t\tif err != nil {\n")
		fmt.Fprintf(output, "\t\t\treturn %sFilter{}, false, err\n", model.Name)
		output.WriteString("\t\t}\n")
		if relation.Cardinality == "one" {
			output.WriteString("\t\tif negated {\n")
			output.WriteString("\t\t\tpositive := related\n")
			fmt.Fprintf(
				output,
				"\t\t\trelated = %sFilter{Not: &positive}\n",
				relation.Target,
			)
			output.WriteString("\t\t}\n")
			fmt.Fprintf(
				output,
				"\t\treturn %sFilter{%s: &related}, false, nil\n",
				model.Name,
				relation.Name,
			)
		} else {
			fmt.Fprintf(output, "\t\trelation := &%sRel{}\n", relation.Target)
			output.WriteString("\t\tif negated {\n")
			output.WriteString("\t\t\trelation.None = &related\n")
			output.WriteString("\t\t} else {\n")
			output.WriteString("\t\t\trelation.Some = &related\n")
			output.WriteString("\t\t}\n")
			fmt.Fprintf(
				output,
				"\t\treturn %sFilter{%s: relation}, false, nil\n",
				model.Name,
				relation.Name,
			)
		}
	}
	output.WriteString("\tdefault:\n")
	fmt.Fprintf(
		output,
		"\t\treturn %sFilter{}, false, invalidFilter(%s, path[0])\n",
		model.Name,
		strconv.Quote(modelName+" lookup has unknown field or relation %q"),
	)
	output.WriteString("\t}\n")
	output.WriteString("}\n\n")
}

func renderLookupField(output *bytes.Buffer, model modelSpec, field fieldSpec) {
	modelName := strings.ToLower(model.Name)
	output.WriteString("\t\tswitch operator {\n")
	output.WriteString("\t\tcase \"exact\":\n")
	output.WriteString("\t\t\tvalue, err := filterLookupOne(")
	fmt.Fprintf(
		output,
		"%s, %s, operator, values)\n",
		strconv.Quote(modelName),
		strconv.Quote(field.JSON),
	)
	output.WriteString("\t\t\tif err != nil {\n")
	fmt.Fprintf(output, "\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
	output.WriteString("\t\t\t}\n")
	switch field.Kind {
	case "string":
		fmt.Fprintf(output, "\t\t\tconverted := %s(value)\n", field.GoType)
	case "int":
		fmt.Fprintf(
			output,
			"\t\t\tconverted, err := filterLookupInt(%s, %s, operator, value)\n",
			strconv.Quote(modelName),
			strconv.Quote(field.JSON),
		)
		output.WriteString("\t\t\tif err != nil {\n")
		fmt.Fprintf(output, "\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
		output.WriteString("\t\t\t}\n")
	case "bool":
		fmt.Fprintf(
			output,
			"\t\t\tconverted, err := filterLookupBool(%s, %s, operator, value)\n",
			strconv.Quote(modelName),
			strconv.Quote(field.JSON),
		)
		output.WriteString("\t\t\tif err != nil {\n")
		fmt.Fprintf(output, "\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
		output.WriteString("\t\t\t}\n")
	}
	fmt.Fprintf(
		output,
		"\t\t\treturn %sFilter{%s: &converted}, false, nil\n",
		model.Name,
		field.Name,
	)

	if fieldHasOperator(field, "in") {
		output.WriteString("\t\tcase \"in\", \"nin\":\n")
		fmt.Fprintf(
			output,
			"\t\t\traw, err := filterLookupMany(%s, %s, operator, values)\n",
			strconv.Quote(modelName),
			strconv.Quote(field.JSON),
		)
		output.WriteString("\t\t\tif err != nil {\n")
		fmt.Fprintf(output, "\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
		output.WriteString("\t\t\t}\n")
		fmt.Fprintf(output, "\t\t\tconverted := make([]%s, len(raw))\n", field.GoType)
		output.WriteString("\t\t\tfor index := range raw {\n")
		if field.Kind == "int" {
			fmt.Fprintf(
				output,
				"\t\t\t\tconverted[index], err = filterLookupInt(%s, %s, operator, raw[index])\n",
				strconv.Quote(modelName),
				strconv.Quote(field.JSON),
			)
			output.WriteString("\t\t\t\tif err != nil {\n")
			fmt.Fprintf(output, "\t\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
			output.WriteString("\t\t\t\t}\n")
		} else {
			fmt.Fprintf(output, "\t\t\t\tconverted[index] = %s(raw[index])\n", field.GoType)
		}
		output.WriteString("\t\t\t}\n")
		fmt.Fprintf(
			output,
			"\t\t\treturn %sFilter{%sIn: converted}, operator == \"nin\", nil\n",
			model.Name,
			field.Name,
		)
	}
	if fieldHasOperator(field, "contains") {
		output.WriteString("\t\tcase \"contains\":\n")
		renderLookupStringValue(output, model, field, "Contains", "value")
	}
	if fieldHasOperator(field, "regex") {
		for _, pattern := range []struct {
			operator   string
			expression string
		}{
			{"iexact", "`(?i)^` + regexp.QuoteMeta(value) + `$`"},
			{"icontains", "`(?i)` + regexp.QuoteMeta(value)"},
			{"startswith", "`^` + regexp.QuoteMeta(value)"},
			{"istartswith", "`(?i)^` + regexp.QuoteMeta(value)"},
			{"endswith", "regexp.QuoteMeta(value) + `$`"},
			{"iendswith", "`(?i)` + regexp.QuoteMeta(value) + `$`"},
			{"regex", "value"},
			{"iregex", "`(?i)(?:` + value + `)`"},
		} {
			fmt.Fprintf(output, "\t\tcase %s:\n", strconv.Quote(pattern.operator))
			renderLookupStringValue(output, model, field, "Regex", pattern.expression)
		}
	}
	output.WriteString("\t\tdefault:\n")
	fmt.Fprintf(
		output,
		"\t\t\treturn %sFilter{}, false, invalidFilter(%s, operator)\n",
		model.Name,
		strconv.Quote(modelName+" lookup field "+field.JSON+" does not support operator %q"),
	)
	output.WriteString("\t\t}\n")
}

func renderLookupStringValue(
	output *bytes.Buffer,
	model modelSpec,
	field fieldSpec,
	suffix string,
	expression string,
) {
	modelName := strings.ToLower(model.Name)
	fmt.Fprintf(
		output,
		"\t\t\tvalue, err := filterLookupOne(%s, %s, operator, values)\n",
		strconv.Quote(modelName),
		strconv.Quote(field.JSON),
	)
	output.WriteString("\t\t\tif err != nil {\n")
	fmt.Fprintf(output, "\t\t\t\treturn %sFilter{}, false, err\n", model.Name)
	output.WriteString("\t\t\t}\n")
	if suffix == "Contains" {
		fmt.Fprintf(
			output,
			"\t\t\treturn %sFilter{%sContains: &value}, false, nil\n",
			model.Name,
			field.Name,
		)
		return
	}
	fmt.Fprintf(output, "\t\t\tpattern := %s\n", expression)
	fmt.Fprintf(
		output,
		"\t\t\treturn %sFilter{%sRegex: pattern}, false, nil\n",
		model.Name,
		field.Name,
	)
}

func renderValidationState(output *bytes.Buffer, spec filterSpec) {
	output.WriteString("type filterValidationState struct {\n")
	for _, model := range spec.Models {
		fmt.Fprintf(output, "\t%s map[*%sFilter]struct{}\n", validationMapName(model.Name), model.Name)
	}
	output.WriteString("}\n\n")
	output.WriteString("func newFilterValidationState() filterValidationState {\n")
	output.WriteString("\treturn filterValidationState{\n")
	for _, model := range spec.Models {
		fmt.Fprintf(output, "\t\t%s: make(map[*%sFilter]struct{}),\n", validationMapName(model.Name), model.Name)
	}
	output.WriteString("\t}\n")
	output.WriteString("}\n\n")
}

func renderValidation(output *bytes.Buffer, model modelSpec) {
	output.WriteString("// Validate checks structure, regular expressions, and contradictory criteria before filter use.\n")
	output.WriteString("// Invalid filters return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
	fmt.Fprintf(output, "func (filter %sFilter) Validate() error {\n", model.Name)
	output.WriteString("\tstate := newFilterValidationState()\n")
	output.WriteString("\tif err := (&filter).validateStructure(&state, 0); err != nil {\n")
	output.WriteString("\t\treturn err\n")
	output.WriteString("\t}\n")
	output.WriteString("\tif err := (&filter).validatePatterns(); err != nil {\n")
	output.WriteString("\t\treturn err\n")
	output.WriteString("\t}\n")
	output.WriteString("\treturn (&filter).validateCriteria()\n")
	output.WriteString("}\n\n")

	fmt.Fprintf(
		output,
		"func (filter *%sFilter) validateStructure(state *filterValidationState, depth int) error {\n",
		model.Name,
	)
	output.WriteString("\tif depth > maxFilterDepth {\n")
	fmt.Fprintf(output, "\t\treturn invalidFilter(%s)\n", strconv.Quote(strings.ToLower(model.Name)+" filter exceeds maximum depth"))
	output.WriteString("\t}\n")
	mapName := validationMapName(model.Name)
	fmt.Fprintf(output, "\tif _, found := state.%s[filter]; found {\n", mapName)
	fmt.Fprintf(output, "\t\treturn invalidFilter(%s)\n", strconv.Quote(strings.ToLower(model.Name)+" filter contains a cycle"))
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\tstate.%s[filter] = struct{}{}\n", mapName)
	fmt.Fprintf(output, "\tdefer delete(state.%s, filter)\n", mapName)

	for _, field := range model.Fields {
		if fieldHasOperator(field, "in") {
			fmt.Fprintf(output, "\tif filter.%sIn != nil && len(filter.%sIn) == 0 {\n", field.Name, field.Name)
			fmt.Fprintf(output, "\t\treturn invalidFilter(%s)\n", strconv.Quote(field.JSON+"In must not be empty"))
			output.WriteString("\t}\n")
		}
	}
	output.WriteString("\tif filter.AnyOf != nil && len(filter.AnyOf) == 0 {\n")
	output.WriteString("\t\treturn invalidFilter(\"anyOf must not be empty\")\n")
	output.WriteString("\t}\n")
	output.WriteString("\tfor index := range filter.AnyOf {\n")
	output.WriteString("\t\tif err := filter.AnyOf[index].validateStructure(state, depth+1); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	output.WriteString("\tif filter.Not != nil {\n")
	output.WriteString("\t\tif err := filter.Not.validateStructure(state, depth+1); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")

	for _, relation := range model.Relations {
		fmt.Fprintf(output, "\tif filter.%s != nil {\n", relation.Name)
		if relation.Cardinality == "one" {
			fmt.Fprintf(output, "\t\tif err := filter.%s.validateStructure(state, depth+1); err != nil {\n", relation.Name)
			output.WriteString("\t\t\treturn err\n")
			output.WriteString("\t\t}\n")
		} else {
			fmt.Fprintf(
				output,
				"\t\tif filter.%s.Some == nil && filter.%s.Every == nil && filter.%s.None == nil {\n",
				relation.Name,
				relation.Name,
				relation.Name,
			)
			fmt.Fprintf(output, "\t\t\treturn invalidFilter(%s)\n", strconv.Quote(relation.JSON+" requires some, every, or none"))
			output.WriteString("\t\t}\n")
			for _, quantifier := range []string{"Some", "Every", "None"} {
				fmt.Fprintf(output, "\t\tif filter.%s.%s != nil {\n", relation.Name, quantifier)
				fmt.Fprintf(output, "\t\t\tif err := filter.%s.%s.validateStructure(state, depth+1); err != nil {\n", relation.Name, quantifier)
				output.WriteString("\t\t\t\treturn err\n")
				output.WriteString("\t\t\t}\n")
				output.WriteString("\t\t}\n")
			}
		}
		output.WriteString("\t}\n")
	}
	output.WriteString("\treturn nil\n")
	output.WriteString("}\n\n")

	fmt.Fprintf(output, "func (filter *%sFilter) validatePatterns() error {\n", model.Name)
	for _, field := range model.Fields {
		if fieldHasOperator(field, "regex") {
			fmt.Fprintf(output, "\tif filter.%sRegex != \"\" {\n", field.Name)
			fmt.Fprintf(output, "\t\tif _, err := regexp.Compile(filter.%sRegex); err != nil {\n", field.Name)
			fmt.Fprintf(output, "\t\t\treturn invalidFilter(%s, err)\n", strconv.Quote(field.JSON+"Regex: %v"))
			output.WriteString("\t\t}\n")
			output.WriteString("\t}\n")
		}
	}
	output.WriteString("\tfor index := range filter.AnyOf {\n")
	output.WriteString("\t\tif err := filter.AnyOf[index].validatePatterns(); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	output.WriteString("\tif filter.Not != nil {\n")
	output.WriteString("\t\tif err := filter.Not.validatePatterns(); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	for _, relation := range model.Relations {
		fmt.Fprintf(output, "\tif filter.%s != nil {\n", relation.Name)
		if relation.Cardinality == "one" {
			fmt.Fprintf(output, "\t\tif err := filter.%s.validatePatterns(); err != nil {\n", relation.Name)
			output.WriteString("\t\t\treturn err\n")
			output.WriteString("\t\t}\n")
		} else {
			for _, quantifier := range []string{"Some", "Every", "None"} {
				fmt.Fprintf(output, "\t\tif filter.%s.%s != nil {\n", relation.Name, quantifier)
				fmt.Fprintf(output, "\t\t\tif err := filter.%s.%s.validatePatterns(); err != nil {\n", relation.Name, quantifier)
				output.WriteString("\t\t\t\treturn err\n")
				output.WriteString("\t\t\t}\n")
				output.WriteString("\t\t}\n")
			}
		}
		output.WriteString("\t}\n")
	}
	output.WriteString("\treturn nil\n")
	output.WriteString("}\n\n")

	fmt.Fprintf(output, "func (filter *%sFilter) validateCriteria() error {\n", model.Name)
	for _, field := range model.Fields {
		renderCriteriaValidation(output, field)
	}
	output.WriteString("\tif filter.Not != nil {\n")
	output.WriteString("\t\tpositive := *filter\n")
	output.WriteString("\t\tpositive.Not = nil\n")
	fmt.Fprintf(
		output,
		"\t\tif filter.Not.isZero() || filter%sEqual(&positive, filter.Not) {\n",
		model.Name,
	)
	fmt.Fprintf(
		output,
		"\t\t\treturn invalidFilter(%s)\n",
		strconv.Quote(strings.ToLower(model.Name)+" criteria negate every possible match"),
	)
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	for _, relation := range model.Relations {
		if relation.Cardinality != "many" {
			continue
		}
		fmt.Fprintf(output, "\tif filter.%s != nil && filter.%s.Some != nil && filter.%s.None != nil {\n", relation.Name, relation.Name, relation.Name)
		fmt.Fprintf(
			output,
			"\t\tif filter.%s.None.isZero() || filter%sEqual(filter.%s.Some, filter.%s.None) {\n",
			relation.Name,
			relation.Target,
			relation.Name,
			relation.Name,
		)
		fmt.Fprintf(
			output,
			"\t\t\treturn invalidFilter(%s)\n",
			strconv.Quote(relation.JSON+" relation requires and forbids the same match"),
		)
		output.WriteString("\t\t}\n")
		output.WriteString("\t}\n")
		fmt.Fprintf(output, "\tif filter.%s != nil && filter.%s.Some != nil && filter.%s.Every != nil && filter.%s.None != nil {\n", relation.Name, relation.Name, relation.Name, relation.Name)
		fmt.Fprintf(
			output,
			"\t\tif filter%sEqual(filter.%s.Every, filter.%s.None) {\n",
			relation.Target,
			relation.Name,
			relation.Name,
		)
		fmt.Fprintf(
			output,
			"\t\t\treturn invalidFilter(%s)\n",
			strconv.Quote(relation.JSON+" relation cannot be nonempty while every value is forbidden"),
		)
		output.WriteString("\t\t}\n")
		output.WriteString("\t}\n")
	}
	output.WriteString("\tfor index := range filter.AnyOf {\n")
	output.WriteString("\t\tif err := filter.AnyOf[index].validateCriteria(); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	output.WriteString("\tif filter.Not != nil {\n")
	output.WriteString("\t\tif err := filter.Not.validateCriteria(); err != nil {\n")
	output.WriteString("\t\t\treturn err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t}\n")
	for _, relation := range model.Relations {
		fmt.Fprintf(output, "\tif filter.%s != nil {\n", relation.Name)
		if relation.Cardinality == "one" {
			fmt.Fprintf(output, "\t\tif err := filter.%s.validateCriteria(); err != nil {\n", relation.Name)
			output.WriteString("\t\t\treturn err\n")
			output.WriteString("\t\t}\n")
		} else {
			for _, quantifier := range []string{"Some", "Every", "None"} {
				fmt.Fprintf(output, "\t\tif filter.%s.%s != nil {\n", relation.Name, quantifier)
				fmt.Fprintf(output, "\t\t\tif err := filter.%s.%s.validateCriteria(); err != nil {\n", relation.Name, quantifier)
				output.WriteString("\t\t\t\treturn err\n")
				output.WriteString("\t\t\t}\n")
				output.WriteString("\t\t}\n")
			}
		}
		output.WriteString("\t}\n")
	}
	output.WriteString("\treturn nil\n")
	output.WriteString("}\n\n")
}

func renderCriteriaValidation(output *bytes.Buffer, field fieldSpec) {
	message := strconv.Quote(field.JSON + " criteria cannot match any value")
	switch field.Kind {
	case "string":
		if object, found := stableFilterObject(field.GoType); found {
			fmt.Fprintf(
				output,
				"\tif !filterStableCriteriaValid(%s, filter.%s, filter.%sIn) {\n",
				strconv.Quote(object),
				field.Name,
				field.Name,
			)
			fmt.Fprintf(output, "\t\treturn invalidFilter(%s)\n", message)
			output.WriteString("\t}\n")
		}
		if fieldHasOperator(field, "contains") || fieldHasOperator(field, "regex") {
			contains := "nil"
			if fieldHasOperator(field, "contains") {
				contains = "filter." + field.Name + "Contains"
			}
			pattern := `""`
			if fieldHasOperator(field, "regex") {
				pattern = "filter." + field.Name + "Regex"
			}
			fmt.Fprintf(
				output,
				"\tif !filterTextCriteriaPossible(filter.%s, filter.%sIn, %s, %s) {\n",
				field.Name,
				field.Name,
				contains,
				pattern,
			)
		} else if fieldHasOperator(field, "in") {
			fmt.Fprintf(
				output,
				"\tif !filterExactInPossible(filter.%s, filter.%sIn) {\n",
				field.Name,
				field.Name,
			)
		} else {
			return
		}
	case "int":
		minimum, hasMinimum := 0, false
		if field.Minimum != nil {
			minimum, hasMinimum = *field.Minimum, true
		}
		fmt.Fprintf(
			output,
			"\tif !filterIntCriteriaPossible(filter.%s, filter.%sIn, filter.%sGT, filter.%sGTE, filter.%sLT, filter.%sLTE, %d, %t) {\n",
			field.Name,
			field.Name,
			field.Name,
			field.Name,
			field.Name,
			field.Name,
			minimum,
			hasMinimum,
		)
	default:
		return
	}
	fmt.Fprintf(output, "\t\treturn invalidFilter(%s)\n", message)
	output.WriteString("\t}\n")
}

func stableFilterObject(goType string) (string, bool) {
	object, found := map[string]string{
		"SessionID":  "session",
		"WindowID":   "window",
		"PaneID":     "pane",
		"ClientName": "client",
	}[goType]
	return object, found
}

func renderFilterMarshal(output *bytes.Buffer, model modelSpec) {
	fmt.Fprintf(output, "// MarshalJSON validates the %s filter and encodes its JSON wire object.\n", strings.ToLower(model.Name))
	output.WriteString("// [FilterSchemaVersion] remains external metadata and is not embedded in the object.\n")
	output.WriteString("// Invalid filters return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
	fmt.Fprintf(output, "func (filter %sFilter) MarshalJSON() ([]byte, error) {\n", model.Name)
	output.WriteString("\tif err := filter.Validate(); err != nil {\n")
	output.WriteString("\t\treturn nil, err\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\ttype wire%sFilter %sFilter\n", model.Name, model.Name)
	fmt.Fprintf(output, "\treturn json.Marshal(wire%sFilter(filter))\n", model.Name)
	output.WriteString("}\n\n")
}

func renderRelationMarshal(output *bytes.Buffer, target string) {
	fmt.Fprintf(output, "func (relation %sRel) validate() error {\n", target)
	output.WriteString("\tif relation.Some == nil && relation.Every == nil && relation.None == nil {\n")
	fmt.Fprintf(
		output,
		"\t\treturn invalidFilter(%s)\n",
		strconv.Quote(strings.ToLower(target)+" relation requires some, every, or none"),
	)
	output.WriteString("\t}\n")
	for _, quantifier := range []string{"Some", "Every", "None"} {
		fmt.Fprintf(output, "\tif relation.%s != nil {\n", quantifier)
		fmt.Fprintf(output, "\t\tif err := relation.%s.Validate(); err != nil {\n", quantifier)
		output.WriteString("\t\t\treturn err\n")
		output.WriteString("\t\t}\n")
		output.WriteString("\t}\n")
	}
	output.WriteString("\treturn nil\n")
	output.WriteString("}\n\n")

	fmt.Fprintf(output, "// MarshalJSON validates and encodes the %s relation quantifiers as JSON.\n", strings.ToLower(target))
	output.WriteString("// [FilterSchemaVersion] remains external metadata and is not embedded in the object.\n")
	output.WriteString("// The zero relation returns [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect it.\n")
	fmt.Fprintf(output, "func (relation %sRel) MarshalJSON() ([]byte, error) {\n", target)
	output.WriteString("\tif err := relation.validate(); err != nil {\n")
	output.WriteString("\t\treturn nil, err\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\ttype wire%sRel %sRel\n", target, target)
	fmt.Fprintf(output, "\treturn json.Marshal(wire%sRel(relation))\n", target)
	output.WriteString("}\n\n")
}

func renderCompiler(output *bytes.Buffer, model modelSpec) {
	fmt.Fprintf(output, "// Predicate validates the %s filter and returns a local predicate accepting [%s] values already materialized by a [Snapshot]; it never runs tmux.\n", strings.ToLower(model.Name), model.Name)
	output.WriteString("// Relation criteria traverse only relationships already materialized on that candidate.\n")
	output.WriteString("// The predicate returns false for a nil candidate. Invalid filters return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
	fmt.Fprintf(output, "func (filter %sFilter) Predicate() (func(*%s) bool, error) {\n", model.Name, model.Name)
	output.WriteString("\tif err := filter.Validate(); err != nil {\n")
	output.WriteString("\t\treturn nil, err\n")
	output.WriteString("\t}\n")
	output.WriteString("\treturn (&filter).compile()\n")
	output.WriteString("}\n\n")

	fmt.Fprintf(output, "func (filter *%sFilter) compile() (func(*%s) bool, error) {\n", model.Name, model.Name)
	for _, field := range model.Fields {
		renderCompiledFieldSetup(output, field)
	}
	fmt.Fprintf(output, "\tanyOf := make([]func(*%s) bool, len(filter.AnyOf))\n", model.Name)
	output.WriteString("\tfor index := range filter.AnyOf {\n")
	output.WriteString("\t\tpredicate, err := filter.AnyOf[index].compile()\n")
	output.WriteString("\t\tif err != nil {\n")
	output.WriteString("\t\t\treturn nil, err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t\tanyOf[index] = predicate\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\tvar not func(*%s) bool\n", model.Name)
	output.WriteString("\tif filter.Not != nil {\n")
	output.WriteString("\t\tpredicate, err := filter.Not.compile()\n")
	output.WriteString("\t\tif err != nil {\n")
	output.WriteString("\t\t\treturn nil, err\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t\tnot = predicate\n")
	output.WriteString("\t}\n")
	for _, relation := range model.Relations {
		renderCompiledRelationSetup(output, relation)
	}

	fmt.Fprintf(output, "\treturn func(value *%s) bool {\n", model.Name)
	output.WriteString("\t\tif value == nil {\n")
	output.WriteString("\t\t\treturn false\n")
	output.WriteString("\t\t}\n")
	for _, field := range model.Fields {
		renderFieldMatch(output, field)
	}
	output.WriteString("\t\tif len(anyOf) != 0 {\n")
	output.WriteString("\t\t\tmatched := false\n")
	output.WriteString("\t\t\tfor _, predicate := range anyOf {\n")
	output.WriteString("\t\t\t\tif predicate(value) {\n")
	output.WriteString("\t\t\t\t\tmatched = true\n")
	output.WriteString("\t\t\t\t\tbreak\n")
	output.WriteString("\t\t\t\t}\n")
	output.WriteString("\t\t\t}\n")
	output.WriteString("\t\t\tif !matched {\n")
	output.WriteString("\t\t\t\treturn false\n")
	output.WriteString("\t\t\t}\n")
	output.WriteString("\t\t}\n")
	output.WriteString("\t\tif not != nil && not(value) {\n")
	output.WriteString("\t\t\treturn false\n")
	output.WriteString("\t\t}\n")
	for _, relation := range model.Relations {
		renderRelationMatch(output, relation)
	}
	output.WriteString("\t\treturn true\n")
	output.WriteString("\t}, nil\n")
	output.WriteString("}\n\n")
}

func renderCompiledFieldSetup(output *bytes.Buffer, field fieldSpec) {
	base := lowerFirst(field.Name)
	for _, operator := range field.Operators {
		switch operator {
		case "exact":
			fmt.Fprintf(output, "\t%sExactSet := filter.%s != nil\n", base, field.Name)
			fmt.Fprintf(output, "\tvar %sExact %s\n", base, field.GoType)
			fmt.Fprintf(output, "\tif %sExactSet {\n", base)
			fmt.Fprintf(output, "\t\t%sExact = *filter.%s\n", base, field.Name)
			output.WriteString("\t}\n")
		case "in":
			fmt.Fprintf(output, "\t%sInSet := filter.%sIn != nil\n", base, field.Name)
			fmt.Fprintf(output, "\t%sIn := filterSet(filter.%sIn)\n", base, field.Name)
		case "contains":
			fmt.Fprintf(output, "\t%sContainsSet := filter.%sContains != nil\n", base, field.Name)
			fmt.Fprintf(output, "\tvar %sContains string\n", base)
			fmt.Fprintf(output, "\tif %sContainsSet {\n", base)
			fmt.Fprintf(output, "\t\t%sContains = *filter.%sContains\n", base, field.Name)
			output.WriteString("\t}\n")
		case "regex":
			fmt.Fprintf(output, "\tvar %sRegex *regexp.Regexp\n", base)
			fmt.Fprintf(output, "\tif filter.%sRegex != \"\" {\n", field.Name)
			output.WriteString("\t\tvar err error\n")
			fmt.Fprintf(output, "\t\t%sRegex, err = regexp.Compile(filter.%sRegex)\n", base, field.Name)
			output.WriteString("\t\tif err != nil {\n")
			fmt.Fprintf(output, "\t\t\treturn nil, invalidFilter(%s, err)\n", strconv.Quote(field.JSON+"Regex: %v"))
			output.WriteString("\t\t}\n")
			output.WriteString("\t}\n")
		case "gt", "gte", "lt", "lte":
			suffix := operatorGoSuffix(operator)
			local := base + suffix
			fmt.Fprintf(output, "\t%sSet := filter.%s%s != nil\n", local, field.Name, suffix)
			fmt.Fprintf(output, "\tvar %s %s\n", local, field.GoType)
			fmt.Fprintf(output, "\tif %sSet {\n", local)
			fmt.Fprintf(output, "\t\t%s = *filter.%s%s\n", local, field.Name, suffix)
			output.WriteString("\t}\n")
		}
	}
}

func renderCompiledRelationSetup(output *bytes.Buffer, relation relationSpec) {
	base := lowerFirst(relation.Name)
	if relation.Cardinality == "one" {
		fmt.Fprintf(output, "\tvar %sPredicate func(*%s) bool\n", base, relation.Target)
		fmt.Fprintf(output, "\tif filter.%s != nil {\n", relation.Name)
		fmt.Fprintf(output, "\t\tpredicate, err := filter.%s.compile()\n", relation.Name)
		output.WriteString("\t\tif err != nil {\n")
		output.WriteString("\t\t\treturn nil, err\n")
		output.WriteString("\t\t}\n")
		fmt.Fprintf(output, "\t\t%sPredicate = predicate\n", base)
		output.WriteString("\t}\n")
		return
	}
	for _, quantifier := range []string{"Some", "Every", "None"} {
		local := base + quantifier
		fmt.Fprintf(output, "\tvar %s func(*%s) bool\n", local, relation.Target)
		fmt.Fprintf(output, "\tif filter.%s != nil && filter.%s.%s != nil {\n", relation.Name, relation.Name, quantifier)
		fmt.Fprintf(output, "\t\tpredicate, err := filter.%s.%s.compile()\n", relation.Name, quantifier)
		output.WriteString("\t\tif err != nil {\n")
		output.WriteString("\t\t\treturn nil, err\n")
		output.WriteString("\t\t}\n")
		fmt.Fprintf(output, "\t\t%s = predicate\n", local)
		output.WriteString("\t}\n")
	}
}

func renderFieldMatch(output *bytes.Buffer, field fieldSpec) {
	base := lowerFirst(field.Name)
	fmt.Fprintf(output, "\t\tif %s {\n", fieldEnabledExpression(field))
	switch field.Source.Kind {
	case "field":
		fmt.Fprintf(output, "\t\t\t%sValue := value.%s\n", base, field.Source.Name)
	case "method":
		fmt.Fprintf(output, "\t\t\t%sValue := value.%s()\n", base, field.Source.Name)
	case "field-string":
		fmt.Fprintf(output, "\t\t\t%sValue := string(value.%s)\n", base, field.Source.Name)
	case "format-string":
		fmt.Fprintf(output, "\t\t\t%sValue, %sAvailable := value.%s()\n", base, base, field.Source.Name)
		fmt.Fprintf(output, "\t\t\tif !%sAvailable {\n", base)
		output.WriteString("\t\t\t\treturn false\n")
		output.WriteString("\t\t\t}\n")
	case "format-bool":
		fmt.Fprintf(output, "\t\t\t%sValue, %sAvailable := value.%s()\n", base, base, field.Source.Name)
		fmt.Fprintf(output, "\t\t\tif !%sAvailable {\n", base)
		output.WriteString("\t\t\t\treturn false\n")
		output.WriteString("\t\t\t}\n")
	case "format-int-bool":
		fmt.Fprintf(output, "\t\t\t%sRaw, %sAvailable := value.%s()\n", base, base, field.Source.Name)
		fmt.Fprintf(output, "\t\t\t%sValue := %sRaw != 0\n", base, base)
		fmt.Fprintf(output, "\t\t\tif !%sAvailable {\n", base)
		output.WriteString("\t\t\t\treturn false\n")
		output.WriteString("\t\t\t}\n")
	}
	for _, operator := range field.Operators {
		switch operator {
		case "exact":
			fmt.Fprintf(output, "\t\t\tif %sExactSet && %sValue != %sExact {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "in":
			fmt.Fprintf(output, "\t\t\tif %sInSet {\n", base)
			fmt.Fprintf(output, "\t\t\t\tif _, found := %sIn[%sValue]; !found {\n", base, base)
			output.WriteString("\t\t\t\t\treturn false\n")
			output.WriteString("\t\t\t\t}\n")
			output.WriteString("\t\t\t}\n")
		case "contains":
			fmt.Fprintf(output, "\t\t\tif %sContainsSet && !strings.Contains(string(%sValue), %sContains) {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "regex":
			fmt.Fprintf(output, "\t\t\tif %sRegex != nil && !%sRegex.MatchString(string(%sValue)) {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "gt":
			fmt.Fprintf(output, "\t\t\tif %sGTSet && %sValue <= %sGT {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "gte":
			fmt.Fprintf(output, "\t\t\tif %sGTESet && %sValue < %sGTE {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "lt":
			fmt.Fprintf(output, "\t\t\tif %sLTSet && %sValue >= %sLT {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		case "lte":
			fmt.Fprintf(output, "\t\t\tif %sLTESet && %sValue > %sLTE {\n", base, base, base)
			output.WriteString("\t\t\t\treturn false\n")
			output.WriteString("\t\t\t}\n")
		}
	}
	output.WriteString("\t\t}\n")
}

func renderRelationMatch(output *bytes.Buffer, relation relationSpec) {
	base := lowerFirst(relation.Name)
	if relation.Cardinality == "one" {
		fmt.Fprintf(output, "\t\tif %sPredicate != nil {\n", base)
		fmt.Fprintf(output, "\t\t\trelated, found := value.%s()\n", relation.Method)
		fmt.Fprintf(output, "\t\t\tif !found || !%sPredicate(&related) {\n", base)
		output.WriteString("\t\t\t\treturn false\n")
		output.WriteString("\t\t\t}\n")
		output.WriteString("\t\t}\n")
		return
	}
	fmt.Fprintf(output, "\t\tif %sSome != nil || %sEvery != nil || %sNone != nil {\n", base, base, base)
	// A record carrying no relations cannot answer a relation criterion, so it
	// does not match one. That is what the to-one branch above already does
	// with its found result, and an empty slice would instead report a session
	// with no windows, which tmux does not have.
	fmt.Fprintf(output, "\t\t\trelated, found := value.%s()\n", relation.Method)
	output.WriteString("\t\t\tif !found {\n")
	output.WriteString("\t\t\t\treturn false\n")
	output.WriteString("\t\t\t}\n")
	fmt.Fprintf(output, "\t\t\tif %sSome != nil {\n", base)
	output.WriteString("\t\t\t\tmatched := false\n")
	output.WriteString("\t\t\t\tfor index := range related {\n")
	fmt.Fprintf(output, "\t\t\t\t\tif %sSome(&related[index]) {\n", base)
	output.WriteString("\t\t\t\t\t\tmatched = true\n")
	output.WriteString("\t\t\t\t\t\tbreak\n")
	output.WriteString("\t\t\t\t\t}\n")
	output.WriteString("\t\t\t\t}\n")
	output.WriteString("\t\t\t\tif !matched {\n")
	output.WriteString("\t\t\t\t\treturn false\n")
	output.WriteString("\t\t\t\t}\n")
	output.WriteString("\t\t\t}\n")
	fmt.Fprintf(output, "\t\t\tif %sEvery != nil {\n", base)
	output.WriteString("\t\t\t\tfor index := range related {\n")
	fmt.Fprintf(output, "\t\t\t\t\tif !%sEvery(&related[index]) {\n", base)
	output.WriteString("\t\t\t\t\t\treturn false\n")
	output.WriteString("\t\t\t\t\t}\n")
	output.WriteString("\t\t\t\t}\n")
	output.WriteString("\t\t\t}\n")
	fmt.Fprintf(output, "\t\t\tif %sNone != nil {\n", base)
	output.WriteString("\t\t\t\tfor index := range related {\n")
	fmt.Fprintf(output, "\t\t\t\t\tif %sNone(&related[index]) {\n", base)
	output.WriteString("\t\t\t\t\t\treturn false\n")
	output.WriteString("\t\t\t\t\t}\n")
	output.WriteString("\t\t\t\t}\n")
	output.WriteString("\t\t\t}\n")
	output.WriteString("\t\t}\n")
}

func renderFilterUnmarshal(output *bytes.Buffer, model modelSpec) {
	fmt.Fprintf(output, "// UnmarshalJSON clears the receiver, then decodes a strict %s filter JSON object.\n", strings.ToLower(model.Name))
	output.WriteString("// [FilterSchemaVersion] remains external metadata and is not embedded in the object.\n")
	output.WriteString("// It rejects unknown or duplicate fields and trailing JSON, then validates decoded criteria.\n")
	output.WriteString("// On error, the receiver can retain a partial or complete decoded value.\n")
	output.WriteString("// All decode and framing failures and semantic validation failures return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
	fmt.Fprintf(output, "func (filter *%sFilter) UnmarshalJSON(data []byte) error {\n", model.Name)
	output.WriteString("\tif filter == nil {\n")
	output.WriteString("\t\treturn invalidFilter(\"cannot decode into a nil filter\")\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\t*filter = %sFilter{}\n", model.Name)
	output.WriteString("\tif err := decodeStrictFilterObject(data, map[string]func(json.RawMessage) error{\n")
	for _, field := range model.Fields {
		for _, generated := range generatedFields(field) {
			fmt.Fprintf(
				output,
				"\t\t%s: func(raw json.RawMessage) error { return json.Unmarshal(raw, &filter.%s) },\n",
				strconv.Quote(generated.JSONName),
				generated.GoName,
			)
		}
	}
	fmt.Fprintf(output, "\t\t\"anyOf\": func(raw json.RawMessage) error { return json.Unmarshal(raw, &filter.AnyOf) },\n")
	fmt.Fprintf(output, "\t\t\"not\": func(raw json.RawMessage) error { return json.Unmarshal(raw, &filter.Not) },\n")
	for _, relation := range model.Relations {
		fmt.Fprintf(
			output,
			"\t\t%s: func(raw json.RawMessage) error { return json.Unmarshal(raw, &filter.%s) },\n",
			strconv.Quote(relation.JSON),
			relation.Name,
		)
	}
	output.WriteString("\t}); err != nil {\n")
	output.WriteString("\t\treturn err\n")
	output.WriteString("\t}\n")
	output.WriteString("\treturn filter.Validate()\n")
	output.WriteString("}\n\n")
}

func renderRelationUnmarshal(output *bytes.Buffer, target string) {
	fmt.Fprintf(output, "// UnmarshalJSON clears the receiver, then decodes strict %s relation quantifiers.\n", strings.ToLower(target))
	output.WriteString("// [FilterSchemaVersion] remains external metadata and is not embedded in the object.\n")
	output.WriteString("// It rejects unknown or duplicate fields and trailing JSON, then validates decoded criteria.\n")
	output.WriteString("// On error, the receiver can retain a partial or complete decoded value.\n")
	output.WriteString("// All decode and framing failures and semantic validation failures return [ErrInvalidFilter]; use errors.Is(err, [ErrInvalidFilter]) to detect them.\n")
	fmt.Fprintf(output, "func (relation *%sRel) UnmarshalJSON(data []byte) error {\n", target)
	output.WriteString("\tif relation == nil {\n")
	output.WriteString("\t\treturn invalidFilter(\"cannot decode into a nil relation\")\n")
	output.WriteString("\t}\n")
	fmt.Fprintf(output, "\t*relation = %sRel{}\n", target)
	output.WriteString("\tif err := decodeStrictFilterObject(data, map[string]func(json.RawMessage) error{\n")
	for _, quantifier := range []struct {
		goName   string
		jsonName string
	}{{"Some", "some"}, {"Every", "every"}, {"None", "none"}} {
		fmt.Fprintf(
			output,
			"\t\t%s: func(raw json.RawMessage) error { return json.Unmarshal(raw, &relation.%s) },\n",
			strconv.Quote(quantifier.jsonName),
			quantifier.goName,
		)
	}
	output.WriteString("\t}); err != nil {\n")
	output.WriteString("\t\treturn err\n")
	output.WriteString("\t}\n")
	output.WriteString("\treturn relation.validate()\n")
	output.WriteString("}\n\n")
}

func generatedFields(field fieldSpec) []generatedField {
	result := make([]generatedField, 0, len(field.Operators))
	for _, operator := range field.Operators {
		goType := "*" + field.GoType
		switch operator {
		case "in":
			goType = "[]" + field.GoType
		case "contains":
			goType = "*string"
		case "regex":
			goType = "string"
		}
		result = append(result, generatedField{
			GoName:   field.Name + operatorGoSuffix(operator),
			JSONName: field.JSON + operatorJSONSuffix(operator),
			GoType:   goType,
			Operator: operator,
		})
	}
	return result
}

func generatedFieldDoc(field fieldSpec, generated generatedField) string {
	criterion := generated.GoName
	description := stripDocumentationLinks(strings.TrimSuffix(strings.TrimSpace(field.Description), "."))
	pointerState := fmt.Sprintf(
		"A nil pointer leaves %s unset; a non-nil pointer applies it, including when it points to the zero value.",
		criterion,
	)
	switch generated.Operator {
	case "exact":
		return fmt.Sprintf("%s exactly matches %s. %s", criterion, description, pointerState)
	case "in":
		return fmt.Sprintf(
			"%s lists accepted values for %s. A candidate matches when its materialized value equals one listed value. A nil slice leaves %s unset; a non-nil empty slice is invalid.",
			criterion,
			description,
			criterion,
		)
	case "contains":
		return fmt.Sprintf(
			"%s requires %s to contain the pointed-to substring. A nil pointer leaves %s unset; a non-nil pointer applies it, and an empty string matches every available string.",
			criterion,
			description,
			criterion,
		)
	case "regex":
		return fmt.Sprintf(
			"%s requires %s to match Go regular expression syntax. An empty string leaves %s unset.",
			criterion,
			description,
			criterion,
		)
	case "gt":
		return fmt.Sprintf("%s requires %s to be strictly greater than the pointed-to value. %s", criterion, description, pointerState)
	case "gte":
		return fmt.Sprintf("%s requires %s to be greater than or equal to the pointed-to value. %s", criterion, description, pointerState)
	case "lt":
		return fmt.Sprintf("%s requires %s to be strictly less than the pointed-to value. %s", criterion, description, pointerState)
	case "lte":
		return fmt.Sprintf("%s requires %s to be less than or equal to the pointed-to value. %s", criterion, description, pointerState)
	default:
		panic("unsupported filter operator " + generated.Operator)
	}
}

func filterDocumentationLinks(model modelSpec) []string {
	seen := make(map[string]bool)
	var links []string
	descriptions := make([]string, 0, len(model.Fields)+len(model.Relations))
	for _, field := range model.Fields {
		descriptions = append(descriptions, field.Description)
	}
	for _, relation := range model.Relations {
		descriptions = append(descriptions, relation.Description)
	}
	for _, description := range descriptions {
		for _, link := range goDocumentationLinkPattern.FindAllString(description, -1) {
			if seen[link] {
				continue
			}
			seen[link] = true
			links = append(links, link)
		}
	}
	return links
}

func joinDocumentationLinks(links []string) string {
	switch len(links) {
	case 0:
		return ""
	case 1:
		return links[0]
	case 2:
		return links[0] + " and " + links[1]
	default:
		return strings.Join(links[:len(links)-1], ", ") + ", and " + links[len(links)-1]
	}
}

func stripDocumentationLinks(value string) string {
	return goDocumentationLinkPattern.ReplaceAllStringFunc(value, func(link string) string {
		return link[1 : len(link)-1]
	})
}

func operatorGoSuffix(operator string) string {
	return map[string]string{
		"exact":    "",
		"in":       "In",
		"contains": "Contains",
		"regex":    "Regex",
		"gt":       "GT",
		"gte":      "GTE",
		"lt":       "LT",
		"lte":      "LTE",
	}[operator]
}

func operatorJSONSuffix(operator string) string {
	return map[string]string{
		"exact":    "",
		"in":       "In",
		"contains": "Contains",
		"regex":    "Regex",
		"gt":       "Gt",
		"gte":      "Gte",
		"lt":       "Lt",
		"lte":      "Lte",
	}[operator]
}

func fieldHasOperator(field fieldSpec, wanted string) bool {
	for _, operator := range field.Operators {
		if operator == wanted {
			return true
		}
	}
	return false
}

func specHasOperator(spec filterSpec, wanted string) bool {
	for _, model := range spec.Models {
		for _, field := range model.Fields {
			if fieldHasOperator(field, wanted) {
				return true
			}
		}
	}
	return false
}

func manyRelationTargets(spec filterSpec) []string {
	seen := make(map[string]struct{})
	var targets []string
	for _, model := range spec.Models {
		for _, relation := range model.Relations {
			if relation.Cardinality != "many" {
				continue
			}
			if _, found := seen[relation.Target]; found {
				continue
			}
			seen[relation.Target] = struct{}{}
			targets = append(targets, relation.Target)
		}
	}
	return targets
}

func fieldEnabledExpression(field fieldSpec) string {
	base := lowerFirst(field.Name)
	parts := make([]string, 0, len(field.Operators))
	for _, operator := range field.Operators {
		switch operator {
		case "exact":
			parts = append(parts, base+"ExactSet")
		case "in":
			parts = append(parts, base+"InSet")
		case "contains":
			parts = append(parts, base+"ContainsSet")
		case "regex":
			parts = append(parts, base+"Regex != nil")
		case "gt", "gte", "lt", "lte":
			parts = append(parts, base+operatorGoSuffix(operator)+"Set")
		}
	}
	return strings.Join(parts, " || ")
}

func validationMapName(model string) string {
	return lowerFirst(model) + "s"
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "ID") {
		return "id" + value[2:]
	}
	return strings.ToLower(value[:1]) + value[1:]
}
