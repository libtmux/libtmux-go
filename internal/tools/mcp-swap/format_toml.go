package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

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
		if !tomlHeaderMatches(trimmed, header) {
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

func tomlHeaderMatches(line, header []byte) bool {
	if !bytes.HasPrefix(line, header) {
		return false
	}
	remainder := bytes.TrimSpace(line[len(header):])
	return len(remainder) == 0 || remainder[0] == '#'
}

func tomlHeaderAt(text []byte, start int) string {
	end := bytes.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	return strings.TrimSuffix(string(text[start:start+end]), "\r")
}

// validateTOMLPreservation refuses syntax the line-oriented writer would drop.
func validateTOMLPreservation(text []byte, table string) error {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil
	}
	baseHeader := []byte("[" + table + "]")
	environmentHeader := []byte("[" + table + ".env]")
	section := ""
	lines := strings.Split(string(text[start:end]), "\n")
	for index := 0; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "["):
			switch {
			case tomlHeaderMatches([]byte(trimmed), baseHeader):
				section = "entry"
			case tomlHeaderMatches([]byte(trimmed), environmentHeader):
				section = "environment"
			default:
				return fmt.Errorf("cannot preserve child table %q under %s", trimmed, table)
			}
			continue
		}

		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		raw := strings.TrimSpace(value)
		if tomlValueComplete(raw) {
			continue
		}
		for !tomlValueComplete(raw) && index+1 < len(lines) {
			index++
			raw += "\n" + lines[index]
		}
		if !tomlValueComplete(raw) {
			return fmt.Errorf("cannot parse multiline TOML value %q in %s", name, table)
		}
		if section == "entry" && (name == "command" || name == "args") {
			continue
		}
		return fmt.Errorf("cannot preserve multiline TOML value %q in %s", name, table)
	}
	return nil
}

func tomlValueComplete(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	const (
		quoteNone = iota
		quoteBasic
		quoteLiteral
		quoteMultilineBasic
		quoteMultilineLiteral
	)
	quote, square, curly := quoteNone, 0, 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch quote {
		case quoteBasic:
			switch character {
			case '\\':
				index++
			case '"':
				quote = quoteNone
			}
			continue
		case quoteLiteral:
			if character == '\'' {
				quote = quoteNone
			}
			continue
		case quoteMultilineBasic:
			if character == '\\' {
				index++
			} else if strings.HasPrefix(value[index:], `"""`) {
				quote = quoteNone
				index += 2
			}
			continue
		case quoteMultilineLiteral:
			if strings.HasPrefix(value[index:], `'''`) {
				quote = quoteNone
				index += 2
			}
			continue
		}

		switch {
		case character == '#':
			if newline := strings.IndexByte(value[index:], '\n'); newline >= 0 {
				index += newline
			} else {
				index = len(value)
			}
		case strings.HasPrefix(value[index:], `"""`):
			quote = quoteMultilineBasic
			index += 2
		case strings.HasPrefix(value[index:], `'''`):
			quote = quoteMultilineLiteral
			index += 2
		case character == '"':
			quote = quoteBasic
		case character == '\'':
			quote = quoteLiteral
		case character == '[':
			square++
		case character == ']':
			square--
		case character == '{':
			curly++
		case character == '}':
			curly--
		}
	}
	return quote == quoteNone && square == 0 && curly == 0
}

func tomlPreserved(text []byte, table string) map[string]any {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil
	}
	kept := map[string]any{}
	lines := strings.Split(string(text[start:end]), "\n")
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			// Read environment sub-tables separately.
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
		raw := strings.TrimSpace(value)
		for !tomlValueComplete(raw) && index+1 < len(lines) {
			index++
			raw += "\n" + lines[index]
		}
		switch name {
		case "command", "args", "":
		default:
			kept[name] = tomlRawValue(raw)
		}
	}
	return kept
}

// tomlRawValue keeps a value as written, since it is re-emitted verbatim.
type tomlRawValue string

func tomlEnvironment(text []byte, table string) map[string]any {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil
	}
	environment := map[string]any{}
	inEnvironment := false
	for line := range strings.SplitSeq(string(text[start:end]), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inEnvironment = trimmed == "["+table+".env]"
			continue
		}
		if !inEnvironment {
			continue
		}
		if name, value, ok := strings.Cut(trimmed, "="); ok {
			environment[strings.TrimSpace(name)] = tomlRawValue(strings.TrimSpace(value))
		}
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}

// renderTOMLTable renders only supported entry values; unrelated file content
// is spliced around it.
func renderTOMLTable(table, header string, entry map[string]any) string {
	var out strings.Builder
	fmt.Fprintln(&out, header)

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
	// Preserve unknown values verbatim.
	for _, name := range sortedKeys(entry) {
		if raw, ok := entry[name].(tomlRawValue); ok {
			fmt.Fprintf(&out, "%s = %s\n", name, string(raw))
		}
	}
	environment, _ := entry["env"].(map[string]any)
	if len(environment) > 0 {
		fmt.Fprintf(&out, "\n[%s.env]\n", table)
		for _, name := range sortedKeys(environment) {
			if raw, ok := environment[name].(tomlRawValue); ok {
				fmt.Fprintf(&out, "%s = %s\n", name, string(raw))
				continue
			}
			fmt.Fprintf(&out, "%s = %s\n", name, tomlString(fmt.Sprint(environment[name])))
		}
	}
	return out.String()
}

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

// readTOMLEntry parses only command, arguments, and environment for status.
func readTOMLEntry(text []byte, table string) (map[string]any, bool) {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return nil, false
	}
	entry := map[string]any{}
	for line := range strings.SplitSeq(string(text[start:end]), "\n") {
		trimmed := strings.TrimSpace(line)
		name, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		switch name {
		case "command":
			if decoded, ok := tomlStringValue(value); ok {
				entry["command"] = decoded
			}
		case "args":
			var arguments []any
			if err := json.Unmarshal([]byte(value), &arguments); err == nil {
				entry["args"] = arguments
			}
		}
	}
	// The environment contains the ownership marker reported by status.
	if rawEnvironment := tomlEnvironment(text, table); rawEnvironment != nil {
		environment := make(map[string]any, len(rawEnvironment))
		for name, raw := range rawEnvironment {
			value := string(raw.(tomlRawValue))
			if decoded, ok := tomlStringValue(value); ok {
				environment[name] = decoded
			} else {
				environment[name] = value
			}
		}
		entry["env"] = environment
	}
	return entry, true
}

func tomlStringValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return "", false
	}
	switch value[0] {
	case '\'':
		if end := strings.IndexByte(value[1:], '\''); end >= 0 {
			return value[1 : end+1], true
		}
	case '"':
		var decoded string
		if err := json.NewDecoder(strings.NewReader(value)).Decode(&decoded); err == nil {
			return decoded, true
		}
	}
	return "", false
}

// sortedKeys stabilizes rendered entries across runs.
func sortedKeys(values map[string]any) []string {
	return slices.Sorted(maps.Keys(values))
}
