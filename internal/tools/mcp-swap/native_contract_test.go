package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeClaudeLayersCoexistAndUnscopedRevertIsLIFO(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "checkout")
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	path := filepath.Join(t.TempDir(), ".claude.json")
	original := []byte(`{
  "keep": true,
  "mcpServers": {"tmux": {"command": "user-old"}},
  "projects": {}
}
`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	claude := client{
		name: "claude", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectClaude,
	}
	plan := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	if err := nativeUse([]client{claude}, []client{claude}, plan, options{
		scope: scopeUser, noPreflight: true,
	}, repository); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{claude}, []client{claude}, plan, options{
		scope: scopeProject, noPreflight: true,
	}, repository); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []configScope{scopeUser, scopeProject} {
		target := claude
		target.scope = scope
		target.repository = repository
		entry, present, err := entryOf(target)
		if err != nil || !present || !isLocal(entry) {
			t.Fatalf("%s entry = (%v, %t, %v)", scope, entry, present, err)
		}
	}
	ledger, _, err := loadNativeLedger()
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Entries) != 2 || ledger.NextSequence != 2 {
		t.Fatalf("ledger = %+v, want two ordered layers", ledger)
	}
	for _, entry := range ledger.Entries {
		if !strings.Contains(filepath.Base(entry.BackupPath), ".bak.mcp-swap-go-") {
			t.Fatalf("backup %q has no Go marker", entry.BackupPath)
		}
	}

	if err := nativeRevert([]client{claude}, []client{claude}, options{}, repository); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, path)); !bytes.Equal(got, original) {
		t.Fatalf("unscoped revert restored %s, want exact original", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = (%v, %v), want 0640", info, err)
	}
	if _, err := os.Stat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revert retained native state: %v", err)
	}
	for _, entry := range ledger.Entries {
		if _, err := os.Stat(entry.BackupPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("revert retained backup %q: %v", entry.BackupPath, err)
		}
	}
}

