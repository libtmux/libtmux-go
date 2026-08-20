package tmux

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// ErrMalformedOptionOutput classifies [OptionDecodeError] through errors.Is.
var (
	// ErrMalformedOptionOutput identifies a recognized option or hook record
	// that cannot be decoded without ambiguity. It is matched by errors.Is for
	// OptionDecodeError.
	ErrMalformedOptionOutput         = errors.New("tmux: malformed option output")
	errMalformedArgsEscapedValue     = errors.New("tmux: malformed args_escape value")
	errMalformedTargetedOptionOutput = errors.New(
		"tmux: malformed targeted option output",
	)
)

// OptionDecodeError reports a malformed recognized record without retaining
// its value. It matches [ErrMalformedOptionOutput] through errors.Is; callers
// can recover Record and Reason with errors.As. Record is the zero-based
// physical output line.
type OptionDecodeError struct {
	// Record is the zero-based physical output line.
	Record int
	// Reason describes the malformed record without retaining its value.
	Reason string
}

// Error implements error.
func (e *OptionDecodeError) Error() string {
	return fmt.Sprintf("%v: record %d: %s", ErrMalformedOptionOutput, e.Record, e.Reason)
}

// Unwrap makes OptionDecodeError compatible with ErrMalformedOptionOutput.
func (e *OptionDecodeError) Unwrap() error { return ErrMalformedOptionOutput }

type optionVersionBoundaryError struct {
	kind error
}

func (e *optionVersionBoundaryError) Error() string {
	return fmt.Sprintf("%v: tmux version boundary is malformed", ErrMalformedOptionOutput)
}

func (e *optionVersionBoundaryError) Unwrap() []error {
	return []error{ErrMalformedOptionOutput, e.kind}
}

type decodedOptionValue struct {
	name              string
	origin            OptionOrigin
	boolValue         bool
	int64Value        int64
	stringValue       string
	sparseStringValue SparseArray[string]
}

type decodedOptionAccumulator struct {
	definition generatedOptionDefinition
	ordinal    int
	record     int
	origin     OptionOrigin
	value      decodedOptionValue
	entries    []SparseEntry[string]
	bare       bool
	seen       bool
}

type decodedOptionKey struct {
	base      string
	index     int
	indexed   bool
	inherited bool
	valid     bool
}

// optionValueDecodeError omits serialized option data because values may be secret.
type optionValueDecodeError struct {
	kind   error
	offset int
	reason string
}

func (e *optionValueDecodeError) Error() string {
	return fmt.Sprintf("%v at byte %d: %s", e.kind, e.offset, e.reason)
}

func (e *optionValueDecodeError) Unwrap() error {
	return e.kind
}

func decodeArgsEscapedValue(serialized []byte) (string, error) {
	decoded, err := decodeArgsEscapedBytes(serialized)
	if err != nil {
		return "", err
	}
	return tmuxcmd.DecodeBackslashReplace(decoded), nil
}

