package main

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
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
	var document map[string]any
	if err := toml.Unmarshal(text, &document); err != nil {
		return fmt.Errorf("invalid TOML: %w", err)
	}
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
			if !tomlHeaderMatches([]byte(trimmed), []byte("["+table+"]")) {
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

type tomlDecoration struct {
	entryComments       []string
	environmentHeader   string
	environmentComments []string
}

func tomlDecorations(text []byte, table string) tomlDecoration {
	start, end, found := tomlTableSpan(text, table)
	if !found {
		return tomlDecoration{}
	}
	baseHeader := []byte("[" + table + "]")
	environmentHeader := []byte("[" + table + ".env]")
	section := ""
	decoration := tomlDecoration{}
	for line := range strings.SplitSeq(string(text[start:end]), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case tomlHeaderMatches([]byte(trimmed), baseHeader):
			section = "entry"
		case tomlHeaderMatches([]byte(trimmed), environmentHeader):
			section = "environment"
			decoration.environmentHeader = line
		case strings.HasPrefix(trimmed, "#"):
			switch section {
			case "environment":
				decoration.environmentComments = append(decoration.environmentComments, line)
			case "entry":
				decoration.entryComments = append(decoration.entryComments, line)
			}
		}
	}
	return decoration
}

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
			inEnvironment = tomlHeaderMatches(
				[]byte(trimmed), []byte("["+table+".env]"),
			)
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
func renderTOMLTable(
	table, header string,
	entry map[string]any,
	decoration tomlDecoration,
) string {
	var out strings.Builder
	fmt.Fprintln(&out, header)
	for _, comment := range decoration.entryComments {
		fmt.Fprintln(&out, comment)
	}

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
		environmentHeader := decoration.environmentHeader
		if environmentHeader == "" {
			environmentHeader = "[" + table + ".env]"
		}
		fmt.Fprintf(&out, "\n%s\n", environmentHeader)
		for _, comment := range decoration.environmentComments {
			fmt.Fprintln(&out, comment)
		}
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

// readTOMLEntry strictly parses the complete document before selecting one table.
func readTOMLEntry(text []byte, table string) (map[string]any, bool, error) {
	var document map[string]any
	if err := toml.Unmarshal(text, &document); err != nil {
		return nil, false, fmt.Errorf("invalid TOML: %w", err)
	}
	current := document
	parts := strings.Split(table, ".")
	for index, part := range parts {
		raw, found := current[part]
		if !found {
			return nil, false, nil
		}
		next, ok := raw.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s is not a table", strings.Join(parts[:index+1], "."))
		}
		current = next
	}
	return current, true, nil
}

// sortedKeys stabilizes rendered entries across runs.
func sortedKeys(values map[string]any) []string {
	return slices.Sorted(maps.Keys(values))
}
