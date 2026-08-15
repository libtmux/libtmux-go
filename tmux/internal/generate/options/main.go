// Command options generates tmux option and hook metadata and typed value surfaces.
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
	"slices"
	"strconv"
	"strings"
)

var (
	tmuxNamePattern        = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	pythonFieldPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
	controlWireNamePattern = regexp.MustCompile(`^%[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	versionPattern         = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?([a-z]*)$`)
)

var scopeOrder = map[string]int{
	"server":  0,
	"session": 1,
	"window":  2,
	"pane":    3,
}

var validKinds = map[string]bool{
	"CHOICE":  true,
	"COLOUR":  true,
	"COMMAND": true,
	"FLAG":    true,
	"KEY":     true,
	"NUMBER":  true,
	"STRING":  true,
}

type optionSpec struct {
	Schema         int                `json:"schema"`
	FeatureFloor   string             `json:"featureFloor"`
	FeatureCeiling string             `json:"featureCeiling"`
	SourceTag      string             `json:"sourceTag"`
	Aliases        []aliasSpec        `json:"aliases"`
	Options        []entrySpec        `json:"options"`
	Hooks          []entrySpec        `json:"hooks"`
	Notifications  []notificationSpec `json:"notifications"`
}

type aliasSpec struct {
	Name      string `json:"name"`
	Canonical string `json:"canonical"`
}

type entrySpec struct {
	Name        string            `json:"name"`
	GoName      string            `json:"goName"`
	GoType      string            `json:"goType"`
	ChoiceNames map[string]string `json:"choiceNames,omitempty"`
	Array       bool              `json:"array"`
	Style       bool              `json:"style"`
	Variants    []variantSpec     `json:"variants"`
}

type variantSpec struct {
	Since    string   `json:"since"`
	TmuxKind string   `json:"tmuxKind"`
	Scopes   []string `json:"scopes"`
	Choices  []string `json:"choices"`
}

type notificationSpec struct {
	PythonField     string   `json:"pythonField"`
	WireName        string   `json:"wireName"`
	GoName          string   `json:"goName"`
	Since           string   `json:"since"`
	PrefixArguments int      `json:"prefixArguments"`
	PrefixLabels    []string `json:"prefixLabels"`
	Tail            string   `json:"tail"`
	TailLabel       string   `json:"tailLabel"`
	AllowEmptyTail  bool     `json:"allowEmptyTail"`
}

type specVersion struct {
	major, minor, patch int
	suffix              string
}

