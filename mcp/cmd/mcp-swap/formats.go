package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// Three config formats, edited in place rather than rewritten.
//
// These files are somebody's, and they hold more than this server's entry:
// other MCP servers, model settings, comments explaining why a thing is set
// the way it is. Parsing one and writing it back reformats all of that — it is
// how a swap of one entry silently dropped a neighbouring "enabled = true" and
// a blank line.
//
// So each format is located rather than decoded: find the span the entry
// occupies, replace exactly those bytes, and leave every other byte alone.
// Reading is different — status only needs to describe what is there, so it
// may decode freely.

// configFormat is how a client stores its servers.
type configFormat int

const (
	// formatJSON is a plain JSON object, which most clients use.
	formatJSON configFormat = iota
	// formatTOML is codex's and grok's shape: a [mcp_servers.<slug>] table,
	// with any environment in a [mcp_servers.<slug>.env] sub-table after it.
	formatTOML
	// formatJSONC is opencode's: JSON with comments, which have to survive.
	formatJSONC
)

// entryDialect is the shape one client expects a server entry to take.
type entryDialect int

const (
	// dialectStandard is command, args and env, which most clients read.
	dialectStandard entryDialect = iota
	// dialectOpencode packs argv into one array and calls the environment
	// "environment". An "env" key here is dropped in silence and a scalar
	// command is a decode error that takes the whole config down with it.
	dialectOpencode
)

// renderEntry converts the canonical entry into what a dialect expects.
func renderEntry(entry map[string]any, dialect entryDialect) map[string]any {
	if dialect != dialectOpencode {
		return entry
	}
	command := []any{entry["command"]}
	if arguments, ok := entry["args"].([]any); ok {
		command = append(command, arguments...)
	}
	rendered := map[string]any{"type": "local", "command": command}
	if environment, ok := entry["env"].(map[string]any); ok && len(environment) > 0 {
		rendered["environment"] = environment
	}
	return rendered
}

// ---------------------------------------------------------------------------
// TOML
// ---------------------------------------------------------------------------

// tomlTableSpan reports the byte span one table and its sub-tables occupy.
//
// A server's environment lives in a sub-table written after the table itself,
// so the entry is not one table but a run of them: everything from the header
// until a header that is not one of its children.
func tomlTableSpan(text []byte, table string) (start int, end int, found bool) {
	header := []byte("[" + table + "]")
	child := []byte("[" + table + ".")
	lines := bytes.SplitAfter(text, []byte("\n"))

	offset := 0
	for index, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if !bytes.Equal(trimmed, header) {
			offset += len(line)
			continue
		}
		start = offset
		end = offset + len(line)
		for _, following := range lines[index+1:] {
			next := bytes.TrimSpace(following)
			if bytes.HasPrefix(next, []byte("[")) && !bytes.HasPrefix(next, child) {
				return start, end, true
			}
			end += len(following)
		}
		return start, end, true
	}
	return 0, 0, false
}

// mergeWithExisting carries forward what the previous entry said and this one
// does not.
//
// Two things survive a swap. Keys this tool does not write — grok's "enabled",
// say — because they configure the client's relationship with the server
// rather than which build it is, and dropping one silently disables a server
// somebody meant to keep. And the environment, because that is where
// LIBTMUX_SAFETY and a socket name live: a swap changes which build answers,
// not how it is configured. The marker this tool sets wins over an old value
// of the same name, since it describes the swap being made now.
func mergeWithExisting(existing, fresh map[string]any) map[string]any {
	merged := map[string]any{}
	maps.Copy(merged, fresh)
	for name, value := range existing {
		switch name {
		case "command", "args", "type":
			// What the swap is for.
		case "env", "environment":
			// Handled below, so an old environment and a new marker combine
			// rather than one replacing the other.
		default:
			if _, replaced := merged[name]; !replaced {
				merged[name] = value
			}
		}
	}

	environment := map[string]any{}
	for _, name := range []string{"env", "environment"} {
		if previous, ok := existing[name].(map[string]any); ok {
			maps.Copy(environment, previous)
		}
	}
	for _, name := range []string{"env", "environment"} {
		if incoming, ok := fresh[name].(map[string]any); ok {
			maps.Copy(environment, incoming)
			if len(environment) > 0 {
				merged[name] = environment
			}
		}
	}
	return merged
}

