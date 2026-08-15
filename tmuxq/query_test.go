package tmuxq

import (
	"errors"
	"slices"
	"testing"
)

type testValue struct {
	ID      int
	Mutable string
	Label   *string
}

// libtmux:parity libtmux._internal.query_list.QueryList
// libtmux:parity libtmux._internal.query_list.QueryList.__eq__
// libtmux:parity libtmux._internal.query_list.QueryList.__eq__#parameter-branch:other:777566d3a56b
// libtmux:parity libtmux._internal.query_list.QueryList.__eq__#parameter-branch:other:997aec5c41fe
// libtmux:parity libtmux._internal.query_list.QueryList.__init__
// libtmux:parity libtmux._internal.query_list.QueryList.__init__#parameter-branch:items:f37f2336fb55
// libtmux:parity libtmux._internal.query_list.QueryList.data
// libtmux:parity libtmux._internal.query_list.QueryList.items
// libtmux:parity libtmux._internal.query_list.QueryList.pk_key
func TestWhereReturnsFreshValuesAndDiscardsPredicateMutation(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1, Mutable: "one"}, {ID: 2, Mutable: "two"}}
	got := Where(input, func(value *testValue) bool {
		keep := value.ID == 2
		value.Mutable = "predicate mutation"
		return keep
	})

	if want := []testValue{{ID: 2, Mutable: "two"}}; !slices.Equal(got, want) {
		t.Fatalf("Where() = %#v, want %#v", got, want)
	}
	got[0].Mutable = "result mutation"
	if input[1].Mutable != "two" {
		t.Fatalf("Where() aliased input: input[1] = %#v", input[1])
	}
}

func TestWhereEvaluatesEveryValue(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1}, {ID: 2}, {ID: 3}}
	visited := 0
	got := Where(input, func(*testValue) bool {
		visited++
		return false
	})
	if len(got) != 0 {
		t.Fatalf("Where() = %#v, want no matches", got)
	}
	if visited != len(input) {
		t.Fatalf("Where() visited %d values, want %d", visited, len(input))
	}
}

// libtmux:parity libtmux._internal.query_list.QueryList.get
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:default:376d5f510564
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:kwargs,matcher:4086b7fdd4bc
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:kwargs,matcher:f47a3e6ef2c5
func TestFirstReturnsOriginalValueAndStops(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1, Mutable: "one"}, {ID: 2, Mutable: "two"}, {ID: 3}}
	visited := 0
	got, ok := First(input, func(value *testValue) bool {
		visited++
		matched := value.ID == 2
		value.Mutable = "predicate mutation"
		return matched
	})
	if !ok || got != input[1] {
		t.Fatalf("First() = (%#v, %v), want (%#v, true)", got, ok, input[1])
	}
	if visited != 2 {
		t.Fatalf("First() visited %d values, want 2", visited)
	}

	zero, ok := First(input, func(*testValue) bool { return false })
	if ok || zero != (testValue{}) {
		t.Fatalf("First() miss = (%#v, %v), want zero, false", zero, ok)
	}
}

// libtmux:parity libtmux._internal.query_list.PKRequiredException
// libtmux:parity libtmux._internal.query_list.PKRequiredException.__init__
// libtmux:parity libtmux._internal.query_list.QueryList.get
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:default:376d5f510564
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:kwargs,matcher:4086b7fdd4bc
// libtmux:parity libtmux._internal.query_list.QueryList.get#parameter-branch:kwargs,matcher:f47a3e6ef2c5
func TestExactlyOneClassifiesCardinality(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1}, {ID: 2}, {ID: 3}}
	visited := 0
	got, err := ExactlyOne(input, func(value *testValue) bool {
		visited++
		return value.ID == 2
	})
	if err != nil || got != input[1] {
		t.Fatalf("ExactlyOne() = (%#v, %v), want (%#v, nil)", got, err, input[1])
	}
	if visited != len(input) {
		t.Fatalf("ExactlyOne() visited %d values, want %d", visited, len(input))
	}
	if _, err := ExactlyOne(input, func(*testValue) bool { return false }); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("ExactlyOne() miss error = %v, want ErrNoMatch", err)
	}
	if _, err := ExactlyOne(input, func(*testValue) bool { return true }); !errors.Is(err, ErrMultipleMatches) {
		t.Fatalf("ExactlyOne() duplicate error = %v, want ErrMultipleMatches", err)
	}
}

func TestExactlyOneStopsAtSecondMatch(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1}, {ID: 2}, {ID: 3}}
	visited := 0
	got, err := ExactlyOne(input, func(*testValue) bool {
		visited++
		return true
	})
	if got != (testValue{}) || !errors.Is(err, ErrMultipleMatches) {
		t.Fatalf("ExactlyOne() = (%#v, %v), want zero, ErrMultipleMatches", got, err)
	}
	if visited != 2 {
		t.Fatalf("ExactlyOne() visited %d values, want 2", visited)
	}
}