func main() {
	specPath := flag.String("spec", "internal/generate/options/spec.json", "option specification")
	outputPath := flag.String("output", "option_generated.go", "generated Go output")
	flag.Parse()

	spec, err := readOptionSpec(*specPath)
	if err != nil {
		fatal(err)
	}
	output, err := generateOptions(spec)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*outputPath, output, 0o644); err != nil {
		fatal(fmt.Errorf("write generated options: %w", err))
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func readOptionSpec(path string) (optionSpec, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return optionSpec{}, fmt.Errorf("read option spec: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var spec optionSpec
	if err := decoder.Decode(&spec); err != nil {
		return optionSpec{}, fmt.Errorf("decode option spec: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return optionSpec{}, errors.New("decode option spec: trailing JSON value")
		}
		return optionSpec{}, fmt.Errorf("decode option spec trailing value: %w", err)
	}
	if err := validateOptionSpec(spec); err != nil {
		return optionSpec{}, err
	}
	return spec, nil
}

func validateOptionSpec(spec optionSpec) error {
	if spec.Schema != 2 {
		return fmt.Errorf("option spec has unsupported schema %d", spec.Schema)
	}
	floor, err := parseSpecVersion(spec.FeatureFloor)
	if err != nil {
		return fmt.Errorf("option spec featureFloor: %w", err)
	}
	ceiling, err := parseSpecVersion(spec.FeatureCeiling)
	if err != nil {
		return fmt.Errorf("option spec featureCeiling: %w", err)
	}
	if compareSpecVersion(floor, ceiling) > 0 {
		return errors.New("option spec featureFloor is newer than featureCeiling")
	}
	source, err := parseSpecVersion(spec.SourceTag)
	if err != nil {
		return fmt.Errorf("option spec sourceTag: %w", err)
	}
	if compareSpecVersion(source, ceiling) < 0 {
		return errors.New("option spec sourceTag is older than featureCeiling")
	}
	if len(spec.Options) == 0 {
		return errors.New("option spec has no options")
	}
	if len(spec.Hooks) == 0 {
		return errors.New("option spec has no hooks")
	}

	optionNames, err := validateEntries("option", spec.Options, floor, ceiling)
	if err != nil {
		return err
	}
	if _, err := validateEntries("hook", spec.Hooks, floor, ceiling); err != nil {
		return err
	}
	if err := validateSetterMethods(spec.Options); err != nil {
		return err
	}
	if err := validateAliases(spec.Aliases, optionNames); err != nil {
		return err
	}
	if err := validateNotifications(spec.Notifications, floor, ceiling); err != nil {
		return err
	}
	return nil
}

func validateNotifications(entries []notificationSpec, floor, ceiling specVersion) error {
	if len(entries) == 0 {
		return errors.New("option spec has no control notifications")
	}
	pythonFields := make(map[string]struct{}, len(entries))
	wireNames := make(map[string]struct{}, len(entries))
	goNames := make(map[string]struct{}, len(entries))
	previous := ""
	for _, entry := range entries {
		if !pythonFieldPattern.MatchString(entry.PythonField) {
			return fmt.Errorf("control notification has invalid Python field %q", entry.PythonField)
		}
		if _, found := pythonFields[entry.PythonField]; found {
			return fmt.Errorf("duplicate control notification Python field %q", entry.PythonField)
		}
		pythonFields[entry.PythonField] = struct{}{}
		if !controlWireNamePattern.MatchString(entry.WireName) {
			return fmt.Errorf("control notification has invalid wire name %q", entry.WireName)
		}
		if entry.WireName <= previous {
			return fmt.Errorf("control notifications are not strictly sorted at %q", entry.WireName)
		}
		previous = entry.WireName
		if _, found := wireNames[entry.WireName]; found {
			return fmt.Errorf("duplicate control notification wire name %q", entry.WireName)
		}
		wireNames[entry.WireName] = struct{}{}
		if !token.IsIdentifier(entry.GoName) || !token.IsExported(entry.GoName) {
			return fmt.Errorf("control notification %q has invalid Go name %q", entry.WireName, entry.GoName)
		}
		if _, found := goNames[entry.GoName]; found {
			return fmt.Errorf("duplicate control notification Go name %q", entry.GoName)
		}
		goNames[entry.GoName] = struct{}{}
		version, err := parseSpecVersion(entry.Since)
		if err != nil {
			return fmt.Errorf("control notification %q: %w", entry.WireName, err)
		}
		if compareSpecVersion(version, floor) < 0 || compareSpecVersion(version, ceiling) > 0 {
			return fmt.Errorf("control notification %q version %q is outside the feature range", entry.WireName, entry.Since)
		}
		if entry.PrefixArguments < 0 {
			return fmt.Errorf("control notification %q has negative prefix argument count", entry.WireName)
		}
		if len(entry.PrefixLabels) != entry.PrefixArguments {
			return fmt.Errorf("control notification %q has %d prefix labels, want %d", entry.WireName, len(entry.PrefixLabels), entry.PrefixArguments)
		}
		for _, label := range entry.PrefixLabels {
			if strings.TrimSpace(label) == "" {
				return fmt.Errorf("control notification %q has a blank prefix label", entry.WireName)
			}
		}
		switch entry.Tail {
		case "none":
			if entry.AllowEmptyTail {
				return fmt.Errorf("control notification %q allows an empty absent tail", entry.WireName)
			}
			if entry.TailLabel != "" {
				return fmt.Errorf("control notification %q has a tail label without a tail", entry.WireName)
			}
		case "required", "colon":
			if strings.TrimSpace(entry.TailLabel) == "" {
				return fmt.Errorf("control notification %q has a blank tail label", entry.WireName)
			}
		case "optional":
			if entry.AllowEmptyTail {
				return fmt.Errorf("control notification %q allows an empty optional tail", entry.WireName)
			}
			if strings.TrimSpace(entry.TailLabel) == "" {
				return fmt.Errorf("control notification %q has a blank tail label", entry.WireName)
			}
		default:
			return fmt.Errorf("control notification %q has invalid tail %q", entry.WireName, entry.Tail)
		}
	}
	return nil
}

func validateAliases(aliases []aliasSpec, optionNames map[string]struct{}) error {
	seen := make(map[string]struct{}, len(aliases))
	previous := ""
	for _, alias := range aliases {
		if !tmuxNamePattern.MatchString(alias.Name) {
			return fmt.Errorf("invalid option alias name %q", alias.Name)
		}
		if alias.Name <= previous {
			return fmt.Errorf("option aliases are not strictly sorted at %q", alias.Name)
		}
		previous = alias.Name
		if _, found := seen[alias.Name]; found {
			return fmt.Errorf("duplicate option alias %q", alias.Name)
		}
		seen[alias.Name] = struct{}{}
		if _, found := optionNames[alias.Name]; found {
			return fmt.Errorf("option alias %q collides with a canonical option", alias.Name)
		}
		if _, found := optionNames[alias.Canonical]; !found {
			return fmt.Errorf("option alias %q has unknown canonical option %q", alias.Name, alias.Canonical)
		}
	}
	return nil
}

func validateEntries(kind string, entries []entrySpec, floor, ceiling specVersion) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(entries))
	goNames := make(map[string]string, len(entries))
	previous := ""
	for _, entry := range entries {
		if !tmuxNamePattern.MatchString(entry.Name) {
			return nil, fmt.Errorf("%s has invalid name %q", kind, entry.Name)
		}
		if entry.Name <= previous {
			return nil, fmt.Errorf("%ss are not strictly sorted at %q", kind, entry.Name)
		}
		previous = entry.Name
		if _, found := names[entry.Name]; found {
			return nil, fmt.Errorf("duplicate %s %q", kind, entry.Name)
		}
		names[entry.Name] = struct{}{}
		if !token.IsIdentifier(entry.GoName) || !token.IsExported(entry.GoName) {
			return nil, fmt.Errorf("%s %q has invalid Go name %q", kind, entry.Name, entry.GoName)
		}
		if prior, found := goNames[entry.GoName]; found {
			return nil, fmt.Errorf("%ss %q and %q share Go name %q", kind, prior, entry.Name, entry.GoName)
		}
		goNames[entry.GoName] = entry.Name
		if err := validateEntry(kind, entry, floor, ceiling); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func validateEntry(entryKind string, entry entrySpec, floor, ceiling specVersion) error {
	if len(entry.Variants) == 0 {
		return fmt.Errorf("%s %q has no variants", entryKind, entry.Name)
	}
	var priorVersion specVersion
	for index, variant := range entry.Variants {
		version, err := parseSpecVersion(variant.Since)
		if err != nil {
			return fmt.Errorf("%s %q variant %d: %w", entryKind, entry.Name, index, err)
		}
		if compareSpecVersion(version, floor) < 0 || compareSpecVersion(version, ceiling) > 0 {
			return fmt.Errorf("%s %q variant %q is outside the feature range", entryKind, entry.Name, variant.Since)
		}
		if index != 0 && compareSpecVersion(priorVersion, version) >= 0 {
			return fmt.Errorf("%s %q variants are not strictly increasing at %q", entryKind, entry.Name, variant.Since)
		}
		priorVersion = version
		if !validKinds[variant.TmuxKind] {
			return fmt.Errorf("%s %q has invalid tmux kind %q", entryKind, entry.Name, variant.TmuxKind)
		}
		if err := validateScopes(entryKind, entry.Name, variant.Scopes); err != nil {
			return err
		}
		if err := validateChoices(entryKind, entry.Name, variant); err != nil {
			return err
		}
		if index != 0 && sameVariant(entry.Variants[index-1], variant) {
			return fmt.Errorf("%s %q has redundant variant %q", entryKind, entry.Name, variant.Since)
		}
	}

	if entryKind == "hook" {
		if !entry.Array || entry.Style || entry.GoType != "SparseArray[string]" {
			return fmt.Errorf("hook %q must be a command sparse array", entry.Name)
		}
		for _, variant := range entry.Variants {
			if variant.TmuxKind != "COMMAND" {
				return fmt.Errorf("hook %q has non-command kind %q", entry.Name, variant.TmuxKind)
			}
			if slices.Contains(variant.Scopes, "server") {
				return fmt.Errorf("hook %q has invalid server scope", entry.Name)
			}
		}
		return nil
	}

	if entry.Array {
		if entry.GoType != "SparseArray[string]" {
			return fmt.Errorf("array option %q has incompatible Go type %q", entry.Name, entry.GoType)
		}
	} else if entry.GoType != scalarGoType(entry) {
		return fmt.Errorf("option %q has incompatible Go type %q", entry.Name, entry.GoType)
	}
	if err := validateChoiceNames(entryKind, entry); err != nil {
		return err
	}
	if entry.Style {
		if entry.Array {
			return fmt.Errorf("style option %q cannot be an array", entry.Name)
		}
		for _, variant := range entry.Variants {
			if variant.TmuxKind != "STRING" {
				return fmt.Errorf("style option %q has non-string kind %q", entry.Name, variant.TmuxKind)
			}
		}
	}
	return nil
}

func validateScopes(entryKind, name string, scopes []string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("%s %q has no scopes", entryKind, name)
	}
	previous := -1
	for _, scope := range scopes {
		order, found := scopeOrder[scope]
		if !found {
			return fmt.Errorf("%s %q has invalid scope %q", entryKind, name, scope)
		}
		if order <= previous {
			return fmt.Errorf("%s %q scopes are not strictly ordered at %q", entryKind, name, scope)
		}
		previous = order
	}
	return nil
}

func validateChoices(entryKind, name string, variant variantSpec) error {
	if variant.TmuxKind != "CHOICE" {
		if len(variant.Choices) != 0 {
			return fmt.Errorf("%s %q has choices for non-choice kind %q", entryKind, name, variant.TmuxKind)
		}
		return nil
	}
	if len(variant.Choices) == 0 {
		return fmt.Errorf("%s %q choice variant has no choices", entryKind, name)
	}
	seen := make(map[string]struct{}, len(variant.Choices))
	for _, choice := range variant.Choices {
		if choice == "" {
			return fmt.Errorf("%s %q has an empty choice", entryKind, name)
		}
		if _, found := seen[choice]; found {
			return fmt.Errorf("%s %q has duplicate choice %q", entryKind, name, choice)
		}
		seen[choice] = struct{}{}
	}
	return nil
}

func scalarGoType(entry entrySpec) string {
	if isChoiceEntry(entry) {
		return entry.GoName
	}
	hasNumber := false
	hasFlag := false
	hasString := false
	for _, variant := range entry.Variants {
		switch variant.TmuxKind {
		case "NUMBER":
			hasNumber = true
		case "FLAG":
			hasFlag = true
		default:
			hasString = true
		}
	}
	if hasNumber && !hasFlag && !hasString {
		return "int64"
	}
	if hasFlag && !hasNumber && !hasString {
		return "bool"
	}
	if !hasNumber {
		return "string"
	}
	return ""
}

func isChoiceEntry(entry entrySpec) bool {
	for _, variant := range entry.Variants {
		if variant.TmuxKind == "CHOICE" {
			return true
		}
	}
	return false
}

func choiceValues(entry entrySpec) []string {
	seen := make(map[string]bool)
	values := make([]string, 0)
	for _, variant := range entry.Variants {
		choices := variant.Choices
		if variant.TmuxKind == "FLAG" {
			choices = []string{"off", "on"}
		}
		for _, choice := range choices {
			if !seen[choice] {
				seen[choice] = true
				values = append(values, choice)
			}
		}
	}
	return values
}

func validateChoiceNames(entryKind string, entry entrySpec) error {
	if entryKind != "option" || !isChoiceEntry(entry) {
		if len(entry.ChoiceNames) == 0 {
			return nil
		}
		return fmt.Errorf("%s %q has choice names without a choice type", entryKind, entry.Name)
	}
	values := make(map[string]bool)
	for _, value := range choiceValues(entry) {
		values[value] = true
	}
	seenNames := make(map[string]bool)
	for value, name := range entry.ChoiceNames {
		if !values[value] {
			return fmt.Errorf("option %q has a choice name for unknown value %q", entry.Name, value)
		}
		if !token.IsIdentifier(name) || !token.IsExported(name) {
			return fmt.Errorf("option %q choice %q has invalid Go suffix %q", entry.Name, value, name)
		}
		if seenNames[name] {
			return fmt.Errorf("option %q has duplicate choice suffix %q", entry.Name, name)
		}
		seenNames[name] = true
	}
	constants := make(map[string]string)
	for _, value := range choiceValues(entry) {
		constant := entry.GoName + choiceConstantSuffix(entry, value)
		if !token.IsIdentifier(constant) || !token.IsExported(constant) {
			return fmt.Errorf("option %q choice %q produces invalid constant %q", entry.Name, value, constant)
		}
		if prior := constants[constant]; prior != "" {
			return fmt.Errorf("option %q choices %q and %q produce constant %q", entry.Name, prior, value, constant)
		}
		constants[constant] = value
	}
	return nil
}

func setterName(entry entrySpec) string {
	if strings.HasPrefix(entry.GoName, "Set") {
		return entry.GoName
	}
	return "Set" + entry.GoName
}

func validateSetterMethods(entries []entrySpec) error {
	reserved := map[string]map[string]bool{
		"Server":             {"SetBuffer": true, "SetEnvironment": true, "SetOption": true},
		"Session":            {"SetEnvironment": true, "SetHook": true, "SetHooks": true, "SetOption": true},
		"Window":             {"SetHook": true, "SetHooks": true, "SetOption": true},
		"Pane":               {"SetHeight": true, "SetHook": true, "SetHooks": true, "SetOption": true, "SetTitle": true, "SetWidth": true},
		"GlobalSessionScope": {"SetHook": true, "SetHooks": true, "SetOption": true},
		"GlobalWindowScope":  {"SetHook": true, "SetHooks": true, "SetOption": true},
	}
	seen := make(map[string]map[string]string)
	for receiver := range reserved {
		seen[receiver] = make(map[string]string)
	}
	for _, entry := range entries {
		method := setterName(entry)
		for _, receiver := range setterReceivers(entry) {
			if reserved[receiver][method] {
				return fmt.Errorf("option %q setter collides with %s.%s", entry.Name, receiver, method)
			}
			if prior := seen[receiver][method]; prior != "" {
				return fmt.Errorf("options %q and %q setters collide on %s.%s", prior, entry.Name, receiver, method)
			}
			seen[receiver][method] = entry.Name
		}
	}
	return nil
}

func sameVariant(left, right variantSpec) bool {
	return left.TmuxKind == right.TmuxKind &&
		slices.Equal(left.Scopes, right.Scopes) &&
		slices.Equal(left.Choices, right.Choices)
}

func parseSpecVersion(raw string) (specVersion, error) {
	matches := versionPattern.FindStringSubmatch(raw)
	if matches == nil {
		return specVersion{}, fmt.Errorf("invalid tmux version %q", raw)
	}
	parts := [3]int{}
	for index, value := range matches[1:4] {
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return specVersion{}, fmt.Errorf("invalid tmux version %q", raw)
		}
		parts[index] = parsed
	}
	return specVersion{
		major:  parts[0],
		minor:  parts[1],
		patch:  parts[2],
		suffix: matches[4],
	}, nil
}

