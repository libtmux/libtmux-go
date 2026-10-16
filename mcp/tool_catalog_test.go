package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

var toolPageLink = regexp.MustCompile(
	`https://libtmux\.org/en/go/latest/mcp/tools/([a-z0-9_]+)/`)

// TestREADMEToolLinksNameAdvertisedTools holds every tool page the README
// links to a tool the server advertises, and the README to linking every
// tool. A renamed tool leaves its old link rendering as a link, and only
// following it shows the page is gone.
func TestREADMEToolLinksNameAdvertisedTools(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}
	tools, err := tmuxmcp.AdvertisedTools(context.Background())
	if err != nil {
		t.Fatalf("AdvertisedTools() error = %v", err)
	}
	advertised := map[string]bool{}
	for _, tool := range tools {
		advertised[tool.Name] = true
	}
	linked := map[string]bool{}
	for _, match := range toolPageLink.FindAllStringSubmatch(string(readme), -1) {
		linked[match[1]] = true
		if !advertised[match[1]] {
			t.Errorf("the README links to tools/%s/, which names no advertised tool", match[1])
		}
	}
	for name := range advertised {
		if !linked[name] {
			t.Errorf("the README links to no page for %s; run go generate ./...", name)
		}
	}
}

// select_layout's manifest schema and tool description must say it
// accepts a saved layout string, matching layout_tools.go's parameter struct.
func TestSelectLayoutAdvertisesSavedLayoutSupport(t *testing.T) {
	tools, err := tmuxmcp.AdvertisedTools(context.Background())
	if err != nil {
		t.Fatalf("AdvertisedTools() error = %v", err)
	}

	var selectLayout *sdkTool
	for _, tool := range tools {
		if tool.Name == "select_layout" {
			selectLayout = &sdkTool{Description: tool.Description, InputSchema: tool.InputSchema}
			break
		}
	}
	if selectLayout == nil {
		t.Fatal(`AdvertisedTools() has no "select_layout" tool`)
	}
	if !strings.Contains(selectLayout.Description, "get_window_info") {
		t.Fatalf(
			"select_layout description = %q, want it to mention get_window_info",
			selectLayout.Description,
		)
	}

	encoded, err := json.Marshal(selectLayout.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Layout struct {
				Description string `json:"description"`
			} `json:"layout"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema.Properties.Layout.Description, "get_window_info") {
		t.Fatalf(
			"select_layout layout schema description = %q, want it to mention get_window_info",
			schema.Properties.Layout.Description,
		)
	}
}

// sdkTool holds only the fields this test reads, so a change to the SDK's
// own Tool shape cannot make an unrelated field the reason this fails.
type sdkTool struct {
	Description string
	InputSchema any
}
