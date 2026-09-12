package tmuxq_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmuxq"
)

type recordFilter struct {
	keep string
	err  error
}

func (f recordFilter) Predicate() (func(*string) bool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return func(value *string) bool { return *value == f.keep }, nil
}

func TestMatchingAppliesAFilterAndReportsItsError(t *testing.T) {
	values := []string{"one", "two", "one"}

	matches, err := tmuxq.Matching(values, recordFilter{keep: "one"})
	if err != nil {
		t.Fatalf("Matching() error = %v", err)
	}
	if want := []string{"one", "one"}; !slices.Equal(matches, want) {
		t.Fatalf("Matching() = %q, want %q", matches, want)
	}

	broken := errors.New("invalid filter")
	if _, err := tmuxq.Matching(values, recordFilter{err: broken}); !errors.Is(err, broken) {
		t.Fatalf("Matching() error = %v, want %v", err, broken)
	}
}

func TestMatchingSeqCompilesBeforeIterating(t *testing.T) {
	values := slices.Values([]string{"one", "two"})

	sequence, err := tmuxq.MatchingSeq(values, recordFilter{keep: "two"})
	if err != nil {
		t.Fatalf("MatchingSeq() error = %v", err)
	}
	if got := slices.Collect(sequence); !slices.Equal(got, []string{"two"}) {
		t.Fatalf("MatchingSeq() = %q, want [two]", got)
	}

	broken := errors.New("invalid filter")
	sequence, err = tmuxq.MatchingSeq(values, recordFilter{err: broken})
	if !errors.Is(err, broken) || sequence != nil {
		t.Fatalf("MatchingSeq() = (%v, %v), want (nil, %v)", sequence, err, broken)
	}
}
