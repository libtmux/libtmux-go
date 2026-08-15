package tmux

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrMalformedComplexOption identifies an invalid entry in a parsed complex option.
var ErrMalformedComplexOption = errors.New("tmux: malformed complex option")

// ComplexOptionDecodeError identifies one malformed sparse-array entry without
// retaining its raw value.
type ComplexOptionDecodeError struct {
	// Option is the option name whose sparse-array entry was malformed.
	Option string
	// Index is the zero-based sparse-array entry index.
	Index int
	// Reason describes the syntax failure without retaining raw option contents.
	Reason string
}

// Error implements error.
func (e *ComplexOptionDecodeError) Error() string {
	return fmt.Sprintf(
		"%v: %s[%d]: %s",
		ErrMalformedComplexOption,
		e.Option,
		e.Index,
		e.Reason,
	)
}

// Unwrap makes ComplexOptionDecodeError compatible with ErrMalformedComplexOption.
func (e *ComplexOptionDecodeError) Unwrap() error { return ErrMalformedComplexOption }

// CommandAlias is one parsed command-alias entry.
type CommandAlias struct {
	// Name is the alias name.
	Name string
	// Command is the tmux command text associated with Name.
	Command string
}

// CommandAliases is an immutable parsed command-alias option. Its zero value
// is an empty collection: Len is zero, Lookup reports false, and Entries
// returns a nonnil empty slice.
type CommandAliases struct {
	entries []CommandAlias
	byName  map[string]int
}

// Lookup returns the command registered for name.
func (a CommandAliases) Lookup(name string) (string, bool) {
	index, ok := a.byName[name]
	if !ok {
		return "", false
	}
	return a.entries[index].Command, true
}

// Entries returns a fresh slice in first-definition order.
func (a CommandAliases) Entries() []CommandAlias {
	return append(make([]CommandAlias, 0, len(a.entries)), a.entries...)
}

// Len returns the number of distinct aliases.
func (a CommandAliases) Len() int { return len(a.entries) }

// TerminalFeature is one terminal pattern and its enabled features.
type TerminalFeature struct {
	// Terminal is the terminal pattern.
	Terminal string
	// Features is an owned feature list for Terminal.
	Features []string
}

// TerminalFeatures is an immutable parsed terminal-features option. Its zero
// value is an empty collection: Len is zero, Lookup reports false, and Entries
// returns a nonnil empty slice.
type TerminalFeatures struct {
	entries    []TerminalFeature
	byTerminal map[string]int
}

// Lookup returns a fresh feature slice for terminal.
func (f TerminalFeatures) Lookup(terminal string) ([]string, bool) {
	index, ok := f.byTerminal[terminal]
	if !ok {
		return nil, false
	}
	return append(make([]string, 0, len(f.entries[index].Features)), f.entries[index].Features...), true
}

// Entries returns a deep fresh copy in first-definition order.
func (f TerminalFeatures) Entries() []TerminalFeature {
	entries := make([]TerminalFeature, len(f.entries))
	for index, entry := range f.entries {
		entries[index] = TerminalFeature{
			Terminal: entry.Terminal,
			Features: append(make([]string, 0, len(entry.Features)), entry.Features...),
		}
	}
	return entries
}

// Len returns the number of distinct terminal patterns.
func (f TerminalFeatures) Len() int { return len(f.entries) }

// TerminalOverrideValueKind identifies one closed terminal capability value form.
type TerminalOverrideValueKind uint8

const (
	// TerminalOverrideValueFlag identifies a capability without an assigned value.
	TerminalOverrideValueFlag TerminalOverrideValueKind = iota + 1
	// TerminalOverrideValueInteger identifies an unsigned decimal integer.
	TerminalOverrideValueInteger
	// TerminalOverrideValueText identifies an assigned non-integer string.
	TerminalOverrideValueText
)

// TerminalOverrideValue is a flag, arbitrary-precision integer, or string. Its
// zero value has no recognized kind: IsFlag is false, Integer and Text report
// false, and it represents no parsed capability assignment.
type TerminalOverrideValue struct {
	kind TerminalOverrideValueKind
	text string
}

