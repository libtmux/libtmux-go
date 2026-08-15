package tmux

import (
	"slices"
	"testing"
)

func TestOptionValueZeroIsAbsent(t *testing.T) {
	t.Parallel()

	var value OptionValue[string]
	if got, ok := value.Get(); ok || got != "" {
		t.Fatalf("Get() = %q, %t, want empty, false", got, ok)
	}
	if got, ok := value.Origin(); ok || got != 0 {
		t.Fatalf("Origin() = %d, %t, want 0, false", got, ok)
	}
}

func TestOptionValuePreservesEmptyValuesAndOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  OptionValue[string]
		origin OptionOrigin
	}{
		{
			name:   "local",
			value:  newLocalOptionValue(""),
			origin: OptionOriginLocal,
		},
		{
			name:   "inherited",
			value:  newInheritedOptionValue(""),
			origin: OptionOriginInherited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.value.Get()
			if !ok || got != "" {
				t.Fatalf("Get() = %q, %t, want empty, true", got, ok)
			}
			origin, ok := tt.value.Origin()
			if !ok || origin != tt.origin {
				t.Fatalf("Origin() = %d, %t, want %d, true", origin, ok, tt.origin)
			}
		})
	}
}

func TestOptionValueCopiesReferenceBearingValuesShallowly(t *testing.T) {
	t.Parallel()

	source := []string{"before"}
	value := newLocalOptionValue(source)
	source[0] = "after"

	got, ok := value.Get()
	if !ok || !slices.Equal(got, []string{"after"}) {
		t.Fatalf("Get() = %#v, %t, want shallowly shared slice", got, ok)
	}
}

func TestOptionValueDistinguishesAbsentAndEmptySparseArrays(t *testing.T) {
	t.Parallel()

	var absent OptionValue[SparseArray[string]]
	if value, ok := absent.Get(); ok || value.Len() != 0 {
		t.Fatalf("absent Get() = (%#v, %t), want empty, false", value, ok)
	}

	tests := []struct {
		name   string
		value  OptionValue[SparseArray[string]]
		origin OptionOrigin
	}{
		{
			name:   "explicit local empty array",
			value:  newLocalOptionValue(SparseArray[string]{}),
			origin: OptionOriginLocal,
		},
		{
			name:   "explicit inherited empty array",
			value:  newInheritedOptionValue(SparseArray[string]{}),
			origin: OptionOriginInherited,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, ok := tt.value.Get()
			if !ok || value.Len() != 0 {
				t.Fatalf("Get() = (%#v, %t), want empty, true", value, ok)
			}
			origin, ok := tt.value.Origin()
			if !ok || origin != tt.origin {
				t.Fatalf("Origin() = (%d, %t), want (%d, true)", origin, ok, tt.origin)
			}
		})
	}
}
