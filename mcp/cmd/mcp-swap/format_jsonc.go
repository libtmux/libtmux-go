package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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

// jsoncSpan identifies a value replacement or insertion point.
type jsoncSpan struct {
	valueStart, valueEnd int
	present              bool
	objectStart          int
	objectEmpty          bool
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
	blanked := blankComments(text)
	span, ok := findJSONCMember(blanked, path)
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
	out.Write(text[:span.objectStart])
	// Preserve the empty object's closing-brace indentation.
	if span.objectEmpty {
		out.WriteString(member)
		out.WriteString("\n" + strings.Repeat(indent, len(path)-1))
		out.Write(text[span.objectStart:])
		return out.Bytes(), nil
	}
	// Otherwise insert after the last existing member.
	closing := span.objectStart
	for {
		next := endOfValueAfterMember(blanked, closing)
		if next <= closing {
			break
		}
		closing = next
	}
	if closing == span.objectStart || blanked[closing-1] != ',' {
		member = "," + member
	}
	out.Reset()
	out.Write(text[:closing])
	out.WriteString(member)
	out.Write(text[closing:])
	return out.Bytes(), nil
}

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

func removeJSONCMember(text []byte, span jsoncSpan) ([]byte, error) {
	blanked := blankComments(text)
	colon := previousNonSpace(blanked, span.valueStart-1)
	keyEnd := previousNonSpace(blanked, colon-1)
	if colon < 0 || blanked[colon] != ':' || keyEnd < 0 || blanked[keyEnd] != '"' {
		return nil, errors.New("cannot locate the server entry key")
	}
	keyStart := keyEnd - 1
	for keyStart >= 0 {
		if blanked[keyStart] == '"' {
			backslashes := 0
			for offset := keyStart - 1; offset >= 0 && blanked[offset] == '\\'; offset-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				break
			}
		}
		keyStart--
	}
	if keyStart < 0 {
		return nil, errors.New("cannot locate the server entry key")
	}

	start, end := keyStart, span.valueEnd
	after := skipSpace(blanked, end)
	if after < len(blanked) && blanked[after] == ',' {
		end = after + 1
	} else if before := previousNonSpace(blanked, start-1); before >= 0 && blanked[before] == ',' {
		start = before
	}
	return replaceBytes(text, start, end, nil), nil
}

func previousNonSpace(text []byte, offset int) int {
	for offset >= 0 {
		switch text[offset] {
		case ' ', '\t', '\n', '\r':
			offset--
		default:
			return offset
		}
	}
	return -1
}

func readJSONC(text []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(stripTrailingCommas(blankComments(text)), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

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