// Kind returns the value's closed form.
func (v TerminalOverrideValue) Kind() TerminalOverrideValueKind { return v.kind }

// IsFlag reports whether the capability has no assigned value.
func (v TerminalOverrideValue) IsFlag() bool {
	return v.kind == TerminalOverrideValueFlag
}

// Integer returns a fresh arbitrary-precision integer when the value is numeric.
func (v TerminalOverrideValue) Integer() (*big.Int, bool) {
	if v.kind != TerminalOverrideValueInteger {
		return nil, false
	}
	integer, ok := new(big.Int).SetString(v.text, 10)
	return integer, ok
}

// Text returns the assigned string when the value is textual.
func (v TerminalOverrideValue) Text() (string, bool) {
	if v.kind != TerminalOverrideValueText {
		return "", false
	}
	return v.text, true
}

// TerminalCapability is one parsed terminal override capability.
type TerminalCapability struct {
	// Name is the capability name.
	Name string
	// Value is the parsed capability value.
	Value TerminalOverrideValue
}

// TerminalOverride is the immutable capability set for one terminal pattern.
// Its zero value is an empty immutable collection: Len is zero, Lookup reports
// false, and Entries returns a nonnil empty slice.
type TerminalOverride struct {
	entries []TerminalCapability
	byName  map[string]int
}

// Lookup returns one capability value.
func (o TerminalOverride) Lookup(name string) (TerminalOverrideValue, bool) {
	index, ok := o.byName[name]
	if !ok {
		return TerminalOverrideValue{}, false
	}
	return o.entries[index].Value, true
}

// Entries returns a fresh slice in first-definition order.
func (o TerminalOverride) Entries() []TerminalCapability {
	return append(make([]TerminalCapability, 0, len(o.entries)), o.entries...)
}

// Len returns the number of distinct capabilities.
func (o TerminalOverride) Len() int { return len(o.entries) }

// TerminalOverrideEntry is one terminal pattern and its parsed capabilities.
type TerminalOverrideEntry struct {
	// Terminal is the terminal pattern.
	Terminal string
	// Capabilities is the immutable capability set for Terminal.
	Capabilities TerminalOverride
}

// TerminalOverrides is an immutable parsed terminal-overrides option. Its zero
// value is an empty immutable collection: Len is zero, Lookup reports false,
// and Entries returns a nonnil empty slice.
type TerminalOverrides struct {
	entries    []TerminalOverrideEntry
	byTerminal map[string]int
}

// Lookup returns the capabilities for terminal.
func (o TerminalOverrides) Lookup(terminal string) (TerminalOverride, bool) {
	index, ok := o.byTerminal[terminal]
	if !ok {
		return TerminalOverride{}, false
	}
	return o.entries[index].Capabilities, true
}

// Entries returns a fresh outer slice in first-definition order. Nested
// TerminalOverride values remain immutable.
func (o TerminalOverrides) Entries() []TerminalOverrideEntry {
	return append(make([]TerminalOverrideEntry, 0, len(o.entries)), o.entries...)
}

// Len returns the number of distinct terminal patterns.
func (o TerminalOverrides) Len() int { return len(o.entries) }

// CommandAliases parses command-alias while preserving option presence and origin.
// A nonnil joined error contains [ComplexOptionDecodeError] values, each usable
// with [errors.Is] or [errors.As]; the returned projection still contains every
// valid entry and is owned by the caller.
func (v ServerOptionValues) CommandAliases() (OptionValue[CommandAliases], error) {
	raw, ok := v.commandAlias.Get()
	if !ok {
		return OptionValue[CommandAliases]{}, nil
	}
	aliases := CommandAliases{byName: make(map[string]int)}
	var decodeErrors []error
	for index, item := range raw.All() {
		name, command, found := strings.Cut(item, "=")
		if !found {
			decodeErrors = append(decodeErrors, &ComplexOptionDecodeError{
				Option: "command-alias",
				Index:  index,
				Reason: "expected alias=command",
			})
			continue
		}
		aliases.set(name, command)
	}
	return projectOptionValue(v.commandAlias, aliases), errors.Join(decodeErrors...)
}

