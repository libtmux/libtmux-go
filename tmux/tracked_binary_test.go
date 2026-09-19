package tmux_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// go build ./tmux/internal/generate/docs/ (or any other generator package,
// run directly instead of through go generate) drops a several-megabyte
// binary in the repo root, named after the package directory. The generated-
// file freshness gate this repository documents (regenerate, then git diff
// --exit-code) is blind to it: that binary is untracked, so it never appears
// in a diff. This repository's own policy for a stray artifact is "delete
// it," not hide it in .gitignore (see the comment at the top of .gitignore),
// so the gate for this has to be one that would notice the artefact were it
// ever actually committed by mistake, rather than one that keeps it out of
// sight some other way.
//
// git itself already has a working definition of "binary": the same one
// core.autocrlf and diff use, a NUL byte in a file's first 8000 bytes (see
// git's own buffer_is_binary). Reusing it means this test flags exactly what
// "git diff" would call a binary file, so the two never disagree about what
// counts.
func TestNoTrackedFileIsBinary(t *testing.T) {
	t.Parallel()

	root := documentationModuleRoot(t)
	command := exec.Command("git", "ls-files", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	var binaries []string
	for path := range strings.SplitSeq(strings.TrimRight(string(output), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		looksBinary, err := fileLooksBinary(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if looksBinary {
			binaries = append(binaries, path)
		}
	}
	if len(binaries) != 0 {
		slices.Sort(binaries)
		t.Fatalf(
			"tracked files are binary, contrary to this repository's stray-artefact "+
				"policy (delete the artefact instead of hiding it):\n%s",
			strings.Join(binaries, "\n"),
		)
	}
}

// binaryProbeWindow matches git's own buffer_is_binary heuristic: a NUL byte
// within a file's first 8000 bytes marks it binary.
const binaryProbeWindow = 8000

// fileLooksBinary reports whether path starts with a NUL byte within
// binaryProbeWindow, git's own definition of a binary file.
func fileLooksBinary(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	buffer := make([]byte, binaryProbeWindow)
	read, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.IndexByte(buffer[:read], 0) != -1, nil
}

// TestFileLooksBinaryDistinguishesTextFromBinary proves fileLooksBinary can
// fail: an ordinary text file, including one with the non-ASCII bytes a
// README or CHANGELOG carries, is never flagged, while content containing a
// NUL byte - real compiled output included - always is.
func TestFileLooksBinaryDistinguishesTextFromBinary(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	write := func(t *testing.T, name string, content []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tests := []struct {
		name    string
		content []byte
		want    bool
	}{
		{name: "empty", content: nil, want: false},
		{name: "ascii text", content: []byte("package tmux\n\nfunc main() {}\n"), want: false},
		{name: "utf8 text", content: []byte("café — em dash — 日本語\n"), want: false},
		{name: "elf header", content: []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"), want: true},
		{name: "nul later in the file", content: append([]byte(strings.Repeat("x", 200)), 0), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := write(t, test.name, test.content)
			got, err := fileLooksBinary(path)
			if err != nil {
				t.Fatalf("fileLooksBinary(%s) error = %v", test.name, err)
			}
			if got != test.want {
				t.Fatalf("fileLooksBinary(%s) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}
