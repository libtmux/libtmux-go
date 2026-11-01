package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// A client rewrites its own configuration while it runs, and Claude Code keeps
// counters and per-project state in the file that holds its servers. Only the
// tmux entry belongs to the swapper, so only that entry decides whether a later
// swap or revert may go on.

type nativeRig struct {
	home       string
	repository string
}

func newNativeRig(t *testing.T) nativeRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	return nativeRig{home: home, repository: filepath.Join(t.TempDir(), "checkout")}
}

func (rig nativeRig) client(t *testing.T, name string, scope configScope) client {
	t.Helper()
	for _, target := range knownClients(rig.home) {
		if target.name == name {
			target.scope = scope
			target.repository = rig.repository
			return target
		}
	}
	t.Fatalf("no client named %s", name)
	return client{}
}

func entryPlanFor(entry map[string]any) entryPlan {
	return entryPlan{configured: entry, install: func() error { return nil }, cleanup: func() {}}
}

func movedEntry() map[string]any {
	entry := devEntry()
	entry["args"] = []any{"-C", "/moved/repo/mcp", "run", "./cmd/libtmux-mcp"}
	return entry
}

func swapSpec(t *testing.T, target client) (processSpec, bool) {
	t.Helper()
	entry, present, err := entryOf(target)
	if err != nil {
		t.Fatalf("%s entry: %v", target.name, err)
	}
	if !present {
		return processSpec{}, false
	}
	spec, err := processSpecFromEntry(entry)
	if err != nil {
		t.Fatalf("%s entry spec: %v", target.name, err)
	}
	return spec, true
}

func readJSONConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// rewriteClaudeState does what Claude Code does to ~/.claude.json while it
// runs: it bumps a counter and records a project, and leaves mcpServers alone.
func rewriteClaudeState(t *testing.T, path, how string, startups float64) {
	t.Helper()
	config := readJSONConfig(t, path)
	config["numStartups"] = startups
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		config["projects"] = projects
	}
	projects["/work/other"] = map[string]any{"hasTrustDialogAccepted": true}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, '\n')
	switch how {
	case "in place":
		if err := os.WriteFile(path, contents, 0o640); err != nil {
			t.Fatal(err)
		}
	case "replaced file":
		replaceContentsAtSamePath(t, path, contents)
	case "replaced file with another mode":
		replaceContentsAtSamePath(t, path, contents)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown rewrite %q", how)
	}
}

func assertClaudeState(t *testing.T, path string, startups float64) {
	t.Helper()
	config := readJSONConfig(t, path)
	if got, _ := config["numStartups"].(float64); got != startups {
		t.Errorf("numStartups = %v, want %v", got, startups)
	}
	projects, _ := config["projects"].(map[string]any)
	if _, kept := projects["/work/other"]; !kept {
		t.Error("the client's own project record did not survive")
	}
}

func swappedClaude(t *testing.T) (client, string) {
	t.Helper()
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeUser)
	original := `{
  "numStartups": 1,
  "mcpServers": {"tmux": {"command": "uvx", "args": ["libtmux-mcp"]}},
  "projects": {}
}
`
	if err := os.WriteFile(target.path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	return target, rig.repository
}

var clientRewrites = []string{"in place", "replaced file", "replaced file with another mode"}

func TestNativeReswapGoesOnAfterAClientRewritesItsOwnState(t *testing.T) {
	for _, how := range clientRewrites {
		t.Run(how, func(t *testing.T) {
			target, repository := swappedClaude(t)
			rewriteClaudeState(t, target.path, how, 2)
			if err := nativeUse([]client{target}, []client{target}, entryPlanFor(movedEntry()),
				options{scope: scopeUser, noPreflight: true}, repository); err != nil {
				t.Fatalf("reswap after the client rewrote its state: %v", err)
			}
			spec, present := swapSpec(t, target)
			if !present || !slices.Contains(spec.args, "/moved/repo/mcp") {
				t.Errorf("reswap left the entry at its old build (present %t)", present)
			}
			assertClaudeState(t, target.path, 2)
		})
	}
}

