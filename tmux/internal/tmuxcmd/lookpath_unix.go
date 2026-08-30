//go:build unix

package tmuxcmd

func resolveExecutable(name string, environment []string, cwd string) (string, error) {
	return resolvePathExecutable(name, environment, cwd, "PATH")
}