func TestNativeScopedRevertCannotSkipANewerClaudeLayer(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "checkout")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	path := filepath.Join(t.TempDir(), ".claude.json")
	original := []byte(`{"mcpServers":{},"projects":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	claude := client{
		name: "claude", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectClaude,
	}
	plan := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	for _, scope := range []configScope{scopeUser, scopeProject} {
		if err := nativeUse([]client{claude}, []client{claude}, plan, options{
			scope: scope, noPreflight: true,
		}, repository); err != nil {
			t.Fatal(err)
		}
	}
	before := readFile(t, path)
	err := nativeRevert([]client{claude}, []client{claude}, options{scope: scopeUser}, repository)
	if err == nil || !strings.Contains(err.Error(), "newer layer") {
		t.Fatalf("scoped revert error = %v, want strict LIFO refusal", err)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("refused scoped revert changed config to %s", got)
	}
	if err := nativeRevert([]client{claude}, []client{claude},
		options{scope: scopeProject}, repository); err != nil {
		t.Fatal(err)
	}
	user := claude
	user.scope = scopeUser
	user.repository = repository
	if entry, present, err := entryOf(user); err != nil || !present || !isLocal(entry) {
		t.Fatalf("older user layer after project revert = (%v, %t, %v)", entry, present, err)
	}
	if err := nativeRevert([]client{claude}, []client{claude},
		options{scope: scopeUser}, repository); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, path)); !bytes.Equal(got, original) {
		t.Fatalf("ordered scoped reverts restored %s, want %s", got, original)
	}
}

func TestNativeAllEightClientsSwapAndRevertExactly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	repository := filepath.Join(t.TempDir(), "checkout")
	clients := knownClients(home)
	originals := make(map[string][]byte, len(clients))
	for _, target := range clients {
		if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
			t.Fatal(err)
		}
		var contents []byte
		if target.format == formatTOML {
			contents = []byte("[" + target.key + ".keep]\ncommand = \"keep\"\n")
		} else {
			contents = []byte("{\n  \"" + target.key + "\": {\"keep\": {\"command\": \"keep\"}}\n}\n")
		}
		if err := os.WriteFile(target.path, contents, 0o640); err != nil {
			t.Fatal(err)
		}
		originals[target.name] = contents
	}
	plan := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	if err := nativeUse(clients, clients, plan, options{noPreflight: true}, repository); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := loadNativeLedger()
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Entries) != 8 {
		t.Fatalf("ledger has %d entries, want 8", len(ledger.Entries))
	}
	for _, target := range clients {
		target.scope = scopeUser
		if target.name == "claude" {
			target.scope = scopeProject
			target.repository = repository
		}
		entry, present, err := entryOf(target)
		if err != nil || !present || !isLocal(entry) {
			t.Fatalf("%s entry = (%v, %t, %v)", target.name, entry, present, err)
		}
	}
	if err := nativeRevert(clients, clients, options{}, repository); err != nil {
		t.Fatal(err)
	}
	for _, target := range clients {
		if got := []byte(readFile(t, target.path)); !bytes.Equal(got, originals[target.name]) {
			t.Errorf("%s did not restore exact bytes", target.name)
		}
		info, err := os.Stat(target.path)
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("%s restored mode = (%v, %v), want 0640", target.name, info, err)
		}
	}
}

func TestNativeReswapOfOlderClaudeLayerKeepsTheRecoveryChain(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "checkout")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	path := filepath.Join(t.TempDir(), ".claude.json")
	original := []byte(`{"mcpServers":{"tmux":{"command":"original"}},"projects":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	claude := client{
		name: "claude", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectClaude,
	}
	first := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	if err := nativeUse([]client{claude}, []client{claude}, first,
		options{scope: scopeUser, noPreflight: true}, repository); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{claude}, []client{claude}, first,
		options{scope: scopeProject, noPreflight: true}, repository); err != nil {
		t.Fatal(err)
	}
	secondEntry := devEntry()
	secondEntry["args"] = []any{"-C", "/new/repo/mcp", "run", "./cmd/libtmux-mcp"}
	second := entryPlan{configured: secondEntry, install: func() error { return nil }, cleanup: func() {}}
	if err := nativeUse([]client{claude}, []client{claude}, second,
		options{scope: scopeUser, noPreflight: true}, repository); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := loadNativeLedger()
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Entries) != 2 || ledger.NextSequence != 2 {
		t.Fatalf("reswap ledger = %+v, want the same two first backups", ledger)
	}
	if err := nativeRevert([]client{claude}, []client{claude}, options{}, repository); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, path)); !bytes.Equal(got, original) {
		t.Fatalf("reswap chain restored %s, want exact original", got)
	}
}

func TestNativeDryRunDoesNotProvisionPreflightOrCreateState(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "absent-state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	repository := filepath.Join(t.TempDir(), "checkout")
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"mcpServers":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "provisioned")
	target := client{name: "codex", path: path, key: "mcpServers", format: formatJSON}
	plan := entryPlan{
		configured: devEntry(),
		install:    func() error { return os.WriteFile(marker, []byte("ran"), 0o600) },
		cleanup:    func() {},
	}
	if err := nativeUse([]client{target}, []client{target}, plan,
		options{dryRun: true}, repository); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, path)); !bytes.Equal(got, original) {
		t.Fatalf("dry run changed config to %s", got)
	}
	for _, absent := range []string{marker, stateRoot} {
		if _, err := os.Lstat(absent); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry run created %s: %v", absent, err)
		}
	}
}

func TestNativeUsePlansEveryConfigBeforeProvisioning(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	first := jsonPreflightClient(t, "first", `{"mcpServers":{}}`)
	second := jsonPreflightClient(t, "second", `{`)
	marker := filepath.Join(t.TempDir(), "provisioned")
	plan := entryPlan{
		configured: devEntry(),
		install:    func() error { return os.WriteFile(marker, []byte("ran"), 0o600) },
		cleanup:    func() {},
	}

	if err := nativeUse([]client{first, second}, []client{first, second}, plan,
		options{noPreflight: true}, t.TempDir()); err == nil {
		t.Fatal("use accepted a malformed later configuration")
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("use provisioned before planning every configuration: %v", err)
	}
}

