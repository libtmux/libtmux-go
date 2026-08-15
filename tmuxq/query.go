package tmuxq

import (
	"errors"
	"iter"
)

// Cardinality sentinels classify [ExactlyOne] and [ExactlyOneSeq] results
// through [errors.Is].
var (
	// ErrNoMatch identifies an ExactlyOne or ExactlyOneSeq query with no
	// matching value. Cardinality errors can be checked with errors.Is.
	ErrNoMatch = errors.New("tmuxq: no match")
	// ErrMultipleMatches identifies an ExactlyOne or ExactlyOneSeq query
	// with more than one matching value. The queries stop when they find the
	// second match. Cardinality errors can be checked with errors.Is.
	ErrMultipleMatches = errors.New("tmuxq: multiple matches")
)

// Where returns a fresh slice of shallow copies for values accepted by
// predicate, retaining their source order. It evaluates every input value, so
// a no-match result is an empty slice.
//
// Where passes predicate a shallow copy. Direct field mutations to that copy
// are discarded, but mutations through reference-bearing fields affect the
// source and selected values. Predicate must inspect rather than mutate.
func Where[T any](values []T, predicate func(*T) bool) []T {
	matches := make([]T, 0, len(values))
	for index := range values {
		candidate := values[index]
		if predicate(&candidate) {
			matches = append(matches, values[index])
		}
	}
	return matches
}

// First returns a shallow copy of the first source-ordered value accepted by
// predicate and stops evaluating later values. If no value matches, it returns
// the zero value of T and false.
//
// First passes predicate a shallow copy. Direct field mutations to that copy
// are discarded, but mutations through reference-bearing fields affect the
// source and returned value. Predicate must inspect rather than mutate.
func First[T any](values []T, predicate func(*T) bool) (T, bool) {
	for index := range values {
		candidate := values[index]
		if predicate(&candidate) {
			return values[index], true
		}
	}
	var zero T
	return zero, false
}

// ExactlyOne returns a shallow copy when exactly one source-ordered value is
// accepted by predicate. It examines values until source exhaustion confirms a
// sole match, or stops at the second match. It returns the zero value of T
// with [ErrNoMatch] for no matches or [ErrMultipleMatches] for multiple
// matches; callers can classify either error with [errors.Is].
//
// ExactlyOne passes predicate a shallow copy. Direct field mutations to that
// copy are discarded, but mutations through reference-bearing fields affect
// the source and returned value. Predicate must inspect rather than mutate.
func ExactlyOne[T any](values []T, predicate func(*T) bool) (T, error) {
	var match T
	found := false
	for index := range values {
		candidate := values[index]
		if !predicate(&candidate) {
			continue
		}
		if found {
			var zero T
			return zero, ErrMultipleMatches
		}
		match = values[index]
		found = true
	}
	if !found {
		var zero T
		return zero, ErrNoMatch
	}
	return match, nil
}

// WhereSeq returns a lazy sequence of shallow copies for values accepted by
// predicate, retaining source order. It does not pull values until the result
// is iterated. When its consumer stops, WhereSeq stops pulling from values.
// For an infinite input, it runs until its consumer stops.
//
// WhereSeq passes predicate a shallow copy. Direct field mutations to that
// copy are discarded, but mutations through reference-bearing fields affect
// the source and yielded value. Predicate must inspect rather than mutate.
func WhereSeq[T any](values iter.Seq[T], predicate func(*T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for value := range values {
			candidate := value
			if predicate(&candidate) && !yield(value) {
				return
			}
		}
	}
}

// FirstSeq returns a shallow copy of the first source-ordered sequence value
// accepted by predicate and stops pulling later values. If no value matches,
// it returns the zero value of T and false. With an infinite input that never
// matches, FirstSeq does not return.
//
// FirstSeq passes predicate a shallow copy. Direct field mutations to that
// copy are discarded, but mutations through reference-bearing fields affect
// the source and returned value. Predicate must inspect rather than mutate.
func FirstSeq[T any](values iter.Seq[T], predicate func(*T) bool) (T, bool) {
	for value := range values {
		candidate := value
		if predicate(&candidate) {
			return value, true
		}
	}
	var zero T
	return zero, false
}

// ExactlyOneSeq returns a shallow copy when exactly one source-ordered
// sequence value is accepted by predicate. It stops pulling at the second
// match. If input ends with no matches, it returns the zero value of T with
// [ErrNoMatch]; if it finds a second match, it returns the zero value of T with
// [ErrMultipleMatches]. Callers can classify either error with [errors.Is].
// With an infinite input, ExactlyOneSeq returns only after a second match; it
// cannot confirm a no-match or exactly-one result without source exhaustion.
//
// ExactlyOneSeq passes predicate a shallow copy. Direct field mutations to
// that copy are discarded, but mutations through reference-bearing fields
// affect the source and returned value. Predicate must inspect rather than
// mutate.
func ExactlyOneSeq[T any](values iter.Seq[T], predicate func(*T) bool) (T, error) {
	var match T
	found := false
	for value := range values {
		candidate := value
		if !predicate(&candidate) {
			continue
		}
		if found {
			var zero T
			return zero, ErrMultipleMatches
		}
		match = value
		found = true
	}
	if !found {
		var zero T
		return zero, ErrNoMatch
	}
	return match, nil
}
