package tmux

//go:generate go run ./internal/generate/filters -spec internal/generate/filters/spec.json -output filter_generated.go

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrInvalidFilter reports a malformed or impossible generated filter. Filter
// validation, lookup parsing, predicates, and JSON methods wrap this error.
var ErrInvalidFilter = errors.New("tmux: invalid filter")

type ptrHolder[T any] struct {
	value T
	_     byte
}

// Ptr returns a distinct pointer to a shallow copy of value, including when T
// has zero size. Its nonzero holder prevents Go from coalescing zero-size
// addresses. Slices, maps, pointers, and other reference-bearing values still
// alias their referenced data. Ptr performs no validation or ownership transfer.
func Ptr[T any](value T) *T {
	holder := &ptrHolder[T]{value: value}
	return &holder.value
}

func invalidFilter(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFilter, fmt.Sprintf(format, arguments...))
}

func decodeStrictFilterObject(
	data []byte,
	fields map[string]func(json.RawMessage) error,
) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return invalidFilter("decode JSON object: %v", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return invalidFilter("JSON value must be an object")
	}

	seen := make(map[string]struct{}, len(fields))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return invalidFilter("decode JSON field name: %v", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return invalidFilter("JSON object field name is not a string")
		}
		if _, found := seen[key]; found {
			return invalidFilter("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return invalidFilter("decode JSON field %q: %v", key, err)
		}
		handler, found := fields[key]
		if !found {
			return invalidFilter("unknown JSON field %q", key)
		}
		if err := handler(raw); err != nil {
			if errors.Is(err, ErrInvalidFilter) {
				return err
			}
			return invalidFilter("decode JSON field %q: %v", key, err)
		}
	}

	closing, err := decoder.Token()
	if err != nil {
		return invalidFilter("decode JSON object: %v", err)
	}
	delimiter, ok = closing.(json.Delim)
	if !ok || delimiter != '}' {
		return invalidFilter("JSON object is not closed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidFilter("trailing JSON value")
		}
		return invalidFilter("decode trailing JSON: %v", err)
	}
	return nil
}

func filterSet[T comparable](values []T) map[T]struct{} {
	set := make(map[T]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func splitFilterLookup(lookup string) ([]string, string, error) {
	if lookup == "" {
		return nil, "", invalidFilter("lookup path must not be empty")
	}
	path := strings.Split(lookup, "__")
	if slices.Contains(path, "") {
		return nil, "", invalidFilter("lookup path contains an empty segment")
	}

	operator := "exact"
	last := path[len(path)-1]
	switch last {
	case "eq":
		operator = "exact"
		path = path[:len(path)-1]
	case "exact", "iexact", "contains", "icontains", "startswith",
		"istartswith", "endswith", "iendswith", "in", "nin", "regex", "iregex":
		operator = last
		path = path[:len(path)-1]
	}
	if len(path) == 0 {
		return nil, "", invalidFilter("lookup path must name a field")
	}
	return path, operator, nil
}

func filterLookupOne(model, field, operator string, values []string) (string, error) {
	if len(values) != 1 {
		return "", invalidFilter(
			"%s lookup %s__%s requires exactly one value",
			model,
			field,
			operator,
		)
	}
	return values[0], nil
}

func filterLookupMany(model, field, operator string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, invalidFilter(
			"%s lookup %s__%s requires at least one value",
			model,
			field,
			operator,
		)
	}
	return append([]string(nil), values...), nil
}

func filterLookupInt(model, field, operator, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, invalidFilter(
			"%s lookup %s__%s requires a base-10 integer",
			model,
			field,
			operator,
		)
	}
	return parsed, nil
}

func filterLookupBool(model, field, operator, value string) (bool, error) {
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, invalidFilter(
		"%s lookup %s__%s requires true or false",
		model,
		field,
		operator,
	)
}

func filterExactInPossible[T comparable](exact *T, values []T) bool {
	if exact == nil || values == nil {
		return true
	}
	return slices.Contains(values, *exact)
}

func filterPointersEqual[T comparable](left, right *T) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func filterSlicesEqual[T comparable](left, right []T) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func filterStableCriteriaValid[T ~string](object string, exact *T, values []T) bool {
	if exact != nil && validateStableTarget(object, string(*exact)) != nil {
		return false
	}
	for _, value := range values {
		if validateStableTarget(object, string(value)) != nil {
			return false
		}
	}
	return filterExactInPossible(exact, values)
}

func filterTextCriteriaPossible[T ~string](
	exact *T,
	values []T,
	contains *string,
	pattern string,
) bool {
	if !filterExactInPossible(exact, values) {
		return false
	}
	var candidates []T
	if exact != nil {
		candidates = []T{*exact}
	} else if values != nil {
		candidates = values
	} else {
		return true
	}

	var expression *regexp.Regexp
	if pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		expression = compiled
	}
	for _, candidate := range candidates {
		text := string(candidate)
		if contains != nil && !strings.Contains(text, *contains) {
			continue
		}
		if expression != nil && !expression.MatchString(text) {
			continue
		}
		return true
	}
	return false
}

func filterIntCriteriaPossible(
	exact *int,
	values []int,
	gt, gte, lt, lte *int,
	minimum int,
	hasMinimum bool,
) bool {
	allowed := func(value int) bool {
		return (!hasMinimum || value >= minimum) &&
			(gt == nil || value > *gt) &&
			(gte == nil || value >= *gte) &&
			(lt == nil || value < *lt) &&
			(lte == nil || value <= *lte)
	}
	if exact != nil {
		if !filterExactInPossible(exact, values) || !allowed(*exact) {
			return false
		}
	}
	for _, value := range values {
		if hasMinimum && value < minimum {
			return false
		}
	}
	if exact != nil {
		return true
	}
	if values != nil {
		return slices.ContainsFunc(values, allowed)
	}

	lower, lowerSet, lowerStrict := 0, false, false
	setLower := func(value int, strict bool) {
		if !lowerSet || value > lower || value == lower && strict && !lowerStrict {
			lower, lowerSet, lowerStrict = value, true, strict
		}
	}
	if hasMinimum {
		setLower(minimum, false)
	}
	if gt != nil {
		setLower(*gt, true)
	}
	if gte != nil {
		setLower(*gte, false)
	}

	upper, upperSet, upperStrict := 0, false, false
	setUpper := func(value int, strict bool) {
		if !upperSet || value < upper || value == upper && strict && !upperStrict {
			upper, upperSet, upperStrict = value, true, strict
		}
	}
	if lt != nil {
		setUpper(*lt, true)
	}
	if lte != nil {
		setUpper(*lte, false)
	}

	const maxInt = int(^uint(0) >> 1)
	const minInt = -maxInt - 1
	if lowerSet && lowerStrict && lower == maxInt || upperSet && upperStrict && upper == minInt {
		return false
	}
	if !lowerSet || !upperSet {
		return true
	}
	if lower > upper {
		return false
	}
	if lower == upper {
		return !lowerStrict && !upperStrict
	}
	return !lowerStrict || !upperStrict || lower+1 != upper
}
