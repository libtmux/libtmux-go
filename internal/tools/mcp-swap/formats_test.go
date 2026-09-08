package main

import (
	"encoding/json"
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
LIBTMUX_TOOLSETS = "inspect"

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
	// The environment is where tool selection lives. A swap changes which
	// build answers, not how it is configured.
	if !strings.Contains(after, `LIBTMUX_TOOLSETS = "inspect"`) {
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

func TestATOMLSwapRecognizesACommentedTableHeader(t *testing.T) {
	t.Parallel()
	const config = `[mcp_servers.tmux] # selected for local development
command = "old"
enabled = true
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
	if count := strings.Count(after, "[mcp_servers.tmux]"); count != 1 {
		t.Fatalf("swap wrote %d tmux tables, want one:\n%s", count, after)
	}
	if !strings.Contains(after, "# selected for local development") {
		t.Fatalf("swap dropped the table-header comment:\n%s", after)
	}
}

func TestATOMLSwapRefusesAnUnknownMultilineValueWithoutWriting(t *testing.T) {
	t.Parallel()
	const config = `[mcp_servers.tmux]
command = "old"
headers = [
  "authorization",
  "traceparent",
]
`
	path := writeTemp(t, "config.toml", config)
	target := client{
		name: "codex", path: path, key: "mcp_servers",
		format: formatTOML, dialect: dialectStandard,
	}

	if err := useLocal([]client{target}, devEntry(), true); err == nil {
		t.Fatal("dry run accepted a multiline value it cannot preserve")
	}
	if err := writeEntry(target, devEntry()); err == nil {
		t.Fatal("swap accepted a multiline value it cannot preserve")
	}
	if after := readFile(t, path); after != config {
		t.Fatalf("refused swap changed the file:\n--- want ---\n%s\n--- got ---\n%s", config, after)
	}
}

func TestATOMLSwapReplacesMultilineArgumentsWithoutReadingTheirRowsAsKeys(t *testing.T) {
	t.Parallel()
	const config = `[mcp_servers.tmux]
command = "old"
args = [
  "--env",
  "KEY=value",
]
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
	if strings.Contains(after, `"KEY = value"`) || strings.Contains(after, `"KEY =`) {
		t.Fatalf("array element became a table key:\n%s", after)
	}
	if !strings.Contains(after, `args = ["-C", "/repo/golang/mcp", "run", "./cmd/libtmux-mcp"]`) {
		t.Fatalf("new arguments were not written:\n%s", after)
	}
}

func TestATOMLSwapRefusesAnUnknownChildTableWithoutWriting(t *testing.T) {
	t.Parallel()
	const config = `[mcp_servers.tmux]
command = "old"

[mcp_servers.tmux.headers]
authorization = "secret"
`
	path := writeTemp(t, "config.toml", config)
	target := client{
		name: "codex", path: path, key: "mcp_servers",
		format: formatTOML, dialect: dialectStandard,
	}

	if err := writeEntry(target, devEntry()); err == nil {
		t.Fatal("swap accepted a child table it cannot preserve")
	}
	if after := readFile(t, path); after != config {
		t.Fatalf("refused swap changed the file:\n--- want ---\n%s\n--- got ---\n%s", config, after)
	}
}

func TestTOMLStrictlyParsesTheWholeDocument(t *testing.T) {
	t.Parallel()
	target := client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard}
	for _, contents := range [][]byte{
		[]byte("broken = [\n\n[mcp_servers.tmux]\ncommand = \"old\"\n"),
		[]byte("value = 1\nvalue = 2\n\n[mcp_servers.tmux]\ncommand = \"old\"\n"),
		[]byte("[mcp_servers.tmux]\ncommand = \"old\"\n\n[mcp_servers.tmux.env]\nCOUNT = 3\n"),
	} {
		if _, _, err := entryFromContents(target, contents); err == nil {
			t.Fatalf("read accepted invalid TOML:\n%s", contents)
		}
		if _, err := renderEntryChange(target, contents, devEntry()); err == nil {
			t.Fatalf("render accepted invalid TOML:\n%s", contents)
		}
	}
}

func TestTOMLSwapPreservesTargetCommentsAndReadsMultilineArguments(t *testing.T) {
	t.Parallel()
	contents := []byte(`[mcp_servers.tmux] # local server
# command rationale
command = "old"
# arguments rationale
args = [
  "one",
  "two",
]

[mcp_servers.tmux.env] # authority
# keep this authority rationale
LIBTMUX_TOOLSETS = "inspect"
`)
	target := client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard}
	entry, present, err := entryFromContents(target, contents)
	if err != nil || !present {
		t.Fatalf("entry = (%v, %t, %v)", entry, present, err)
	}
	spec, err := processSpecFromEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(spec.args, ","); got != "one,two" {
		t.Fatalf("multiline args = %q, want one,two", got)
	}
	updated, err := renderEntryChange(target, contents, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{
		"# local server", "# command rationale", "# arguments rationale",
		"# authority", "# keep this authority rationale",
	} {
		if !strings.Contains(string(updated), comment) {
			t.Errorf("swap dropped %q:\n%s", comment, updated)
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

func TestJSONCTrailingCommaScanLeavesStringsAlone(t *testing.T) {
	t.Parallel()
	decoded, err := readJSONC([]byte(`{"object":"value,}","array":"value,]",}`))
	if err != nil {
		t.Fatal(err)
	}
	if decoded["object"] != "value,}" || decoded["array"] != "value,]" {
		t.Fatalf("decoded JSONC strings = %#v", decoded)
	}
}

func TestAJSONCDryRunRefusesMalformedConfiguration(t *testing.T) {
	t.Parallel()
	const config = `{"mcp":{"other":{"command":"keep"}}`
	path := writeTemp(t, "config.jsonc", config)
	target := client{
		name: "opencode", path: path, key: "mcp",
		format: formatJSONC, dialect: dialectOpencode,
	}

	if err := useLocal([]client{target}, devEntry(), true); err == nil {
		t.Fatal("dry run accepted malformed JSONC")
	}
	if err := writeEntry(target, devEntry()); err == nil {
		t.Fatal("swap accepted malformed JSONC")
	}
	if after := readFile(t, path); after != config {
		t.Fatalf("refused dry run changed the file: %s", after)
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
		{
			"json", "{\n    \"theme\": \"system\",\n    \"mcpServers\": {\n        \"tmux\": {\"command\": \"old\"}\n    }\n}\n",
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

func TestConfigFormatsRejectMalformedUTF8(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents []byte
		client   client
	}{
		{
			"json", []byte("{\"note\":\"\xc3(\",\"mcpServers\":{}}"),
			client{key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		},
		{
			"jsonc", []byte("{// keep \xc3(\n\"mcp\":{}}"),
			client{key: "mcp", format: formatJSONC, dialect: dialectOpencode},
		},
		{
			"toml", []byte("# keep \xc3(\n[mcp_servers]\n"),
			client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := entryFromContents(test.client, test.contents); err == nil {
				t.Fatal("read accepted malformed UTF-8")
			}
			if _, err := renderEntryChange(test.client, test.contents, devEntry()); err == nil {
				t.Fatal("update accepted malformed UTF-8")
			}
		})
	}
}

func TestJSONFormatsRejectDuplicateMembers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents []byte
		client   client
	}{
		{
			"json", []byte(`{"mcpServers": {}, "mcpServers": {}}`),
			client{key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		},
		{
			"jsonc", []byte("{\n  // first\n  \"mcp\": {},\n  \"mcp\": {},\n}\n"),
			client{key: "mcp", format: formatJSONC, dialect: dialectOpencode},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := entryFromContents(test.client, test.contents); err == nil ||
				!strings.Contains(err.Error(), "duplicate JSON member") {
				t.Fatalf("read error = %v, want duplicate-member refusal", err)
			}
			if _, err := renderEntryChange(test.client, test.contents, devEntry()); err == nil ||
				!strings.Contains(err.Error(), "duplicate JSON member") {
				t.Fatalf("render error = %v, want duplicate-member refusal", err)
			}
		})
	}
}

func TestJSONFormatsRejectNullRoot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		format configFormat
	}{
		{"json", formatJSON},
		{"jsonc", formatJSONC},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := client{
				name:    "test",
				format:  test.format,
				key:     "mcpServers",
				dialect: dialectStandard,
			}
			if _, _, err := entryFromContents(target, []byte("null\n")); err == nil {
				t.Fatal("read accepted a null config root")
			}
			if _, err := renderEntryChange(target, []byte("null\n"), devEntry()); err == nil {
				t.Fatal("render accepted a null config root")
			}
		})
	}
}

