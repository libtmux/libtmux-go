package tmuxcmd

import (
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// ResolveExecutable resolves name using only environment and cwd. It never
// consults the process environment or working directory.
func ResolveExecutable(name string, environment []string, cwd string) (string, error) {
	if name == "" || name == "." || name == ".." {
		return "", executableError(name, exec.ErrNotFound)
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", executableError(name, fs.ErrInvalid)
	}
	return resolveExecutable(name, environment, filepath.Clean(cwd))
}

func executableError(name string, err error) error {
	return &exec.Error{Name: name, Err: err}
}

func executableLookupError(name string, err error) error {
	if lookup, ok := errors.AsType[*exec.Error](err); ok {
		err = lookup.Err
	}
	return executableError(name, err)
}

func environmentValue(
	environment []string,
	name string,
	caseInsensitive bool,
) (string, bool) {
	for _, entry := range slices.Backward(environment) {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if key == name || caseInsensitive && strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}
