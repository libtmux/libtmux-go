package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestBreakPaneNameRefusalWordsTheExactTmux37Remedy(t *testing.T) {
	version := func(raw string) tmux.Version {
		t.Helper()
		parsed, err := tmux.ParseVersion(raw)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	refused := &tmux.VersionTooLowError{
		Current: version("3.7"), Minimum: version("3.7a"),
		Subcommand: "break-pane", Feature: "automatic_window_name",
	}
	err := breakPaneNameRefusal(refused)
	for _, want := range []string{"break_pane needs name", "tmux 3.7", "3.7a or newer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want %q", err, want)
		}
	}

	unrelated := []error{
		errors.New("no space for new pane"),
		&tmux.VersionTooLowError{
			Current: version("3.7"), Minimum: version("3.7a"),
			Subcommand: "break-pane", Feature: "raw_break_pane",
		},
		&tmux.VersionTooLowError{
			Current: version("3.2a"), Minimum: version("3.3a"),
			Subcommand: "move-pane", Feature: "automatic_window_name",
		},
	}
	for _, other := range unrelated {
		if got := breakPaneNameRefusal(other); !errors.Is(got, other) {
			t.Errorf("breakPaneNameRefusal(%v) = %v, want it unchanged", other, got)
		}
	}
}
