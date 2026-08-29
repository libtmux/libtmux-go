//go:build windows

package tmux

import "strings"

const processEnvironmentNULAllowed = false

func processEnvironmentKey(name string) string { return strings.ToLower(name) }

func addCriticalProcessEnvironment(environment, parent []string) []string {
	if _, found := processEnvironmentValue(environment, "SYSTEMROOT"); found {
		return environment
	}
	value, _ := processEnvironmentValue(parent, "SYSTEMROOT")
	return append(environment, "SYSTEMROOT="+value)
}
