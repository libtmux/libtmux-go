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

// TestOutcomeRecordedWaitsForTmuxToReapTheCommand covers the state a pane
// passes through on its way to dead: tmux closes the pane's terminal, which is
// all pane_dead reports through tmux 3.5a, and only reaps the command
// afterwards. A wait that accepted the first of those would report every
// command as having exited zero.
func TestOutcomeRecordedWaitsForTmuxToReapTheCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		formats map[string]string
		want    bool
	}{
		{
			name:    "terminal closed before tmux reaped the command",
			formats: map[string]string{"pane_dead": "1"},
			want:    false,
		},
		{
			name: "outcome fields present but still empty",
			formats: map[string]string{
				"pane_dead": "1", "pane_dead_status": "", "pane_dead_signal": "",
			},
			want: false,
		},
		{
			name:    "nonzero exit status recorded",
			formats: map[string]string{"pane_dead": "1", "pane_dead_status": "7"},
			want:    true,
		},
		{
			name:    "zero exit status recorded",
			formats: map[string]string{"pane_dead": "1", "pane_dead_status": "0"},
			want:    true,
		},
		{
			name:    "signal recorded, as tmux 3.3 and later report it",
			formats: map[string]string{"pane_dead": "1", "pane_dead_signal": "KILL"},
			want:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pane := Pane{formats: formatValues{values: test.formats}}
			if got := outcomeRecorded(pane); got != test.want {
				t.Errorf("outcomeRecorded(%v) = %v, want %v", test.formats, got, test.want)
			}
		})
	}
}
