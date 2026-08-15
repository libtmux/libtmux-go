// Package tmuxq queries materialized values with caller-supplied predicates.
//
// tmuxq is model-free: it neither represents tmux objects nor invokes tmux.
// It works with values of any type, including values obtained from a tmux
// client. Use [Where] to retain every match, [First] to select the first, and
// [ExactlyOne] to require one match. [ErrNoMatch] and [ErrMultipleMatches]
// distinguish the two cardinality failures from a successful result.
//
// # Slices and sequences
//
// Use the slice functions for materialized input. [Where] preserves source
// order in a fresh slice; [First] and [ExactlyOne] return a shallow copy.
// Use [WhereSeq], [FirstSeq], and [ExactlyOneSeq] when the input is an
// [iter.Seq] and retaining lazy iteration matters. WhereSeq does not pull input
// until its result is iterated. The Go module requires Go 1.23 or later for
// iter.Seq.
//
// Every query passes a shallow copy of each examined value to its predicate,
// and every selected value is also a shallow copy. Mutating fields directly on
// the predicate value does not modify the source or result. Reference-bearing
// fields still share their referenced data, so predicates must inspect rather
// than mutate values; a result can likewise alias referenced source data.
//
// A sequence query pulls values only as needed. A consumer that stops a
// WhereSeq result stops further pulls. FirstSeq stops after its first match,
// and ExactlyOneSeq stops after its second match. An infinite sequence cannot
// produce a no-match or exactly-one result: FirstSeq needs a match, and
// ExactlyOneSeq needs a second match or source exhaustion to return.
//
// tmuxq provides no synchronization. Callers are responsible for coordinating
// concurrent access to input, referenced data, predicates, and sequences.
package tmuxq