func compareSpecVersion(left, right specVersion) int {
	leftParts := [...]int{left.major, left.minor, left.patch}
	rightParts := [...]int{right.major, right.minor, right.patch}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return strings.Compare(left.suffix, right.suffix)
}

func generateOptions(spec optionSpec) ([]byte, error) {
	if err := validateOptionSpec(spec); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n\n")
	output.WriteString("package tmux\n\n")
	output.WriteString("import \"context\"\n\n")
	writeChoiceTypes(&output, spec.Options)
	writeMetadataTypes(&output)
	writeControlNotificationTypes(&output)
	output.WriteString("const (\n")
	fmt.Fprintf(&output, "generatedOptionSpecSchema = %d\n", spec.Schema)
	fmt.Fprintf(&output, "generatedOptionSourceTag = %s\n", strconv.Quote(spec.SourceTag))
	output.WriteString(")\n\n")
	writeVersionVariable(&output, "generatedOptionFeatureFloor", spec.FeatureFloor)
	writeVersionVariable(&output, "generatedOptionFeatureCeiling", spec.FeatureCeiling)
	writeAliases(&output, spec.Aliases)
	writeDefinitions(&output, "generatedOptionDefinitions", spec.Options)
	writeDefinitions(&output, "generatedHookDefinitions", spec.Hooks)
	writeControlNotifications(&output, spec.Notifications)
	writeCountConstants(&output, spec)
	for _, surface := range generatedSurfaces(spec) {
		writeSurface(&output, surface)
	}
	writeScalarSetters(&output, spec.Options)
	writeArraySetters(&output, spec.Options)

	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated options: %w", err)
	}
	return formatted, nil
}

