package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRecoveryStateDescribesPublishedConfiguration(t *testing.T) {
	target, original := recoveryFixture(t)
	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	current := []byte(readFile(t, target.path))
	var state map[string]any
	if err := json.Unmarshal([]byte(readFile(t, recoveryStatePath(target))), &state); err != nil {
		t.Fatal(err)
	}
	if state["expected_sha256"] != digest(current) {
		t.Errorf("recovery expected digest = %v, want %s", state["expected_sha256"], digest(current))
	}
	info, err := os.Stat(target.path)
	if err != nil {
		t.Fatal(err)
	}
	if state["expected_mode"] != float64(info.Mode().Perm()) {
		t.Errorf("recovery expected mode = %v, want %o", state["expected_mode"], info.Mode().Perm())
	}
	if state["original_mode"] != float64(0o640) {
		t.Errorf("recovery original mode = %v, want 640", state["original_mode"])
	}
	if got := readFile(t, backupPath(target)); got != string(original) {
		t.Fatalf("backup = %q, want %q", got, original)
	}
}

func TestRecoveryStateIsDurableBeforeConfigPublish(t *testing.T) {
	target, original := recoveryFixture(t)
	change, err := planEntryChange(target, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	change, created, err := change.prepareBackup()
	if err != nil || !created {
		t.Fatalf("prepareBackup() = (%t, %v)", created, err)
	}
	stop := errors.New("stop before config publish")
	_, err = change.commitWith(func(next recoveryState) error {
		if got := readFile(t, target.path); got != string(original) {
			t.Fatalf("config became visible before the recovery state: %q", got)
		}
		onDisk, _, _, readErr := readRecoveryState(recoveryStatePath(target))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if onDisk.ExpectedSHA256 != digest(change.updated) || !reflect.DeepEqual(onDisk, next) {
			t.Fatalf("pre-publish recovery state = %+v, want %+v", onDisk, next)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("commitWith() error = %v, want injected stop", err)
	}
	if got := readFile(t, target.path); got != string(original) {
		t.Fatalf("aborted publish changed config: %q", got)
	}
	restored, _, _, err := readRecoveryState(recoveryStatePath(target))
	if err != nil {
		t.Fatal(err)
	}
	if restored.ExpectedSHA256 != digest(original) {
		t.Fatalf("aborted publish left state expecting %s", restored.ExpectedSHA256)
	}
}

func TestRevertRejectsPostSwapContentOrModeChanges(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		target, _ := recoveryFixture(t)
		if err := writeEntry(target, devEntry()); err != nil {
			t.Fatal(err)
		}
		tampered := append([]byte(readFile(t, target.path)), ' ')
		if err := os.WriteFile(target.path, tampered, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := revert([]client{target}, false); err == nil {
			t.Fatal("revert accepted post-swap content tampering")
		}
		if got := []byte(readFile(t, target.path)); !bytes.Equal(got, tampered) {
			t.Fatalf("refused revert changed config: %q", got)
		}
		assertRecoveryPairExists(t, target)
	})

	t.Run("mode", func(t *testing.T) {
		target, _ := recoveryFixture(t)
		if err := writeEntry(target, devEntry()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target.path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revert([]client{target}, false); err == nil {
			t.Fatal("revert accepted post-swap mode tampering")
		}
		info, err := os.Stat(target.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("refused revert changed mode to %o", info.Mode().Perm())
		}
		assertRecoveryPairExists(t, target)
	})
}

func TestRestoreRevalidatesRecoveryArtifacts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "backup identity", mutate: replaceFileAtSamePath},
		{name: "backup content", mutate: appendToFile},
		{name: "backup mode", mutate: func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "state identity", mutate: replaceFileAtSamePath},
		{name: "state content", mutate: appendToFile},
		{name: "state mode", mutate: func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, _ := recoveryFixture(t)
			if err := writeEntry(target, devEntry()); err != nil {
				t.Fatal(err)
			}
			swapped := readFile(t, target.path)
			change, exists, err := planRestore(target)
			if err != nil || !exists {
				t.Fatalf("planRestore() = (%t, %v)", exists, err)
			}
			path := backupPath(target)
			if test.name[:5] == "state" {
				path = recoveryStatePath(target)
			}
			test.mutate(t, path)
			if err := applyRestoreChanges([]restoreChange{change}); err == nil {
				t.Fatal("restore accepted a recovery artifact changed after planning")
			}
			if got := readFile(t, target.path); got != swapped {
				t.Fatalf("refused restore changed config: %q", got)
			}
			assertRecoveryPairExists(t, target)
		})
	}
}

