package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUsePreparedLocalPreflightsDistinctFinalEnvironments(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	marker := filepath.Join(t.TempDir(), "second-preflight")
	first := jsonPreflightClient(t, "first", `{
  "mcpServers": {
    "tmux": {
      "command": "old-server",
      "env": {"MCP_SWAP_PREFLIGHT_HELPER": "wrong-name"}
    }
  }
}
`)
	second := jsonPreflightClient(t, "second", `{
  "mcpServers": {
    "tmux": {
      "command": "old-server",
      "env": {
        "MCP_SWAP_PREFLIGHT_HELPER": "handshake",
        "MCP_SWAP_PREFLIGHT_MARKER": "`+filepath.ToSlash(marker)+`"
      }
    }
  }
}
`)
	firstOriginal := readFile(t, first.path)
	secondOriginal := readFile(t, second.path)

	err := usePreparedLocal(
		[]client{first, second}, preflightTestPlan(), false, true,
	)
	if err == nil || !strings.Contains(err.Error(), "first preflight failed") {
		t.Fatalf("preflight error = %v, want the first environment to fail", err)
	}
	if got := readFile(t, marker); got != "initialized\nping\n" {
		t.Fatalf("second final environment was not independently checked: %q", got)
	}
	assertConfigWasNotWritten(t, first, firstOriginal)
	assertConfigWasNotWritten(t, second, secondOriginal)
}

