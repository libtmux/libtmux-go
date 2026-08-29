package tmux

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

//go:generate go run ./internal/generate/formats -spec internal/generate/formats/spec.json -output format_generated.go

// ErrMalformedFormatOutput identifies invalid escaped tmux format output. It
// is matched by errors.Is for [FormatDecodeError].
var ErrMalformedFormatOutput = errors.New("tmux: malformed format output")

// FormatDecodeError identifies a malformed escaped tmux format record. It
// matches [ErrMalformedFormatOutput] through errors.Is; callers can recover its
// location fields with errors.As. Library-created errors do not retain decoded
// record values, which may contain caller data.
type FormatDecodeError struct {
	// Record is the one-based physical record number.
	Record int
	// Field names the format field that could not be decoded.
	Field string
	// Offset is the zero-based byte offset within Field's encoded value.
	Offset int
	// Reason describes the malformed encoding.
	Reason string
}

// Error implements error.
func (e *FormatDecodeError) Error() string {
	return fmt.Sprintf(
		"%v at byte %d, record %d, field %q: %s",
		ErrMalformedFormatOutput,
		e.Offset,
		e.Record,
		e.Field,
		e.Reason,
	)
}

// Unwrap makes FormatDecodeError compatible with ErrMalformedFormatOutput.
func (e *FormatDecodeError) Unwrap() error {
	return ErrMalformedFormatOutput
}

type formatScope uint8

const (
	formatScopeBuffer formatScope = iota
	formatScopeClient
	formatScopeContext
	formatScopeEvent
	formatScopePane
	formatScopeSession
	formatScopeUniversal
	formatScopeWindow
)

type formatKind uint8

const (
	formatKindString formatKind = iota
	formatKindBool
	formatKindInt
	formatKindTime
	formatKindSessionID
	formatKindWindowID
	formatKindPaneID
	formatKindClientName
	formatKindVersion
)

type formatField struct {
	name    string
	scope   formatScope
	kind    formatKind
	minimum Version
}

type formatValues struct {
	version Version
	values  map[string]string
}

// FormatValues is a read-only view of format expansions materialized with a
// Session, Window, Pane, or Client. Its methods do not query tmux. Projected
// cross-scope fields do not imply that the referenced record was materialized.
//
// Typed accessors return ok == false for an absent, empty, or malformed value;
// [FormatValues.Raw] distinguishes absent from present empty expansions. The
// zero value reports every field as absent.
type FormatValues struct {
	values formatValues
}

const (
	formatFieldSeparator    byte = '|'
	formatRecordTerminator  byte = '='
	formatShellEscapedBytes      = "|&;<>()$`\\\"'*?[# =%"
)

func newFormatValues(version Version, fields []formatField, values []string) (formatValues, error) {
	if len(fields) != len(values) {
		return formatValues{}, &FormatDecodeError{
			Record: 1,
			Offset: 0,
			Reason: fmt.Sprintf("decoded %d values for %d fields", len(values), len(fields)),
		}
	}
	result := formatValues{
		version: version,
		values:  make(map[string]string, len(fields)),
	}
	for index, field := range fields {
		result.values[field.name] = values[index]
	}
	return result, nil
}

func (v formatValues) get(name string) (string, bool) {
	value, ok := v.values[name]
	return value, ok
}

