// Package tmuxq queries materialized values without representing or invoking tmux.
//
// # Slices and sequences
//
// [Where], [First], and [ExactlyOne] query slices. [WhereSeq] returns a lazy
// sequence; [FirstSeq] and [ExactlyOneSeq] consume only until they determine a
// result.
//
// Every query passes a shallow copy of each examined value to its predicate,
// and returns or yields shallow copies. Reference-bearing fields still alias
// source data, so predicates must inspect rather than mutate values.
//
// Sequence queries pull only as needed. Infinite inputs cannot establish no
// match or exactly one match without ending.
//
// tmuxq provides no synchronization.
package tmuxq