// tomlPreserved reports the lines of a table this tool would not rewrite.
func tomlPreserved(text []byte, table string) map[string]any {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil
	}
	kept := map[string]any{}
	for _, line := range strings.Split(string(text[start:end]), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			// A sub-table header; its keys are the environment, read
			// separately so they merge rather than round-trip as raw text.
			if trimmed != "["+table+"]" {
				break
			}
			continue
		}
		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		switch name {
		case "command", "args", "":
		default:
			kept[name] = tomlRawValue(strings.TrimSpace(value))
		}
	}
	return kept
}

// tomlRawValue keeps a value as written, since it is re-emitted verbatim.
type tomlRawValue string

// tomlEnvironment reads a table's environment sub-table.
func tomlEnvironment(text []byte, table string) map[string]any {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil
	}
	environment := map[string]any{}
	inEnvironment := false
	for _, line := range strings.Split(string(text[start:end]), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inEnvironment = trimmed == "["+table+".env]"
			continue
		}
		if !inEnvironment {
			continue
		}
		if name, value, ok := strings.Cut(trimmed, "="); ok {
			environment[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}

// renderTOMLTable writes one server entry as TOML.
//
// Only the value shapes this tool writes are handled — strings, string lists,
// and a flat environment — because those are what a server entry is. Anything
// else in the file is never re-rendered, only skipped over.
func renderTOMLTable(table string, entry map[string]any) string {
	var out strings.Builder
	fmt.Fprintf(&out, "[%s]\n", table)

	if command, ok := entry["command"].(string); ok {
		fmt.Fprintf(&out, "command = %s\n", tomlString(command))
	}
	if arguments, ok := entry["args"].([]any); ok && len(arguments) > 0 {
		rendered := make([]string, 0, len(arguments))
		for _, argument := range arguments {
			rendered = append(rendered, tomlString(fmt.Sprint(argument)))
		}
		fmt.Fprintf(&out, "args = [%s]\n", strings.Join(rendered, ", "))
	}
	// Whatever the previous entry said that this tool does not write, in the
	// form it was written, since re-rendering a value it does not understand
	// would be guessing at its type.
	for _, name := range sortedKeys(entry) {
		if raw, ok := entry[name].(tomlRawValue); ok {
			fmt.Fprintf(&out, "%s = %s\n", name, string(raw))
		}
	}
	environment, _ := entry["env"].(map[string]any)
	if len(environment) > 0 {
		fmt.Fprintf(&out, "\n[%s.env]\n", table)
		for _, name := range sortedKeys(environment) {
			fmt.Fprintf(&out, "%s = %s\n", name, tomlString(fmt.Sprint(environment[name])))
		}
	}
	return out.String()
}

// tomlString quotes a value as a TOML basic string.
func tomlString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteRune(character)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// readTOMLEntry reads back enough of a table to describe it.
//
// Deliberately shallow: status prints a command line, so the command and its
// arguments are all this needs, and a hand-rolled reader that tried for more
// would be a TOML parser with none of the testing one deserves.
func readTOMLEntry(text []byte, table string) (map[string]any, bool) {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil, false
	}
	entry := map[string]any{}
	for _, line := range strings.Split(string(text[start:end]), "\n") {
		trimmed := strings.TrimSpace(line)
		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		switch name {
		case "command":
			entry["command"] = strings.Trim(value, `"`)
		case "args":
			var arguments []any
			if err := json.Unmarshal([]byte(value), &arguments); err == nil {
				entry["args"] = arguments
			}
		}
	}
	// The environment lives in a sub-table, and status needs it: the marker
	// this tool sets is in there, and without it a swapped entry reads as
	// somebody else's.
	if environment := tomlEnvironment(text, table); environment != nil {
		entry["env"] = environment
	}
	return entry, true
}

// ---------------------------------------------------------------------------
// JSONC
// ---------------------------------------------------------------------------

