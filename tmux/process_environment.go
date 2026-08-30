package tmux

import (
	"fmt"
	"slices"
	"strings"
)

func normalizeProcessEnvironment(
	environment []string,
	parent []string,
) ([]string, error) {
	seen := make(map[string]struct{}, len(environment))
	normalized := make([]string, 0, len(environment))
	for _, entry := range slices.Backward(environment) {
		if !processEnvironmentNULAllowed && strings.ContainsRune(entry, '\x00') {
			return nil, fmt.Errorf(
				"%w: ProcessEnvironment contains NUL",
				ErrInvalidServerOptions,
			)
		}
		if entry == "" {
			continue
		}
		name, _, found := splitProcessEnvironmentEntry(entry)
		if !found {
			normalized = append(normalized, entry)
			continue
		}
		key := processEnvironmentKey(name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, entry)
	}
	slices.Reverse(normalized)
	return addCriticalProcessEnvironment(normalized, parent), nil
}

func processEnvironmentValue(environment []string, name string) (string, bool) {
	key := processEnvironmentKey(name)
	for _, entry := range slices.Backward(environment) {
		entryName, value, found := splitProcessEnvironmentEntry(entry)
		if found && processEnvironmentKey(entryName) == key {
			return value, true
		}
	}
	return "", false
}

func setProcessEnvironmentValue(environment []string, name, value string) []string {
	key := processEnvironmentKey(name)
	updated := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		entryName, _, found := splitProcessEnvironmentEntry(entry)
		if found && processEnvironmentKey(entryName) == key {
			continue
		}
		updated = append(updated, entry)
	}
	return append(updated, name+"="+value)
}

func splitProcessEnvironmentEntry(entry string) (name, value string, found bool) {
	separator := strings.IndexByte(entry, '=')
	if separator == 0 {
		if next := strings.IndexByte(entry[1:], '='); next >= 0 {
			separator = next + 1
		}
	}
	if separator < 0 {
		return "", "", false
	}
	return entry[:separator], entry[separator+1:], true
}
