package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagedFilePreservesLatePathReplacements(t *testing.T) {
	t.Run("before staging", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		original, binding, err := readBoundFile(target)
		if err != nil {
			t.Fatal(err)
		}
		human := []byte("human replacement")
		replaceContentsAtSamePath(t, target, human)
		if _, err := stageAtomicFile(target, []byte("updated"), 0o600, stageOptions{
			expectedContents: original, expectedBinding: &binding,
		}); err == nil {
			t.Fatal("staging accepted an already-replaced destination")
		}
		if got := []byte(readFile(t, target)); !bytes.Equal(got, human) {
			t.Fatalf("refused staging changed target to %q", got)
		}
	})

	t.Run("source", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "config.json")
		original := []byte("original")
		if err := os.WriteFile(target, original, 0o600); err != nil {
			t.Fatal(err)
		}
		staged, err := stageAtomicFile(target, []byte("updated"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		stagedPath := staged.temporary
		human := []byte("human replacement")
		replaceContentsAtSamePath(t, stagedPath, human)
		humanIdentity, err := physicalIdentityAt(stagedPath, true)
		if err != nil {
			t.Fatal(err)
		}

		published, err := staged.publish()
		if err == nil || published {
			t.Fatalf("publish() = (%t, %v), want refusal", published, err)
		}
		if got := []byte(readFile(t, target)); !bytes.Equal(got, original) {
			t.Fatalf("refused publish changed target to %q", got)
		}
		assertFileIdentityAndContents(t, stagedPath, humanIdentity, human)
	})

	t.Run("existing destination", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "config.json")
		if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		staged, err := stageAtomicFile(target, []byte("updated"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer staged.cleanup()
		human := []byte("human replacement")
		replaceContentsAtSamePath(t, target, human)
		humanIdentity, err := physicalIdentityAt(target, true)
		if err != nil {
			t.Fatal(err)
		}

		published, err := staged.publish()
		if err == nil || published {
			t.Fatalf("publish() = (%t, %v), want refusal", published, err)
		}
		assertFileIdentityAndContents(t, target, humanIdentity, human)
	})

	t.Run("post-link guard refusal", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "config.json")
		original, updated := []byte("original"), []byte("updated")
		if err := os.WriteFile(target, original, 0o600); err != nil {
			t.Fatal(err)
		}
		originalIdentity, err := physicalIdentityAt(target, true)
		if err != nil {
			t.Fatal(err)
		}
		refusal := errors.New("held lock changed")
		guard := func() error {
			current, _ := os.ReadFile(target)
			if bytes.Equal(current, updated) {
				return refusal
			}
			return nil
		}
		staged, err := stageAtomicFile(
			target, updated, 0o600, stageOptions{guard: guard},
		)
		if err != nil {
			t.Fatal(err)
		}
		published, err := staged.publish()
		if !errors.Is(err, refusal) || published {
			t.Fatalf("publish() = (%t, %v), want rolled-back refusal", published, err)
		}
		assertFileIdentityAndContents(t, target, originalIdentity, original)
		staged.cleanup()
		assertNoTransactionResidue(t, directory)
	})

	t.Run("post-link destination", func(t *testing.T) {
		target, original := recoveryFixture(t)
		originalIdentity, err := physicalIdentityAt(target.path, true)
		if err != nil {
			t.Fatal(err)
		}
		change, err := planEntryChange(target, devEntry())
		if err != nil {
			t.Fatal(err)
		}
		human := []byte(`{"human":true}`)
		var humanIdentity physicalIdentity
		change.recovery.guard = func() error {
			current, _ := os.ReadFile(target.path)
			if humanIdentity == (physicalIdentity{}) && bytes.Equal(current, change.updated) {
				replaceContentsAtSamePath(t, target.path, human)
				humanIdentity, _ = physicalIdentityAt(target.path, true)
			}
			return nil
		}
		if err := applyEntryChanges([]entryChange{change}); err == nil {
			t.Fatal("apply accepted a post-link destination replacement")
		}
		assertFileIdentityAndContents(t, target.path, humanIdentity, human)
		assertIdentityRetained(
			t, filepath.Dir(target.path), originalIdentity, original, 0o640,
		)
		assertRecoveryPairExists(t, target)
	})

	t.Run("missing destination", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "state.json")
		staged, err := stageAtomicFile(target, []byte("updated"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer staged.cleanup()
		human := []byte("human replacement")
		if err := os.WriteFile(target, human, 0o600); err != nil {
			t.Fatal(err)
		}
		humanIdentity, err := physicalIdentityAt(target, true)
		if err != nil {
			t.Fatal(err)
		}

		published, err := staged.publish()
		if err == nil || published {
			t.Fatalf("publish() = (%t, %v), want refusal", published, err)
		}
		assertFileIdentityAndContents(t, target, humanIdentity, human)
	})
}

