//go:build plan9

package tmux

const processEnvironmentNULAllowed = true

func processEnvironmentKey(name string) string { return name }

func addCriticalProcessEnvironment(environment, _ []string) []string {
	return environment
}