func TestNativeRevertGoesOnAfterAClientRewritesItsOwnState(t *testing.T) {
	for _, how := range clientRewrites {
		t.Run(how, func(t *testing.T) {
			target, repository := swappedClaude(t)
			rewriteClaudeState(t, target.path, how, 2)
			before, err := os.Stat(target.path)
			if err != nil {
				t.Fatal(err)
			}
			ledger, _, err := loadNativeLedger()
			if err != nil {
				t.Fatal(err)
			}
			if err := nativeRevert([]client{target}, []client{target}, options{}, repository); err != nil {
				t.Fatalf("revert after the client rewrote its state: %v", err)
			}
			spec, present := swapSpec(t, target)
			if !present || spec.command != "uvx" || !slices.Equal(spec.args, []string{"libtmux-mcp"}) {
				t.Errorf("revert did not put the original entry back (present %t)", present)
			}
			assertClaudeState(t, target.path, 2)
			after, err := os.Stat(target.path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode().Perm() != before.Mode().Perm() {
				t.Errorf("revert changed the mode of a file the client had rewritten")
			}
			if _, err := os.Stat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("revert retained native state: %v", err)
			}
			for _, entry := range ledger.Entries {
				if _, err := os.Stat(entry.BackupPath); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("revert retained a backup: %v", err)
				}
			}
		})
	}
}

