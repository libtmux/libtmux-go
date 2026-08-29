//go:build !windows && !plan9

package tmux

const processEnvironmentNULAllowed = false

func processEnvironmentKey(name string) string { return name }

func addCriticalProcessEnvironment(environment, _ []string) []string {
	return environment
}
