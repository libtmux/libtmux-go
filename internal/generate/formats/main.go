// Command formats generates tmux format metadata and typed accessors.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"go/token"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/internal/goname"
)

var (
	fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	versionPattern   = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?[a-z0-9.-]*$`)
)

type formatSpec struct {
	Schema int         `json:"schema"`
	Fields []fieldSpec `json:"fields"`
}

type fieldSpec struct {
	Name        string     `json:"name"`
	Scope       string     `json:"scope"`
	Since       string     `json:"since"`
	Kind        formatKind `json:"kind"`
	OwnAccessor string     `json:"ownAccessor,omitempty"`
}

type formatKind string

const (
	formatKindString     formatKind = "string"
	formatKindBool       formatKind = "bool"
	formatKindInt        formatKind = "int"
	formatKindTime       formatKind = "time"
	formatKindSessionID  formatKind = "session-id"
	formatKindWindowID   formatKind = "window-id"
	formatKindPaneID     formatKind = "pane-id"
	formatKindClientName formatKind = "client-name"
	formatKindVersion    formatKind = "version"
)

type accessorKind struct {
	returnType string
	getter     string
}

var accessorKinds = map[formatKind]accessorKind{
	formatKindString:     {returnType: "string", getter: "getString"},
	formatKindBool:       {returnType: "bool", getter: "getBool"},
	formatKindInt:        {returnType: "int", getter: "getInt"},
	formatKindTime:       {returnType: "time.Time", getter: "getTime"},
	formatKindSessionID:  {returnType: "SessionID", getter: "getSessionID"},
	formatKindWindowID:   {returnType: "WindowID", getter: "getWindowID"},
	formatKindPaneID:     {returnType: "PaneID", getter: "getPaneID"},
	formatKindClientName: {returnType: "ClientName", getter: "getClientName"},
	formatKindVersion:    {returnType: "Version", getter: "getVersion"},
}

type accessorReceiver struct {
	typeName string
	variable string
}

var accessorReceivers = [...]accessorReceiver{
	{typeName: "Session", variable: "s"},
	{typeName: "Window", variable: "w"},
	{typeName: "Pane", variable: "p"},
	{typeName: "Client", variable: "c"},
}

var reservedAccessorMethods = map[string]map[string]bool{
	"Session": {
		"ID": true, "Formats": true, "Raw": true, "Windows": true, "Panes": true,
		"ActiveWindow": true, "ActivePane": true,
	},
	"Window": {
		"SessionID": true, "ID": true, "Index": true, "Formats": true, "Raw": true,
		"Session": true, "Panes": true, "ActivePane": true, "LinkedSessions": true,
	},
	"Pane": {
		"SessionID": true, "WindowID": true, "WindowIndex": true,
		"ID": true, "Index": true, "Formats": true, "Raw": true, "Session": true,
		"Window": true, "Pipe": true,
	},
	"Client": {
		"Name": true, "Formats": true, "Raw": true, "AttachedSession": true,
		"AttachedWindow": true, "AttachedPane": true,
	},
}

var reservedFormatValuesMethods = map[string]bool{"Raw": true}

func main() {
	specPath := flag.String("spec", "internal/generate/formats/spec.json", "format specification")
	outputPath := flag.String("output", "format_generated.go", "generated Go output")
	flag.Parse()

	spec, err := readFormatSpec(*specPath)
	if err != nil {
		fatal(err)
	}
	output, err := generateFormats(spec)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*outputPath, output, 0o644); err != nil {
		fatal(fmt.Errorf("write generated formats: %w", err))
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func readFormatSpec(path string) (formatSpec, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return formatSpec{}, fmt.Errorf("read format spec: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var spec formatSpec
	if err := decoder.Decode(&spec); err != nil {
		return formatSpec{}, fmt.Errorf("decode format spec: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return formatSpec{}, errors.New("decode format spec: trailing JSON value")
		}
		return formatSpec{}, fmt.Errorf("decode format spec trailing value: %w", err)
	}
	if err := validateFormatSpec(spec); err != nil {
		return formatSpec{}, err
	}
	return spec, nil
}

func validateFormatSpec(spec formatSpec) error {
	if spec.Schema != 2 {
		return fmt.Errorf("unsupported format spec schema %d", spec.Schema)
	}
	if len(spec.Fields) == 0 {
		return errors.New("format spec has no fields")
	}
	validScopes := map[string]bool{
		"buffer":    true,
		"client":    true,
		"context":   true,
		"event":     true,
		"pane":      true,
		"session":   true,
		"universal": true,
		"window":    true,
	}
	seenNames := make(map[string]struct{}, len(spec.Fields))
	seenAccessors := make(map[string]string, len(spec.Fields)*len(accessorReceivers))
	for _, field := range spec.Fields {
		if !fieldNamePattern.MatchString(field.Name) {
			return fmt.Errorf("invalid format field name %q", field.Name)
		}
		if _, found := seenNames[field.Name]; found {
			return fmt.Errorf("duplicate format field %q", field.Name)
		}
		seenNames[field.Name] = struct{}{}
		if !validScopes[field.Scope] {
			return fmt.Errorf("format field %q has invalid scope %q", field.Name, field.Scope)
		}
		if _, ok := accessorKinds[field.Kind]; !ok {
			return fmt.Errorf("format field %q has invalid kind %q", field.Name, field.Kind)
		}
		if field.OwnAccessor != "" {
			if !token.IsIdentifier(field.OwnAccessor) || !token.IsExported(field.OwnAccessor) {
				return fmt.Errorf("format field %q has invalid ownAccessor %q", field.Name, field.OwnAccessor)
			}
			if field.Scope == "universal" || !strings.HasPrefix(field.Name, field.Scope+"_") {
				return fmt.Errorf(
					"format field %q ownAccessor is only valid for a matching receiver-prefixed field",
					field.Name,
				)
			}
		}
		if _, _, _, err := parseVersion(field.Since); err != nil {
			return fmt.Errorf("format field %q: %w", field.Name, err)
		}
		if generateFormatValuesAccessor(field) {
			accessor := accessorName(field.Name)
			key := "FormatValues." + accessor
			if reservedFormatValuesMethods[accessor] {
				return fmt.Errorf("format field %q produces reserved method %q", field.Name, key)
			}
			if previous, found := seenAccessors[key]; found {
				return fmt.Errorf(
					"format fields %q and %q produce accessor %q",
					previous,
					field.Name,
					key,
				)
			}
			seenAccessors[key] = field.Name
		}
		for _, receiver := range accessorReceivers {
			if !generateRecordAccessor(receiver.typeName, field) {
				continue
			}
			accessor := accessorNameForReceiver(receiver.typeName, field)
			key := receiver.typeName + "." + accessor
			if reservedAccessorMethods[receiver.typeName][accessor] {
				return fmt.Errorf("format field %q produces reserved method %q", field.Name, key)
			}
			if previous, found := seenAccessors[key]; found {
				return fmt.Errorf(
					"format fields %q and %q produce accessor %q",
					previous,
					field.Name,
					key,
				)
			}
			seenAccessors[key] = field.Name
		}
	}
	return nil
}

func generateFormats(spec formatSpec) ([]byte, error) {
	if err := validateFormatSpec(spec); err != nil {
		return nil, err
	}
	fields := append([]fieldSpec(nil), spec.Fields...)
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Name < fields[j].Name
	})

	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n\n")
	output.WriteString("package tmux\n\n")
	for _, field := range fields {
		if field.Kind == formatKindTime && generatesAnyAccessor(field) {
			output.WriteString("import \"time\"\n\n")
			break
		}
	}
	output.WriteString("var generatedFormatFields = [...]formatField{\n")
	for _, field := range fields {
		major, minor, patch, _ := parseVersion(field.Since)
		fmt.Fprintf(
			&output,
			"{name: %s, scope: formatScope%s, kind: formatKind%s, minimum: Version{raw: %s, major: %d, minor: %d, patch: %d}},\n",
			strconv.Quote(field.Name),
			accessorName(field.Scope),
			formatKindName(field.Kind),
			strconv.Quote(field.Since),
			major,
			minor,
			patch,
		)
	}
	output.WriteString("}\n\n")

	for _, field := range fields {
		if !generateFormatValuesAccessor(field) {
			continue
		}
		accessor := accessorName(field.Name)
		kind := accessorKinds[field.Kind]
		fmt.Fprintf(
			&output,
			"// %s returns a typed %s value and an ok result parsed from tmux #{%s} in a materialized hierarchy record's %s-scoped fields (tmux %s or later), not a live tmux read.\n",
			accessor,
			kind.returnType,
			field.Name,
			field.Scope,
			field.Since,
		)
		output.WriteString("// See [Server.Snapshot] for a fresh hierarchy; projected cross-scope fields do not guarantee that the referenced object is present in the same snapshot.\n")
		output.WriteString("// ok == false means the field was absent, empty, or malformed; use [FormatValues.Raw] to inspect the exact materialized expansion.\n")
		fmt.Fprintf(
			&output,
			"func (v FormatValues) %s() (%s, bool) { return v.values.%s(%s) }\n\n",
			accessor,
			kind.returnType,
			kind.getter,
			strconv.Quote(field.Name),
		)
	}

	for _, receiver := range accessorReceivers {
		for _, field := range fields {
			if !generateRecordAccessor(receiver.typeName, field) {
				continue
			}
			accessor := accessorNameForReceiver(receiver.typeName, field)
			kind := accessorKinds[field.Kind]
			fmt.Fprintf(
				&output,
				"// %s returns a typed %s value and an ok result parsed from tmux #{%s} in this %s's materialized %s-scoped record (tmux %s or later), not a live tmux read.\n",
				accessor,
				kind.returnType,
				field.Name,
				receiver.typeName,
				field.Scope,
				field.Since,
			)
			fmt.Fprintf(&output, "// See [Server.Snapshot] for a fresh hierarchy and [%s.Formats] for projected fields.\n", receiver.typeName)
			output.WriteString("// ok == false means the field was absent, empty, or malformed; use [FormatValues.Raw] to inspect the exact materialized expansion.\n")
			fmt.Fprintf(
				&output,
				"func (%s %s) %s() (%s, bool) { return %s.formats.%s(%s) }\n\n",
				receiver.variable,
				receiver.typeName,
				accessor,
				kind.returnType,
				receiver.variable,
				kind.getter,
				strconv.Quote(field.Name),
			)
		}
	}

	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated formats: %w", err)
	}
	return formatted, nil
}

func generatesAnyAccessor(field fieldSpec) bool {
	if generateFormatValuesAccessor(field) {
		return true
	}
	for _, receiver := range accessorReceivers {
		if generateRecordAccessor(receiver.typeName, field) {
			return true
		}
	}
	return false
}

func formatKindName(kind formatKind) string {
	return accessorName(strings.ReplaceAll(string(kind), "-", "_"))
}

func generateFormatValuesAccessor(field fieldSpec) bool {
	switch field.Scope {
	case "client", "pane", "session", "universal", "window":
		return true
	default:
		return false
	}
}

func generateRecordAccessor(receiver string, field fieldSpec) bool {
	if field.Scope != strings.ToLower(receiver) {
		return false
	}

	switch receiver {
	case "Session":
		return field.Name != "session_id"
	case "Window":
		switch field.Name {
		case "session_id", "window_id", "window_index":
			return false
		}
	case "Pane":
		switch field.Name {
		case "session_id", "window_id", "window_index", "pane_id", "pane_index":
			return false
		}
	case "Client":
		return field.Name != "client_name"
	}
	return true
}

func accessorNameForReceiver(receiver string, field fieldSpec) string {
	prefix := strings.ToLower(receiver) + "_"
	if !strings.HasPrefix(field.Name, prefix) {
		return accessorName(field.Name)
	}
	if field.OwnAccessor != "" {
		return field.OwnAccessor
	}
	return accessorName(strings.TrimPrefix(field.Name, prefix))
}

func parseVersion(raw string) (int, int, int, error) {
	matches := versionPattern.FindStringSubmatch(raw)
	if matches == nil {
		return 0, 0, 0, fmt.Errorf("invalid minimum version %q", raw)
	}
	parts := [3]int{}
	for index, value := range matches[1:4] {
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid minimum version %q", raw)
		}
		parts[index] = parsed
	}
	return parts[0], parts[1], parts[2], nil
}

// accessorName converts a tmux format token to its generated Go accessor name
// using the module-wide naming convention, so generated accessors and the
// parity omission guard derive the same spelling from the same rules.
func accessorName(name string) string {
	return goname.Exported(name)
}