// ParsedTerminalFeatures parses terminal-features while preserving option
// presence and origin. A nonnil joined error contains
// [ComplexOptionDecodeError] values and the returned owned projection contains
// every valid entry.
func (v ServerOptionValues) ParsedTerminalFeatures() (OptionValue[TerminalFeatures], error) {
	raw, ok := v.terminalFeatures.Get()
	if !ok {
		return OptionValue[TerminalFeatures]{}, nil
	}
	features := TerminalFeatures{byTerminal: make(map[string]int)}
	var decodeErrors []error
	for index, item := range raw.All() {
		terminal, featureList, found := strings.Cut(item, ":")
		if !found {
			decodeErrors = append(decodeErrors, &ComplexOptionDecodeError{
				Option: "terminal-features",
				Index:  index,
				Reason: "expected terminal:features",
			})
			continue
		}
		features.set(terminal, strings.Split(featureList, ":"))
	}
	return projectOptionValue(v.terminalFeatures, features), errors.Join(decodeErrors...)
}

// ParsedTerminalOverrides parses terminal-overrides while preserving option
// presence and origin. Terminal-overrides syntax is permissive: no malformed
// entry error is returned, and the result owns its parsed projection.
func (v ServerOptionValues) ParsedTerminalOverrides() (OptionValue[TerminalOverrides], error) {
	raw, ok := v.terminalOverrides.Get()
	if !ok {
		return OptionValue[TerminalOverrides]{}, nil
	}
	overrides := TerminalOverrides{byTerminal: make(map[string]int)}
	for _, item := range raw.Values() {
		parts := strings.Split(item, ":")
		terminal := parts[0]
		for _, capability := range parts[1:] {
			if capability == "" {
				continue
			}
			name, assigned, hasAssignment := strings.Cut(capability, "=")
			value := TerminalOverrideValue{kind: TerminalOverrideValueFlag}
			if hasAssignment {
				value = TerminalOverrideValue{kind: TerminalOverrideValueText, text: assigned}
				if decimalDigits(assigned) {
					value.kind = TerminalOverrideValueInteger
				}
			}
			overrides.set(terminal, name, value)
		}
		overrides.ensureTerminal(terminal)
	}
	return projectOptionValue(v.terminalOverrides, overrides), nil
}

func (a *CommandAliases) set(name, command string) {
	if index, ok := a.byName[name]; ok {
		a.entries[index].Command = command
		return
	}
	a.byName[name] = len(a.entries)
	a.entries = append(a.entries, CommandAlias{Name: name, Command: command})
}

func (f *TerminalFeatures) set(terminal string, features []string) {
	owned := append(make([]string, 0, len(features)), features...)
	if index, ok := f.byTerminal[terminal]; ok {
		f.entries[index].Features = owned
		return
	}
	f.byTerminal[terminal] = len(f.entries)
	f.entries = append(f.entries, TerminalFeature{Terminal: terminal, Features: owned})
}

func (o *TerminalOverrides) ensureTerminal(terminal string) int {
	if index, ok := o.byTerminal[terminal]; ok {
		return index
	}
	index := len(o.entries)
	o.byTerminal[terminal] = index
	o.entries = append(o.entries, TerminalOverrideEntry{
		Terminal: terminal,
		Capabilities: TerminalOverride{
			byName: make(map[string]int),
		},
	})
	return index
}

func (o *TerminalOverrides) set(terminal, name string, value TerminalOverrideValue) {
	index := o.ensureTerminal(terminal)
	capabilities := &o.entries[index].Capabilities
	if capabilityIndex, ok := capabilities.byName[name]; ok {
		capabilities.entries[capabilityIndex].Value = value
		return
	}
	capabilities.byName[name] = len(capabilities.entries)
	capabilities.entries = append(capabilities.entries, TerminalCapability{Name: name, Value: value})
}

func projectOptionValue[Raw, Parsed any](
	raw OptionValue[Raw],
	parsed Parsed,
) OptionValue[Parsed] {
	return OptionValue[Parsed]{value: parsed, origin: raw.origin}
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