func TestProcessSpecsRejectNonStringArgumentsAndEnvironment(t *testing.T) {
	t.Parallel()
	for _, entry := range []map[string]any{
		{"command": "server", "args": []any{"okay", 7}},
		{"command": "server", "env": map[string]any{"TOKEN": 7}},
		{"command": "server", "args": "not-an-array"},
		{"command": "server", "env": []any{"not-an-object"}},
	} {
		if _, err := processSpecFromEntry(entry); err == nil {
			t.Fatalf("process spec accepted %+v", entry)
		}
	}
}

func TestRevertRefusesChangesOutsideTheServerEntry(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents string
		before   string
		after    string
		client   client
	}{
		{
			"toml", codexConfig, `model = "gpt-5"`, `model = "gpt-5.1"`,
			client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard},
		},
		{
			"jsonc", opencodeConfig, `"theme": "system"`, `"theme": "changed"`,
			client{key: "mcp", format: formatJSONC, dialect: dialectOpencode},
		},
		{
			"json", `{"theme":"system","mcpServers":{"tmux":{"command":"old"}}}`,
			`"theme": "system"`, `"theme": "changed"`,
			client{key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeTemp(t, "config."+test.name, test.contents)
			target := test.client
			target.name, target.path = test.name, path

			if err := writeEntry(target, devEntry()); err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(readFile(t, path), test.before, test.after, 1)
			if changed == readFile(t, path) {
				t.Fatalf("the fixture has no %q to edit", test.before)
			}
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := revert([]client{target}, false); err == nil {
				t.Fatal("revert accepted a configuration changed after the swap")
			}
			if after := readFile(t, path); after != changed {
				t.Fatalf("refused revert changed the configuration:\n%s", after)
			}
			assertRecoveryPairExists(t, target)
		})
	}
}