func TestNativeSwapRejectsAnAliasInAnUnselectedKnownConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	directory := t.TempDir()
	selectedPath := filepath.Join(directory, "selected.json")
	unselectedPath := filepath.Join(directory, "unselected.json")
	original := []byte(`{"mcpServers":{}}`)
	if err := os.WriteFile(selectedPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(selectedPath, unselectedPath); err != nil {
		t.Fatal(err)
	}
	selectedClient := client{
		name: "codex", path: selectedPath, key: "mcpServers", format: formatJSON,
	}
	unselectedClient := client{
		name: "cursor", path: unselectedPath, key: "mcpServers", format: formatJSON,
	}
	plan := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	err := nativeUse(
		[]client{selectedClient, unselectedClient}, []client{selectedClient}, plan,
		options{noPreflight: true}, t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "same physical path") {
		t.Fatalf("alias error = %v, want physical-path refusal", err)
	}
	if got := []byte(readFile(t, selectedPath)); !bytes.Equal(got, original) {
		t.Fatalf("refused alias swap changed config to %s", got)
	}
	if _, err := os.Lstat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused alias swap created state: %v", err)
	}
}

func TestNativeLateConfigChangeRollsBackEveryEarlierPublication(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	directory := t.TempDir()
	clients := []client{
		{name: "codex", path: filepath.Join(directory, "first.json"), key: "mcpServers", format: formatJSON},
		{name: "cursor", path: filepath.Join(directory, "second.json"), key: "mcpServers", format: formatJSON},
	}
	original := []byte(`{"mcpServers":{}}`)
	for _, target := range clients {
		if err := os.WriteFile(target.path, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := scopedClients(clients, scopeProject, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := nativeLedger{Entries: map[string]nativeLedgerEntry{}}
	plans, err := planNativeUse(targets, devEntry(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireTransactionLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.close() }()
	if err := ensureNativeStateDirectory(); err != nil {
		t.Fatal(err)
	}
	operations, err := stageNativeUse(lock, plans, ledger, nil)
	if err != nil {
		t.Fatal(err)
	}
	human := []byte(`{"mcpServers":{"human":{"command":"keep"}}}`)
	replaceContentsAtSamePath(t, clients[1].path, human)
	if err := runNativeOperations(lock, operations); err == nil {
		t.Fatal("native transaction accepted a late second-config replacement")
	}
	if got := []byte(readFile(t, clients[0].path)); !bytes.Equal(got, original) {
		t.Fatalf("rollback left first config as %s", got)
	}
	if got := []byte(readFile(t, clients[1].path)); !bytes.Equal(got, human) {
		t.Fatalf("rollback overwrote the human replacement with %s", got)
	}
	for _, plan := range plans {
		if _, err := os.Lstat(plan.BackupPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("rollback retained backup %s: %v", plan.BackupPath, err)
		}
	}
	if _, err := os.Lstat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback retained recovery state: %v", err)
	}
}

func TestNativeUseReplansTheExactFinalProcessAfterPreflight(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv(preflightHelperEnvironment, "mutate-config")
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"mcpServers":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_SWAP_PREFLIGHT_CONFIG", path)
	target := client{name: "codex", path: path, key: "mcpServers", format: formatJSON}
	plan := preflightTestPlan()
	err := nativeUse([]client{target}, []client{target}, plan, options{}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("native use error = %v, want final-spec replan refusal", err)
	}
	if got := readFile(t, path); !strings.Contains(got, "MCP_SWAP_CHANGED") {
		t.Fatalf("refused native use overwrote the concurrent config: %s", got)
	}
	if _, err := os.Lstat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused native use created recovery state: %v", err)
	}
}

func TestNativeStateRejectsAnotherImplementation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	path := nativeStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"next_sequence": 0, "entries": map[string]any{}}
	contents, err := json.Marshal(map[string]any{
		"version": 1, "implementation": "python", "checksum": strings.Repeat("0", 64),
		"payload": payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadNativeLedger(); err == nil || !strings.Contains(err.Error(), "another implementation") {
		t.Fatalf("ledger error = %v, want strict implementation refusal", err)
	}
}

func TestNativeStateIsStrictAboutSchemaChecksumAndPaths(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := client{name: "codex", path: config, key: "mcpServers", format: formatJSON}
	plan := entryPlan{configured: devEntry(), install: func() error { return nil }, cleanup: func() {}}
	if err := nativeUse([]client{target}, []client{target}, plan,
		options{noPreflight: true}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(nativeStatePath())
	if err != nil {
		t.Fatal(err)
	}

	withUnknown := bytes.Replace(contents, []byte(`"version": 1,`),
		[]byte(`"version": 1, "future": true,`), 1)
	if _, err := decodeNativeLedger(withUnknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	withDuplicate := bytes.Replace(contents, []byte(`"version": 1,`),
		[]byte(`"version": 1, "version": 1,`), 1)
	if _, err := decodeNativeLedger(withDuplicate); err == nil || !strings.Contains(err.Error(), "duplicate JSON member") {
		t.Fatalf("duplicate-field error = %v", err)
	}
	withBadChecksum := bytes.Replace(contents, []byte(`"checksum": "`),
		[]byte(`"checksum": "0`), 1)
	if _, err := decodeNativeLedger(withBadChecksum); err == nil {
		t.Fatal("state accepted a changed checksum")
	}

	var envelope nativeLedgerEnvelope
	if err := json.Unmarshal(contents, &envelope); err != nil {
		t.Fatal(err)
	}
	entry := envelope.Payload.Entries["codex:user"]
	entry.BackupPath = filepath.Join(t.TempDir(), "not-the-derived-backup")
	envelope.Payload.Entries["codex:user"] = entry
	payload, err := json.Marshal(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Checksum = digest(payload)
	unsafeState, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeNativeLedger(unsafeState); err == nil || !strings.Contains(err.Error(), "invalid paths") {
		t.Fatalf("unsafe-path error = %v", err)
	}
	if err := os.Chmod(nativeStateDirectory(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadNativeLedger(); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("public-state-directory error = %v", err)
	}
}

func TestNativeStateDoesNotGuessPythonLegacyState(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	legacy := filepath.Join(stateRoot, "libtmux-mcp-dev", "swap", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"claude:user":{"backup_path":"somewhere"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, state, err := loadNativeLedger()
	if err != nil {
		t.Fatal(err)
	}
	if state != nil || len(ledger.Entries) != 0 {
		t.Fatalf("Go guessed legacy state: (%+v, %+v)", ledger, state)
	}
}

func TestNativeStatusShowsBothClaudeLayersWithoutLeakingEnvironmentValues(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "checkout")
	path := filepath.Join(t.TempDir(), ".claude.json")
	contents := fmt.Sprintf(`{
  "mcpServers": {"tmux": {"command": "user-server", "env": {"TOKEN": "do-not-print"}}},
  "projects": {%q: {"mcpServers": {"tmux": {"command": "project-server"}}}}
}
`, repository)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	target := client{
		name: "claude", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectClaude,
	}
	var output bytes.Buffer
	if err := nativeStatusTo(&output, []client{target}, "", repository); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, wanted := range []string{"[claude:user]", "user-server", "[claude:project]", "project-server"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("status = %q, want %q", text, wanted)
		}
	}
	if strings.Contains(text, "do-not-print") {
		t.Fatalf("status leaked an environment value: %q", text)
	}
}

func TestNativeDoctorIsReadOnlyAndReportsSafetyAuthAndOrphans(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("OPENAI_API_KEY", "do-not-print")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "mcpServers": {"tmux": {"command": "server", "env": {"LIBTMUX_SAFETY": "readonly"}}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orphan := path + ".bak.mcp-swap-go-00000000000000000042"
	if err := os.WriteFile(orphan, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := client{
		name: "codex", path: path, key: "mcpServers",
		format: formatJSON, dialect: dialectStandard,
	}
	var output bytes.Buffer
	if err := nativeDoctorTo(&output, []client{target}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, wanted := range []string{"LIBTMUX_SAFETY", "OPENAI_API_KEY", "orphaned backups: 1"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("doctor = %q, want %q", text, wanted)
		}
	}
	if strings.Contains(text, "do-not-print") {
		t.Fatalf("doctor leaked an environment value: %q", text)
	}
	if _, err := os.Lstat(stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor created the state root: %v", err)
	}
}