// blankComments replaces every comment with spaces of the same length.
//
// Same length on purpose: every byte offset in the blanked text is the offset
// of that byte in the original, so a span found here can be spliced there. A
// comment inside a string is not a comment, which is why this tracks strings
// rather than scanning for "//".
func blankComments(text []byte) []byte {
	blanked := make([]byte, len(text))
	copy(blanked, text)

	inString, inLine, inBlock, escaped := false, false, false, false
	for index := 0; index < len(blanked); index++ {
		character := blanked[index]
		switch {
		case inLine:
			if character == '\n' {
				inLine = false
				continue
			}
			blanked[index] = ' '
		case inBlock:
			if character == '*' && index+1 < len(blanked) && blanked[index+1] == '/' {
				blanked[index], blanked[index+1] = ' ', ' '
				index++
				inBlock = false
				continue
			}
			if character != '\n' {
				blanked[index] = ' '
			}
		case inString:
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
		case character == '"':
			inString = true
		case character == '/' && index+1 < len(blanked):
			switch blanked[index+1] {
			case '/':
				blanked[index], blanked[index+1] = ' ', ' '
				index++
				inLine = true
			case '*':
				blanked[index], blanked[index+1] = ' ', ' '
				index++
				inBlock = true
			}
		}
	}
	return blanked
}

// jsoncSpan reports where a member's value sits, and where its object is.
//
// Both are needed: replacing an entry that exists is a splice over its value,
// and adding one that does not is an insertion just inside its parent's brace.
type jsoncSpan struct {
	// valueStart and valueEnd bound the member's value, when it is present.
	valueStart, valueEnd int
	// present reports whether the member was there at all.
	present bool
	// objectStart is the byte after the parent object's opening brace.
	objectStart int
	// objectEmpty reports that the parent has no members, so an insertion
	// needs no separating comma.
	objectEmpty bool
}

// findJSONCMember walks a path of object keys and reports what it found.
func findJSONCMember(blanked []byte, path []string) (jsoncSpan, bool) {
	offset := skipSpace(blanked, 0)
	if offset >= len(blanked) || blanked[offset] != '{' {
		return jsoncSpan{}, false
	}
	for depth, key := range path {
		start, end, present, objectStart, empty := memberIn(blanked, offset, key)
		if depth == len(path)-1 {
			return jsoncSpan{
				valueStart: start, valueEnd: end, present: present,
				objectStart: objectStart, objectEmpty: empty,
			}, true
		}
		if !present {
			return jsoncSpan{}, false
		}
		offset = skipSpace(blanked, start)
		if offset >= len(blanked) || blanked[offset] != '{' {
			return jsoncSpan{}, false
		}
	}
	return jsoncSpan{}, false
}

// memberIn finds one key directly inside the object starting at offset.
func memberIn(
	blanked []byte,
	offset int,
	key string,
) (start, end int, present bool, objectStart int, empty bool) {
	objectStart = offset + 1
	cursor := skipSpace(blanked, objectStart)
	empty = cursor < len(blanked) && blanked[cursor] == '}'

	for cursor < len(blanked) && blanked[cursor] != '}' {
		cursor = skipSpace(blanked, cursor)
		if cursor >= len(blanked) || blanked[cursor] != '"' {
			break
		}
		nameStart := cursor
		nameEnd := endOfString(blanked, cursor)
		name := string(blanked[nameStart+1 : nameEnd-1])
		cursor = skipSpace(blanked, nameEnd)
		if cursor >= len(blanked) || blanked[cursor] != ':' {
			break
		}
		valueStart := skipSpace(blanked, cursor+1)
		valueEnd := endOfValue(blanked, valueStart)
		if name == key {
			return valueStart, valueEnd, true, objectStart, empty
		}
		cursor = skipSpace(blanked, valueEnd)
		if cursor < len(blanked) && blanked[cursor] == ',' {
			cursor++
		}
	}
	return 0, 0, false, objectStart, empty
}

// skipSpace advances past whitespace, which is also what a blanked comment is.
func skipSpace(text []byte, offset int) int {
	for offset < len(text) {
		switch text[offset] {
		case ' ', '\t', '\n', '\r':
			offset++
		default:
			return offset
		}
	}
	return offset
}

