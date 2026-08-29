package mcp

import (
	"bytes"
	"testing"
)

func TestTerminalTextPreservesPrintableUTF8AcrossChunks(t *testing.T) {
	t.Parallel()

	chunks := [][]byte{
		[]byte("plain\tcaf"),
		{0xc3},
		{0xa9, 0xe2, 0x80},
		{0x94, 0xe6, 0x9d, 0xb1, 0xe4},
		{0xba, 0xac, '\n'},
	}
	var normalizer terminalTextNormalizer
	var got []byte
	for _, chunk := range chunks {
		got = normalizer.appendChunk(got, chunk)
	}

	const want = "plain\tcafé—東京\n"
	if string(got) != want {
		t.Fatalf("normalized text = %q, want %q", got, want)
	}
}

func TestTerminalTextMakesOverwriteControlsReadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{
			name:   "carriage return overwrite",
			chunks: []string{"TOKEN\rERASED"},
			want:   "TOKEN\nERASED",
		},
		{
			name:   "split CRLF",
			chunks: []string{"first\r", "\nsecond\r", "\nthird"},
			want:   "first\nsecond\nthird",
		},
		{
			name:   "backspace overwrite",
			chunks: []string{"visible\b", "replacement"},
			want:   "visible\nreplacement",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var normalizer terminalTextNormalizer
			var got []byte
			for _, chunk := range test.chunks {
				got = normalizer.appendChunk(got, []byte(chunk))
			}
			if string(got) != test.want {
				t.Fatalf("normalized text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTerminalTextStripsCSIAndEscapeControlsAcrossChunks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chunks [][]byte
		want   string
	}{
		{
			name: "split seven bit CSI",
			chunks: [][]byte{
				[]byte("before\x1b["),
				[]byte("31;1mred\x1b[2"),
				[]byte("K\x1b(Bafter"),
			},
			want: "beforeredafter",
		},
		{
			name:   "raw eight bit CSI",
			chunks: [][]byte{{'a', 0x9b}, []byte("38;5;4mblue")},
			want:   "ablue",
		},
		{
			name:   "UTF-8 eight bit CSI",
			chunks: [][]byte{{'a', 0xc2}, {0x9b}, []byte("1mbright")},
			want:   "abright",
		},
		{
			name: "generic escape and C0 controls",
			chunks: [][]byte{
				{'a', 0x1b},
				{'7', 0x1b, ' ', 'F', 0x00, 0x07, 0x0b, 0x0c, 0x0e, 0x7f, '\t', 'b'},
			},
			want: "a\tb",
		},
		{
			name:   "CRLF around CSI",
			chunks: [][]byte{[]byte("line\r\x1b[0"), []byte("m\nnext")},
			want:   "line\nnext",
		},
		{
			name:   "invalid UTF-8 before CSI",
			chunks: [][]byte{{'a', 0xc3}, []byte("\x1b[31mred")},
			want:   "ared",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var normalizer terminalTextNormalizer
			var got []byte
			for _, chunk := range test.chunks {
				got = normalizer.appendChunk(got, chunk)
			}
			if string(got) != test.want {
				t.Fatalf("normalized text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTerminalTextStripsStringControlsAcrossChunks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chunks [][]byte
	}{
		{
			name: "OSC ending in BEL",
			chunks: [][]byte{
				[]byte("left\x1b"),
				[]byte("]title\x1b[still-title"),
				append([]byte{0x07}, []byte("right")...),
			},
		},
		{
			name: "OSC ending in ST",
			chunks: [][]byte{
				[]byte("left\x1b]title\x1b"),
				[]byte("\\right"),
			},
		},
		{
			name: "ESC followed by UTF-8 is not ST",
			chunks: [][]byte{
				[]byte("left\x1b]title\x1b"),
				[]byte("Ü\\still-hidden\x1b"),
				[]byte("\\right"),
			},
		},
		{
			name: "DCS",
			chunks: [][]byte{
				[]byte("left\x1b"),
				[]byte("P1;2|device payload\x1b"),
				[]byte("\\right"),
			},
		},
		{
			name:   "SOS",
			chunks: [][]byte{[]byte("left\x1bXprivate\x1b"), []byte("\\right")},
		},
		{
			name:   "PM",
			chunks: [][]byte{[]byte("left\x1b^private\x1b"), []byte("\\right")},
		},
		{
			name:   "APC",
			chunks: [][]byte{[]byte("left\x1b_private\x1b"), []byte("\\right")},
		},
		{
			name: "raw eight bit DCS and ST",
			chunks: [][]byte{
				append([]byte("left"), 0x90),
				[]byte("payload-Ü"),
				append([]byte{0x9c}, []byte("right")...),
			},
		},
		{
			name: "UTF-8 eight bit OSC and ST",
			chunks: [][]byte{
				append([]byte("left"), 0xc2),
				append([]byte{0x9d}, []byte("title\xc2")...),
				append([]byte{0x9c}, []byte("right")...),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var normalizer terminalTextNormalizer
			var got []byte
			for _, chunk := range test.chunks {
				got = normalizer.appendChunk(got, chunk)
			}
			if string(got) != "leftright" {
				t.Fatalf("normalized text = %q, want %q", got, "leftright")
			}
		})
	}
}

func TestTerminalTextCancellationEndsControlStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "OSC CAN", input: "left\x1b]hidden\x18right"},
		{name: "DCS SUB", input: "left\x1bPhidden\x1aright"},
		{name: "SOS CAN", input: "left\x1bXhidden\x18right"},
		{name: "PM SUB", input: "left\x1b^hidden\x1aright"},
		{name: "APC CAN", input: "left\x1b_hidden\x18right"},
		{name: "pending ST SUB", input: "left\x1b]hidden\x1b\x1aright"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var normalizer terminalTextNormalizer
			got := normalizer.appendChunk(nil, []byte(test.input))
			if string(got) != "leftright" {
				t.Fatalf("normalized text = %q, want %q", got, "leftright")
			}
		})
	}
}

func TestTerminalTextDoesNotBufferControlPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 64*1024)
	start := []byte("\x1b]")
	retained := 0
	allocations := testing.AllocsPerRun(20, func() {
		var normalizer terminalTextNormalizer
		got := normalizer.appendChunk(nil, start)
		got = normalizer.appendChunk(got, payload)
		retained += len(got)
	})
	if retained != 0 {
		t.Fatalf("unterminated OSC returned %d payload bytes", retained)
	}
	if allocations != 0 {
		t.Fatalf("unterminated OSC allocated %.1f times per payload", allocations)
	}

	var normalizer terminalTextNormalizer
	got := normalizer.appendChunk(nil, []byte("before\x1b]"))
	got = normalizer.appendChunk(got, payload)
	got = normalizer.appendChunk(got, []byte("\x1b\\after"))
	if string(got) != "beforeafter" {
		t.Fatalf("normalized text = %q, want %q", got, "beforeafter")
	}
	if normalizer.pendingUTF8Len > len(normalizer.pendingUTF8) {
		t.Fatalf(
			"pending UTF-8 bytes = %d, capacity %d",
			normalizer.pendingUTF8Len,
			len(normalizer.pendingUTF8),
		)
	}
}