func writeControlNotificationTypes(output *bytes.Buffer) {
	output.WriteString("// ControlNotificationKind identifies a tmux control-mode notification record. Its zero value is unknown\n")
	output.WriteString("// and is not emitted by tmux. [ParseControlNotification] returns known\n")
	output.WriteString("// values for the pinned wire vocabulary and rejects unknown notification records.\n")
	output.WriteString("type ControlNotificationKind string\n\n")
	output.WriteString("type generatedControlNotificationTail uint8\n\n")
	output.WriteString("const (\n")
	output.WriteString("generatedControlNotificationTailNone generatedControlNotificationTail = iota\n")
	output.WriteString("generatedControlNotificationTailRequired\n")
	output.WriteString("generatedControlNotificationTailOptional\n")
	output.WriteString("generatedControlNotificationTailColon\n")
	output.WriteString(")\n\n")
	output.WriteString("type generatedControlNotificationDefinition struct {\n")
	output.WriteString("kind ControlNotificationKind\nminimum Version\nprefixArguments int\ntail generatedControlNotificationTail\nallowEmptyTail bool\n")
	output.WriteString("}\n\n")
}

func writeControlNotifications(output *bytes.Buffer, entries []notificationSpec) {
	output.WriteString("const (\n")
	for _, entry := range entries {
		constant := "ControlNotification" + entry.GoName
		fmt.Fprintf(output, "// %s identifies the %s notification, available since tmux %s. Its wire grammar is %s.\n", constant, entry.WireName, entry.Since, notificationGrammar(entry))
		fmt.Fprintf(output, "// ParseControlNotification returns its arguments as %s.\n", notificationArguments(entry))
		if entry.AllowEmptyTail {
			output.WriteString("// Its tail may be empty.\n")
		}
		fmt.Fprintf(
			output,
			"%s ControlNotificationKind = %s\n",
			constant,
			strconv.Quote(entry.WireName),
		)
	}
	output.WriteString(")\n\n")
	output.WriteString("var generatedControlNotificationDefinitions = [...]generatedControlNotificationDefinition{\n")
	for _, entry := range entries {
		version, _ := parseSpecVersion(entry.Since)
		fmt.Fprintf(
			output,
			"{kind: ControlNotification%s, minimum: Version{raw: %s, major: %d, minor: %d, patch: %d}, prefixArguments: %d, tail: generatedControlNotificationTail%s, allowEmptyTail: %t},\n",
			entry.GoName,
			strconv.Quote(entry.Since),
			version.major,
			version.minor,
			version.patch,
			entry.PrefixArguments,
			titleWord(entry.Tail),
			entry.AllowEmptyTail,
		)
	}
	output.WriteString("}\n\n")
	output.WriteString("// MinimumVersion returns the oldest [Version] that emits k. It returns ok == false\n")
	output.WriteString("// for the zero value and unknown kinds.\n")
	output.WriteString("func (k ControlNotificationKind) MinimumVersion() (Version, bool) {\n")
	output.WriteString("for _, definition := range generatedControlNotificationDefinitions {\n")
	output.WriteString("if definition.kind == k { return definition.minimum, true }\n")
	output.WriteString("}\n")
	output.WriteString("return Version{}, false\n")
	output.WriteString("}\n\n")
}

