package tmux

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeArgsEscapedValueMatchesPinnedTmuxSerialization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serialized []byte
		want       string
	}{
		{name: "empty", serialized: []byte("''"), want: ""},
		{name: "plain", serialized: []byte("status-left"), want: "status-left"},
		{name: "space", serialized: []byte(`"hello world"`), want: "hello world"},
		{name: "single quote special case", serialized: []byte(`\'`), want: "'"},
		{name: "double quote special case", serialized: []byte(`\"`), want: `"`},
		{name: "tilde special case", serialized: []byte(`\~`), want: "~"},
		{name: "tmux 3.2a percent", serialized: []byte(`%`), want: "%"},
		{name: "tmux 3.3 and newer percent", serialized: []byte(`\%`), want: "%"},
		{name: "double quoted dollar", serialized: []byte(`"\$HOME"`), want: "$HOME"},
		{name: "single quoted double quote", serialized: []byte(`'a"b'`), want: `a"b`},
		{name: "C escapes", serialized: []byte(`\a\b\f\n\r\t\v`), want: "\a\b\f\n\r\t\v"},
		{name: "octal", serialized: []byte(`\001\177\377`), want: "\x01\x7f\\xff"},
		{name: "backslash", serialized: []byte(`\\`), want: `\`},
		{name: "UTF-8", serialized: []byte("開発"), want: "開発"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeArgsEscapedValue(tt.serialized)
			if err != nil {
				t.Fatalf("decodeArgsEscapedValue() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeArgsEscapedValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeArgsEscapedValueSupportsCAndGenericBackslashEscapes(t *testing.T) {
	t.Parallel()

	got, err := decodeArgsEscapedValue([]byte(`left\s\E\qright`))
	if err != nil {
		t.Fatalf("decodeArgsEscapedValue() error = %v", err)
	}
	if want := "left \x1bqright"; got != want {
		t.Fatalf("decodeArgsEscapedValue() = %q, want %q", got, want)
	}
}

func TestDecodeArgsEscapedValueRejectsMalformedInputWithoutDisclosingIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serialized []byte
	}{
		{name: "missing value", serialized: nil},
		{name: "unclosed single quote", serialized: []byte(`'sensitive-token`)},
		{name: "unclosed double quote", serialized: []byte(`"sensitive-token`)},
		{name: "unexpected trailing quote", serialized: []byte(`sensitive-token'`)},
		{name: "unescaped inner quote", serialized: []byte(`"sensitive"token"`)},
		{name: "dangling escape", serialized: []byte(`sensitive-token\`)},
		{name: "one-digit octal", serialized: []byte(`sensitive-token\1`)},
		{name: "two-digit octal", serialized: []byte(`sensitive-token\12`)},
		{name: "nonoctal third digit", serialized: []byte(`sensitive-token\128`)},
		{name: "octal byte overflow", serialized: []byte(`sensitive-token\400`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeArgsEscapedValue(tt.serialized)
			if !errors.Is(err, errMalformedArgsEscapedValue) {
				t.Fatalf("decodeArgsEscapedValue() error = %v, want malformed args_escape value", err)
			}
			if strings.Contains(err.Error(), "sensitive-token") {
				t.Fatalf("decode error disclosed option value: %v", err)
			}
		})
	}
}

func TestDecodeTargetedOptionOutputRemovesOneTerminalLineFeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "empty value", raw: []byte("\n"), want: ""},
		{name: "one line", raw: []byte("value\n"), want: "value"},
		{name: "embedded line feed", raw: []byte("line one\nline two\n"), want: "line one\nline two"},
		{name: "value ending in line feed", raw: []byte("value\n\n"), want: "value\n"},
		{name: "carriage return is data", raw: []byte("value\r\n"), want: "value\r"},
		{name: "invalid UTF-8", raw: []byte{0xff, '\n'}, want: `\xff`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeTargetedOptionOutput(tt.raw)
			if err != nil {
				t.Fatalf("decodeTargetedOptionOutput() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeTargetedOptionOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeTargetedOptionOutputRequiresTerminalLineFeed(t *testing.T) {
	t.Parallel()

	for _, raw := range [][]byte{nil, {}, []byte("sensitive-token")} {
		_, err := decodeTargetedOptionOutput(raw)
		if !errors.Is(err, errMalformedTargetedOptionOutput) {
			t.Fatalf("decodeTargetedOptionOutput(%q) error = %v, want malformed targeted output", raw, err)
		}
		if strings.Contains(err.Error(), "sensitive-token") {
			t.Fatalf("decode error disclosed option value: %v", err)
		}
	}
}