func TestRecoveryArtifactAliasesStopAllWrites(t *testing.T) {
	for _, artifact := range []string{"backup", "state"} {
		t.Run(artifact, func(t *testing.T) {
			first, _ := recoveryFixture(t)
			if err := writeEntry(first, devEntry()); err != nil {
				t.Fatal(err)
			}
			aliased := backupPath(first)
			if artifact == "state" {
				aliased = recoveryStatePath(first)
			}
			second := client{
				name: "second", path: filepath.Join(t.TempDir(), "second.json"),
				key: "mcpServers", format: formatJSON, dialect: dialectStandard,
			}
			if err := os.Link(aliased, second.path); err != nil {
				t.Fatal(err)
			}
			firstBefore := readFile(t, first.path)
			artifactBefore := readFile(t, aliased)
			secondBefore := readFile(t, second.path)

			if err := useLocal([]client{first, second}, devEntry(), false); err == nil {
				t.Fatal("use-local accepted a config aliased to a recovery artifact")
			}
			if got := readFile(t, first.path); got != firstBefore {
				t.Fatalf("first config changed: %q", got)
			}
			if got := readFile(t, aliased); got != artifactBefore {
				t.Fatalf("recovery artifact changed: %q", got)
			}
			if got := readFile(t, second.path); got != secondBefore {
				t.Fatalf("aliased config changed: %q", got)
			}
		})
	}
}