func writeMetadataTypes(output *bytes.Buffer) {
	output.WriteString("type generatedOptionKind uint8\n\n")
	output.WriteString("const (\n")
	for index, kind := range []string{"CHOICE", "COLOUR", "COMMAND", "FLAG", "KEY", "NUMBER", "STRING"} {
		name := "generatedOptionKind" + titleWord(strings.ToLower(kind))
		if index == 0 {
			fmt.Fprintf(output, "%s generatedOptionKind = iota + 1\n", name)
		} else {
			fmt.Fprintln(output, name)
		}
	}
	output.WriteString(")\n\n")
	output.WriteString("type generatedOptionValueKind uint8\n\n")
	output.WriteString("const (\n")
	output.WriteString("generatedOptionValueKindBool generatedOptionValueKind = iota + 1\n")
	output.WriteString("generatedOptionValueKindInt64\n")
	output.WriteString("generatedOptionValueKindString\n")
	output.WriteString("generatedOptionValueKindSparseString\n")
	output.WriteString(")\n\n")
	output.WriteString("type generatedOptionScope uint8\n\n")
	output.WriteString("const (\n")
	output.WriteString("generatedOptionScopeServer generatedOptionScope = 1 << iota\n")
	output.WriteString("generatedOptionScopeSession\n")
	output.WriteString("generatedOptionScopeWindow\n")
	output.WriteString("generatedOptionScopePane\n")
	output.WriteString(")\n\n")
	output.WriteString("type generatedOptionVariant struct {\n")
	output.WriteString("minimum Version\nkind generatedOptionKind\nscopes generatedOptionScope\nchoices []string\n")
	output.WriteString("}\n\n")
	output.WriteString("type generatedOptionDefinition struct {\n")
	output.WriteString("name string\ngoName string\nvalueKind generatedOptionValueKind\narray bool\nstyle bool\nvariants []generatedOptionVariant\n")
	output.WriteString("}\n\n")
	output.WriteString("type generatedOptionAlias struct {\nname string\ncanonical string\n}\n\n")
}

func writeChoiceTypes(output *bytes.Buffer, entries []entrySpec) {
	for _, entry := range entries {
		if !isChoiceEntry(entry) {
			continue
		}
		fmt.Fprintf(output, "// %s is a typed value for the %q tmux option. Its zero value is invalid.\n", entry.GoName, entry.Name)
		fmt.Fprintf(output, "type %s string\n\n", entry.GoName)
		output.WriteString("const (\n")
		for _, choice := range choiceValues(entry) {
			constant := entry.GoName + choiceConstantSuffix(entry, choice)
			fmt.Fprintf(output, "// %s selects %q.\n", constant, choice)
			fmt.Fprintf(output, "%s %s = %s\n", constant, entry.GoName, strconv.Quote(choice))
		}
		output.WriteString(")\n\n")
		fmt.Fprintf(output, "// String returns the exact tmux spelling of v.\nfunc (v %s) String() string { return string(v) }\n\n", entry.GoName)
		fmt.Fprintf(output, "// Valid reports whether v belongs to the supported tmux-version union.\nfunc (v %s) Valid() bool {\n", entry.GoName)
		output.WriteString("switch v {\ncase ")
		constants := make([]string, 0, len(choiceValues(entry)))
		for _, choice := range choiceValues(entry) {
			constants = append(constants, entry.GoName+choiceConstantSuffix(entry, choice))
		}
		output.WriteString(strings.Join(constants, ", "))
		output.WriteString(":\nreturn true\ndefault:\nreturn false\n}\n}\n\n")
	}
}

func choiceConstantSuffix(entry entrySpec, choice string) string {
	if explicit := entry.ChoiceNames[choice]; explicit != "" {
		return explicit
	}
	parts := strings.FieldsFunc(choice, func(value rune) bool {
		return value == '-' || value == '_'
	})
	var suffix strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		suffix.WriteString(strings.ToUpper(part[:1]))
		suffix.WriteString(part[1:])
	}
	return suffix.String()
}

func writeVersionVariable(output *bytes.Buffer, name, raw string) {
	version, _ := parseSpecVersion(raw)
	fmt.Fprintf(
		output,
		"var %s = Version{raw: %s, major: %d, minor: %d, patch: %d}\n\n",
		name,
		strconv.Quote(raw),
		version.major,
		version.minor,
		version.patch,
	)
}

func writeAliases(output *bytes.Buffer, aliases []aliasSpec) {
	output.WriteString("var generatedOptionAliases = [...]generatedOptionAlias{\n")
	for _, alias := range aliases {
		fmt.Fprintf(output, "{name: %s, canonical: %s},\n", strconv.Quote(alias.Name), strconv.Quote(alias.Canonical))
	}
	output.WriteString("}\n\n")
}

func writeDefinitions(output *bytes.Buffer, variable string, entries []entrySpec) {
	fmt.Fprintf(output, "var %s = [...]generatedOptionDefinition{\n", variable)
	for _, entry := range entries {
		fmt.Fprintf(
			output,
			"{name: %s, goName: %s, valueKind: generatedOptionValueKind%s, array: %t, style: %t, variants: []generatedOptionVariant{\n",
			strconv.Quote(entry.Name),
			strconv.Quote(entry.GoName),
			valueKindName(entry),
			entry.Array,
			entry.Style,
		)
		for _, variant := range entry.Variants {
			version, _ := parseSpecVersion(variant.Since)
			fmt.Fprintf(
				output,
				"{minimum: Version{raw: %s, major: %d, minor: %d, patch: %d}, kind: generatedOptionKind%s, scopes: %s, choices: %#v},\n",
				strconv.Quote(variant.Since),
				version.major,
				version.minor,
				version.patch,
				titleWord(strings.ToLower(variant.TmuxKind)),
				generatedScopes(variant.Scopes),
				variant.Choices,
			)
		}
		output.WriteString("}},\n")
	}
	output.WriteString("}\n\n")
}

