package mcp

import (
	"slices"
	"testing"
)

// TestTheWrapperEchoIsDroppedWhereverItWraps covers the rows a recovery must
// discard.
//
// The echo is one logical line the shell wrapped, so where the grid breaks it
// is a function of the prompt's width and moves with it. Dropping through the
// row the path ends on rather than the row the whole echo ends on leaves the
// tail of the path behind as a line of output.
func TestTheWrapperEchoIsDroppedWhereverItWraps(t *testing.T) {
	const echo = ". '/tmp/libtmux-mcp-run478228931/script'"
	for _, probe := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "on one row",
			lines: []string{"$ " + echo, "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "wrapped inside the path",
			lines: []string{"prompt ❯ . '/tmp/libtmux-mcp-run478228931/scrip", "t'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "wrapped before the closing quote",
			lines: []string{"prompt ❯ . '/tmp/libtmux-mcp-run478228931/script", "'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "content above the echo goes too",
			lines: []string{"BEFORE", "$ " + echo, "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			// An interactive shell draws the line it read and then redraws it
			// beneath the prompt, so the echo is in the grid twice and only the
			// second one has the output after it.
			name:  "echoed once plainly and once under the prompt",
			lines: []string{"KEPT", echo, "prompt ❯ . '/tmp/libtmux-mcp-run478228931/scrip", "t'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "no echo leaves the lines alone",
			lines: []string{"BEFORE", "SURVIVES"},
			want:  []string{"BEFORE", "SURVIVES"},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			got := afterTheWrapperEcho(probe.lines, echo)
			if !slices.Equal(got, probe.want) {
				t.Errorf("got %q, want %q", got, probe.want)
			}
		})
	}
}