func TestNativeRevertRemovesAnEntryThatWasAbsentBeforeTheSwap(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeUser)
	if err := os.WriteFile(target.path, []byte(`{"numStartups": 1, "projects": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	rewriteClaudeState(t, target.path, "in place", 2)
	if err := nativeRevert([]client{target}, []client{target}, options{}, rig.repository); err != nil {
		t.Fatalf("revert after the client rewrote its state: %v", err)
	}
	if _, present := swapSpec(t, target); present {
		t.Error("revert left a tmux entry the file did not have before the swap")
	}
	assertClaudeState(t, target.path, 2)
}

func TestNativeRefusesWhenTheManagedEntryItselfChanged(t *testing.T) {
	edits := map[string]func(servers map[string]any){
		"arguments": func(servers map[string]any) {
			servers["tmux"].(map[string]any)["args"] = []any{"edited"}
		},
		"command": func(servers map[string]any) {
			servers["tmux"].(map[string]any)["command"] = "edited"
		},
		"environment": func(servers map[string]any) {
			servers["tmux"].(map[string]any)["env"] = map[string]any{"LIBTMUX_MCP_SWAP": "edited"}
		},
		"removed": func(servers map[string]any) { delete(servers, "tmux") },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			target, repository := swappedClaude(t)
			config := readJSONConfig(t, target.path)
			edit(config["mcpServers"].(map[string]any))
			contents, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target.path, contents, 0o640); err != nil {
				t.Fatal(err)
			}
			configBefore := readFile(t, target.path)
			stateBefore := readFile(t, nativeStatePath())

			want := "the tmux entry changed since mcp-swap wrote it"
			if name == "removed" {
				want = "the tmux entry is missing"
			}
			err = nativeUse([]client{target}, []client{target}, entryPlanFor(movedEntry()),
				options{scope: scopeUser, noPreflight: true}, repository)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("reswap error = %v, want a refusal saying %q", err, want)
			}
			err = nativeRevert([]client{target}, []client{target}, options{}, repository)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("revert error = %v, want a refusal saying %q", err, want)
			}
			if readFile(t, target.path) != configBefore {
				t.Error("a refused command changed the configuration")
			}
			if readFile(t, nativeStatePath()) != stateBefore {
				t.Error("a refused command changed the recovery state")
			}
		})
	}
}

func writeNativeOriginals(t *testing.T, rig nativeRig, withForeignEntry bool) []client {
	t.Helper()
	foreign := map[string]any{"command": "old", "args": []any{"a"}}
	var targets []client
	for _, known := range knownClients(rig.home) {
		target := rig.client(t, known.name, scopeUser)
		var base string
		if target.format == formatTOML {
			base = "[" + target.key + ".keep]\ncommand = \"keep\"\n"
		} else {
			base = "{\n  \"" + target.key + "\": {\"keep\": {\"command\": \"keep\"}}\n}\n"
		}
		contents := []byte(base)
		if withForeignEntry {
			var err error
			if contents, err = renderEntryChange(target, contents, foreign); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target.path, contents, 0o640); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	return targets
}

// driftClientConfig edits a configuration the way its owner might: outside the
// tmux entry, in the format the file is written in.
func driftClientConfig(t *testing.T, target client) {
	t.Helper()
	contents := readFile(t, target.path)
	switch target.format {
	case formatTOML:
		contents = "# rewritten by the client\n" + contents + "\n[drift]\nvalue = 1\n"
	case formatJSONC:
		contents = strings.Replace(contents, "{", "{\n  // rewritten by the client\n  \"drift\": 1,", 1)
	case formatJSON:
		var config map[string]any
		if err := json.Unmarshal([]byte(contents), &config); err != nil {
			t.Fatal(err)
		}
		config["drift"] = 1
		rewritten, err := json.MarshalIndent(config, "", "    ")
		if err != nil {
			t.Fatal(err)
		}
		contents = string(rewritten) + "\n"
	}
	if err := os.WriteFile(target.path, []byte(contents), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEveryClientGoesOnAfterItsConfigDrifts(t *testing.T) {
	for _, withForeignEntry := range []bool{false, true} {
		name := "no entry before the swap"
		if withForeignEntry {
			name = "foreign entry before the swap"
		}
		t.Run(name, func(t *testing.T) {
			rig := newNativeRig(t)
			targets := writeNativeOriginals(t, rig, withForeignEntry)
			if err := nativeUse(targets, targets, entryPlanFor(devEntry()),
				options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				driftClientConfig(t, target)
			}
			if err := nativeUse(targets, targets, entryPlanFor(movedEntry()),
				options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
				t.Fatalf("reswap after the configs drifted: %v", err)
			}
			for _, target := range targets {
				spec, present := swapSpec(t, target)
				if !present || !slices.Contains(spec.args, "/moved/repo/mcp") {
					t.Errorf("%s: reswap left the entry at its old build (present %t)", target.name, present)
				}
			}
			if err := nativeRevert(targets, targets, options{scope: scopeUser}, rig.repository); err != nil {
				t.Fatalf("revert after the configs drifted: %v", err)
			}
			for _, target := range targets {
				text := readFile(t, target.path)
				if !strings.Contains(text, "drift") || !strings.Contains(text, "keep") {
					t.Errorf("%s: revert dropped content the swapper does not own", target.name)
				}
				spec, present := swapSpec(t, target)
				if withForeignEntry {
					if !present || spec.command != "old" || !slices.Equal(spec.args, []string{"a"}) {
						t.Errorf("%s: revert did not restore the foreign entry (present %t)", target.name, present)
					}
				} else if present {
					t.Errorf("%s: revert left an entry the file did not have", target.name)
				}
			}
			if _, err := os.Stat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("revert retained native state: %v", err)
			}
		})
	}
}

func foreignClaudeWithUserAndProjectLayers(t *testing.T) (user, project client, repository string) {
	t.Helper()
	rig := newNativeRig(t)
	user = rig.client(t, "claude", scopeUser)
	project = rig.client(t, "claude", scopeProject)
	original := `{
  "numStartups": 1,
  "mcpServers": {"tmux": {"command": "uvx", "args": ["libtmux-mcp"]}},
  "projects": {}
}
`
	if err := os.WriteFile(user.path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []configScope{scopeUser, scopeProject} {
		if err := nativeUse([]client{user}, []client{user}, entryPlanFor(devEntry()),
			options{scope: scope, noPreflight: true}, rig.repository); err != nil {
			t.Fatal(err)
		}
	}
	return user, project, rig.repository
}

func TestNativeClaudeLayersGoOnAfterDriftThroughScopedReverts(t *testing.T) {
	for _, driftAgain := range []bool{true, false} {
		t.Run(fmt.Sprintf("drift again %t", driftAgain), func(t *testing.T) {
			user, project, repository := foreignClaudeWithUserAndProjectLayers(t)
			rewriteClaudeState(t, user.path, "in place", 2)
			if err := nativeRevert([]client{user}, []client{user},
				options{scope: scopeProject}, repository); err != nil {
				t.Fatalf("project revert after the client rewrote its state: %v", err)
			}
			if _, present := swapSpec(t, project); present {
				t.Error("project revert left the project entry")
			}
			entry, present, err := entryOf(user)
			if err != nil || !present || !isLocal(entry) {
				t.Fatalf("project revert disturbed the user layer: present %t, err %v", present, err)
			}
			assertClaudeState(t, user.path, 2)

			startups := float64(2)
			if driftAgain {
				startups = 3
				rewriteClaudeState(t, user.path, "replaced file", startups)
			}
			if err := nativeRevert([]client{user}, []client{user},
				options{scope: scopeUser}, repository); err != nil {
				t.Fatalf("user revert: %v", err)
			}
			if spec, present := swapSpec(t, user); !present || spec.command != "uvx" {
				t.Errorf("user revert did not restore the original entry (present %t)", present)
			}
			assertClaudeState(t, user.path, startups)
			if _, err := os.Stat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("reverts retained native state: %v", err)
			}
		})
	}
}

func TestNativeRevertKeepsWhatAReswapAlreadyTookFromTheClient(t *testing.T) {
	target, repository := swappedClaude(t)
	rewriteClaudeState(t, target.path, "in place", 2)
	if err := nativeUse([]client{target}, []client{target}, entryPlanFor(movedEntry()),
		options{scope: scopeUser, noPreflight: true}, repository); err != nil {
		t.Fatal(err)
	}
	if err := nativeRevert([]client{target}, []client{target}, options{}, repository); err != nil {
		t.Fatal(err)
	}
	if spec, present := swapSpec(t, target); !present || spec.command != "uvx" {
		t.Errorf("revert did not restore the original entry (present %t)", present)
	}
	assertClaudeState(t, target.path, 2)
}

func TestNativeLayerSwappedAfterDriftKeepsItThroughReverts(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped %t", scoped), func(t *testing.T) {
			rig := newNativeRig(t)
			user := rig.client(t, "claude", scopeUser)
			project := rig.client(t, "claude", scopeProject)
			original := `{"numStartups": 1, "mcpServers": {"tmux": {"command": "uvx"}}, "projects": {}}`
			if err := os.WriteFile(user.path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			plan := entryPlanFor(devEntry())
			if err := nativeUse([]client{user}, []client{user}, plan,
				options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
				t.Fatal(err)
			}
			rewriteClaudeState(t, user.path, "in place", 2)
			if err := nativeUse([]client{user}, []client{user}, plan,
				options{scope: scopeProject, noPreflight: true}, rig.repository); err != nil {
				t.Fatal(err)
			}
			reverts := []options{{}}
			if scoped {
				reverts = []options{{scope: scopeProject}, {scope: scopeUser}}
			}
			for _, chosen := range reverts {
				if err := nativeRevert([]client{user}, []client{user}, chosen, rig.repository); err != nil {
					t.Fatalf("revert of a layer swapped after the client rewrote its state: %v", err)
				}
			}
			if spec, present := swapSpec(t, user); !present || spec.command != "uvx" {
				t.Errorf("revert did not restore the user entry (present %t)", present)
			}
			if _, present := swapSpec(t, project); present {
				t.Error("revert left the project entry")
			}
			assertClaudeState(t, user.path, 2)
		})
	}
}

func TestNativeOlderLayerReswappedAfterDriftKeepsItThroughRevert(t *testing.T) {
	rig := newNativeRig(t)
	user := rig.client(t, "claude", scopeUser)
	original := `{"numStartups": 1, "mcpServers": {"tmux": {"command": "uvx"}}, "projects": {}}`
	if err := os.WriteFile(user.path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []configScope{scopeUser, scopeProject} {
		if scope == scopeProject {
			rewriteClaudeState(t, user.path, "in place", 2)
		}
		if err := nativeUse([]client{user}, []client{user}, entryPlanFor(devEntry()),
			options{scope: scope, noPreflight: true}, rig.repository); err != nil {
			t.Fatal(err)
		}
	}
	if err := nativeUse([]client{user}, []client{user}, entryPlanFor(movedEntry()),
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	if err := nativeRevert([]client{user}, []client{user}, options{}, rig.repository); err != nil {
		t.Fatal(err)
	}
	if spec, present := swapSpec(t, user); !present || spec.command != "uvx" {
		t.Errorf("revert did not restore the user entry (present %t)", present)
	}
	assertClaudeState(t, user.path, 2)
}

func TestNativeRevertRefusesATOMLChildTableItWouldDrop(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "codex", scopeUser)
	if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.path, []byte("[mcp_servers.keep]\ncommand = \"keep\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	child := "\n[mcp_servers.tmux.tools.run_shell_command]\napproval_mode = \"approve\"\n"
	if err := os.WriteFile(target.path, []byte(readFile(t, target.path)+child), 0o600); err != nil {
		t.Fatal(err)
	}
	configBefore, stateBefore := readFile(t, target.path), readFile(t, nativeStatePath())
	err := nativeRevert([]client{target}, []client{target}, options{}, rig.repository)
	if err == nil || !strings.Contains(err.Error(), "child table") {
		t.Fatalf("revert error = %v, want a refusal to drop the client's child table", err)
	}
	if readFile(t, target.path) != configBefore || readFile(t, nativeStatePath()) != stateBefore {
		t.Error("a refused revert changed the configuration or the recovery state")
	}
}

func TestNativeOlderClaudeLayerReswapsAfterDriftAndTheChainStillUnwinds(t *testing.T) {
	user, project, repository := foreignClaudeWithUserAndProjectLayers(t)
	rewriteClaudeState(t, user.path, "in place", 2)
	if err := nativeUse([]client{user}, []client{user}, entryPlanFor(movedEntry()),
		options{scope: scopeUser, noPreflight: true}, repository); err != nil {
		t.Fatalf("reswap of the older layer after the client rewrote its state: %v", err)
	}
	if spec, present := swapSpec(t, user); !present || !slices.Contains(spec.args, "/moved/repo/mcp") {
		t.Errorf("reswap left the user entry at its old build (present %t)", present)
	}
	rewriteClaudeState(t, user.path, "in place", 3)
	if err := nativeRevert([]client{user}, []client{user}, options{}, repository); err != nil {
		t.Fatalf("unscoped revert after further drift: %v", err)
	}
	if spec, present := swapSpec(t, user); !present || spec.command != "uvx" {
		t.Errorf("revert did not restore the user entry (present %t)", present)
	}
	if _, present := swapSpec(t, project); present {
		t.Error("revert left the project entry")
	}
	assertClaudeState(t, user.path, 3)
}

func TestNativeProjectLayerSwapsFromASecondCheckout(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeProject)
	original := []byte(`{"numStartups":1,"projects":{}}`)
	if err := os.WriteFile(target.path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other-checkout")
	for _, repository := range []string{rig.repository, other} {
		if err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
			options{scope: scopeProject, noPreflight: true}, repository); err != nil {
			t.Fatalf("project swap from %s: %v", filepath.Base(repository), err)
		}
	}
	target.repository = other
	if entry, present, err := entryOf(target); err != nil || !present || !isLocal(entry) {
		t.Fatalf("second checkout entry: present %t, err %v", present, err)
	}
	if err := nativeRevert([]client{target}, []client{target}, options{}, other); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(readFile(t, target.path)), original) {
		t.Error("revert did not restore the file the first swap saw")
	}
}

func TestNativeProjectLayerRefusesASecondCheckoutAfterTheFileChanged(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeProject)
	if err := os.WriteFile(target.path, []byte(`{"numStartups":1,"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
		options{scope: scopeProject, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	rewriteClaudeState(t, target.path, "in place", 2)
	configBefore, stateBefore := readFile(t, target.path), readFile(t, nativeStatePath())
	other := filepath.Join(t.TempDir(), "other-checkout")
	err := nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
		options{scope: scopeProject, noPreflight: true}, other)
	if err == nil || !strings.Contains(err.Error(), rig.repository) ||
		!strings.Contains(err.Error(), "revert --scope project") {
		t.Fatalf("swap from a second checkout error = %v, want a refusal naming the first and the way forward", err)
	}
	if readFile(t, target.path) != configBefore || readFile(t, nativeStatePath()) != stateBefore {
		t.Error("a refused swap changed the configuration or the recovery state")
	}
	if err := nativeRevert([]client{target}, []client{target}, options{}, rig.repository); err != nil {
		t.Fatalf("revert after the refusal: %v", err)
	}
	if _, present := swapSpec(t, target); present {
		t.Error("revert left the project entry")
	}
	assertClaudeState(t, target.path, 2)
}

func TestNativeProjectLayerReswapsFromTheSameCheckoutAfterTheFileChanged(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeProject)
	original := []byte(`{"numStartups":1,"projects":{}}`)
	if err := os.WriteFile(target.path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	use := func(entry map[string]any) {
		t.Helper()
		if err := nativeUse([]client{target}, []client{target}, entryPlanFor(entry),
			options{scope: scopeProject, noPreflight: true}, rig.repository); err != nil {
			t.Fatal(err)
		}
	}
	use(devEntry())
	rewriteClaudeState(t, target.path, "in place", 2)
	use(movedEntry())
	if err := nativeRevert([]client{target}, []client{target}, options{}, rig.repository); err != nil {
		t.Fatal(err)
	}
	if _, present := swapSpec(t, target); present {
		t.Error("revert left the project entry")
	}
	assertClaudeState(t, target.path, 2)
}

func TestNativeProjectLayerRefusesASecondCheckoutBehindAnotherLayer(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeProject)
	if err := os.WriteFile(target.path, []byte(`{"numStartups":1,"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other-checkout")
	use := func(scope configScope, repository string) error {
		return nativeUse([]client{target}, []client{target}, entryPlanFor(devEntry()),
			options{scope: scope, noPreflight: true}, repository)
	}
	if err := use(scopeProject, other); err != nil {
		t.Fatal(err)
	}
	rewriteClaudeState(t, target.path, "in place", 2)
	if err := use(scopeUser, rig.repository); err != nil {
		t.Fatal(err)
	}
	configBefore, stateBefore := readFile(t, target.path), readFile(t, nativeStatePath())
	err := use(scopeProject, rig.repository)
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("swap from a second checkout error = %v, want a refusal naming the first", err)
	}
	if readFile(t, target.path) != configBefore || readFile(t, nativeStatePath()) != stateBefore {
		t.Error("a refused swap changed the configuration or the recovery state")
	}
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = stdout }()
	done := make(chan string)
	go func() {
		var buffer bytes.Buffer
		_, _ = buffer.ReadFrom(reader)
		done <- buffer.String()
	}()
	run()
	_ = writer.Close()
	return <-done
}

func TestNativeRevertSaysWhenItPutsBackOnlyTheEntry(t *testing.T) {
	target, repository := swappedClaude(t)
	dryRun := func() string {
		var err error
		output := captureStdout(t, func() {
			err = nativeRevert([]client{target}, []client{target}, options{dryRun: true}, repository)
		})
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	if output := dryRun(); !strings.Contains(output, "would restore .claude.json.bak") ||
		strings.Contains(output, "entry") {
		t.Errorf("dry run of a revert from the backup said %q", output)
	}
	rewriteClaudeState(t, target.path, "in place", 2)
	if output := dryRun(); !strings.Contains(output, "would restore the tmux entry from .claude.json.bak") {
		t.Errorf("dry run of an entry-only revert said %q", output)
	}
}

// A foreign server's empty LIBTMUX_TOOLSETS means something else there. The Go
// server reads a set, empty value as no toolsets, so carrying it over leaves a
// client with only the tools LIBTMUX_TOOLS names.

func foreignEntryWithEmptyToolsets(t *testing.T, target client) {
	t.Helper()
	foreign := map[string]any{
		"command": "npx", "args": []any{"-y", "libtmux-mcp-ts"},
		"env": map[string]any{"LIBTMUX_TOOLSETS": "", "LIBTMUX_TOOLS": "run_shell_command"},
	}
	base := "{\"" + target.key + "\": {}}\n"
	if target.format == formatTOML {
		base = ""
	}
	contents, err := renderEntryChange(target, []byte(base), foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installedPlan(t *testing.T, repository string, environment map[string]string) (entryPlan, *bool) {
	t.Helper()
	entry, err := buildEntry(options{mode: modeInstalled, environment: environment}, repository)
	if err != nil {
		t.Fatal(err)
	}
	provisioned := false
	return entryPlan{
		configured: entry, cleanup: func() {},
		install: func() error { provisioned = true; return nil },
	}, &provisioned
}

func swappedEnvironment(t *testing.T, target client) map[string]string {
	t.Helper()
	entry, present, err := entryOf(target)
	if err != nil || !present {
		t.Fatalf("%s entry: present %t, err %v", target.name, present, err)
	}
	spec, err := processSpecFromEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	return spec.env
}

func TestNativeUseRefusesACarriedEmptyToolsetsBeforeWritingAnything(t *testing.T) {
	for _, name := range []string{"claude", "codex", "opencode"} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s dry-run %t", name, dryRun), func(t *testing.T) {
				rig := newNativeRig(t)
				target := rig.client(t, name, scopeUser)
				foreignEntryWithEmptyToolsets(t, target)
				before := readFile(t, target.path)
				plan, provisioned := installedPlan(t, rig.repository, nil)
				err := nativeUse([]client{target}, []client{target}, plan,
					options{scope: scopeUser, noPreflight: true, dryRun: dryRun}, rig.repository)
				if err == nil {
					t.Fatal("use carried an empty LIBTMUX_TOOLSETS into the entry it wrote")
				}
				message := err.Error()
				if !strings.Contains(message, "LIBTMUX_TOOLSETS") ||
					!strings.Contains(message, "--env LIBTMUX_TOOLSETS=inspect,manage,execute") ||
					strings.Count(message, "--env LIBTMUX_TOOLSETS=") != 2 {
					t.Errorf("refusal does not name the variable and both ways forward: %s", message)
				}
				if strings.Contains(message, "run_shell_command") {
					t.Error("refusal printed an environment value")
				}
				if readFile(t, target.path) != before {
					t.Error("a refused swap changed the configuration")
				}
				if !dryRun && *provisioned {
					t.Error("a refused swap provisioned the server first")
				}
				if _, err := os.Stat(nativeStatePath()); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a refused swap created recovery state: %v", err)
				}
			})
		}
	}
}

func TestNativeUseKeepsAnEmptyToolsetsTheUserAskedFor(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeUser)
	foreignEntryWithEmptyToolsets(t, target)
	plan, _ := installedPlan(t, rig.repository, map[string]string{"LIBTMUX_TOOLSETS": ""})
	if err := nativeUse([]client{target}, []client{target}, plan,
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatalf("use refused an empty LIBTMUX_TOOLSETS that --env asked for: %v", err)
	}
	environment := swappedEnvironment(t, target)
	if value, set := environment["LIBTMUX_TOOLSETS"]; !set || value != "" {
		t.Errorf("LIBTMUX_TOOLSETS set %t, empty %t, want set and empty", set, value == "")
	}
	if environment["LIBTMUX_TOOLS"] != "run_shell_command" {
		t.Error("use dropped LIBTMUX_TOOLS")
	}

	again, _ := installedPlan(t, rig.repository, nil)
	if err := nativeUse([]client{target}, []client{target}, again,
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatalf("use asked again about a value an earlier swap already kept: %v", err)
	}
	if value, set := swappedEnvironment(t, target)["LIBTMUX_TOOLSETS"]; !set || value != "" {
		t.Errorf("a later swap changed LIBTMUX_TOOLSETS: set %t, empty %t", set, value == "")
	}
}

func TestNativeUseTakesTheToolsetsTheUserNames(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeUser)
	foreignEntryWithEmptyToolsets(t, target)
	plan, _ := installedPlan(t, rig.repository,
		map[string]string{"LIBTMUX_TOOLSETS": "inspect,manage,execute"})
	if err := nativeUse([]client{target}, []client{target}, plan,
		options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
		t.Fatal(err)
	}
	if swappedEnvironment(t, target)["LIBTMUX_TOOLSETS"] != "inspect,manage,execute" {
		t.Error("--env did not replace the carried empty LIBTMUX_TOOLSETS")
	}
}

func TestNativeUseCarriesToolsetsThatAreNotEmpty(t *testing.T) {
	for name, test := range map[string]struct {
		environment map[string]any
		toolsets    string
		set         bool
	}{
		"named":  {map[string]any{"LIBTMUX_TOOLSETS": "inspect"}, "inspect", true},
		"unset":  {map[string]any{"LIBTMUX_TOOLS": "run_shell_command"}, "", false},
		"no env": {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newNativeRig(t)
			target := rig.client(t, "claude", scopeUser)
			entry := map[string]any{"command": "npx"}
			if test.environment != nil {
				entry["env"] = test.environment
			}
			contents, err := renderEntryChange(target, []byte("{}\n"), entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target.path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			plan, _ := installedPlan(t, rig.repository, nil)
			if err := nativeUse([]client{target}, []client{target}, plan,
				options{scope: scopeUser, noPreflight: true}, rig.repository); err != nil {
				t.Fatalf("use refused an entry whose LIBTMUX_TOOLSETS is not empty: %v", err)
			}
			got, set := swappedEnvironment(t, target)["LIBTMUX_TOOLSETS"]
			if set != test.set || got != test.toolsets {
				t.Errorf("LIBTMUX_TOOLSETS set %t, matches %t; want set %t, matches %t",
					set, got == test.toolsets, test.set, true)
			}
		})
	}
}

func TestExecuteNamesTheWayForwardForACarriedEmptyToolsets(t *testing.T) {
	rig := newNativeRig(t)
	target := rig.client(t, "claude", scopeUser)
	foreignEntryWithEmptyToolsets(t, target)
	arguments := []string{
		"use", "--scope", "user", "--client", "claude", "--mode", "installed", "--no-preflight",
	}
	var stdout, stderr bytes.Buffer
	if status := execute(arguments, &stdout, &stderr); status != 1 ||
		!strings.Contains(stderr.String(), "--env LIBTMUX_TOOLSETS=inspect,manage,execute") {
		t.Fatalf("use exited %d without naming the way forward", status)
	}
	stdout.Reset()
	stderr.Reset()
	keep := append(slices.Clone(arguments), "--env", "LIBTMUX_TOOLSETS=")
	if status := execute(keep, &stdout, &stderr); status != 0 {
		t.Fatalf("use --env LIBTMUX_TOOLSETS= exited %d", status)
	}
	if value, set := swappedEnvironment(t, target)["LIBTMUX_TOOLSETS"]; !set || value != "" {
		t.Errorf("LIBTMUX_TOOLSETS set %t, empty %t, want set and empty", set, value == "")
	}
}
