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

// removeJSONCMember deletes the member at path with the comma that joined it
// to a neighbour, preserving every other byte.
func removeJSONCMember(text []byte, path []string) ([]byte, error) {
	blanked := blankComments(text)
	object, ok := jsoncObjectAt(blanked, path[:len(path)-1])
	if !ok {
		return nil, fmt.Errorf("no %q object to remove from", strings.Join(path[:len(path)-1], "."))
	}
	key := path[len(path)-1]
	previousComma := -1
	cursor := skipSpace(blanked, object+1)
	for cursor < len(blanked) && blanked[cursor] == '"' {
		nameEnd := endOfString(blanked, cursor)
		colon := skipSpace(blanked, nameEnd)
		if colon >= len(blanked) || blanked[colon] != ':' {
			break
		}
		valueEnd := endOfValue(blanked, skipSpace(blanked, colon+1))
		after := skipSpace(blanked, valueEnd)
		comma := -1
		if after < len(blanked) && blanked[after] == ',' {
			comma = after
		}
		if string(blanked[cursor+1:nameEnd-1]) == key {
			return cutJSONCMember(text, object, cursor, valueEnd, comma, previousComma), nil
		}
		if comma < 0 {
			break
		}
		previousComma = comma
		cursor = skipSpace(blanked, comma+1)
	}
	return nil, fmt.Errorf("no %q member to remove", strings.Join(path, "."))
}

// cutJSONCMember removes the member between keyStart and valueEnd. A member
// with a comma after it takes that comma and the space behind it; the last
// one takes the comma before it and the space ahead of it, leaving any
// comment between; an only member leaves the object empty.
func cutJSONCMember(text []byte, object, keyStart, valueEnd, comma, previousComma int) []byte {
	switch {
	case comma >= 0:
		return append(append([]byte{}, text[:keyStart]...), text[skipSpace(text, comma+1):]...)
	case previousComma >= 0:
		from := keyStart
		for from > previousComma+1 && strings.IndexByte(" \t\r\n", text[from-1]) >= 0 {
			from--
		}
		kept := append(append([]byte{}, text[:previousComma]...), text[previousComma+1:from]...)
		if endsInLineComment(kept) && !startsWithNewline(text[valueEnd:]) {
			kept = append(kept, '\n')
		}
		return append(kept, text[valueEnd:]...)
	}
	closing := skipSpace(text, valueEnd)
	if closing < len(text) && text[closing] == '}' && len(bytes.TrimSpace(text[object+1:keyStart])) == 0 {
		return append(append([]byte{}, text[:object+1]...), text[closing:]...)
	}
	return append(append([]byte{}, text[:keyStart]...), text[valueEnd:]...)
}

// endsInLineComment reports whether text ends inside a // comment.
func endsInLineComment(text []byte) bool {
	blanked := blankComments(text)
	line := bytes.LastIndexByte(text, '\n') + 1
	for index := line; index+1 < len(text); index++ {
		if text[index] == '/' && text[index+1] == '/' && blanked[index] == ' ' {
			return true
		}
	}
	return false
}

func startsWithNewline(text []byte) bool {
	text = bytes.TrimLeft(text, " \t")
	return len(text) != 0 && (text[0] == '\n' || text[0] == '\r')
}

// jsoncObjectAt reports the offset of the object a path of keys names; an
// empty path names the root.
func jsoncObjectAt(blanked []byte, path []string) (int, bool) {
	offset := skipSpace(blanked, 0)
	if offset >= len(blanked) || blanked[offset] != '{' {
		return 0, false
	}
	if len(path) == 0 {
		return offset, true
	}
	span, ok := findJSONCMember(blanked, path)
	if !ok || !span.present {
		return 0, false
	}
	offset = skipSpace(blanked, span.valueStart)
	if offset >= len(blanked) || blanked[offset] != '{' {
		return 0, false
	}
	return offset, true
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

func readJSONC(text []byte) (map[string]any, error) {
	strict := stripTrailingCommas(blankComments(text))
	if err := rejectDuplicateJSONNames(strict); err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(strict, &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, errors.New("JSONC config root is not an object")
	}
	return decoded, nil
}

func stripTrailingCommas(blanked []byte) []byte {
	out := make([]byte, len(blanked))
	copy(out, blanked)
	inString, escaped := false, false
	for index := range out {
		character := out[index]
		if inString {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			continue
		}
		if character != ',' {
			continue
		}
		next := skipSpace(out, index+1)
		if next < len(out) && (out[next] == '}' || out[next] == ']') {
			out[index] = ' '
		}
	}
	return out
}