func (v formatValues) getString(name string) (string, bool) {
	value, ok := v.values[name]
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func (v formatValues) getBool(name string) (bool, bool) {
	value, ok := v.values[name]
	if !ok {
		return false, false
	}
	switch value {
	case "0":
		return false, true
	case "1":
		return true, true
	default:
		return false, false
	}
}

func (v formatValues) getInt(name string) (int, bool) {
	value, ok := v.values[name]
	if !ok || value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func (v formatValues) getTime(name string) (time.Time, bool) {
	value, ok := v.values[name]
	if !ok || value == "" {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func (v formatValues) getSessionID(name string) (SessionID, bool) {
	value, ok := v.getNominalID(name, '$')
	return SessionID(value), ok
}

func (v formatValues) getWindowID(name string) (WindowID, bool) {
	value, ok := v.getNominalID(name, '@')
	return WindowID(value), ok
}

func (v formatValues) getPaneID(name string) (PaneID, bool) {
	value, ok := v.getNominalID(name, '%')
	return PaneID(value), ok
}

func (v formatValues) getNominalID(name string, sigil byte) (string, bool) {
	value, ok := v.values[name]
	if !ok || len(value) < 2 || value[0] != sigil {
		return "", false
	}
	if _, err := strconv.ParseUint(value[1:], 10, 64); err != nil {
		return "", false
	}
	return value, true
}

func (v formatValues) getClientName(name string) (ClientName, bool) {
	value, ok := v.getString(name)
	return ClientName(value), ok
}

func (v formatValues) getVersion(name string) (Version, bool) {
	value, ok := v.values[name]
	if !ok || value == "" {
		return Version{}, false
	}
	parsed, err := ParseVersion(value)
	if err != nil {
		return Version{}, false
	}
	return parsed, true
}

// Raw returns the exact materialized tmux format expansion for name. It never
// queries tmux; ok distinguishes an absent field from a present empty value.
func (v FormatValues) Raw(name string) (string, bool) {
	return v.values.get(name)
}

// Formats returns this Session's read-only materialized tmux format values. It
// does not query tmux; use [Server.Snapshot] to obtain a fresh record.
func (s Session) Formats() FormatValues {
	return FormatValues{values: s.formats}
}

// Formats returns this Window's read-only materialized tmux format values. It
// does not query tmux; use [Server.Snapshot] to obtain a fresh record.
func (w Window) Formats() FormatValues {
	return FormatValues{values: w.formats}
}

// Formats returns this Pane's read-only materialized tmux format values. It
// does not query tmux; use [Server.Snapshot] to obtain a fresh record.
func (p Pane) Formats() FormatValues {
	return FormatValues{values: p.formats}
}

// Formats returns this Client's read-only materialized tmux format values. It
// does not query tmux; use [Server.Snapshot] to obtain a fresh record.
func (c Client) Formats() FormatValues {
	return FormatValues{values: c.formats}
}

func (v formatValues) tmuxVersion() Version {
	return v.version
}

func formatFieldsFor(listCommand string, version Version) ([]formatField, error) {
	includeClient := false
	switch listCommand {
	case "list-clients":
		includeClient = true
	case "list-panes", "list-sessions", "list-windows":
	default:
		return nil, fmt.Errorf("tmux: unsupported format list command %q", listCommand)
	}

	fields := make([]formatField, 0, len(generatedFormatFields))
	for _, field := range generatedFormatFields {
		if !version.AtLeast(field.minimum) {
			continue
		}
		switch field.scope {
		case formatScopeUniversal, formatScopeSession, formatScopeWindow, formatScopePane:
			fields = append(fields, field)
		case formatScopeClient:
			if includeClient {
				fields = append(fields, field)
			}
		case formatScopeBuffer, formatScopeContext, formatScopeEvent:
			continue
		}
	}
	return fields, nil
}

func formatTemplate(fields []formatField) string {
	var template strings.Builder
	for index, field := range fields {
		if index != 0 {
			template.WriteByte(formatFieldSeparator)
		}
		template.WriteString("#{q:")
		template.WriteString(field.name)
		template.WriteByte('}')
	}
	template.WriteByte(formatRecordTerminator)
	return template.String()
}

func decodeFormatRecords(output []byte, version Version, fields []formatField) ([]formatValues, error) {
	records := make([]formatValues, 0)
	if len(output) == 0 {
		return records, nil
	}
	if len(fields) == 0 {
		return nil, &FormatDecodeError{
			Record: 1,
			Offset: 0,
			Reason: "received output for an empty format template",
		}
	}

	offset := 0
	for recordIndex := 0; offset < len(output); recordIndex++ {
		values := make([]string, len(fields))
		for fieldIndex, field := range fields {
			value, next, err := decodeQuotedFormatValue(
				output,
				offset,
				recordIndex+1,
				field.name,
				fieldIndex == len(fields)-1,
			)
			if err != nil {
				return nil, err
			}
			values[fieldIndex] = tmuxcmd.DecodeBackslashReplace(value)
			offset = next
		}

		lastField := fields[len(fields)-1].name
		if offset >= len(output) {
			return nil, newFormatDecodeError(recordIndex+1, lastField, offset, "missing record newline")
		}
		if output[offset] != '\n' {
			return nil, newFormatDecodeError(recordIndex+1, lastField, offset, "expected record newline")
		}
		offset++

		record, err := newFormatValues(version, fields, values)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func decodeQuotedFormatValue(
	output []byte,
	offset int,
	record int,
	field string,
	last bool,
) ([]byte, int, error) {
	value := make([]byte, 0)
	for offset < len(output) {
		switch output[offset] {
		case '\\':
			if offset+1 >= len(output) {
				return nil, offset, newFormatDecodeError(record, field, offset, "dangling escape")
			}
			if strings.IndexByte(formatShellEscapedBytes, output[offset+1]) < 0 {
				return nil, offset, newFormatDecodeError(record, field, offset, "invalid quoted escape")
			}
			value = append(value, output[offset+1])
			offset += 2
		case formatFieldSeparator:
			if last {
				return nil, offset, newFormatDecodeError(
					record,
					field,
					offset,
					"unexpected field separator after final field",
				)
			}
			return value, offset + 1, nil
		case formatRecordTerminator:
			if !last {
				return nil, offset, newFormatDecodeError(
					record,
					field,
					offset,
					"record ended before final field",
				)
			}
			return value, offset + 1, nil
		default:
			value = append(value, output[offset])
			offset++
		}
	}
	return nil, offset, newFormatDecodeError(record, field, offset, "missing record terminator")
}

func newFormatDecodeError(record int, field string, offset int, reason string) *FormatDecodeError {
	return &FormatDecodeError{
		Record: record,
		Field:  field,
		Offset: offset,
		Reason: reason,
	}
}
