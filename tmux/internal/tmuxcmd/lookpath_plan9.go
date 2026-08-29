//go:build plan9

package tmuxcmd

func resolveExecutable(name string, environment []string, cwd string) (string, error) {
	return resolvePathExecutable(name, environment, cwd, "path")
}
