package mcp_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
)

func TestAdvertisedToolsHasNoTmuxOrRuntimeSideEffects(t *testing.T) {
	t.Setenv("PATH", "")
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(tmuxmcp.AuditEnvironmentVariable, auditPath)

	tools, err := tmuxmcp.AdvertisedTools(context.Background())
	if err != nil {
		t.Fatalf("AdvertisedTools() error = %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("AdvertisedTools() returned no tools")
	}
	if _, err := os.Stat(auditPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("AdvertisedTools() audit file error = %v, want no file", err)
	}
}
