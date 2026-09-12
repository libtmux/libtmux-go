package tmux

import (
	"slices"
	"testing"
)

func TestTrimScreenKeepsOnlyWhatTheCommandShowed(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		fixedNotice bool
		want        []string
	}{
		{name: "padding only", lines: []string{"one", "two", "", ""}, want: []string{"one", "two"}},
		{
			name:        "notice on the bottom row below padding",
			lines:       []string{"one", "two", "", "", "Pane is dead (status 7, Wed Sep  9 14:42:18 2026)"},
			fixedNotice: true,
			want:        []string{"one", "two"},
		},
		{
			name:        "notice appended to the last line",
			lines:       []string{"built        Pane is dead (status 3, Wed Sep  9 14:42:18 2026)", ""},
			fixedNotice: true,
			want:        []string{"built"},
		},
		{
			name:        "a signal notice on the bottom row",
			lines:       []string{"watching", "", "Pane is dead (signal 9, Sat Sep 12 12:24:50 2026)"},
			fixedNotice: true,
			want:        []string{"watching"},
		},
		{
			name:  "a command's own line is not a notice where tmux writes none",
			lines: []string{"echo 'Pane is dead (status 1, faked)'", ""},
			want:  []string{"echo 'Pane is dead (status 1, faked)'"},
		},
		{name: "nothing shown", lines: []string{"", "", ""}, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := trimScreen(slices.Clone(test.lines), test.fixedNotice)
			if !slices.Equal(got, test.want) {
				t.Errorf("trimScreen(%q, %v) = %q, want %q", test.lines, test.fixedNotice, got, test.want)
			}
		})
	}
}