func TestDryRunDoesNotStartAConfiguredServer(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	directory := t.TempDir()
	marker := filepath.Join(directory, "preflight-marker")
	path := filepath.Join(directory, "opencode.jsonc")
	original := `{
  "mcp": {
    "tmux": {
      "type": "local",
      "command": ["old-server", "old-argument"],
      "environment": {
        "MCP_SWAP_PREFLIGHT_HELPER": "handshake",
        "MCP_SWAP_PREFLIGHT_MARKER": "` + filepath.ToSlash(marker) + `"
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	target := client{
		name: "opencode", path: path, key: "mcp",
		format: formatJSONC, dialect: dialectOpencode,
	}

	if err := usePreparedLocal(
		[]client{target}, preflightTestPlan(), true, true,
	); err != nil {
		t.Fatal(err)
	}
	assertConfigWasNotWritten(t, target, original)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run started the configured server: %v", err)
	}
}

func TestUsePreparedLocalPreflightsWithoutAClientConfig(t *testing.T) {
	plan := preflightTestPlan()
	plan.configured["env"] = map[string]any{
		"LIBTMUX_MCP_SWAP":         "installed",
		preflightHelperEnvironment: "wrong-name",
	}
	missing := client{
		name: "missing", path: filepath.Join(t.TempDir(), "missing.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}

	err := usePreparedLocal([]client{missing}, plan, false, true)
	if err == nil || !strings.Contains(err.Error(), "preflight failed, nothing written") {
		t.Fatalf("preflight error = %v, want the configured build rejected", err)
	}
}

func TestDistinctProcessSpecsDeduplicateOnlyExactFinalProcesses(t *testing.T) {
	t.Parallel()
	shared := processSpec{
		command: "persistent", args: []string{"serve"}, env: map[string]string{"A": "1"},
	}
	changes := []entryChange{
		{target: client{name: "claude"}, spec: shared},
		{target: client{name: "cursor"}, spec: shared},
		{target: client{name: "codex"}, spec: processSpec{
			command: "persistent", args: []string{"serve"}, env: map[string]string{"A": "2"},
		}},
	}

	got := distinctProcessSpecs(changes, "temporary")
	if len(got) != 2 {
		t.Fatalf("distinct specs = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0].clients, []string{"claude", "cursor"}) {
		t.Errorf("deduplicated clients = %v", got[0].clients)
	}
	if got[0].spec.command != "temporary" ||
		!reflect.DeepEqual(got[0].spec.args, shared.args) ||
		!reflect.DeepEqual(got[0].spec.env, shared.env) {
		t.Errorf("build preflight changed more than the command: %+v", got[0].spec)
	}
}

func TestEntryChangeRejectsAConfigChangedAfterPlanning(t *testing.T) {
	t.Parallel()
	target := jsonPreflightClient(t, "racing", `{"mcpServers":{}}`)
	change, err := planEntryChange(target, map[string]any{
		"command": "replacement", "args": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	newer := `{"mcpServers":{"other":{"command":"newer"}}}`
	if err := os.WriteFile(target.path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := change.apply(); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("apply error = %v, want a stale-plan refusal", err)
	}
	assertConfigWasNotWritten(t, target, newer)
}

func TestProvisioningChangesAreIncludedInTheInitialPlan(t *testing.T) {
	first := jsonPreflightClient(t, "first", `{"mcpServers":{}}`)
	second := jsonPreflightClient(t, "second", `{"mcpServers":{}}`)
	firstOriginal := readFile(t, first.path)
	const newer = `{"mcpServers":{"other":{"command":"newer"}}}`
	plan := entryPlan{
		configured: devEntry(),
		install: func() error {
			return os.WriteFile(second.path, []byte(newer), 0o600)
		},
		cleanup: func() {},
	}

	if err := usePreparedLocal([]client{first, second}, plan, false, false); err != nil {
		t.Fatal(err)
	}
	if err := revert([]client{first, second}, false); err != nil {
		t.Fatal(err)
	}
	assertConfigWasNotWritten(t, first, firstOriginal)
	assertConfigWasNotWritten(t, second, newer)
}

func TestUseReplansTheFinalProcessSpecAfterPreflight(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv(preflightHelperEnvironment, "mutate-config")
	target := jsonPreflightClient(t, "racing", `{"mcpServers":{}}`)
	t.Setenv("MCP_SWAP_PREFLIGHT_CONFIG", target.path)
	plan := preflightTestPlan()

	err := usePreparedLocal([]client{target}, plan, false, true)
	if err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("use error = %v, want final-spec replan refusal", err)
	}
	if got := readFile(t, target.path); !strings.Contains(got, "MCP_SWAP_CHANGED") {
		t.Fatalf("tool overwrote the concurrent config: %s", got)
	}
	assertPathMissing(t, backupPath(target))
	assertPathMissing(t, recoveryStatePath(target))
}

func TestUseProvisionsBeforePreflight(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	marker := filepath.Join(t.TempDir(), "provisioned")
	t.Setenv(preflightHelperEnvironment, "handshake")
	target := jsonPreflightClient(t, "provisioned", `{"mcpServers":{}}`)
	plan := preflightTestPlan()
	plan.install = func() error { return os.WriteFile(marker, []byte("ready"), 0o600) }
	plan.configured["env"].(map[string]any)["MCP_SWAP_PREFLIGHT_MARKER"] = marker + ".handshake"
	plan.configured["env"].(map[string]any)["MCP_SWAP_REQUIRE_FILE"] = marker

	// The helper's environment-mode branch checks inherited and configured
	// variables; this wrapper fails at launch if provisioning did not run first.
	plan.configured["command"] = "sh"
	plan.configured["args"] = []any{
		"-c", `test -f "$MCP_SWAP_REQUIRE_FILE" && exec "$MCP_SWAP_TEST_BINARY"`,
	}
	plan.configured["env"].(map[string]any)["MCP_SWAP_TEST_BINARY"] = os.Args[0]
	if err := usePreparedLocal([]client{target}, plan, false, true); err != nil {
		t.Fatal(err)
	}
}

func preflightTestPlan() entryPlan {
	return entryPlan{
		configured: map[string]any{
			"command": os.Args[0],
			"args":    []any{},
			"env":     map[string]any{"LIBTMUX_MCP_SWAP": "installed"},
		},
		install: func() error { return nil },
		cleanup: func() {},
	}
}

func jsonPreflightClient(t *testing.T, name, contents string) client {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return client{
		name: name, path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectStandard,
	}
}

func assertConfigWasNotWritten(t *testing.T, target client, original string) {
	t.Helper()
	if got := readFile(t, target.path); got != original {
		t.Fatalf("config changed: got %q, want %q", got, original)
	}
	if _, err := os.Stat(backupPath(target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup exists after rejected write: %v", err)
	}
	if _, err := os.Stat(recoveryStatePath(target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery state exists after rejected write: %v", err)
	}
}
