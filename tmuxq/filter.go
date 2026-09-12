package tmuxq

import "iter"

// Filter is anything that compiles itself into a predicate over T, which is
// what the tmux package's generated model filters do. It exists so a filter
// and a query can meet without this package naming a model.
type Filter[T any] interface {
	// Predicate returns the test this filter describes, or the reason it
	// cannot be one.
	Predicate() (func(*T) bool, error)
}

// Matching returns a fresh slice of shallow copies for values that filter
// accepts, retaining their source order, and reports the one error a filter
// can have rather than leaving it for the caller to unpack first. It evaluates
// every input value, so a no-match result is an empty slice.
//
//	active, err := tmuxq.Matching(snapshot.Panes(), tmux.PaneActiveIs(true))
func Matching[T any](values []T, filter Filter[T]) ([]T, error) {
	predicate, err := filter.Predicate()
	if err != nil {
		return nil, err
	}
	return Where(values, predicate), nil
}

// MatchingSeq returns a lazy sequence of shallow copies for values that filter
// accepts, retaining source order. The filter is compiled before iteration
// begins, so an invalid filter is an error here rather than a sequence that
// yields nothing.
func MatchingSeq[T any](values iter.Seq[T], filter Filter[T]) (iter.Seq[T], error) {
	predicate, err := filter.Predicate()
	if err != nil {
		return nil, err
	}
	return WhereSeq(values, predicate), nil
}
