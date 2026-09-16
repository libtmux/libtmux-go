package mcp

import "testing"

// tmux 3.8 changed #{window_layout} to report JSON for non-control clients
// (get_window_info's own source), and select-layout accepts that shape back
// alongside the classic grammar. This tool's own pre-check must accept
// both shapes too, since it runs before tmux.Window.SelectLayout sees the
// value.
func TestLayoutLooksValidAcceptsBothLayoutShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		layout string
		want   bool
	}{
		{name: "preset", layout: "main-vertical", want: true},
		{name: "mirrored preset", layout: "main-vertical-mirrored", want: true},
		{
			name:   "classic checksum-prefixed layout",
			layout: "a1b2,80x24,0,0,0",
			want:   true,
		},
		{
			name:   "JSON layout tmux 3.8+ reports",
			layout: `{"V":2,"L":{"t":"p","w":80,"h":24,"x":0,"y":0,"a":true,"i":0,"I":"%2"}}`,
			want:   true,
		},
		{name: "unknown name", layout: "no-such-layout", want: false},
		{name: "text that merely starts with a brace", layout: "{not json", want: false},
		{name: "empty", layout: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := layoutLooksValid(test.layout); got != test.want {
				t.Fatalf("layoutLooksValid(%q) = %t, want %t", test.layout, got, test.want)
			}
		})
	}
}