func valueKindName(entry entrySpec) string {
	switch entry.GoType {
	case "bool":
		return "Bool"
	case "int64":
		return "Int64"
	case "string":
		return "String"
	case "SparseArray[string]":
		return "SparseString"
	default:
		if isChoiceEntry(entry) {
			return "String"
		}
		panic("validated option spec has unknown Go type " + entry.GoType)
	}
}

func generatedScopes(scopes []string) string {
	values := make([]string, len(scopes))
	for index, scope := range scopes {
		values[index] = "generatedOptionScope" + titleWord(scope)
	}
	return strings.Join(values, " | ")
}

type generatedSurface struct {
	TypeName   string
	Noun       string
	Scope      string
	Sources    []string
	RawSources []string
	// Setters lists the receivers carrying a typed setter per entry. Hook
	// surfaces leave it empty because hooks are written by name.
	Setters []string
	Entries []entrySpec
}

func generatedSurfaces(spec optionSpec) []generatedSurface {
	surfaces := make([]generatedSurface, 0, 8)
	for _, scope := range []string{"server", "session", "window", "pane"} {
		sources := map[string][]string{
			"server":  {"[Server.Options]"},
			"session": {"[Session.Options]", "[GlobalSessionScope.Options]"},
			"window":  {"[Window.Options]", "[GlobalWindowScope.Options]"},
			"pane":    {"[Pane.Options]"},
		}[scope]
		rawSources := map[string][]string{
			"server":  {"[Server.RawOption]"},
			"session": {"[Session.RawOption]", "[GlobalSessionScope.RawOption]"},
			"window":  {"[Window.RawOption]", "[GlobalWindowScope.RawOption]"},
			"pane":    {"[Pane.RawOption]"},
		}[scope]
		surfaces = append(surfaces, generatedSurface{
			TypeName:   titleWord(scope) + "OptionValues",
			Noun:       scope + " option",
			Scope:      scope,
			Sources:    sources,
			RawSources: rawSources,
			Setters:    scopeSetterReceivers(scope),
			Entries:    entriesForScope(spec.Options, scope),
		})
	}
	for _, scope := range []string{"server", "session", "window", "pane"} {
		lookupScope := scope
		noun := scope + " hook"
		sources := map[string][]string{
			"server":  {"[GlobalSessionScope.Hooks]"},
			"session": {"[Session.Hooks]"},
			"window":  {"[Window.Hooks]", "[GlobalWindowScope.Hooks]"},
			"pane":    {"[Pane.Hooks]"},
		}[scope]
		rawSources := map[string][]string{
			"server":  {"[GlobalSessionScope.RawHook]"},
			"session": {"[Session.RawHook]"},
			"window":  {"[Window.RawHook]", "[GlobalWindowScope.RawHook]"},
			"pane":    {"[Pane.RawHook]"},
		}[scope]
		if scope == "server" {
			lookupScope = "session"
			noun = "global session-scope hook"
		}
		surfaces = append(surfaces, generatedSurface{
			TypeName:   titleWord(scope) + "HookValues",
			Noun:       noun,
			Scope:      lookupScope,
			Sources:    sources,
			RawSources: rawSources,
			Entries:    entriesForScope(spec.Hooks, lookupScope),
		})
	}
	return surfaces
}

func entriesForScope(entries []entrySpec, scope string) []entrySpec {
	result := make([]entrySpec, 0, len(entries))
	for _, entry := range entries {
		found := false
		for _, variant := range entry.Variants {
			if slices.Contains(variant.Scopes, scope) {
				found = true
				break
			}
		}
		if found {
			result = append(result, entry)
		}
	}
	return result
}

func writeCountConstants(output *bytes.Buffer, spec optionSpec) {
	output.WriteString("const (\n")
	for _, scope := range []string{"server", "session", "window", "pane"} {
		fmt.Fprintf(output, "generated%sOptionCount = %d\n", titleWord(scope), len(entriesForScope(spec.Options, scope)))
	}
	for _, scope := range []string{"server", "session", "window", "pane"} {
		lookupScope := scope
		if scope == "server" {
			lookupScope = "session"
		}
		fmt.Fprintf(output, "generated%sHookCount = %d\n", titleWord(scope), len(entriesForScope(spec.Hooks, lookupScope)))
	}
	output.WriteString(")\n\n")
}

func writeSurface(output *bytes.Buffer, surface generatedSurface) {
	fmt.Fprintf(output, "// %s is an immutable point-in-time view of known %s values. Its zero value\n", surface.TypeName, surface.Noun)
	fmt.Fprintf(output, "// has no present values. Obtain it with %s; it may become stale after tmux changes.\n", strings.Join(surface.Sources, " or "))
	output.WriteString("// Use [OptionValue.Get] to read a present value and [OptionValue.Origin] to distinguish\n")
	output.WriteString("// values set at this scope from inherited values.\n")
	fmt.Fprintf(output, "type %s struct {\n", surface.TypeName)
	for _, entry := range surface.Entries {
		fmt.Fprintf(output, "%s OptionValue[%s]\n", privateName(entry.GoName), entry.GoType)
	}
	output.WriteString("}\n\n")
	for _, entry := range surface.Entries {
		writeAccessorDocumentation(output, surface, entry)
		fmt.Fprintf(
			output,
			"func (v %s) %s() OptionValue[%s] { return v.%s }\n\n",
			surface.TypeName,
			entry.GoName,
			entry.GoType,
			privateName(entry.GoName),
		)
	}
	fmt.Fprintf(
		output,
		"func new%s(values []decodedOptionValue) %s {\n",
		surface.TypeName,
		surface.TypeName,
	)
	fmt.Fprintf(output, "var result %s\n", surface.TypeName)
	output.WriteString("for _, value := range values {\n")
	output.WriteString("switch value.name {\n")
	for _, entry := range surface.Entries {
		fmt.Fprintf(output, "case %s:\n", strconv.Quote(entry.Name))
		fmt.Fprintf(
			output,
			"result.%s = optionValueFromDecoded(%s, value.origin)\n",
			privateName(entry.GoName),
			decodedValueExpression(entry),
		)
	}
	output.WriteString("}\n}\nreturn result\n}\n\n")
}

