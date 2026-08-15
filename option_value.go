package tmux

//go:generate go run ./internal/generate/options -spec internal/generate/options/spec.json -output option_generated.go

// OptionOrigin identifies where a present option value was resolved.
// The declared values form the complete set of valid origins.
type OptionOrigin uint8

// String returns the origin's tmux vocabulary, so an origin prints as a word
// beside the generated option values, which all carry String.
func (o OptionOrigin) String() string {
	switch o {
	case OptionOriginLocal:
		return "local"
	case OptionOriginInherited:
		return "inherited"
	default:
		return "unset"
	}
}

const (
	// OptionOriginLocal identifies a value set directly at the queried scope.
	OptionOriginLocal OptionOrigin = iota + 1
	// OptionOriginInherited identifies a value inherited from a parent scope.
	OptionOriginInherited
)

// OptionValue preserves option presence and resolution origin.
// Its zero value is absent. Copies of reference-bearing T values are shallow.
type OptionValue[T any] struct {
	value  T
	origin OptionOrigin
}

// Get returns the option value and reports whether it is present.
func (v OptionValue[T]) Get() (T, bool) {
	if !v.present() {
		var zero T
		return zero, false
	}
	return v.value, true
}

// Origin returns the resolution origin and reports whether the option is present.
func (v OptionValue[T]) Origin() (OptionOrigin, bool) {
	if !v.present() {
		return 0, false
	}
	return v.origin, true
}

func newLocalOptionValue[T any](value T) OptionValue[T] {
	return OptionValue[T]{value: value, origin: OptionOriginLocal}
}

func newInheritedOptionValue[T any](value T) OptionValue[T] {
	return OptionValue[T]{value: value, origin: OptionOriginInherited}
}

func (v OptionValue[T]) present() bool {
	return v.origin == OptionOriginLocal || v.origin == OptionOriginInherited
}