func TestQueryMissesReturnZeroValuesAndSentinelErrors(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1}}
	never := func(*testValue) bool { return false }

	if got, ok := First(input, never); ok || got != (testValue{}) {
		t.Fatalf("First() miss = (%#v, %v), want zero, false", got, ok)
	}
	if got, ok := FirstSeq(slices.Values(input), never); ok || got != (testValue{}) {
		t.Fatalf("FirstSeq() miss = (%#v, %v), want zero, false", got, ok)
	}
	if got, err := ExactlyOne(input, never); got != (testValue{}) || !errors.Is(err, ErrNoMatch) {
		t.Fatalf("ExactlyOne() miss = (%#v, %v), want zero, ErrNoMatch", got, err)
	}
	if got, err := ExactlyOneSeq(slices.Values(input), never); got != (testValue{}) || !errors.Is(err, ErrNoMatch) {
		t.Fatalf("ExactlyOneSeq() miss = (%#v, %v), want zero, ErrNoMatch", got, err)
	}
}

func TestFirstSeqStopsAtFirstMatch(t *testing.T) {
	t.Parallel()

	produced := 0
	values := func(yield func(testValue) bool) {
		for id := 1; ; id++ {
			produced++
			if !yield(testValue{ID: id}) {
				return
			}
		}
	}

	got, ok := FirstSeq(values, func(value *testValue) bool { return value.ID == 2 })
	if !ok || got.ID != 2 {
		t.Fatalf("FirstSeq() = (%#v, %v), want ID 2, true", got, ok)
	}
	if produced != 2 {
		t.Fatalf("FirstSeq() produced %d values, want 2", produced)
	}
}

func TestExactlyOneSeqStopsAtSecondMatch(t *testing.T) {
	t.Parallel()

	produced := 0
	values := func(yield func(testValue) bool) {
		for id := 1; ; id++ {
			produced++
			if !yield(testValue{ID: id}) {
				return
			}
		}
	}

	got, err := ExactlyOneSeq(values, func(value *testValue) bool { return value.ID <= 2 })
	if got != (testValue{}) || !errors.Is(err, ErrMultipleMatches) {
		t.Fatalf("ExactlyOneSeq() = (%#v, %v), want zero, ErrMultipleMatches", got, err)
	}
	if produced != 2 {
		t.Fatalf("ExactlyOneSeq() produced %d values, want 2", produced)
	}
}

func TestWhereCopiesValuesButAliasesReferencedData(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{{ID: 1, Mutable: "original", Label: &label}}
	got := Where(input, func(value *testValue) bool {
		value.Mutable = "predicate mutation"
		*value.Label = "referenced mutation"
		return true
	})

	if input[0].Mutable != "original" {
		t.Fatalf("Where() predicate changed input value: %#v", input[0])
	}
	if *input[0].Label != "referenced mutation" {
		t.Fatalf("Where() predicate did not retain referenced mutation: %#v", input[0])
	}
	*got[0].Label = "result mutation"
	if *input[0].Label != "result mutation" {
		t.Fatalf("Where() result did not alias referenced data: %#v", input[0])
	}
}

func TestFirstCopiesValuesButAliasesReferencedData(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{{ID: 1, Mutable: "original", Label: &label}}
	got, ok := First(input, func(value *testValue) bool {
		value.Mutable = "predicate mutation"
		*value.Label = "referenced mutation"
		return true
	})
	if !ok || got.Mutable != "original" || input[0].Mutable != "original" {
		t.Fatalf("First() did not copy direct fields: got %#v, input %#v", got, input[0])
	}
	if *input[0].Label != "referenced mutation" {
		t.Fatalf("First() predicate did not retain referenced mutation: %#v", input[0])
	}
	*got.Label = "result mutation"
	if *input[0].Label != "result mutation" {
		t.Fatalf("First() result did not alias referenced data: %#v", input[0])
	}
}

func TestExactlyOneCopiesValuesButAliasesReferencedData(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{{ID: 1, Mutable: "original", Label: &label}}
	got, err := ExactlyOne(input, func(value *testValue) bool {
		value.Mutable = "predicate mutation"
		*value.Label = "referenced mutation"
		return true
	})
	if err != nil || got.Mutable != "original" || input[0].Mutable != "original" {
		t.Fatalf("ExactlyOne() did not copy direct fields: got %#v, input %#v, err %v", got, input[0], err)
	}
	if *input[0].Label != "referenced mutation" {
		t.Fatalf("ExactlyOne() predicate did not retain referenced mutation: %#v", input[0])
	}
	*got.Label = "result mutation"
	if *input[0].Label != "result mutation" {
		t.Fatalf("ExactlyOne() result did not alias referenced data: %#v", input[0])
	}
}