func writeScalarSetters(output *bytes.Buffer, entries []entrySpec) {
	for _, entry := range entries {
		if entry.Array {
			continue
		}
		for _, receiver := range setterReceivers(entry) {
			writeScalarSetter(output, entry, receiver)
		}
	}
}

func writeArraySetters(output *bytes.Buffer, entries []entrySpec) {
	for _, entry := range entries {
		if !entry.Array {
			continue
		}
		for _, receiver := range setterReceivers(entry) {
			writeArraySetter(output, entry, receiver)
		}
	}
}

func setterReceivers(entry entrySpec) []string {
	scopes := make(map[string]bool)
	for _, variant := range entry.Variants {
		for _, scope := range variant.Scopes {
			scopes[scope] = true
		}
	}
	receivers := make([]string, 0, 6)
	for _, scope := range []string{"server", "session", "window", "pane"} {
		if scopes[scope] {
			receivers = append(receivers, scopeSetterReceivers(scope)...)
		}
	}
	return receivers
}

// scopeSetterReceivers lists the Go receivers that carry typed setters for one
// tmux option scope, in the order generated documentation cites them.
func scopeSetterReceivers(scope string) []string {
	return map[string][]string{
		"server":  {"Server"},
		"session": {"Session", "GlobalSessionScope"},
		"window":  {"Window", "GlobalWindowScope"},
		"pane":    {"Pane"},
	}[scope]
}

// setterValueSurface names the value set and the acquisition method that read
// back what one setter receiver stores.
func setterValueSurface(receiver string) (typeName string, source string) {
	switch receiver {
	case "Server":
		return "ServerOptionValues", "[Server.Options]"
	case "Session":
		return "SessionOptionValues", "[Session.Options]"
	case "GlobalSessionScope":
		return "SessionOptionValues", "[GlobalSessionScope.Options]"
	case "Window":
		return "WindowOptionValues", "[Window.Options]"
	case "GlobalWindowScope":
		return "WindowOptionValues", "[GlobalWindowScope.Options]"
	case "Pane":
		return "PaneOptionValues", "[Pane.Options]"
	default:
		panic("unknown option setter receiver " + receiver)
	}
}

func setterReceiverName(receiver string) string {
	switch receiver {
	case "Window":
		return "w"
	case "Pane":
		return "p"
	default:
		return "s"
	}
}

func writeScalarSetter(output *bytes.Buffer, entry entrySpec, receiver string) {
	method := setterName(entry)
	receiverName := setterReceiverName(receiver)
	scope := map[string]string{
		"Server": "server", "Session": "session", "Window": "window", "Pane": "pane",
		"GlobalSessionScope": "session", "GlobalWindowScope": "window",
	}[receiver]
	minimum := variantsForScope(entry, scope)[0].Since
	valuesType, valuesSource := setterValueSurface(receiver)
	fmt.Fprintf(output, "// %s stores the %q %s option, available since tmux %s.\n", method, entry.Name, scope, minimum)
	fmt.Fprintf(output, "// It accepts %s and does not expose raw set-option flags.\n", entry.GoType)
	fmt.Fprintf(output, "// Read it back with [%s.%s] from %s, and [%s.UnsetOption] restores inheritance or the global default.\n", valuesType, entry.GoName, valuesSource, receiver)
	fmt.Fprintf(output, "// Use [%s.SetOption] for caller-named options or raw values.\n", receiver)
	fmt.Fprintf(output, "func (%s %s) %s(ctx context.Context, value %s) error {\n", receiverName, receiver, method, entry.GoType)
	switch receiver {
	case "Server":
		fmt.Fprintf(output, "server := %s\nscope := []string{\"-s\"}\n", receiverName)
	case "GlobalSessionScope":
		fmt.Fprintf(output, "server := %s.server\nscope := []string{\"-g\"}\n", receiverName)
	case "GlobalWindowScope":
		fmt.Fprintf(output, "server := %s.server\nscope := []string{\"-g\", \"-w\"}\n", receiverName)
	case "Session":
		fmt.Fprintf(output, "server, scope, err := sessionOptionRuntimeScope(%s)\nif err != nil { return err }\n", receiverName)
	case "Window":
		fmt.Fprintf(output, "server, scope, err := windowOptionRuntimeScope(%s)\nif err != nil { return err }\n", receiverName)
	case "Pane":
		fmt.Fprintf(output, "server, scope, err := paneOptionRuntimeScope(%s)\nif err != nil { return err }\n", receiverName)
	}
	encoded := "value"
	switch entry.GoType {
	case "bool":
		encoded = "encodeTypedOptionBool(value)"
	case "int64":
		encoded = "encodeTypedOptionInt64(value)"
	default:
		if isChoiceEntry(entry) {
			encoded = "value.String()"
		}
	}
	fmt.Fprintf(
		output,
		"return setTypedOption(ctx, server, scope, generatedOptionScope%s, %s, %s, %t)\n}\n\n",
		titleWord(scope), strconv.Quote(entry.Name), encoded, isChoiceEntry(entry),
	)
}