func TestStagedCleanupPreservesAReplacedSource(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.json")
	staged, err := stageAtomicFile(target, []byte("staged"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	stagedPath := staged.temporary
	human := []byte("human replacement")
	replaceContentsAtSamePath(t, stagedPath, human)
	humanIdentity, err := physicalIdentityAt(stagedPath, true)
	if err != nil {
		t.Fatal(err)
	}

	staged.cleanup()

	assertFileIdentityAndContents(t, stagedPath, humanIdentity, human)
}

func TestRecoveryCleanupPreservesALateReplacement(t *testing.T) {
	target, _ := recoveryFixture(t)
	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	change, exists, err := planRestore(target)
	if err != nil || !exists {
		t.Fatalf("planRestore() = (%t, %v)", exists, err)
	}
	human := []byte("human replacement")
	var humanIdentity physicalIdentity
	replaced := false
	_, err = removeRecoveryArtifacts(
		[]namedRecovery{{name: target.name, recovery: change.recovery}},
		func(path string) error {
			if !replaced {
				replaced = true
				replaceContentsAtSamePath(t, path, human)
				var identityErr error
				humanIdentity, identityErr = physicalIdentityAt(path, true)
				return identityErr
			}
			return nil
		},
	)
	if err == nil {
		t.Fatal("cleanup accepted a recovery artifact replaced after planning")
	}
	assertFileIdentityAndContents(
		t, change.recovery.stateBinding.Resolved, humanIdentity, human,
	)
	if _, statErr := os.Stat(change.recovery.backupBinding.Resolved); statErr != nil {
		t.Fatalf("cleanup removed the retained backup: %v", statErr)
	}
}

func TestTransactionLockAliasesStopAllWrites(t *testing.T) {
	for _, test := range []struct {
		name, artifact, command, link string
		priorSwap                     bool
	}{
		{"config hardlink", "config", "use", "hardlink", false},
		{"config symlink dry-run", "config", "dry-run", "symlink", false},
		{"backup hardlink", "backup", "use", "hardlink", true},
		{"state hardlink revert", "state", "revert", "hardlink", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, _ := recoveryFixture(t)
			if err := os.Chmod(target.path, 0o600); err != nil {
				t.Fatal(err)
			}
			if test.priorSwap {
				if err := writeEntry(target, devEntry()); err != nil {
					t.Fatal(err)
				}
			}
			aliased := target.path
			switch test.artifact {
			case "backup":
				aliased = backupPath(target)
			case "state":
				aliased = recoveryStatePath(target)
			}
			lockPath := isolatedLockPath(t)
			if test.link == "symlink" {
				if err := os.Symlink(aliased, lockPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Link(aliased, lockPath); err != nil {
				t.Fatal(err)
			}
			before := []byte(readFile(t, target.path))
			var err error
			if test.command == "revert" {
				err = revert([]client{target}, false)
			} else {
				err = useLocal(
					[]client{target}, devEntry(), test.command == "dry-run",
				)
			}
			if err == nil {
				t.Fatalf("%s accepted a lock aliased to its %s", test.command, test.artifact)
			}
			if got := []byte(readFile(t, target.path)); !bytes.Equal(got, before) {
				t.Fatalf("refused %s changed config to %q", test.command, got)
			}
			if test.priorSwap {
				assertRecoveryPairExists(t, target)
			} else {
				assertPathMissing(t, backupPath(target))
				assertPathMissing(t, recoveryStatePath(target))
			}
		})
	}
}

func TestDryRunDoesNotCreateATransactionLock(t *testing.T) {
	target, original := recoveryFixture(t)
	stateRoot := filepath.Join(t.TempDir(), "absent-state")
	t.Setenv("XDG_STATE_HOME", stateRoot)

	if err := useLocal([]client{target}, devEntry(), true); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, target.path)); !bytes.Equal(got, original) {
		t.Fatalf("dry run changed config to %q", got)
	}
	assertPathMissing(t, stateRoot)
}

func TestDryRunRefusesAnInsecureLockDirectory(t *testing.T) {
	target, original := recoveryFixture(t)
	lockPath := isolatedLockPath(t)
	if err := os.Chmod(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := useLocal([]client{target}, devEntry(), true); err == nil {
		t.Fatal("dry run accepted an insecure transaction lock directory")
	}
	if got := []byte(readFile(t, target.path)); !bytes.Equal(got, original) {
		t.Fatalf("refused dry run changed config to %q", got)
	}
	assertPathMissing(t, lockPath)
}

func TestTransactionLockRetryIsBounded(t *testing.T) {
	_ = isolatedLockPath(t)
	calls := 0
	_, err := acquireTransactionLockWith(
		func(string, bool) (*os.File, bool, error) {
			calls++
			return nil, false, os.ErrExist
		},
	)
	if err == nil || calls != transactionLockAttempts {
		t.Fatalf("acquire after path churn = (%d calls, %v)", calls, err)
	}
}

func TestTransactionRefusesAReplacedHeldLock(t *testing.T) {
	target, original := recoveryFixture(t)
	lockPath := isolatedLockPath(t)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	human := []byte("human replacement")
	plan := entryPlan{
		configured: devEntry(),
		install: func() error {
			replaceContentsAtSamePath(t, lockPath, human)
			return nil
		},
		cleanup: func() {},
	}

	if err := usePreparedLocal([]client{target}, plan, false, false); err == nil {
		t.Fatal("use-local accepted a replaced transaction lock")
	}
	if got := []byte(readFile(t, target.path)); !bytes.Equal(got, original) {
		t.Fatalf("refused use changed config to %q", got)
	}
	if got := []byte(readFile(t, lockPath)); !bytes.Equal(got, human) {
		t.Fatalf("refused use changed replacement lock to %q", got)
	}
	assertPathMissing(t, backupPath(target))
	assertPathMissing(t, recoveryStatePath(target))
}

func TestHeldLockIsRecheckedAtConfigPublication(t *testing.T) {
	target, original := recoveryFixture(t)
	lockPath := isolatedLockPath(t)
	lock, err := acquireTransactionLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.close(); err != nil {
			t.Error(err)
		}
	})
	change, err := planEntryChange(target, devEntry())
	if err != nil {
		t.Fatal(err)
	}
	change.recovery.guard = lock.validate
	change, created, err := change.prepareBackup()
	if err != nil || !created {
		t.Fatalf("prepareBackup() = (%t, %v)", created, err)
	}
	human := []byte("human replacement")
	_, err = change.commitWith(func(recoveryState) error {
		replaceContentsAtSamePath(t, lockPath, human)
		return nil
	})
	if err == nil {
		t.Fatal("commit accepted a replaced held lock")
	}
	if got := []byte(readFile(t, target.path)); !bytes.Equal(got, original) {
		t.Fatalf("refused commit changed config to %q", got)
	}
	if got := []byte(readFile(t, lockPath)); !bytes.Equal(got, human) {
		t.Fatalf("refused commit changed replacement lock to %q", got)
	}
	current, binding, err := readBoundFile(target.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryFor(target, current, binding); err != nil {
		t.Fatalf("refused commit left invalid recovery: %v", err)
	}
}

func TestConcurrentSwapsSerializeAndReplan(t *testing.T) {
	target, original := recoveryFixture(t)
	lockPath := isolatedLockPath(t)
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	first := entryPlan{
		configured: devEntry(),
		install: func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		},
		cleanup: func() {},
	}
	second := entryPlan{
		configured: devEntry(),
		install: func() error {
			close(secondEntered)
			return nil
		},
		cleanup: func() {},
	}

	go func() {
		firstDone <- usePreparedLocal([]client{target}, first, false, false)
	}()
	<-firstEntered
	go func() {
		secondDone <- usePreparedLocal([]client{target}, second, false, false)
	}()
	select {
	case <-secondEntered:
		close(releaseFirst)
		<-firstDone
		<-secondDone
		t.Fatal("second swap entered its mutation plan while the first held the transaction")
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first use-local: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second use-local: %v", err)
	}
	if err := revert([]client{target}, false); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := []byte(readFile(t, target.path)); !bytes.Equal(got, original) {
		t.Fatalf("revert restored %q", got)
	}
	assertRecoveryPairMissing(t, target)
	info, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	links, err := fileLinkCount(info)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 {
		t.Fatalf("persistent lock = (%s, %o, %d links)", info.Mode(), info.Mode().Perm(), links)
	}
	assertNoTransactionResidue(t, filepath.Dir(target.path), filepath.Dir(lockPath))
}