func TestRevertRemovesAnEntryThatDidNotExistBeforeTheSwap(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		contents string
		client   client
	}{
		{
			"toml", "model = \"gpt-5\"\n",
			client{key: "mcp_servers", format: formatTOML, dialect: dialectStandard},
		},
		{
			"jsonc", "{\n  // keep this\n  \"mcp\": {\"other\": {\"command\": \"keep\"}}\n}\n",
			client{key: "mcp", format: formatJSONC, dialect: dialectOpencode},
		},
		{
			"json", `{"theme":"system","mcpServers":{"other":{"command":"keep"}}}`,
			client{key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeTemp(t, "config."+test.name, test.contents)
			target := test.client
			target.name, target.path = test.name, path

			if err := writeEntry(target, devEntry()); err != nil {
				t.Fatal(err)
			}
			if err := revert([]client{target}, false); err != nil {
				t.Fatal(err)
			}
			if entry, present, err := entryOf(target); err != nil || present {
				t.Fatalf("entry after revert = (%v, %t, %v), want absent", entry, present, err)
			}
			after := readFile(t, path)
			if !strings.Contains(after, "keep") && !strings.Contains(after, `model = "gpt-5"`) {
				t.Fatalf("revert discarded the neighbouring configuration:\n%s", after)
			}
			if test.name == "toml" && after != test.contents {
				t.Fatalf("revert left the appended table's separator:\n%q", after)
			}
		})
	}
}

