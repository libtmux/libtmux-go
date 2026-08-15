package tmux

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzDecodeFormatRecordsRoundTrip(f *testing.F) {
	f.Add("dev", "title", "")
	f.Add("dev:␞", "line1\nline2", "開発")
	f.Add(string([]byte{0xff, ':', '\n'}), "0:", "\n")

	fields := []formatField{
		{name: "window_name"},
		{name: "pane_title"},
		{name: "pane_current_path"},
	}
	version, err := ParseVersion("3.2a")
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, first, second, third string) {
		values := []string{first, second, third}
		framed := quotedFormatRecord(values)

		records, decodeErr := decodeFormatRecords(framed, version, fields)
		if decodeErr != nil {
			t.Fatalf("decodeFormatRecords() error = %v", decodeErr)
		}
		if len(records) != 1 {
			t.Fatalf("len(records) = %d, want 1", len(records))
		}
		for index, field := range fields {
			got, ok := records[0].get(field.name)
			want := backslashReplaceTest([]byte(values[index]))
			if !ok || got != want {
				t.Fatalf("field %q = %q, %t, want %q, true", field.name, got, ok, want)
			}
		}
	})
}

func backslashReplaceTest(value []byte) string {
	var decoded strings.Builder
	const hexadecimal = "0123456789abcdef"
	for len(value) > 0 {
		r, size := utf8.DecodeRune(value)
		if r == utf8.RuneError && size == 1 {
			decoded.WriteString(`\x`)
			decoded.WriteByte(hexadecimal[value[0]>>4])
			decoded.WriteByte(hexadecimal[value[0]&0x0f])
			value = value[1:]
			continue
		}
		decoded.WriteRune(r)
		value = value[size:]
	}
	return decoded.String()
}