func decodeArgsEscapedBytes(serialized []byte) ([]byte, error) {
	if len(serialized) == 0 {
		return nil, newOptionValueDecodeError(
			errMalformedArgsEscapedValue,
			0,
			"missing serialized value",
		)
	}

	body := serialized
	bodyOffset := 0
	quote := byte(0)
	if serialized[0] == '\'' || serialized[0] == '"' {
		quote = serialized[0]
		if len(serialized) < 2 || serialized[len(serialized)-1] != quote {
			return nil, newOptionValueDecodeError(
				errMalformedArgsEscapedValue,
				0,
				"unmatched outer quote",
			)
		}
		body = serialized[1 : len(serialized)-1]
		bodyOffset = 1
	}

	decoded := make([]byte, 0, len(body))
	for index := 0; index < len(body); index++ {
		current := body[index]
		if current == '\'' || current == '"' {
			if quote == 0 || current == quote {
				return nil, newOptionValueDecodeError(
					errMalformedArgsEscapedValue,
					bodyOffset+index,
					"unexpected unescaped quote",
				)
			}
		}
		if current != '\\' {
			decoded = append(decoded, current)
			continue
		}

		if index+1 >= len(body) {
			return nil, newOptionValueDecodeError(
				errMalformedArgsEscapedValue,
				bodyOffset+index,
				"dangling escape",
			)
		}
		escaped := body[index+1]
		if isOctalDigit(escaped) {
			if index+3 >= len(body) ||
				!isOctalDigit(body[index+2]) ||
				!isOctalDigit(body[index+3]) {
				return nil, newOptionValueDecodeError(
					errMalformedArgsEscapedValue,
					bodyOffset+index,
					"octal escape must contain exactly three digits",
				)
			}
			value := (int(escaped-'0') << 6) |
				(int(body[index+2]-'0') << 3) |
				int(body[index+3]-'0')
			if value > 0xff {
				return nil, newOptionValueDecodeError(
					errMalformedArgsEscapedValue,
					bodyOffset+index,
					"octal escape exceeds one byte",
				)
			}
			decoded = append(decoded, byte(value))
			index += 3
			continue
		}

		decoded = append(decoded, decodeArgsCStyleEscape(escaped))
		index++
	}
	return decoded, nil
}

func decodeArgsCStyleEscape(value byte) byte {
	switch value {
	case 'a':
		return '\a'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 's':
		return ' '
	case 't':
		return '\t'
	case 'v':
		return '\v'
	case 'E':
		return 0x1b
	default:
		return value
	}
}

func isOctalDigit(value byte) bool {
	return value >= '0' && value <= '7'
}

func decodeTargetedOptionOutput(raw []byte) (string, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return "", newOptionValueDecodeError(
			errMalformedTargetedOptionOutput,
			len(raw),
			"missing terminal line feed",
		)
	}
	return tmuxcmd.DecodeBackslashReplace(raw[:len(raw)-1]), nil
}

func newOptionValueDecodeError(kind error, offset int, reason string) *optionValueDecodeError {
	return &optionValueDecodeError{kind: kind, offset: offset, reason: reason}
}