func TestRevertRefusesAnEntryWithoutItsSwapMarker(t *testing.T) {
	t.Parallel()
	const original = `{"mcpServers":{"tmux":{"command":"old"}}}`
	path := writeTemp(t, "config.json", original)
	target := client{
		name: "changed", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectStandard,
	}
	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(readFile(t, path),
		`"LIBTMUX_MCP_SWAP": "dev"`, `"OWNER": "manual"`, 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := revert([]client{target}, true); err == nil {
		t.Fatal("revert dry run accepted an entry without its swap marker")
	}
	if err := revert([]client{target}, false); err == nil {
		t.Fatal("revert accepted an entry that no longer carries its swap marker")
	}
	if after := readFile(t, path); after != changed {
		t.Fatalf("revert overwrote the changed entry:\n%s", after)
	}
	if _, err := os.Stat(backupPath(target)); err != nil {
		t.Fatalf("revert removed the backup after refusing the restore: %v", err)
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

func TestToolsetsExplicitlyMigratesTheRetiredSafetyEnvironment(t *testing.T) {
	t.Parallel()
	target := client{
		name: "json", key: "mcpServers", format: formatJSON,
		dialect: dialectStandard,
	}
	original := []byte(`{
  "mcpServers": {
    "tmux": {
      "command": "old",
      "env": {"KEEP": "yes", "LIBTMUX_SAFETY": "deny"}
    }
  }
}`)

	withoutToolsets, err := renderEntryChange(target, original, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	entry, present, err := entryFromContents(target, withoutToolsets)
	if err != nil || !present {
		t.Fatalf("read entry = (%v, %t, %v)", entry, present, err)
	}
	environment := entryEnvironment(entry)
	if environment["LIBTMUX_SAFETY"] != "deny" || environment["KEEP"] != "yes" {
		t.Fatalf("implicit environment = %v, want safety and unrelated values preserved", environment)
	}

	requested := devEntry()
	requested["env"].(map[string]any)["LIBTMUX_TOOLSETS"] = "inspect,execute"
	withToolsets, err := renderEntryChange(target, original, requested)
	if err != nil {
		t.Fatal(err)
	}
	entry, present, err = entryFromContents(target, withToolsets)
	if err != nil || !present {
		t.Fatalf("read migrated entry = (%v, %t, %v)", entry, present, err)
	}
	environment = entryEnvironment(entry)
	if _, found := environment["LIBTMUX_SAFETY"]; found {
		t.Fatalf("migrated environment retained LIBTMUX_SAFETY: %v", environment)
	}
	if environment["LIBTMUX_TOOLSETS"] != "inspect,execute" || environment["KEEP"] != "yes" {
		t.Fatalf("migrated environment = %v, want explicit toolsets and unrelated values", environment)
	}
}

func TestClaudeScopesAddressIndependentEntries(t *testing.T) {
	t.Parallel()
	repository := filepath.Join(t.TempDir(), "checkout")
	target := client{
		name: "claude", key: "mcpServers", format: formatJSON,
		dialect: dialectClaude, repository: repository,
	}
	original := []byte(`{
  "mcpServers": {"tmux": {"command": "user-old"}},
  "projects": {
    "` + filepath.ToSlash(repository) + `": {
      "mcpServers": {"tmux": {"command": "project-old"}},
      "keep": true
    },
    "/other": {"mcpServers": {"tmux": {"command": "other"}}}
  }
}`)

	project := target
	project.scope = scopeProject
	updated, err := renderEntryChange(project, original, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	projectEntry, present, err := entryFromContents(project, updated)
	if err != nil || !present || !isLocal(projectEntry) {
		t.Fatalf("project entry = (%v, %t, %v)", projectEntry, present, err)
	}
	if projectEntry["type"] != "stdio" {
		t.Fatalf("Claude project type = %v, want stdio", projectEntry["type"])
	}
	user := target
	user.scope = scopeUser
	userEntry, present, err := entryFromContents(user, updated)
	if err != nil || !present || describe(userEntry) != "user-old" {
		t.Fatalf("user entry = (%v, %t, %v), want untouched", userEntry, present, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatal(err)
	}
	projects := decoded["projects"].(map[string]any)
	if _, found := projects["/other"]; !found {
		t.Fatal("project-scoped edit removed another project")
	}
	selected := projects[filepath.ToSlash(repository)].(map[string]any)
	if selected["keep"] != true {
		t.Fatal("project-scoped edit removed a neighboring project setting")
	}
}

func TestClaudeProjectScopeRejectsAnUnsafeProjectsShape(t *testing.T) {
	t.Parallel()
	target := client{
		name: "claude", key: "mcpServers", format: formatJSON,
		dialect: dialectClaude, scope: scopeProject, repository: t.TempDir(),
	}
	_, err := renderEntryChange(target, []byte(`{"projects": []}`), devEntry())
	if err == nil || !strings.Contains(err.Error(), "projects") {
		t.Fatalf("render error = %v, want the invalid projects shape rejected", err)
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
