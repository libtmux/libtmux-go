//go:build unix

package tmuxcmd

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveExecutableUsesFrozenPath(t *testing.T) {
	cwd := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	live := t.TempDir()
	name := "tmux-lookpath-test"
	makeUnixExecutable(t, filepath.Join(first, name))
	makeUnixExecutable(t, filepath.Join(second, name))
	makeUnixExecutable(t, filepath.Join(live, name))
	t.Setenv("PATH", live)

	got, err := ResolveExecutable(name, []string{
		"PATH=" + first,
		"PATH=" + second,
	}, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if want := filepath.Join(second, name); got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableResolvesExplicitRelativePathAgainstFrozenCWD(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "bin", "tmux")
	makeUnixExecutable(t, want)

	got, err := ResolveExecutable(filepath.Join("bin", "tmux"), nil, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableReturnsAbsoluteExplicitPath(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(t.TempDir(), "tmux")
	makeUnixExecutable(t, want)

	got, err := ResolveExecutable(want, nil, cwd)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutableRejectsRelativePathHit(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "bin", "tmux")
	makeUnixExecutable(t, want)

	got, err := ResolveExecutable("tmux", []string{"PATH=bin"}, cwd)
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("ResolveExecutable() error = %v, want exec.ErrDot", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want unsafe hit %q", got, want)
	}
}

func TestResolveExecutableTreatsEmptyPathEntryAsCWD(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "tmux")
	makeUnixExecutable(t, want)

	got, err := ResolveExecutable("tmux", []string{"PATH=:/missing"}, cwd)
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("ResolveExecutable() error = %v, want exec.ErrDot", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want unsafe hit %q", got, want)
	}
}

func TestResolveExecutableSkipsNonExecutablePathEntry(t *testing.T) {
	cwd := t.TempDir()
	blocked := t.TempDir()
	allowed := t.TempDir()
	name := "tmux"
	if err := os.WriteFile(filepath.Join(blocked, name), []byte("blocked"), 0o644); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}
	want := filepath.Join(allowed, name)
	makeUnixExecutable(t, want)

	got, err := ResolveExecutable(
		name,
		[]string{"PATH=" + blocked + string(os.PathListSeparator) + allowed},
		cwd,
	)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != want {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, want)
	}
}

func TestResolveExecutablePreservesExplicitPathErrorIdentity(t *testing.T) {
	cwd := t.TempDir()
	missingName := filepath.Join("missing", "tmux")
	missing := filepath.Join(cwd, missingName)

	_, err := ResolveExecutable(missingName, nil, cwd)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing error = %v, want fs.ErrNotExist", err)
	}
	lookup, ok := errors.AsType[*exec.Error](err)
	if !ok {
		t.Fatalf("missing error type = %T, want *exec.Error", err)
	}
	if lookup.Name != missingName {
		t.Fatalf("missing error name = %q, want original %q", lookup.Name, missingName)
	}
	if filepath.IsAbs(lookup.Name) || lookup.Name == missing {
		t.Fatalf("missing error disclosed rewritten path %q", lookup.Name)
	}

	blocked := filepath.Join(cwd, "blocked")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o644); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}
	_, err = ResolveExecutable(blocked, nil, cwd)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("non-executable error = %v, want fs.ErrPermission", err)
	}
}

func TestResolveExecutableRejectsInvalidInputsWithoutLiveState(t *testing.T) {
	for _, name := range []string{"", ".", ".."} {
		t.Run("name="+name, func(t *testing.T) {
			_, err := ResolveExecutable(name, nil, "/frozen")
			if !errors.Is(err, exec.ErrNotFound) {
				t.Fatalf("ResolveExecutable(%q) error = %v, want exec.ErrNotFound", name, err)
			}
		})
	}

	_, err := ResolveExecutable("tmux", nil, "relative")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("relative cwd error = %v, want fs.ErrInvalid", err)
	}
	_, err = ResolveExecutable("tmux", nil, "")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("empty cwd error = %v, want fs.ErrInvalid", err)
	}
}

func makeUnixExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create executable directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}
