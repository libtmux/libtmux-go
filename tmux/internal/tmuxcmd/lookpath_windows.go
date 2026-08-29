//go:build windows

package tmuxcmd

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const noDefaultCurrentDirectory = "NoDefaultCurrentDirectoryInExePath"

func resolveExecutable(name string, environment []string, cwd string) (string, error) {
	extensions := windowsPathExtensions(environment)
	if strings.ContainsAny(name, `:\/`) {
		candidate, err := resolveWindowsExplicitPath(name, cwd)
		if err != nil {
			return "", executableError(name, err)
		}
		resolved, err := findWindowsExecutable(candidate, extensions)
		if err != nil {
			return "", executableError(name, err)
		}
		return filepath.Clean(resolved), nil
	}

	var unsafePath string
	if _, disabled := environmentValue(
		environment,
		noDefaultCurrentDirectory,
		true,
	); !disabled {
		if resolved, err := findWindowsExecutable(
			filepath.Join(cwd, name),
			extensions,
		); err == nil {
			unsafePath = filepath.Clean(resolved)
		}
	}

	path, _ := environmentValue(environment, "PATH", true)
	for _, directory := range filepath.SplitList(path) {
		if directory == "" {
			continue
		}
		unsafe := !filepath.IsAbs(directory)
		if unsafe && filepath.VolumeName(directory) != "" {
			return "", executableError(name, fs.ErrInvalid)
		}
		if unsafe {
			if os.IsPathSeparator(directory[0]) {
				directory = filepath.VolumeName(cwd) + directory
			} else {
				directory = filepath.Join(cwd, directory)
			}
		}
		resolved, err := findWindowsExecutable(
			filepath.Join(directory, name),
			extensions,
		)
		if err != nil {
			continue
		}
		resolved = filepath.Clean(resolved)
		if unsafe {
			if unsafePath == "" {
				unsafePath = resolved
			}
			continue
		}
		if unsafePath != "" && !sameFile(unsafePath, resolved) {
			return unsafePath, executableError(name, exec.ErrDot)
		}
		return resolved, nil
	}
	if unsafePath != "" {
		return unsafePath, executableError(name, exec.ErrDot)
	}
	return "", executableError(name, exec.ErrNotFound)
}

func resolveWindowsExplicitPath(name, cwd string) (string, error) {
	if filepath.IsAbs(name) {
		return filepath.Clean(name), nil
	}
	if filepath.VolumeName(name) != "" {
		return "", fs.ErrInvalid
	}
	if os.IsPathSeparator(name[0]) {
		return filepath.Clean(filepath.VolumeName(cwd) + name), nil
	}
	return filepath.Join(cwd, name), nil
}

func windowsPathExtensions(environment []string) []string {
	value, _ := environmentValue(environment, "PATHEXT", true)
	if value == "" {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	extensions := make([]string, 0, strings.Count(value, ";")+1)
	for extension := range strings.SplitSeq(strings.ToLower(value), ";") {
		if extension == "" {
			continue
		}
		if extension[0] != '.' {
			extension = "." + extension
		}
		extensions = append(extensions, extension)
	}
	return extensions
}

func findWindowsExecutable(name string, extensions []string) (string, error) {
	if len(extensions) == 0 {
		return name, checkWindowsExecutable(name)
	}
	if windowsHasExtension(name) {
		if err := checkWindowsExecutable(name); err == nil {
			return name, nil
		}
	}
	for _, extension := range extensions {
		candidate := name + extension
		if err := checkWindowsExecutable(candidate); err == nil {
			return candidate, nil
		}
	}
	if windowsHasExtension(name) {
		return "", fs.ErrNotExist
	}
	return "", exec.ErrNotFound
}

func windowsHasExtension(name string) bool {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return false
	}
	return strings.LastIndexAny(name, `:\/`) < dot
}

func checkWindowsExecutable(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fs.ErrPermission
	}
	return nil
}

func sameFile(first, second string) bool {
	firstInfo, firstErr := os.Lstat(first)
	secondInfo, secondErr := os.Lstat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
