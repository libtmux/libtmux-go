//go:build unix || plan9

package tmuxcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func resolvePathExecutable(
	name string,
	environment []string,
	cwd string,
	pathName string,
) (string, error) {
	if filepath.IsAbs(name) {
		return resolvePathCandidate(name, name)
	}
	// Treat an embedded separator as an explicit relative path on every
	// platform, including Plan 9 where os/exec searches some such names.
	if strings.ContainsRune(name, os.PathSeparator) {
		return resolvePathCandidate(name, filepath.Join(cwd, name))
	}

	path, _ := environmentValue(environment, pathName, false)
	for _, directory := range filepath.SplitList(path) {
		unsafe := directory == "" || !filepath.IsAbs(directory)
		if directory == "" {
			directory = "."
		}
		if unsafe {
			directory = filepath.Join(cwd, directory)
		}
		candidate, err := exec.LookPath(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		candidate = filepath.Clean(candidate)
		if unsafe {
			return candidate, executableError(name, exec.ErrDot)
		}
		return candidate, nil
	}
	return "", executableError(name, exec.ErrNotFound)
}

func resolvePathCandidate(name, candidate string) (string, error) {
	candidate = filepath.Clean(candidate)
	resolved, err := exec.LookPath(candidate)
	if err != nil {
		return "", executableLookupError(name, err)
	}
	return filepath.Clean(resolved), nil
}
