package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestQuickstartMatchesDisplayedSource(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(readme), "<!-- docs:quickstart -->")
	if !ok {
		t.Fatal("README has no quickstart marker")
	}
	_, block, ok = strings.Cut(block, "```go\n")
	if !ok {
		t.Fatal("README quickstart has no Go fence")
	}
	displayed, _, ok := strings.Cut(block, "\n```")
	if !ok {
		t.Fatal("README quickstart has no closing fence")
	}
	program, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := slices.DeleteFunc(strings.Split(string(program), "\n"), func(line string) bool {
		return strings.HasPrefix(strings.TrimSpace(line), "// docs:")
	})
	if strings.TrimSpace(displayed) != strings.TrimSpace(strings.Join(lines, "\n")) {
		t.Fatal("README quickstart differs from the complete ordinary program")
	}
}