func writeArraySetter(output *bytes.Buffer, entry entrySpec, receiver string) {
	method := setterName(entry)
	receiverName := setterReceiverName(receiver)
	scope := map[string]string{
		"Server": "server", "Session": "session", "Window": "window", "Pane": "pane",
		"GlobalSessionScope": "session", "GlobalWindowScope": "window",
	}[receiver]
	minimum := variantsForScope(entry, scope)[0].Since
	valuesType, valuesSource := setterValueSurface(receiver)
	fmt.Fprintf(output, "// %s performs a complete replacement of the %q %s option, available since tmux %s.\n", method, entry.Name, scope, minimum)
	output.WriteString("// It accepts SparseArray[string], preserves sparse holes and explicit empty values, and does not expose raw set-option flags.\n")
	fmt.Fprintf(output, "// Read it back with [%s.%s] from %s.\n", valuesType, entry.GoName, valuesSource)
	fmt.Fprintf(output, "// Use [%s.SetOption] for caller-named options or raw values.\n", receiver)
	output.WriteString("// Replacement is not atomic: the result reports only confirmed writes and failures stop without rollback.\n")
	fmt.Fprintf(output, "// Callers must serialize replacement of the same target and option when final ordering matters. Use [%s.UnsetOption] to restore inheritance or the global default.\n", receiver)
	fmt.Fprintf(output, "func (%s %s) %s(ctx context.Context, value SparseArray[string]) (SetArrayResult, error) {\n", receiverName, receiver, method)
	switch receiver {
	case "Server":
		fmt.Fprintf(output, "server := %s\nscope := []string{\"-s\"}\n", receiverName)
	case "GlobalSessionScope":
		fmt.Fprintf(output, "server := %s.server\nscope := []string{\"-g\"}\n", receiverName)
	case "GlobalWindowScope":
		fmt.Fprintf(output, "server := %s.server\nscope := []string{\"-g\", \"-w\"}\n", receiverName)
	case "Session":
		fmt.Fprintf(output, "server, scope, err := sessionOptionRuntimeScope(%s)\nif err != nil { return SetArrayResult{}, err }\n", receiverName)
	case "Window":
		fmt.Fprintf(output, "server, scope, err := windowOptionRuntimeScope(%s)\nif err != nil { return SetArrayResult{}, err }\n", receiverName)
	case "Pane":
		fmt.Fprintf(output, "server, scope, err := paneOptionRuntimeScope(%s)\nif err != nil { return SetArrayResult{}, err }\n", receiverName)
	}
	fmt.Fprintf(
		output,
		"return setTypedOptionArray(ctx, server, scope, generatedOptionScope%s, %s, value)\n}\n\n",
		titleWord(scope), strconv.Quote(entry.Name),
	)
}

func writeAccessorDocumentation(output *bytes.Buffer, surface generatedSurface, entry entrySpec) {
	variants := variantsForScope(entry, surface.Scope)
	fmt.Fprintf(output, "// %s returns the %q %s value as [OptionValue] with Go value shape OptionValue[%s]. It does not query tmux.\n", entry.GoName, entry.Name, surface.Noun, entry.GoType)
	fmt.Fprintf(output, "// Its scope-specific minimum tmux version and supported variants are %s.\n", documentedVariants(variants))
	if len(surface.Setters) != 0 {
		links := make([]string, len(surface.Setters))
		for index, receiver := range surface.Setters {
			links[index] = "[" + receiver + "." + setterName(entry) + "]"
		}
		fmt.Fprintf(output, "// Set it with %s.\n", strings.Join(links, " or "))
	}
	fmt.Fprintf(output, "// Use %s for caller-named or undecoded values.\n", strings.Join(surface.RawSources, " or "))
	if entry.Style {
		output.WriteString("// It is a style option.\n")
	} else {
		output.WriteString("// It is not a style option.\n")
	}
	if entry.Array {
		output.WriteString("// Its present SparseArray value preserves assigned tmux indexes, including gaps.\n")
	}
}

func variantsForScope(entry entrySpec, scope string) []variantSpec {
	result := make([]variantSpec, 0, len(entry.Variants))
	for _, variant := range entry.Variants {
		if slices.Contains(variant.Scopes, scope) {
			result = append(result, variant)
		}
	}
	return result
}

func documentedVariants(variants []variantSpec) string {
	values := make([]string, 0, len(variants))
	for _, variant := range variants {
		value := fmt.Sprintf("%s since tmux %s", variant.TmuxKind, variant.Since)
		if len(variant.Choices) != 0 {
			choices := make([]string, len(variant.Choices))
			for index, choice := range variant.Choices {
				choices[index] = strconv.Quote(choice)
			}
			value += " (choices: " + strings.Join(choices, ", ") + ")"
		}
		values = append(values, value)
	}
	return strings.Join(values, "; ")
}

func notificationGrammar(entry notificationSpec) string {
	parts := []string{entry.WireName}
	for _, label := range entry.PrefixLabels {
		parts = append(parts, "<"+label+">")
	}
	prefix := strings.Join(parts, " ")
	switch entry.Tail {
	case "none":
		return prefix
	case "required":
		return prefix + " <" + entry.TailLabel + ">"
	case "optional":
		return prefix + " or " + prefix + " <" + entry.TailLabel + ">"
	case "colon":
		return prefix + " { zero or more <reserved argument> } : <" + entry.TailLabel + ">"
	default:
		panic("validated notification has unknown tail grammar")
	}
}

func notificationArguments(entry notificationSpec) string {
	if len(entry.PrefixLabels) == 0 && entry.Tail == "none" {
		return "no arguments"
	}
	parts := slices.Clone(entry.PrefixLabels)
	switch entry.Tail {
	case "required", "optional":
		parts = append(parts, entry.TailLabel+" tail")
	case "colon":
		return "the prefix arguments, then zero or more reserved arguments, then the " + entry.TailLabel + " tail; the prefix arguments are " + strings.Join(parts, ", ")
	}
	return strings.Join(parts, ", ")
}

func decodedValueField(entry entrySpec) string {
	switch entry.GoType {
	case "bool":
		return "boolValue"
	case "int64":
		return "int64Value"
	case "string":
		return "stringValue"
	case "SparseArray[string]":
		return "sparseStringValue"
	default:
		if isChoiceEntry(entry) {
			return "stringValue"
		}
		panic("validated option spec has unknown Go type " + entry.GoType)
	}
}

func decodedValueExpression(entry entrySpec) string {
	field := "value." + decodedValueField(entry)
	if isChoiceEntry(entry) {
		return entry.GoType + "(" + field + ")"
	}
	return field
}

func privateName(name string) string {
	return strings.ToLower(name[:1]) + name[1:]
}

func titleWord(word string) string {
	return strings.ToUpper(word[:1]) + word[1:]
}