// endOfString reports the offset just past a string's closing quote.
func endOfString(text []byte, offset int) int {
	offset++
	for offset < len(text) {
		if text[offset] == '\\' {
			offset += 2
			continue
		}
		if text[offset] == '"' {
			return offset + 1
		}
		offset++
	}
	return offset
}

// endOfValue reports the offset just past a value of any shape.
func endOfValue(text []byte, offset int) int {
	if offset >= len(text) {
		return offset
	}
	switch text[offset] {
	case '"':
		return endOfString(text, offset)
	case '{', '[':
		opening, closing := text[offset], byte('}')
		if opening == '[' {
			closing = ']'
		}
		depth := 0
		for offset < len(text) {
			switch text[offset] {
			case '"':
				offset = endOfString(text, offset)
				continue
			case opening:
				depth++
			case closing:
				depth--
				if depth == 0 {
					return offset + 1
				}
			}
			offset++
		}
		return offset
	default:
		for offset < len(text) {
			switch text[offset] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				return offset
			}
			offset++
		}
		return offset
	}
}

// setJSONCMember writes a member at a path, preserving every other byte.
func setJSONCMember(text []byte, path []string, value any, indent string) ([]byte, error) {
	rendered, err := json.MarshalIndent(value, strings.Repeat(indent, len(path)), indent)
	if err != nil {
		return nil, err
	}
	span, ok := findJSONCMember(blankComments(text), path)
	if !ok {
		return nil, fmt.Errorf("no %q object to write into", strings.Join(path[:len(path)-1], "."))
	}

	var out bytes.Buffer
	if span.present {
		out.Write(text[:span.valueStart])
		out.Write(rendered)
		out.Write(text[span.valueEnd:])
		return out.Bytes(), nil
	}

	member := fmt.Sprintf("\n%s%q: %s",
		strings.Repeat(indent, len(path)), path[len(path)-1], rendered)
	if !span.objectEmpty {
		member = "," + member
	}
	out.Write(text[:span.objectStart])
	// An empty parent has its closing brace right after the opening one, so
	// the new member needs the newline the file would otherwise have had.
	if span.objectEmpty {
		out.WriteString(member)
		out.WriteString("\n" + strings.Repeat(indent, len(path)-1))
		out.Write(text[span.objectStart:])
		return out.Bytes(), nil
	}
	// Otherwise it goes after the last existing member, which is just before
	// the closing brace.
	closing := span.objectStart
	for {
		next := endOfValueAfterMember(blankComments(text), closing)
		if next <= closing {
			break
		}
		closing = next
	}
	out.Reset()
	out.Write(text[:closing])
	out.WriteString(member)
	out.Write(text[closing:])
	return out.Bytes(), nil
}

// endOfValueAfterMember advances past one member, for finding the last.
func endOfValueAfterMember(blanked []byte, offset int) int {
	cursor := skipSpace(blanked, offset)
	if cursor >= len(blanked) || blanked[cursor] != '"' {
		return offset
	}
	cursor = skipSpace(blanked, endOfString(blanked, cursor))
	if cursor >= len(blanked) || blanked[cursor] != ':' {
		return offset
	}
	end := endOfValue(blanked, skipSpace(blanked, cursor+1))
	after := skipSpace(blanked, end)
	if after < len(blanked) && blanked[after] == ',' {
		return after + 1
	}
	return end
}

// readJSONC decodes a commented config, for reporting what is in it.
func readJSONC(text []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(stripTrailingCommas(blankComments(text)), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// stripTrailingCommas removes the commas JSONC allows and JSON does not.
func stripTrailingCommas(blanked []byte) []byte {
	out := make([]byte, len(blanked))
	copy(out, blanked)
	for index := range out {
		if out[index] != ',' {
			continue
		}
		next := skipSpace(out, index+1)
		if next < len(out) && (out[next] == '}' || out[next] == ']') {
			out[index] = ' '
		}
	}
	return out
}

// sortedKeys returns a map's keys in a stable order, so a rewritten entry does
// not change shape between runs for no reason.
func sortedKeys(values map[string]any) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
