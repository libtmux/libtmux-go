package tmux

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
)

func encodeTypedOptionBool(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func encodeTypedOptionInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

// SetArrayResult reports confirmed progress from a typed sparse-array
// replacement. AppliedIndices is caller-owned and is non-nil whenever mutation
// was attempted.
type SetArrayResult struct {
	// Replaced reports whether tmux confirmed the base replacement.
	Replaced bool
	// AppliedIndices lists confirmed indexed writes in ascending order.
	AppliedIndices []int
}

func setTypedOption(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	value string,
	choice bool,
) error {
	if err := validateServerCommandArgument("set-option", "Name", name, true); err != nil {
		return err
	}
	if err := validateServerCommandArgument("set-option", "Value", value, true); err != nil {
		return err
	}
	if choice {
		definition := generatedOptionDefinitionByName(name)
		if definition == nil || !generatedChoiceValueValid(*definition, value) {
			return &OptionValueError{Name: name}
		}
		if generatedChoiceDomainVariesByVersion(*definition, generatedScope) {
			version, err := server.Version(ctx)
			if err != nil {
				return err
			}
			variant := generatedActiveOptionVariant(*definition, version)
			if variant != nil && variant.scopes&generatedScope != 0 &&
				!generatedVariantChoiceValid(*variant, value) {
				return &OptionValueError{Name: name}
			}
		}
	}
	return changeOption(
		ctx, server, scope, generatedScope, name, value, SetOptionOptions{}, false,
	)
}

func setTypedOptionArray(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	value SparseArray[string],
) (SetArrayResult, error) {
	entries := value.Entries()
	slices.SortFunc(entries, func(left, right SparseEntry[string]) int {
		switch {
		case left.Index < right.Index:
			return -1
		case left.Index > right.Index:
			return 1
		default:
			return 0
		}
	})
	if err := validateServerCommandArgument("set-option", "Name", name, true); err != nil {
		return SetArrayResult{}, err
	}
	for index, entry := range entries {
		if entry.Index < 0 || entry.Index > math.MaxInt32 {
			return SetArrayResult{}, fmt.Errorf("%w: %d", ErrInvalidSparseIndex, entry.Index)
		}
		if index != 0 && entries[index-1].Index == entry.Index {
			return SetArrayResult{}, fmt.Errorf("%w: %d", ErrDuplicateSparseIndex, entry.Index)
		}
		if err := validateServerCommandArgument(
			"set-option", "Value", entry.Value, true,
		); err != nil {
			return SetArrayResult{}, err
		}
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-option",
		name,
		generatedOptionDefinitions[:],
		generatedOptionAliases[:],
		generatedScope,
		true,
	); err != nil {
		return SetArrayResult{}, err
	}

	result := SetArrayResult{AppliedIndices: make([]int, 0, len(entries))}
	if err := runTypedArrayOptionMutation(ctx, server, scope, name, ""); err != nil {
		return result, err
	}
	result.Replaced = true
	for _, entry := range entries {
		indexedName := name + "[" + strconv.Itoa(entry.Index) + "]"
		if err := runTypedArrayOptionMutation(ctx, server, scope, indexedName, entry.Value); err != nil {
			return result, err
		}
		result.AppliedIndices = append(result.AppliedIndices, entry.Index)
	}
	return result, nil
}

func runTypedArrayOptionMutation(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	value string,
) error {
	arguments := make([]string, 0, len(scope)+4)
	arguments = append(arguments, "set-option")
	arguments = append(arguments, scope...)
	arguments = append(arguments, "--", name, value)
	return runOptionMutation(ctx, server, arguments, name, false)
}

func generatedOptionDefinitionByName(name string) *generatedOptionDefinition {
	for index := range generatedOptionDefinitions {
		if generatedOptionDefinitions[index].name == name {
			return &generatedOptionDefinitions[index]
		}
	}
	return nil
}

func generatedChoiceValueValid(definition generatedOptionDefinition, value string) bool {
	for _, variant := range definition.variants {
		if generatedVariantChoiceValid(variant, value) {
			return true
		}
	}
	return false
}

func generatedVariantChoiceValid(variant generatedOptionVariant, value string) bool {
	if variant.kind == generatedOptionKindFlag {
		return value == "off" || value == "on"
	}
	return slices.Contains(variant.choices, value)
}

func generatedChoiceDomainVariesByVersion(
	definition generatedOptionDefinition,
	scope generatedOptionScope,
) bool {
	var baseline []string
	for _, variant := range definition.variants {
		if variant.scopes&scope == 0 {
			continue
		}
		domain := variant.choices
		if variant.kind == generatedOptionKindFlag {
			domain = []string{"off", "on"}
		}
		if baseline == nil {
			baseline = domain
			continue
		}
		if !slices.Equal(baseline, domain) {
			return true
		}
	}
	return false
}

func generatedActiveOptionVariant(
	definition generatedOptionDefinition,
	version Version,
) *generatedOptionVariant {
	var active *generatedOptionVariant
	for index := range definition.variants {
		if !version.AtLeast(definition.variants[index].minimum) {
			break
		}
		active = &definition.variants[index]
	}
	return active
}
