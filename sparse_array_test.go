package tmux_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/tmux-python/libtmux/golang"
)

func TestSparseArrayZeroValueIsEmptyAndReturnsFreshSlices(t *testing.T) {
	var array tmux.SparseArray[string]
	if array.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", array.Len())
	}
	if _, ok := array.Get(0); ok {
		t.Fatal("Get(0) found a value in the zero array")
	}
	if array.Indices() == nil || array.Entries() == nil || array.Values() == nil {
		t.Fatal("empty projections must be nonnil")
	}
}

// libtmux:parity libtmux._internal.sparse_array.is_sparse_array_list
func TestNewSparseArraySortsEntriesAndRejectsInvalidIndices(t *testing.T) {
	array, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 8, Value: "eight"},
		tmux.SparseEntry[string]{Index: 1, Value: "one"},
		tmux.SparseEntry[string]{Index: 3, Value: "three"},
	)
	if err != nil {
		t.Fatalf("NewSparseArray() error = %v", err)
	}
	if got := array.Indices(); !slices.Equal(got, []int{1, 3, 8}) {
		t.Fatalf("Indices() = %v, want [1 3 8]", got)
	}
	if got := array.Values(); !slices.Equal(got, []string{"one", "three", "eight"}) {
		t.Fatalf("Values() = %v", got)
	}

	_, err = tmux.NewSparseArray(tmux.SparseEntry[string]{Index: -1, Value: "bad"})
	if !errors.Is(err, tmux.ErrInvalidSparseIndex) {
		t.Fatalf("negative index error = %v, want ErrInvalidSparseIndex", err)
	}
	_, err = tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 2, Value: "first"},
		tmux.SparseEntry[string]{Index: 2, Value: "second"},
	)
	if !errors.Is(err, tmux.ErrDuplicateSparseIndex) {
		t.Fatalf("duplicate index error = %v, want ErrDuplicateSparseIndex", err)
	}
}

// libtmux:parity libtmux._internal.sparse_array.SparseArray
// libtmux:parity libtmux._internal.sparse_array.SparseArray.add
// libtmux:parity libtmux._internal.sparse_array.SparseArray.append
func TestSparseArrayFunctionalUpdatesDoNotMutateEarlierValues(t *testing.T) {
	var empty tmux.SparseArray[string]
	first, err := empty.Append("zero")
	if err != nil {
		t.Fatalf("empty Append() error = %v", err)
	}
	if value, ok := first.Get(0); !ok || value != "zero" {
		t.Fatalf("empty Append() Get(0) = (%q, %t), want (zero, true)", value, ok)
	}

	original, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 2, Value: "two"},
		tmux.SparseEntry[string]{Index: 5, Value: "five"},
	)
	if err != nil {
		t.Fatalf("NewSparseArray() error = %v", err)
	}
	replaced, err := original.With(2, "TWO")
	if err != nil {
		t.Fatalf("With() replace error = %v", err)
	}
	inserted, err := replaced.With(3, "three")
	if err != nil {
		t.Fatalf("With() insert error = %v", err)
	}
	appended, err := inserted.Append("six")
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if got := original.Values(); !slices.Equal(got, []string{"two", "five"}) {
		t.Fatalf("original Values() = %v", got)
	}
	if got := appended.Indices(); !slices.Equal(got, []int{2, 3, 5, 6}) {
		t.Fatalf("appended Indices() = %v, want [2 3 5 6]", got)
	}
	if _, err := original.With(-1, "bad"); !errors.Is(err, tmux.ErrInvalidSparseIndex) {
		t.Fatalf("With(-1) error = %v, want ErrInvalidSparseIndex", err)
	}
	overflow, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: int(^uint(0) >> 1), Value: "last"},
	)
	if err != nil {
		t.Fatalf("NewSparseArray(max int) error = %v", err)
	}
	if _, err := overflow.Append("bad"); !errors.Is(err, tmux.ErrInvalidSparseIndex) {
		t.Fatalf("overflow Append() error = %v, want ErrInvalidSparseIndex", err)
	}
}

// libtmux:parity libtmux._internal.sparse_array.SparseArray.as_list
// libtmux:parity libtmux._internal.sparse_array.SparseArray.iter_values
func TestSparseArrayProjectionsAndIterationPreserveOwnership(t *testing.T) {
	array, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "zero"},
		tmux.SparseEntry[string]{Index: 4, Value: "four"},
	)
	if err != nil {
		t.Fatalf("NewSparseArray() error = %v", err)
	}
	entries := array.Entries()
	entries[0] = tmux.SparseEntry[string]{Index: 99, Value: "changed"}
	indices := array.Indices()
	indices[0] = 99
	values := array.Values()
	values[0] = "changed"

	var iterated []tmux.SparseEntry[string]
	for index, value := range array.All() {
		iterated = append(iterated, tmux.SparseEntry[string]{Index: index, Value: value})
	}
	want := []tmux.SparseEntry[string]{{Index: 0, Value: "zero"}, {Index: 4, Value: "four"}}
	if !slices.Equal(iterated, want) {
		t.Fatalf("All() = %#v, want %#v", iterated, want)
	}
	if !slices.Equal(array.Entries(), want) {
		t.Fatalf("Entries() changed through a projection: %#v", array.Entries())
	}
}