func replaceContentsAtSamePath(t *testing.T, path string, contents []byte) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := os.CreateTemp(filepath.Dir(path), ".human-*")
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := replacement.Name()
	if _, err := replacement.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Chmod(info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, path); err != nil {
		t.Fatal(err)
	}
}

func assertFileIdentityAndContents(
	t *testing.T,
	path string,
	wantIdentity physicalIdentity,
	wantContents []byte,
) {
	t.Helper()
	gotIdentity, err := physicalIdentityAt(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if gotIdentity != wantIdentity {
		t.Fatalf("%s identity = %+v, want %+v", path, gotIdentity, wantIdentity)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantContents) {
		t.Fatalf("%s contents = %q, want %q", path, got, wantContents)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s remains: %v", path, err)
	}
}

func isolatedLockPath(t *testing.T) string {
	t.Helper()
	stateRoot := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	directory := filepath.Join(stateRoot, "libtmux-go")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "mcp-swap.lock")
}

func assertNoTransactionResidue(t *testing.T, roots ...string) {
	t.Helper()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path != root && bytes.Contains([]byte(entry.Name()), []byte(".mcp-swap-hold-")) {
				t.Errorf("transaction residue remains: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func assertIdentityRetained(
	t *testing.T,
	root string,
	want physicalIdentity,
	wantContents []byte,
	wantMode os.FileMode,
) {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		identity, err := physicalIdentityAt(path, true)
		if err == nil && identity == want {
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				return statErr
			}
			if !bytes.Equal(contents, wantContents) || info.Mode().Perm() != wantMode {
				t.Errorf("retained configuration contents or mode changed")
			}
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("prior configuration inode was not retained")
	}
}