func TestRecoveryDestinationRefusesParentSymlinkRetarget(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "selected")
	if err := os.Symlink(filepath.Base(first), linked); err != nil {
		t.Fatal(err)
	}
	target := client{
		name: "linked-parent", path: filepath.Join(linked, "config.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	original := []byte(`{"mcpServers":{}}`)
	if err := os.WriteFile(filepath.Join(first, "config.json"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	change, err := planEntryChange(target, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(second), linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := change.prepareBackup(); err == nil {
		t.Fatal("backup preparation accepted a retargeted parent symlink")
	}
	if got := readFile(t, filepath.Join(first, "config.json")); got != string(original) {
		t.Fatalf("original config changed: %q", got)
	}
	for _, directory := range []string{first, second} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() != "config.json" {
				t.Fatalf("retargeted preparation left %s/%s", directory, entry.Name())
			}
		}
	}
}

func TestRestoreCleanupFailureRollsBackAllConfigs(t *testing.T) {
	first, firstOriginal := recoveryFixture(t)
	first.name = "first"
	second, secondOriginal := recoveryFixture(t)
	second.name = "second"
	for _, target := range []client{first, second} {
		if err := writeEntry(target, devEntry()); err != nil {
			t.Fatal(err)
		}
	}
	firstSwapped := readFile(t, first.path)
	secondSwapped := readFile(t, second.path)
	changes := make([]restoreChange, 0, 2)
	for _, target := range []client{first, second} {
		change, exists, err := planRestore(target)
		if err != nil || !exists {
			t.Fatalf("planRestore(%s) = (%t, %v)", target.name, exists, err)
		}
		changes = append(changes, change)
	}
	removeFailure := errors.New("injected artifact remove failure")
	removes := 0
	err := applyRestoreChangesWith(changes, func(path string) error {
		removes++
		if removes == 2 {
			return removeFailure
		}
		return os.Remove(path)
	})
	if !errors.Is(err, removeFailure) {
		t.Fatalf("applyRestoreChangesWith() error = %v", err)
	}
	if got := readFile(t, first.path); got != firstSwapped {
		t.Fatalf("first config was not rolled back: %q", got)
	}
	if got := readFile(t, second.path); got != secondSwapped {
		t.Fatalf("second config was not rolled back: %q", got)
	}
	assertRecoveryPairExists(t, first)
	assertRecoveryPairExists(t, second)
	if err := revert([]client{first, second}, false); err != nil {
		t.Fatalf("retained recovery pair could not be retried: %v", err)
	}
	if got := readFile(t, first.path); got != string(firstOriginal) {
		t.Fatalf("retry restored first config to %q", got)
	}
	if got := readFile(t, second.path); got != string(secondOriginal) {
		t.Fatalf("retry restored second config to %q", got)
	}
	assertRecoveryPairMissing(t, first)
	assertRecoveryPairMissing(t, second)
}

func TestPreparedCleanupFailureRetainsCompleteRecovery(t *testing.T) {
	target, original := recoveryFixture(t)
	change, err := planEntryChange(target, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	change, created, err := change.prepareBackup()
	if err != nil || !created {
		t.Fatalf("prepareBackup() = (%t, %v)", created, err)
	}
	removeFailure := errors.New("injected artifact remove failure")
	removes := 0
	err = removePreparedBackupsWith([]entryChange{change}, func(path string) error {
		removes++
		if removes == 2 {
			return removeFailure
		}
		return os.Remove(path)
	})
	if !errors.Is(err, removeFailure) {
		t.Fatalf("removePreparedBackupsWith() error = %v", err)
	}
	if got := readFile(t, target.path); got != string(original) {
		t.Fatalf("prepared cleanup changed config: %q", got)
	}
	assertRecoveryPairExists(t, target)
	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatalf("retained prepared recovery could not be reused: %v", err)
	}
	if err := revert([]client{target}, false); err != nil {
		t.Fatalf("retained prepared recovery could not be reverted: %v", err)
	}
	if got := readFile(t, target.path); got != string(original) {
		t.Fatalf("reused recovery restored %q", got)
	}
}

func TestEntryRollbackRemovesRecoveryAfterStateRewrites(t *testing.T) {
	first, firstOriginal := recoveryFixture(t)
	first.name = "first"
	second, _ := recoveryFixture(t)
	second.name = "second"
	changes := make([]entryChange, 0, 2)
	prepared := make([]entryChange, 0, 2)
	for _, target := range []client{first, second} {
		change, err := planEntryChange(target, devEntry())
		if err != nil {
			t.Fatal(err)
		}
		change, created, err := change.prepareBackup()
		if err != nil || !created {
			t.Fatalf("prepareBackup(%s) = (%t, %v)", target.name, created, err)
		}
		changes = append(changes, change)
		prepared = append(prepared, change)
	}
	committed, err := changes[0].commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second.path, []byte(`{"raced":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := changes[1].commit(); err == nil {
		t.Fatal("second commit accepted a changed configuration")
	}
	if err := rollbackEntryChanges(
		[]entryChange{committed}, prepared, errors.New("second commit failed"),
	); err == nil {
		t.Fatal("rollback discarded the initiating failure")
	}
	if got := readFile(t, first.path); got != string(firstOriginal) {
		t.Fatalf("first config was not rolled back: %q", got)
	}
	if got := readFile(t, second.path); got != `{"raced":true}` {
		t.Fatalf("second config changed after its refusal: %q", got)
	}
	assertRecoveryPairMissing(t, first)
	assertRecoveryPairMissing(t, second)
}

func recoveryFixture(t *testing.T) (client, []byte) {
	t.Helper()
	target := client{
		name: "fixture", path: filepath.Join(t.TempDir(), "config.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	original := []byte(`{"mcpServers":{"other":{"command":"keep"}}}`)
	if err := os.WriteFile(target.path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	return target, original
}

func replaceFileAtSamePath(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(filepath.Dir(path), ".replacement")
	if err := os.WriteFile(temporary, contents, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func appendToFile(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, ' '), info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func assertRecoveryPairExists(t *testing.T, target client) {
	t.Helper()
	for _, path := range []string{backupPath(target), recoveryStatePath(target)} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("recovery artifact %s = (%v, %v)", filepath.Base(path), info, err)
		}
	}
}

func assertRecoveryPairMissing(t *testing.T, target client) {
	t.Helper()
	for _, path := range []string{backupPath(target), recoveryStatePath(target)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("recovery artifact %s remains: %v", filepath.Base(path), err)
		}
	}
}