func TestWhereSeqIsLazyStopsAndDiscardsPredicateMutation(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{
		{ID: 1, Mutable: "original"},
		{ID: 2, Mutable: "original", Label: &label},
		{ID: 3, Mutable: "original"},
		{ID: 4, Mutable: "original"},
	}
	produced := 0
	values := func(yield func(testValue) bool) {
		for _, value := range input {
			produced++
			if !yield(value) {
				return
			}
		}
	}
	filtered := WhereSeq(values, func(value *testValue) bool {
		matched := value.ID%2 == 0
		if matched {
			value.Mutable = "predicate mutation"
			*value.Label = "referenced mutation"
		}
		return matched
	})
	if produced != 0 {
		t.Fatalf("WhereSeq() eagerly produced %d values", produced)
	}

	var got []testValue
	filtered(func(value testValue) bool {
		got = append(got, value)
		return false
	})
	if len(got) != 1 || got[0].Mutable != "original" || input[1].Mutable != "original" {
		t.Fatalf("WhereSeq() did not copy direct fields: got %#v, input %#v", got, input[1])
	}
	if *input[1].Label != "referenced mutation" {
		t.Fatalf("WhereSeq() predicate did not retain referenced mutation: %#v", input[1])
	}
	*got[0].Label = "result mutation"
	if *input[1].Label != "result mutation" {
		t.Fatalf("WhereSeq() result did not alias referenced data: %#v", input[1])
	}
	if produced != 2 {
		t.Fatalf("WhereSeq() produced %d values after consumer stopped, want 2", produced)
	}
}

func TestFirstSeqCopiesValuesButAliasesReferencedData(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{{ID: 1, Mutable: "original", Label: &label}}
	got, ok := FirstSeq(slices.Values(input), func(value *testValue) bool {
		value.Mutable = "predicate mutation"
		*value.Label = "referenced mutation"
		return true
	})
	if !ok || got.Mutable != "original" || input[0].Mutable != "original" {
		t.Fatalf("FirstSeq() did not copy direct fields: got %#v, input %#v", got, input[0])
	}
	if *input[0].Label != "referenced mutation" {
		t.Fatalf("FirstSeq() predicate did not retain referenced mutation: %#v", input[0])
	}
	*got.Label = "result mutation"
	if *input[0].Label != "result mutation" {
		t.Fatalf("FirstSeq() result did not alias referenced data: %#v", input[0])
	}
}

func TestExactlyOneSeqCopiesValuesButAliasesReferencedData(t *testing.T) {
	t.Parallel()

	label := "source"
	input := []testValue{{ID: 1, Mutable: "original", Label: &label}}
	got, err := ExactlyOneSeq(slices.Values(input), func(value *testValue) bool {
		value.Mutable = "predicate mutation"
		*value.Label = "referenced mutation"
		return true
	})
	if err != nil || got.Mutable != "original" || input[0].Mutable != "original" {
		t.Fatalf("ExactlyOneSeq() did not copy direct fields: got %#v, input %#v, err %v", got, input[0], err)
	}
	if *input[0].Label != "referenced mutation" {
		t.Fatalf("ExactlyOneSeq() predicate did not retain referenced mutation: %#v", input[0])
	}
	*got.Label = "result mutation"
	if *input[0].Label != "result mutation" {
		t.Fatalf("ExactlyOneSeq() result did not alias referenced data: %#v", input[0])
	}
}

func TestExactlyOneSeqUniqueMatchExhaustsInput(t *testing.T) {
	t.Parallel()

	produced := 0
	values := func(yield func(testValue) bool) {
		for id := 1; id <= 3; id++ {
			produced++
			if !yield(testValue{ID: id}) {
				return
			}
		}
	}

	got, err := ExactlyOneSeq(values, func(value *testValue) bool { return value.ID == 2 })
	if err != nil || got.ID != 2 {
		t.Fatalf("ExactlyOneSeq() = (%#v, %v), want ID 2, nil", got, err)
	}
	if produced != 3 {
		t.Fatalf("ExactlyOneSeq() produced %d values, want 3", produced)
	}
}

func TestSequenceCardinalityHelpers(t *testing.T) {
	t.Parallel()

	input := []testValue{{ID: 1}, {ID: 2}, {ID: 3}}
	predicate := func(value *testValue) bool { return value.ID == 2 }
	first, ok := FirstSeq(slices.Values(input), predicate)
	if !ok || first != input[1] {
		t.Fatalf("FirstSeq() = (%#v, %v), want (%#v, true)", first, ok, input[1])
	}
	one, err := ExactlyOneSeq(slices.Values(input), predicate)
	if err != nil || one != input[1] {
		t.Fatalf("ExactlyOneSeq() = (%#v, %v), want (%#v, nil)", one, err, input[1])
	}
	if _, err := ExactlyOneSeq(slices.Values(input), func(*testValue) bool { return false }); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("ExactlyOneSeq() miss error = %v, want ErrNoMatch", err)
	}
	if _, err := ExactlyOneSeq(slices.Values(input), func(*testValue) bool { return true }); !errors.Is(err, ErrMultipleMatches) {
		t.Fatalf("ExactlyOneSeq() duplicate error = %v, want ErrMultipleMatches", err)
	}
}
