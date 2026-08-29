package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These files belong to whoever runs the tool, and hold far more than this
// server's entry. Every test here is one claim: the swap changed the entry and
// nothing else.

const codexConfig = `model = "gpt-5"
approval_policy = "on-request"

[projects."/home/user/project"]
trust_level = "trusted"

[mcp_servers.tmux]
command = "uv"
args = ["--directory", "/repo", "run", "libtmux-mcp"]
enabled = true

[mcp_servers.tmux.env]
LIBTMUX_SAFETY = "readonly"

[mcp_servers.other]
command = "other-server"
`

func TestATOMLSwapTouchesOnlyItsOwnTable(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "config.toml", codexConfig)
	client := client{
		name: "codex", path: path, key: "mcp_servers",
		format: formatTOML, dialect: dialectStandard,
	}

	if err := writeEntry(client, devEntry()); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)

	// The neighbours.
	for _, kept := range []string{
		`model = "gpt-5"`,
		`approval_policy = "on-request"`,
		`[projects."/home/user/project"]`,
		`[mcp_servers.other]`,
		`command = "other-server"`,
	} {
		if !strings.Contains(after, kept) {
			t.Errorf("the swap removed %q", kept)
		}
	}
	// A key this tool does not write configures the client's relationship with
	// the server, not which build it is. Dropping it silently disables a
	// server somebody meant to keep.
	if !strings.Contains(after, "enabled = true") {
		t.Error("the swap dropped enabled = true")
	}
	// The environment is where LIBTMUX_SAFETY lives. A swap changes which
	// build answers, not how it is configured.
	if !strings.Contains(after, `LIBTMUX_SAFETY = "readonly"`) {
		t.Error("the swap dropped the existing environment")
	}
	if !strings.Contains(after, `LIBTMUX_MCP_SWAP = "dev"`) {
		t.Error("the swap wrote no marker, so revert cannot recognise it")
	}
	// The new command ends in ./cmd/libtmux-mcp, so the old one is recognised
	// by what only it had.
	if strings.Contains(after, "--directory") || strings.Contains(after, `"uv"`) {
		t.Errorf("the old command survived the swap:\n%s", after)
	}
}

func TestATOMLSwapAddsATableWhenThereIsNone(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "config.toml", "model = \"gpt-5\"\n")
	client := client{
		name: "codex", path: path, key: "mcp_servers",
		format: formatTOML, dialect: dialectStandard,
	}

	if err := writeEntry(client, devEntry()); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	if !strings.Contains(after, "[mcp_servers.tmux]") {
		t.Errorf("no table was added:\n%s", after)
	}
	if !strings.Contains(after, `model = "gpt-5"`) {
		t.Error("the existing configuration was lost")
	}
}

func TestATOMLSwapPreservesEnvironmentValueSyntax(t *testing.T) {
	t.Parallel()
	const config = `[mcp_servers.tmux]
command = "old"

[mcp_servers.tmux.env]
BASIC = "tab\\tquote\\\"slash\\\\"
LITERAL = 'C:\Users\name'
COMMENTED = "readonly" # why this is restricted
`
	path := writeTemp(t, "config.toml", config)
	target := client{
		name: "codex", path: path, key: "mcp_servers",
		format: formatTOML, dialect: dialectStandard,
	}

	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	for _, line := range []string{
		`BASIC = "tab\\tquote\\\"slash\\\\"`,
		`LITERAL = 'C:\Users\name'`,
		`COMMENTED = "readonly" # why this is restricted`,
	} {
		if !strings.Contains(after, line) {
			t.Errorf("the swap changed %q:\n%s", line, after)
		}
	}
}

const opencodeConfig = `{
  // Why this file looks the way it does.
  "$schema": "https://opencode.ai/config.json",
  "theme": "system",
  "mcp": {
    "tmux": {
      "type": "local",
      // A comment about the old entry.
      "command": ["uvx", "libtmux-mcp==0.1.0"]
    },
    "other": { "type": "local", "command": ["other-server"] }
  }
}
`