func decodeOptionOutput(
	raw []byte,
	definitions []generatedOptionDefinition,
	scope generatedOptionScope,
	hooks bool,
	version *Version,
) ([]decodedOptionValue, error) {
	if len(raw) == 0 {
		return make([]decodedOptionValue, 0), nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, &OptionDecodeError{
			Record: bytes.Count(raw, []byte{'\n'}),
			Reason: "missing terminal line feed",
		}
	}

	type catalogEntry struct {
		definition generatedOptionDefinition
		ordinal    int
	}
	catalog := make(map[string]catalogEntry, len(definitions))
	for ordinal, definition := range definitions {
		if generatedDefinitionSupportsScope(definition, scope) {
			catalog[definition.name] = catalogEntry{definition: definition, ordinal: ordinal}
		}
	}

	lines := bytes.Split(raw[:len(raw)-1], []byte{'\n'})
	values := make([]decodedOptionValue, 0, len(catalog))
	seenDefinitions := make(map[int]struct{}, len(catalog))
	var current *decodedOptionAccumulator
	for record, line := range lines {
		keyBytes, serialized, hasValue := bytes.Cut(line, []byte{' '})
		key := parseDecodedOptionKey(string(keyBytes))
		entry, known := catalog[key.base]
		if !known {
			continue
		}
		if !key.valid {
			return nil, newBulkOptionDecodeError(record, "recognized record has malformed key")
		}
		if generatedDefinitionScopeVariesByVersion(entry.definition, scope) {
			if version == nil {
				return nil, newBulkOptionDecodeError(record, "recognized record requires a tmux version boundary")
			}
			if !generatedDefinitionSupportsVersionScope(entry.definition, *version, scope) {
				return nil, newBulkOptionDecodeError(record, "recognized record is unsupported at this tmux version and scope")
			}
		}

		if current == nil || entry.ordinal != current.ordinal {
			if _, seen := seenDefinitions[entry.ordinal]; seen {
				return nil, newBulkOptionDecodeError(record, "recognized records are not contiguous")
			}
			if current != nil {
				value, err := finishDecodedOption(*current)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			seenDefinitions[entry.ordinal] = struct{}{}
			current = &decodedOptionAccumulator{
				definition: entry.definition,
				ordinal:    entry.ordinal,
				record:     record,
				origin:     OptionOriginLocal,
				value:      decodedOptionValue{name: entry.definition.name},
				entries:    make([]SparseEntry[string], 0),
			}
		}

		origin := OptionOriginLocal
		if key.inherited {
			origin = OptionOriginInherited
		}
		if current.seen && current.origin != origin {
			return nil, newBulkOptionDecodeError(record, "recognized array records mix origins")
		}
		current.origin = origin

		if !entry.definition.array {
			if current.seen {
				return nil, newBulkOptionDecodeError(record, "recognized scalar is repeated")
			}
			if key.indexed {
				return nil, newBulkOptionDecodeError(record, "recognized scalar has an array index")
			}
			if !hasValue {
				return nil, newBulkOptionDecodeError(record, "recognized scalar is missing its value")
			}
			if err := decodeScalarOptionValue(&current.value, entry.definition, serialized); err != nil {
				return nil, newBulkOptionDecodeError(record, "recognized scalar value is malformed")
			}
			current.seen = true
			continue
		}

		if !key.indexed {
			if current.seen {
				return nil, newBulkOptionDecodeError(record, "recognized array mixes bare and indexed records")
			}
			empty, err := decodeArrayOptionElement(serialized, hasValue, hooks, true)
			if err != nil || empty != "" {
				return nil, newBulkOptionDecodeError(record, "recognized bare array is not empty")
			}
			current.bare = true
			current.seen = true
			continue
		}
		if current.bare {
			return nil, newBulkOptionDecodeError(record, "recognized array mixes bare and indexed records")
		}
		if !hasValue {
			return nil, newBulkOptionDecodeError(record, "recognized indexed array value is missing")
		}
		if len(current.entries) != 0 && key.index <= current.entries[len(current.entries)-1].Index {
			return nil, newBulkOptionDecodeError(record, "recognized array indices are not unique and ascending")
		}
		value, err := decodeArrayOptionElement(serialized, true, hooks, false)
		if err != nil {
			return nil, newBulkOptionDecodeError(record, "recognized array value is malformed")
		}
		current.entries = append(current.entries, SparseEntry[string]{Index: key.index, Value: value})
		current.seen = true
	}

	if current != nil {
		value, err := finishDecodedOption(*current)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	slices.SortFunc(values, func(left, right decodedOptionValue) int {
		return cmp.Compare(catalog[left.name].ordinal, catalog[right.name].ordinal)
	})
	return values, nil
}

func parseDecodedOptionKey(raw string) decodedOptionKey {
	key := decodedOptionKey{valid: true}
	if strings.HasSuffix(raw, "*") {
		key.inherited = true
		raw = strings.TrimSuffix(raw, "*")
	}
	if open := strings.LastIndexByte(raw, '['); open >= 0 {
		key.base = raw[:open]
		if open == 0 || !strings.HasSuffix(raw, "]") || strings.ContainsAny(raw[:open], "[]*") {
			key.valid = false
			return key
		}
		indexText := raw[open+1 : len(raw)-1]
		index, err := strconv.Atoi(indexText)
		if err != nil || index < 0 {
			key.valid = false
			return key
		}
		key.index = index
		key.indexed = true
		return key
	}
	key.base = raw
	if raw == "" || strings.ContainsAny(raw, "]*") {
		key.valid = false
	}
	return key
}

func generatedDefinitionSupportsScope(
	definition generatedOptionDefinition,
	scope generatedOptionScope,
) bool {
	for _, variant := range definition.variants {
		if variant.scopes&scope != 0 {
			return true
		}
	}
	return false
}

func generatedDefinitionSupportsVersionScope(
	definition generatedOptionDefinition,
	version Version,
	scope generatedOptionScope,
) bool {
	var active *generatedOptionVariant
	for index := range definition.variants {
		variant := &definition.variants[index]
		if !version.AtLeast(variant.minimum) {
			break
		}
		active = variant
	}
	return active != nil && active.scopes&scope != 0
}

func generatedDefinitionScopeVariesByVersion(
	definition generatedOptionDefinition,
	scope generatedOptionScope,
) bool {
	baseline := generatedDefinitionSupportsVersionScope(
		definition,
		generatedOptionFeatureFloor,
		scope,
	)
	if !baseline {
		return true
	}
	for _, variant := range definition.variants {
		if variant.minimum.Compare(generatedOptionFeatureFloor) <= 0 {
			continue
		}
		if variant.scopes&scope == 0 {
			return true
		}
	}
	return false
}

func optionOutputRequiresVersion(
	raw []byte,
	definitions []generatedOptionDefinition,
	scope generatedOptionScope,
) bool {
	versioned := make(map[string]struct{})
	for _, definition := range definitions {
		if generatedDefinitionSupportsScope(definition, scope) &&
			generatedDefinitionScopeVariesByVersion(definition, scope) {
			versioned[definition.name] = struct{}{}
		}
	}
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		keyBytes, _, _ := bytes.Cut(line, []byte{' '})
		key := parseDecodedOptionKey(string(keyBytes))
		if _, ok := versioned[key.base]; ok {
			return true
		}
	}
	return false
}

func decodeScalarOptionValue(
	destination *decodedOptionValue,
	definition generatedOptionDefinition,
	serialized []byte,
) error {
	switch definition.valueKind {
	case generatedOptionValueKindBool:
		switch string(serialized) {
		case "on":
			destination.boolValue = true
		case "off":
			destination.boolValue = false
		default:
			return errors.New("invalid flag")
		}
	case generatedOptionValueKindInt64:
		value, err := strconv.ParseInt(string(serialized), 10, 64)
		if err != nil {
			return err
		}
		destination.int64Value = value
	case generatedOptionValueKindString:
		value, err := decodeArgsEscapedValue(serialized)
		if err != nil {
			return err
		}
		destination.stringValue = value
	case generatedOptionValueKindSparseString:
		return errors.New("invalid scalar kind")
	default:
		return errors.New("invalid scalar kind")
	}
	return nil
}

func decodeArrayOptionElement(
	serialized []byte,
	hasValue bool,
	hooks bool,
	bare bool,
) (string, error) {
	if bare && !hasValue {
		return "", nil
	}
	if hooks {
		if !hasValue {
			if bare {
				return "", nil
			}
			return "", errors.New("missing hook value")
		}
		return tmuxcmd.DecodeBackslashReplace(serialized), nil
	}
	if !hasValue {
		return "", errors.New("missing serialized value")
	}
	return decodeArgsEscapedValue(serialized)
}

func finishDecodedOption(current decodedOptionAccumulator) (decodedOptionValue, error) {
	if !current.seen {
		return decodedOptionValue{}, newBulkOptionDecodeError(current.record, "recognized record is incomplete")
	}
	current.value.origin = current.origin
	if current.definition.array {
		array, err := NewSparseArray(current.entries...)
		if err != nil {
			return decodedOptionValue{}, newBulkOptionDecodeError(current.record, "recognized array indices are invalid")
		}
		current.value.sparseStringValue = array
	}
	return current.value, nil
}

func optionValueFromDecoded[T any](value T, origin OptionOrigin) OptionValue[T] {
	if origin == OptionOriginInherited {
		return newInheritedOptionValue(value)
	}
	return newLocalOptionValue(value)
}

func newBulkOptionDecodeError(record int, reason string) *OptionDecodeError {
	return &OptionDecodeError{Record: record, Reason: reason}
}
