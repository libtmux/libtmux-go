package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsAtOrUnderResolvesDirectoryAliases(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "workspace")
	child := filepath.Join(realRoot, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	if !isAtOrUnder(child, alias) {
		t.Fatalf("%q was not matched under its alias %q", child, alias)
	}
	sibling := filepath.Join(filepath.Dir(realRoot), "workspace-sibling")
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if isAtOrUnder(sibling, alias) {
		t.Fatalf("sibling %q matched under alias %q", sibling, alias)
	}
}
