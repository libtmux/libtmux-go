package tmux

import (
	"errors"
	"fmt"
	"iter"
	"slices"
)

var (
	// ErrInvalidSparseIndex identifies a negative or overflowing sparse index.
	ErrInvalidSparseIndex = errors.New("tmux: invalid sparse array index")
	// ErrDuplicateSparseIndex identifies repeated constructor indices.
	ErrDuplicateSparseIndex = errors.New("tmux: duplicate sparse array index")
)

// SparseEntry is one indexed [SparseArray] value.
type SparseEntry[T any] struct {
	// Index is the nonnegative sparse-array index.
	Index int
	// Value is the value stored at Index.
	Value T
}

// SparseArray stores values at sorted, nonnegative indices. Its zero value is
// an empty array. Constructors and functional updates own their entry slices;
// copies of reference-bearing T values remain shallow.
type SparseArray[T any] struct {
	entries []SparseEntry[T]
}

// NewSparseArray builds an array from entries, sorting them by index. It returns
// errors matching [ErrInvalidSparseIndex] or [ErrDuplicateSparseIndex] for
// negative, overflowing, or duplicate indices.
func NewSparseArray[T any](entries ...SparseEntry[T]) (SparseArray[T], error) {
	owned := append(make([]SparseEntry[T], 0, len(entries)), entries...)
	for _, entry := range owned {
		if entry.Index < 0 {
			return SparseArray[T]{}, fmt.Errorf("%w: %d", ErrInvalidSparseIndex, entry.Index)
		}
	}
	slices.SortFunc(owned, func(left, right SparseEntry[T]) int {
		switch {
		case left.Index < right.Index:
			return -1
		case left.Index > right.Index:
			return 1
		default:
			return 0
		}
	})
	for index := 1; index < len(owned); index++ {
		if owned[index-1].Index == owned[index].Index {
			return SparseArray[T]{}, fmt.Errorf(
				"%w: %d",
				ErrDuplicateSparseIndex,
				owned[index].Index,
			)
		}
	}
	return SparseArray[T]{entries: owned}, nil
}

// Get returns the value at index and reports whether it is present. A missing
// index, including a sparse hole, returns the zero value and false.
func (a SparseArray[T]) Get(index int) (T, bool) {
	position, found := a.position(index)
	if !found {
		var zero T
		return zero, false
	}
	return a.entries[position].Value, true
}

// Len returns the number of present entries, not the highest index.
func (a SparseArray[T]) Len() int {
	return len(a.entries)
}

// Indices returns a fresh slice of present indices in ascending order.
func (a SparseArray[T]) Indices() []int {
	indices := make([]int, len(a.entries))
	for index, entry := range a.entries {
		indices[index] = entry.Index
	}
	return indices
}

// Entries returns a fresh shallow copy in ascending index order.
func (a SparseArray[T]) Entries() []SparseEntry[T] {
	return append(make([]SparseEntry[T], 0, len(a.entries)), a.entries...)
}

// Values returns a fresh shallow slice in ascending index order.
func (a SparseArray[T]) Values() []T {
	values := make([]T, len(a.entries))
	for index, entry := range a.entries {
		values[index] = entry.Value
	}
	return values
}

// All returns an iterator over indices and values in ascending index order.
func (a SparseArray[T]) All() iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for _, entry := range a.entries {
			if !yield(entry.Index, entry.Value) {
				return
			}
		}
	}
}

// With returns a copy with value inserted or replaced at index. It leaves the
// receiver unchanged and returns an error matching [ErrInvalidSparseIndex] for
// a negative index.
func (a SparseArray[T]) With(index int, value T) (SparseArray[T], error) {
	if index < 0 {
		return SparseArray[T]{}, fmt.Errorf("%w: %d", ErrInvalidSparseIndex, index)
	}
	position, found := a.position(index)
	entries := a.Entries()
	if found {
		entries[position].Value = value
		return SparseArray[T]{entries: entries}, nil
	}
	entries = append(entries, SparseEntry[T]{})
	copy(entries[position+1:], entries[position:])
	entries[position] = SparseEntry[T]{Index: index, Value: value}
	return SparseArray[T]{entries: entries}, nil
}

// Append returns a copy with value stored after the highest present index. It
// leaves the receiver unchanged and returns an error matching
// [ErrInvalidSparseIndex] when that index overflows.
func (a SparseArray[T]) Append(value T) (SparseArray[T], error) {
	index := 0
	if len(a.entries) != 0 {
		highest := a.entries[len(a.entries)-1].Index
		if highest == int(^uint(0)>>1) {
			return SparseArray[T]{}, fmt.Errorf("%w: append after %d", ErrInvalidSparseIndex, highest)
		}
		index = highest + 1
	}
	return a.With(index, value)
}

func (a SparseArray[T]) position(index int) (int, bool) {
	return slices.BinarySearchFunc(a.entries, index, func(entry SparseEntry[T], target int) int {
		switch {
		case entry.Index < target:
			return -1
		case entry.Index > target:
			return 1
		default:
			return 0
		}
	})
}
