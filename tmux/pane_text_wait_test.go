package tmux

import (
	"errors"
	"testing"
)

func TestPaneEndedErrorMatchesItsSentinels(t *testing.T) {
	cause := &CommandError{Subcommand: "capture-pane", targetNotFound: true}
	for _, testCase := range []struct {
		name    string
		err     *PaneEndedError
		is      []error
		isNot   []error
		unwraps error
	}{
		{
			name:  "exited",
			err:   &PaneEndedError{Reason: PaneExited},
			is:    []error{ErrPaneEnded},
			isNot: []error{ErrNotFound, ErrNoServer},
		},
		{
			name:    "gone with a failed read",
			err:     &PaneEndedError{Reason: PaneGone, cause: cause},
			is:      []error{ErrPaneEnded, ErrNotFound, ErrCommand},
			isNot:   []error{ErrNoServer},
			unwraps: cause,
		},
		{
			name:  "gone from the report",
			err:   &PaneEndedError{Reason: PaneGone},
			is:    []error{ErrPaneEnded, ErrNotFound},
			isNot: []error{ErrNoServer},
		},
		{
			name:  "server gone",
			err:   &PaneEndedError{Reason: ServerGone},
			is:    []error{ErrPaneEnded, ErrNoServer},
			isNot: []error{ErrNotFound},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, target := range testCase.is {
				if !errors.Is(testCase.err, target) {
					t.Errorf("errors.Is(%v, %v) = false, want true", testCase.err, target)
				}
			}
			for _, target := range testCase.isNot {
				if errors.Is(testCase.err, target) {
					t.Errorf("errors.Is(%v, %v) = true, want false", testCase.err, target)
				}
			}
			if testCase.unwraps != nil && !errors.Is(testCase.err, testCase.unwraps) {
				t.Errorf("error does not unwrap to the failed read")
			}
		})
	}
}

func TestTrimWaitScreenDropsLineEndSpacesAndTrailingBlankLines(t *testing.T) {
	got := trimWaitScreen("one  \n  two \n\n \n")
	if want := "one\n  two"; got != want {
		t.Fatalf("trimWaitScreen() = %q, want %q", got, want)
	}
}