func TestAJSONCSwapKeepsTheCommentsAroundIt(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "opencode.jsonc", opencodeConfig)
	client := client{
		name: "opencode", path: path, key: "mcp",
		format: formatJSONC, dialect: dialectOpencode,
	}

	if err := writeEntry(client, devEntry()); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)

	if !strings.Contains(after, "// Why this file looks the way it does.") {
		t.Error("a comment outside the entry was lost")
	}
	if !strings.Contains(after, `"theme": "system"`) {
		t.Error("a neighbouring setting was lost")
	}
	if !strings.Contains(after, `"other"`) {
		t.Error("a neighbouring server was lost")
	}
	// opencode reads one array for argv and calls the environment
	// "environment"; an "env" key here is dropped in silence and a scalar
	// command is a decode error that takes the whole config down.
	if !strings.Contains(after, `"go"`) || strings.Contains(after, `"command": "go"`) {
		t.Errorf("the command is not opencode's array shape:\n%s", after)
	}
	if strings.Contains(after, `"env"`) && !strings.Contains(after, `"environment"`) {
		t.Error("the environment was written under the wrong key")
	}
}

func TestAJSONCSwapAddsAfterATrailingComma(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "opencode.jsonc", `{
  "mcp": {
    "other": { "type": "local", "command": ["other-server"] },
  },
}
`)
	target := client{
		name: "opencode", path: path, key: "mcp",
		format: formatJSONC, dialect: dialectOpencode,
	}

	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONC([]byte(readFile(t, path))); err != nil {
		t.Fatalf("the inserted entry left invalid JSONC: %v\n%s", err, readFile(t, path))
	}
}

func TestBlankingCommentsKeepsEveryOffset(t *testing.T) {
	t.Parallel()
	// Offsets have to line up, because a span found in the blanked text is
	// spliced into the original.
	source := []byte(`{"a": 1, // note
	"b": "// not a comment", /* block */ "c": 2}`)
	blanked := blankComments(source)

	if len(blanked) != len(source) {
		t.Fatalf("blanking changed the length: %d became %d", len(source), len(blanked))
	}
	if strings.Contains(string(blanked), "note") || strings.Contains(string(blanked), "block") {
		t.Errorf("a comment survived blanking: %s", blanked)
	}
	if !strings.Contains(string(blanked), "// not a comment") {
		t.Errorf("a comment inside a string was blanked: %s", blanked)
	}
}

// A swap and a revert have to leave the file exactly as it was, byte for byte,
// or the tool is lossy in a way nobody notices until they read the diff.
func TestSwapThenRevertIsByteIdentical(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents string
		client   client
	}{
		{"toml", codexConfig, client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard}},
		{"jsonc", opencodeConfig, client{key: "mcp", format: formatJSONC, dialect: dialectOpencode}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeTemp(t, "config."+test.name, test.contents)
			c := test.client
			c.name, c.path = test.name, path

			if err := writeEntry(c, devEntry()); err != nil {
				t.Fatal(err)
			}
			if err := revert([]client{c}, false); err != nil {
				t.Fatal(err)
			}
			if after := readFile(t, path); after != test.contents {
				t.Errorf("revert did not restore the file:\n--- want ---\n%s\n--- got ---\n%s",
					test.contents, after)
			}
		})
	}
}

// Reading is separate from writing, and status depends on it alone.
func TestEveryFormatReadsBackWhatItWrote(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents string
		client   client
	}{
		{"toml", codexConfig, client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard}},
		{"jsonc", opencodeConfig, client{key: "mcp", format: formatJSONC, dialect: dialectOpencode}},
		{
			"json", `{"mcpServers":{"tmux":{"command":"uv","args":["run"]}}}`,
			client{key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeTemp(t, "config."+test.name, test.contents)
			c := test.client
			c.name, c.path = test.name, path

			if err := writeEntry(c, devEntry()); err != nil {
				t.Fatal(err)
			}
			entry, present, err := entryOf(c)
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				t.Fatal("the entry just written is not there")
			}
			if command, _ := entry["command"].(string); command != "go" {
				t.Errorf("read back command %q, want go", command)
			}
			if !isLocal(entry) {
				t.Error("the marker did not survive the round trip, so revert cannot see it")
			}
		})
	}
}

// devEntry is what a dev-mode swap writes.
func devEntry() map[string]any {
	return map[string]any{
		"command": "go",
		"args":    []any{"-C", "/repo/golang/mcp", "run", "./cmd/libtmux-mcp"},
		"env":     map[string]any{"LIBTMUX_MCP_SWAP": "dev"},
	}
}

func writeTemp(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
