package tmux

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

func FuzzDecodeArgsEscapedValueRoundTrip(f *testing.F) {
	f.Add([]byte("status-left"))
	f.Add([]byte("space and $HOME"))
	f.Add([]byte("line one\nline two\t\\"))
	f.Add([]byte("開発"))
	f.Add([]byte{0xff, 'x', 0x01})

	f.Fuzz(func(t *testing.T, value []byte) {
		if bytes.IndexByte(value, 0) >= 0 {
			t.Skip("tmux option values are NUL-terminated C strings")
		}
		serialized := pinnedArgsEscape37b(value)

		got, err := decodeArgsEscapedValue(serialized)
		if err != nil {
			t.Fatalf("decodeArgsEscapedValue(%q) error = %v", serialized, err)
		}
		want := tmuxcmd.DecodeBackslashReplace(value)
		if got != want {
			t.Fatalf("decodeArgsEscapedValue(%q) = %q, want %q", serialized, got, want)
		}
	})
}

// pinnedArgsEscape37b is a test-only transcription of args_escape and
// utf8_stravis from the tmux 3.7b tag. Table tests cover the one output-shape
// difference at the supported 3.2a floor: percent did not trigger quoting.
func pinnedArgsEscape37b(value []byte) []byte {
	if len(value) == 0 {
		return []byte("''")
	}

	quote := byte(0)
	if bytes.ContainsAny(value, " #';${}%") {
		quote = '"'
	} else if bytes.IndexByte(value, '"') >= 0 {
		quote = '\''
	}
	if len(value) == 1 && value[0] != ' ' && (quote != 0 || value[0] == '~') {
		return []byte{'\\', value[0]}
	}

	escaped := make([]byte, 0, len(value)*2)
	for index := 0; index < len(value); {
		if value[index] >= utf8.RuneSelf {
			_, size := utf8.DecodeRune(value[index:])
			if size > 1 {
				escaped = append(escaped, value[index:index+size]...)
				index += size
				continue
			}
		}

		current := value[index]
		switch current {
		case '\n':
			escaped = append(escaped, '\\', 'n')
		case '\r':
			escaped = append(escaped, '\\', 'r')
		case '\b':
			escaped = append(escaped, '\\', 'b')
		case '\a':
			escaped = append(escaped, '\\', 'a')
		case '\v':
			escaped = append(escaped, '\\', 'v')
		case '\t':
			escaped = append(escaped, '\\', 't')
		case '\f':
			escaped = append(escaped, '\\', 'f')
		case '\\':
			escaped = append(escaped, '\\', '\\')
		case '"':
			if quote == '"' {
				escaped = append(escaped, '\\')
			}
			escaped = append(escaped, current)
		case '$':
			if quote == '"' && index+1 < len(value) && isArgsEscapeVariableStart(value[index+1]) {
				escaped = append(escaped, '\\')
			}
			escaped = append(escaped, current)
		default:
			if current < 0x20 || current == 0x7f || current >= utf8.RuneSelf {
				escaped = appendArgsEscapeOctal(escaped, current)
			} else {
				escaped = append(escaped, current)
			}
		}
		index++
	}

	if quote == '\'' {
		return append(append([]byte{'\''}, escaped...), '\'')
	}
	if quote == '"' {
		if len(escaped) > 0 && escaped[0] == '~' {
			escaped = append([]byte{'\\'}, escaped...)
		}
		return append(append([]byte{'"'}, escaped...), '"')
	}
	if escaped[0] == '~' {
		return append([]byte{'\\'}, escaped...)
	}
	return escaped
}

func isArgsEscapeVariableStart(value byte) bool {
	return value == '_' || value == '{' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func appendArgsEscapeOctal(destination []byte, value byte) []byte {
	return append(
		destination,
		'\\',
		'0'+(value>>6)&7,
		'0'+(value>>3)&7,
		'0'+value&7,
	)
}
