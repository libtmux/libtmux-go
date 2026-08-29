//go:build windows

package tmuxcmd

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveExecutableWindowsUsesFrozenCaseInsensitiveEnvironment(t *testing.T) {
	cwd := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	want := filepath.Join(second, "tmux.cmd")
	makeWindowsExecutable(t, filepath.Join(first, "tmux.exe"))
	makeWindowsExecutable(t, want)

	got, err := ResolveExecutable("tmux", []string{
		"Path=" + first,
		"PATH=" + second,
		"Pathext=.EXE",
		"PATHEXT=.CMD",
	}, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableWindowsUsesDefaultPathExt(t *testing.T) {
	cwd := t.TempDir()
	pathDirectory := t.TempDir()
	want := filepath.Join(pathDirectory, "tmux.exe")
	makeWindowsExecutable(t, want)

	got, err := ResolveExecutable("tmux", []string{"PATH=" + pathDirectory}, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableWindowsEmptyPathExtAllowsExtensionlessFile(t *testing.T) {
	cwd := t.TempDir()
	pathDirectory := t.TempDir()
	want := filepath.Join(pathDirectory, "tmux")
	makeWindowsExecutable(t, want)

	got, err := ResolveExecutable(
		"tmux",
		[]string{"PATH=" + pathDirectory, "PATHEXT=;;"},
		cwd,
	)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableWindowsResolvesExplicitRelativePath(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "bin", "tmux.exe")
	makeWindowsExecutable(t, want)

	got, err := ResolveExecutable(filepath.Join("bin", "tmux"), nil, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableWindowsCurrentDirectoryRules(t *testing.T) {
	t.Run("unsafe default", func(t *testing.T) {
		cwd := t.TempDir()
		want := filepath.Join(cwd, "tmux.exe")
		makeWindowsExecutable(t, want)

		got, err := ResolveExecutable("tmux", nil, cwd)
		if !errors.Is(err, exec.ErrDot) {
			t.Fatalf("ResolveExecutable() error = %v, want exec.ErrDot", err)
		}
		if got != want {
			t.Fatalf("ResolveExecutable() = %q, want unsafe hit %q", got, want)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		cwd := t.TempDir()
		makeWindowsExecutable(t, filepath.Join(cwd, "tmux.exe"))

		_, err := ResolveExecutable(
			"tmux",
			[]string{"NoDefaultCurrentDirectoryInExePath="},
			cwd,
		)
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("ResolveExecutable() error = %v, want exec.ErrNotFound", err)
		}
	})

	t.Run("same absolute path wins", func(t *testing.T) {
		cwd := t.TempDir()
		want := filepath.Join(cwd, "tmux.exe")
		makeWindowsExecutable(t, want)

		got, err := ResolveExecutable("tmux", []string{"PATH=" + cwd}, cwd)
		if err != nil {
			t.Fatalf("ResolveExecutable() error = %v", err)
		}
		if got != want {
			t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
		}
	})
}

func TestResolveExecutableWindowsRejectsRelativePathHit(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "bin", "tmux.exe")
	makeWindowsExecutable(t, want)

	got, err := ResolveExecutable(
		"tmux",
		[]string{
			"NoDefaultCurrentDirectoryInExePath=1",
			"PATH=bin",
		},
		cwd,
	)
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("ResolveExecutable() error = %v, want exec.ErrDot", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want unsafe hit %q", got, want)
	}
}

func TestResolveExecutableWindowsPreservesDifferentUnsafeHit(t *testing.T) {
	cwd := t.TempDir()
	unsafe := filepath.Join(cwd, "tmux.exe")
	makeWindowsExecutable(t, unsafe)
	pathDirectory := t.TempDir()
	makeWindowsExecutable(t, filepath.Join(pathDirectory, "tmux.exe"))

	got, err := ResolveExecutable("tmux", []string{"PATH=" + pathDirectory}, cwd)
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("ResolveExecutable() error = %v, want exec.ErrDot", err)
	}
	if got != unsafe {
		t.Fatalf("ResolveExecutable() = %q, want unsafe hit %q", got, unsafe)
	}
}

func TestResolveExecutableWindowsRejectsDriveRelativePath(t *testing.T) {
	cwd := t.TempDir()
	_, err := ResolveExecutable(`C:tmux.exe`, nil, cwd)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("ResolveExecutable() error = %v, want fs.ErrInvalid", err)
	}
}

func makeWindowsExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create executable directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}
