package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

type nativeSnapshot struct {
	Contents []byte
	Binding  destinationBinding
}

type nativeUsePlan struct {
	Target         client
	Key            string
	Sequence       uint64
	Original       nativeSnapshot
	Updated        []byte
	Spec           processSpec
	Action         string
	BackupPath     string
	Existing       *nativeLedgerEntry
	BackupRewrites []nativeBackupRewrite
}

type nativeBackupRewrite struct {
	Key      string
	Entry    nativeLedgerEntry
	Snapshot nativeSnapshot
	Updated  []byte
}

type nativeRevertPlan struct {
	Target client
	Key    string
	Entry  nativeLedgerEntry
	Route  nativeSnapshot
	Backup nativeSnapshot
}

type nativeOperation struct {
	label       string
	destination string
	logical     string
	prior       *nativeSnapshot
	staged      *stagedFile
	held        *fileMove
	published   bool
}

func nativeUse(
	allClients, selectedClients []client,
	entry entryPlan,
	chosen options,
	repository string,
) (err error) {
	targets, err := scopedClients(selectedClients, chosen.scope, repository)
	if err != nil {
		return err
	}
	observedLock, err := inspectTransactionLockClaim()
	if err != nil {
		return err
	}
	ledger, _, err := loadNativeLedger()
	if err != nil {
		return err
	}
	preflighted, err := planNativeUse(targets, entry.configured, ledger)
	if err != nil {
		return err
	}
	if len(preflighted) == 0 {
		return errors.New("no selected configuration files exist")
	}
	if err := validateNativeAliases(allClients, ledger, nativeUseBackupPaths(preflighted), observedLock); err != nil {
		return err
	}
	if chosen.dryRun {
		printNativeUse(preflighted, true)
		return nil
	}
	if err := entry.install(); err != nil {
		return fmt.Errorf("provision server: %w", err)
	}
	if !chosen.noPreflight {
		if err := preflightNativeUse(preflighted, entry); err != nil {
			return err
		}
	}

	lock, err := acquireTransactionLock()
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, lock.close())
		}
	}()
	lockedLedger, state, err := loadNativeLedger()
	if err != nil {
		return err
	}
	plans, err := planNativeUse(targets, entry.configured, lockedLedger)
	if err != nil {
		return err
	}
	if !sameNativeUseSpecs(preflighted, plans) {
		return errors.New("configuration changed after preflight; retry the swap")
	}
	if err := validateNativeAliases(allClients, lockedLedger, nativeUseBackupPaths(plans), lockClaim(lock.binding)); err != nil {
		return err
	}
	if err := ensureNativeStateDirectory(); err != nil {
		return err
	}
	operations, err := stageNativeUse(lock, plans, lockedLedger, state)
	if err != nil {
		return err
	}
	if err := runNativeOperations(lock, operations); err != nil {
		return err
	}
	closeErr := lock.close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	printNativeUse(plans, false)
	return nil
}

func nativeRevert(
	allClients, selectedClients []client,
	chosen options,
	repository string,
) (err error) {
	observedLock, err := inspectTransactionLockClaim()
	if err != nil {
		return err
	}
	ledger, _, err := loadNativeLedger()
	if err != nil {
		return err
	}
	plans, err := planNativeRevert(selectedClients, chosen.scope, repository, ledger)
	if err != nil {
		return err
	}
	if err := validateNativeAliases(allClients, ledger, nil, observedLock); err != nil {
		return err
	}
	if chosen.dryRun {
		printNativeRevert(plans, true)
		return nil
	}
	lock, err := acquireTransactionLock()
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, lock.close())
		}
	}()
	lockedLedger, state, err := loadNativeLedger()
	if err != nil {
		return err
	}
	if state == nil {
		return errors.New("no recorded swaps to revert")
	}
	plans, err = planNativeRevert(selectedClients, chosen.scope, repository, lockedLedger)
	if err != nil {
		return err
	}
	if err := validateNativeAliases(allClients, lockedLedger, nil, lockClaim(lock.binding)); err != nil {
		return err
	}
	operations, err := stageNativeRevert(lock, plans, lockedLedger, state)
	if err != nil {
		return err
	}
	if err := runNativeOperations(lock, operations); err != nil {
		return err
	}
	closeErr := lock.close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	printNativeRevert(plans, false)
	return nil
}

func planNativeUse(
	clients []client,
	configured map[string]any,
	ledger nativeLedger,
) ([]nativeUsePlan, error) {
	plans := make([]nativeUsePlan, 0, len(clients))
	next := ledger.NextSequence
	for _, target := range clients {
		contents, binding, err := readNativeConfig(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.name, err)
		}
		before, present, err := entryFromContents(target, contents)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.name, err)
		}
		updated, err := renderEntryChange(target, contents, configured)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.name, err)
		}
		finalEntry, finalPresent, err := entryFromContents(target, updated)
		if err != nil || !finalPresent {
			return nil, fmt.Errorf("%s: rendered server entry: %w", target.name,
				errors.Join(err, errors.New("entry is missing")))
		}
		spec, err := processSpecFromEntry(finalEntry)
		if err != nil {
			return nil, fmt.Errorf("%s: rendered server entry: %w", target.name, err)
		}
		key := nativeStateKey(target.name, target.scope)
		existing, exists := ledger.Entries[key]
		sequence := next
		backup := nativeBackupPath(binding.Resolved, sequence)
		var existingPointer *nativeLedgerEntry
		var rewrites []nativeBackupRewrite
		if exists {
			existingCopy := existing
			existingPointer = &existingCopy
			sequence = existing.Sequence
			backup = existing.BackupPath
			rewrites, err = planNativeBackupRewrites(target, configured, ledger, existing, contents, binding)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", target.name, err)
			}
		} else {
			if next == math.MaxUint64 {
				return nil, errors.New("go recovery sequence is exhausted")
			}
			if _, err := os.Lstat(backup); err == nil {
				return nil, fmt.Errorf("%s: backup destination already exists", target.name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%s: inspect backup: %w", target.name, err)
			}
			next++
		}
		action := "added"
		if present && before != nil {
			action = "replaced"
		}
		plans = append(plans, nativeUsePlan{
			Target: target, Key: key, Sequence: sequence,
			Original: nativeSnapshot{Contents: contents, Binding: binding},
			Updated:  updated, Spec: spec, Action: action, BackupPath: backup,
			Existing: existingPointer, BackupRewrites: rewrites,
		})
	}
	return plans, validateNativeLedgerArtifacts(ledger)
}

func planNativeBackupRewrites(
	target client,
	configured map[string]any,
	ledger nativeLedger,
	existing nativeLedgerEntry,
	current []byte,
	binding destinationBinding,
) ([]nativeBackupRewrite, error) {
	if existing.ConfigPath != target.path || !sameTopology(existing.Route, binding) {
		return nil, errors.New("configuration route changed")
	}
	newer := make([]struct {
		key   string
		entry nativeLedgerEntry
	}, 0, 1)
	newest := existing
	for key, candidate := range ledger.Entries {
		if candidate.Route.Resolved == existing.Route.Resolved && candidate.Sequence > existing.Sequence {
			newer = append(newer, struct {
				key   string
				entry nativeLedgerEntry
			}{key, candidate})
			if candidate.Sequence > newest.Sequence {
				newest = candidate
			}
		}
	}
	if !newest.Expected.matches(current, binding) {
		return nil, errors.New("configuration changed since mcp-swap wrote its newest layer")
	}
	sort.Slice(newer, func(left, right int) bool {
		return newer[left].entry.Sequence < newer[right].entry.Sequence
	})
	rewrites := make([]nativeBackupRewrite, 0, len(newer))
	for _, item := range newer {
		backup, err := readNativeBackup(item.entry)
		if err != nil {
			return nil, err
		}
		updated, err := renderEntryChange(target, backup.Contents, configured)
		if err != nil {
			return nil, fmt.Errorf("recovery chain: %w", err)
		}
		rewrites = append(rewrites, nativeBackupRewrite{
			Key: item.key, Entry: item.entry, Snapshot: backup, Updated: updated,
		})
	}
	return rewrites, nil
}

func planNativeRevert(
	clients []client,
	scope configScope,
	repository string,
	ledger nativeLedger,
) ([]nativeRevertPlan, error) {
	if len(ledger.Entries) == 0 {
		return nil, errors.New("no recorded swaps to revert")
	}
	selected := map[string]client{}
	for _, target := range clients {
		target.repository = filepath.Clean(repository)
		selected[target.name] = target
	}
	entries := make([]struct {
		key   string
		entry nativeLedgerEntry
	}, 0, len(ledger.Entries))
	for key, entry := range ledger.Entries {
		if _, wanted := selected[entry.Client]; !wanted {
			continue
		}
		if entry.Client == "claude" && scope != "" && entry.Scope != scope {
			continue
		}
		entries = append(entries, struct {
			key   string
			entry nativeLedgerEntry
		}{key, entry})
	}
	if len(entries) == 0 {
		return nil, errors.New("no recorded swaps match the selection")
	}
	wantedKeys := map[string]bool{}
	for _, item := range entries {
		wantedKeys[item.key] = true
	}
	for _, item := range entries {
		for key, candidate := range ledger.Entries {
			if candidate.Route.Resolved == item.entry.Route.Resolved &&
				candidate.Sequence > item.entry.Sequence && !wantedKeys[key] {
				return nil, errors.New("cannot revert a recovery layer while a newer layer remains")
			}
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].entry.Sequence > entries[right].entry.Sequence
	})
	restored := map[string]struct {
		contents []byte
		mode     uint32
	}{}
	plans := make([]nativeRevertPlan, 0, len(entries))
	for _, item := range entries {
		target := selected[item.entry.Client]
		target.scope = item.entry.Scope
		target.repository = item.entry.Repository
		contents, binding, err := readNativeConfig(target.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.key, err)
		}
		if target.path != item.entry.ConfigPath || !sameTopology(binding, item.entry.Route) {
			return nil, fmt.Errorf("%s: configuration route changed", item.key)
		}
		if prior, found := restored[binding.Resolved]; found {
			if !item.entry.Expected.matchesContent(prior.contents, prior.mode) {
				return nil, fmt.Errorf("%s: recovery chain changed", item.key)
			}
		} else if !item.entry.Expected.matches(contents, binding) {
			return nil, fmt.Errorf("%s: configuration changed before revert", item.key)
		}
		backup, err := readNativeBackup(item.entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.key, err)
		}
		restored[binding.Resolved] = struct {
			contents []byte
			mode     uint32
		}{backup.Contents, item.entry.OriginalMode}
		plans = append(plans, nativeRevertPlan{
			Target: target, Key: item.key, Entry: item.entry,
			Route: nativeSnapshot{Contents: contents, Binding: binding}, Backup: backup,
		})
	}
	if err := validateNativeLedgerArtifacts(ledger); err != nil {
		return nil, err
	}
	return plans, nil
}

func stageNativeUse(
	lock *transactionLock,
	plans []nativeUsePlan,
	ledger nativeLedger,
	state *nativeStateSnapshot,
) ([]*nativeOperation, error) {
	next := cloneNativeLedger(ledger)
	for _, plan := range plans {
		if plan.Sequence >= next.NextSequence {
			next.NextSequence = plan.Sequence + 1
		}
	}
	operations := make([]*nativeOperation, 0, len(plans)*3+1)
	fail := func(err error) ([]*nativeOperation, error) {
		cleanupNativeStages(operations)
		return nil, err
	}
	configOperations := make([]*nativeOperation, 0, len(plans))
	for _, plan := range plans {
		var backupIdentity nativeFileIdentity
		if plan.Existing == nil {
			operation, err := stageNativeReplacement(
				lock, "backup-"+plan.Key, plan.BackupPath, plan.BackupPath,
				plan.Original.Contents, 0o600, nil,
			)
			if err != nil {
				return fail(err)
			}
			operations = append(operations, operation)
			backupIdentity = nativeIdentity(plan.Original.Contents, operation.futureBinding())
		} else {
			backupIdentity = plan.Existing.Backup
		}
		for _, rewrite := range plan.BackupRewrites {
			prior := rewrite.Snapshot
			operation, err := stageNativeReplacement(
				lock, "chain-backup-"+rewrite.Key, rewrite.Entry.BackupPath,
				rewrite.Entry.BackupPath, rewrite.Updated, 0o600, &prior,
			)
			if err != nil {
				return fail(err)
			}
			operations = append(operations, operation)
			entry := next.Entries[rewrite.Key]
			entry.Backup = nativeIdentity(rewrite.Updated, operation.futureBinding())
			next.Entries[rewrite.Key] = entry
		}
		prior := plan.Original
		operation, err := stageNativeReplacement(
			lock, "config-"+plan.Key, plan.Original.Binding.Resolved,
			plan.Target.path, plan.Updated, plan.Original.Binding.Mode, &prior,
		)
		if err != nil {
			return fail(err)
		}
		configIdentity := nativeIdentity(plan.Updated, operation.futureBinding())
		expectedIdentity := configIdentity
		if len(plan.BackupRewrites) != 0 && plan.Existing != nil {
			expectedIdentity = plan.Existing.Expected
			expectedIdentity.Mode = plan.Existing.OriginalMode
			expectedIdentity.Size = int64(len(plan.BackupRewrites[0].Updated))
			expectedIdentity.SHA256 = digest(plan.BackupRewrites[0].Updated)
		}
		entry := nativeLedgerEntry{
			Client: plan.Target.name, Scope: plan.Target.scope, Sequence: plan.Sequence,
			Server: serverName, Repository: plan.Target.repository,
			ConfigPath: plan.Target.path, BackupPath: plan.BackupPath,
			OriginalMode: plan.Original.Binding.Mode, Route: plan.Original.Binding,
			Backup: backupIdentity, Expected: expectedIdentity, Spec: storedNativeSpec(plan.Spec),
		}
		if plan.Existing != nil {
			entry.OriginalMode = plan.Existing.OriginalMode
			entry.Route = plan.Existing.Route
		}
		next.Entries[plan.Key] = entry
		newestKey := plan.Key
		newestSequence := plan.Sequence
		for key, candidate := range next.Entries {
			if candidate.Route.Resolved == plan.Original.Binding.Resolved && candidate.Sequence > newestSequence {
				newestKey, newestSequence = key, candidate.Sequence
			}
		}
		newest := next.Entries[newestKey]
		newest.Expected = configIdentity
		next.Entries[newestKey] = newest
		configOperations = append(configOperations, operation)
	}
	stateContents, err := encodeNativeLedger(next)
	if err != nil {
		return fail(err)
	}
	var priorState *nativeSnapshot
	if state != nil {
		priorState = &nativeSnapshot{Contents: state.Contents, Binding: state.Binding}
	}
	stateOperation, err := stageNativeReplacement(
		lock, "state", nativeStatePath(), nativeStatePath(), stateContents, 0o600, priorState,
	)
	if err != nil {
		return fail(err)
	}
	operations = append(operations, stateOperation)
	operations = append(operations, configOperations...)
	return operations, nil
}

func stageNativeRevert(
	lock *transactionLock,
	plans []nativeRevertPlan,
	ledger nativeLedger,
	state *nativeStateSnapshot,
) ([]*nativeOperation, error) {
	next := cloneNativeLedger(ledger)
	operations := make([]*nativeOperation, 0, len(plans)*2+1)
	fail := func(err error) ([]*nativeOperation, error) {
		cleanupNativeStages(operations)
		return nil, err
	}
	current := map[string]nativeSnapshot{}
	restored := make([]struct {
		target   string
		sequence uint64
		identity nativeFileIdentity
	}, 0, len(plans))
	for _, plan := range plans {
		prior, found := current[plan.Route.Binding.Resolved]
		if !found {
			prior = plan.Route
		}
		operation, err := stageNativeReplacement(
			lock, "config-"+plan.Key, plan.Route.Binding.Resolved,
			plan.Target.path, plan.Backup.Contents, plan.Entry.OriginalMode, &prior,
			&plan.Route,
		)
		if err != nil {
			return fail(err)
		}
		operations = append(operations, operation)
		future := operation.futureBinding()
		current[plan.Route.Binding.Resolved] = nativeSnapshot{
			Contents: plan.Backup.Contents, Binding: future,
		}
		restored = append(restored, struct {
			target   string
			sequence uint64
			identity nativeFileIdentity
		}{
			plan.Entry.Route.Resolved, plan.Entry.Sequence,
			nativeIdentity(plan.Backup.Contents, future),
		})
		delete(next.Entries, plan.Key)
	}
	for _, item := range restored {
		predecessorKey := ""
		predecessorSequence := uint64(0)
		for key, candidate := range next.Entries {
			if candidate.Route.Resolved == item.target && candidate.Sequence < item.sequence &&
				(predecessorKey == "" || candidate.Sequence > predecessorSequence) {
				predecessorKey, predecessorSequence = key, candidate.Sequence
			}
		}
		if predecessorKey != "" {
			entry := next.Entries[predecessorKey]
			entry.Expected = item.identity
			next.Entries[predecessorKey] = entry
		}
	}
	priorState := &nativeSnapshot{Contents: state.Contents, Binding: state.Binding}
	if len(next.Entries) == 0 {
		operations = append(operations, &nativeOperation{
			label: "state", destination: nativeStatePath(), logical: nativeStatePath(),
			prior: priorState,
		})
	} else {
		stateContents, err := encodeNativeLedger(next)
		if err != nil {
			return fail(err)
		}
		stateOperation, err := stageNativeReplacement(
			lock, "state", nativeStatePath(), nativeStatePath(), stateContents, 0o600, priorState,
		)
		if err != nil {
			return fail(err)
		}
		operations = append(operations, stateOperation)
	}
	for _, plan := range plans {
		backup := plan.Backup
		operations = append(operations, &nativeOperation{
			label: "backup-" + plan.Key, destination: plan.Entry.BackupPath,
			logical: plan.Entry.BackupPath, prior: &backup,
		})
	}
	return operations, nil
}

func stageNativeReplacement(
	lock *transactionLock,
	label, destination, logical string,
	contents []byte,
	mode uint32,
	prior *nativeSnapshot,
	stagingExpected ...*nativeSnapshot,
) (*nativeOperation, error) {
	options := stageOptions{guard: lock.validate}
	expected := prior
	if len(stagingExpected) != 0 {
		expected = stagingExpected[0]
	}
	if expected != nil {
		options.expectedContents = expected.Contents
		options.expectedBinding = &expected.Binding
	}
	staged, err := stageAtomicFile(destination, contents, os.FileMode(mode), options)
	if err != nil {
		return nil, fmt.Errorf("stage %s: %w", label, err)
	}
	return &nativeOperation{
		label: label, destination: staged.target, logical: logical,
		prior: prior, staged: &staged,
	}, nil
}

func (operation *nativeOperation) futureBinding() destinationBinding {
	binding := operation.staged.sourceBinding
	binding.Resolved = operation.destination
	if operation.prior != nil {
		binding.Nodes = append([]topologyNode(nil), operation.prior.Binding.Nodes...)
	}
	return binding
}

func (operation *nativeOperation) commit(lock *transactionLock) error {
	if err := lock.validate(); err != nil {
		return err
	}
	if operation.prior != nil {
		if err := validateBoundContents(
			operation.logical, operation.prior.Contents, operation.prior.Binding,
		); err != nil {
			return fmt.Errorf("%s changed before commit: %w", operation.label, err)
		}
		var journal fileJournal
		if err := journal.take(
			operation.destination, operation.prior.Contents, operation.prior.Binding,
		); err != nil {
			return fmt.Errorf("take aside %s: %w", operation.label, err)
		}
		operation.held = &journal.moves[0]
	} else if operation.staged != nil {
		if err := validateMissingDestination(
			operation.destination, operation.staged.target, operation.staged.targetParent,
		); err != nil {
			return fmt.Errorf("%s destination changed: %w", operation.label, err)
		}
	}
	if operation.staged == nil {
		return nil
	}
	if err := lock.validate(); err != nil {
		return err
	}
	if err := os.Link(operation.staged.temporary, operation.destination); err != nil {
		return fmt.Errorf("publish %s: %w", operation.label, err)
	}
	operation.published = true
	if err := syncDirectory(filepath.Dir(operation.destination)); err != nil {
		return err
	}
	return validatePhysicalFile(
		operation.destination, operation.staged.contents,
		operation.staged.sourceBinding.Target, operation.staged.sourceBinding.Mode,
	)
}

func (operation *nativeOperation) rollback() error {
	var failures []error
	if operation.published {
		if err := validatePhysicalFile(
			operation.destination, operation.staged.contents,
			operation.staged.sourceBinding.Target, operation.staged.sourceBinding.Mode,
		); err != nil {
			return fmt.Errorf("%s changed before rollback: %w", operation.label, err)
		}
		var journal fileJournal
		binding, err := captureDestination(operation.destination)
		if err == nil {
			err = journal.take(operation.destination, operation.staged.contents, binding)
		}
		if err == nil {
			err = journal.commit()
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("remove published %s: %w", operation.label, err))
		} else {
			operation.published = false
		}
	}
	if operation.held != nil {
		if err := operation.held.restore(); err != nil {
			failures = append(failures, fmt.Errorf("restore %s: %w", operation.label, err))
		} else {
			operation.held = nil
		}
	}
	return errors.Join(failures...)
}

func (operation *nativeOperation) verify(lock *transactionLock, destination bool) error {
	if err := lock.validate(); err != nil {
		return err
	}
	if destination && operation.published {
		if err := validatePhysicalFile(
			operation.destination, operation.staged.contents,
			operation.staged.sourceBinding.Target, operation.staged.sourceBinding.Mode,
		); err != nil {
			return fmt.Errorf("verify %s: %w", operation.label, err)
		}
	}
	if destination && operation.staged == nil {
		if _, err := os.Lstat(operation.destination); err == nil {
			return fmt.Errorf("verify deleted %s: destination still exists", operation.label)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("verify deleted %s: %w", operation.label, err)
		}
	}
	if operation.held != nil {
		return operation.held.validate()
	}
	return nil
}

func (operation *nativeOperation) finish() error {
	var failures []error
	if operation.held != nil {
		if err := operation.held.discard(); err != nil {
			failures = append(failures, err)
		} else {
			operation.held = nil
		}
	}
	if operation.staged != nil && operation.staged.temporary != "" {
		if err := removeRecoveryFile(operation.staged.temporary, nil); err != nil {
			failures = append(failures, err)
		} else {
			operation.staged.temporary = ""
		}
	}
	return errors.Join(failures...)
}

func runNativeOperations(lock *transactionLock, operations []*nativeOperation) error {
	committed := 0
	for index, operation := range operations {
		if err := operation.commit(lock); err != nil {
			failures := []error{err}
			for rollback := index; rollback >= 0; rollback-- {
				if rollbackErr := operations[rollback].rollback(); rollbackErr != nil {
					failures = append(failures, rollbackErr)
				}
			}
			cleanupNativeStages(operations)
			return errors.Join(failures...)
		}
		committed++
	}
	last := map[string]int{}
	for index, operation := range operations {
		last[operation.destination] = index
	}
	for index, operation := range operations {
		if err := operation.verify(lock, last[operation.destination] == index); err != nil {
			failures := []error{err}
			for rollback := committed - 1; rollback >= 0; rollback-- {
				if rollbackErr := operations[rollback].rollback(); rollbackErr != nil {
					failures = append(failures, rollbackErr)
				}
			}
			cleanupNativeStages(operations)
			return errors.Join(failures...)
		}
	}
	for _, operation := range operations {
		if err := operation.finish(); err != nil {
			return fmt.Errorf("finish %s: %w", operation.label, err)
		}
	}
	return nil
}

func cleanupNativeStages(operations []*nativeOperation) {
	for _, operation := range operations {
		if operation.staged != nil {
			operation.staged.cleanup()
		}
	}
}

func readNativeConfig(path string) ([]byte, destinationBinding, error) {
	contents, binding, err := readBoundFileLimit(path, nativeConfigLimit+1)
	if len(contents) > nativeConfigLimit {
		return nil, destinationBinding{}, errors.New("configuration exceeds its size limit")
	}
	return contents, binding, err
}

func readNativeBackup(entry nativeLedgerEntry) (nativeSnapshot, error) {
	info, err := os.Lstat(entry.BackupPath)
	if err != nil {
		return nativeSnapshot{}, err
	}
	links, linksErr := fileLinkCount(info)
	if !info.Mode().IsRegular() || !exactMode(info, 0o600) ||
		linksErr != nil || links != 1 || !fileOwnedByCurrentUser(info) {
		return nativeSnapshot{}, errors.New("backup must be an owned 0600 regular file with one link")
	}
	contents, binding, err := readNativeConfig(entry.BackupPath)
	if err != nil {
		return nativeSnapshot{}, err
	}
	if !entry.Backup.matches(contents, binding) {
		return nativeSnapshot{}, errors.New("backup changed after it was recorded")
	}
	return nativeSnapshot{Contents: contents, Binding: binding}, nil
}

func validateNativeLedgerArtifacts(ledger nativeLedger) error {
	for key, entry := range ledger.Entries {
		if _, err := readNativeBackup(entry); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func validateNativeAliases(
	clients []client,
	ledger nativeLedger,
	extra []string,
	lock pathClaim,
) error {
	claims := []pathClaim{lock}
	add := func(owner, kind, path string) error {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		absolute = filepath.Clean(absolute)
		claim, err := nativePathClaim(owner, kind, absolute)
		if err == nil {
			claims = append(claims, claim)
		}
		return err
	}
	for _, target := range clients {
		if err := add(target.name, "configuration", target.path); err != nil {
			return err
		}
	}
	if err := add("go", "recovery state", nativeStatePath()); err != nil {
		return err
	}
	for key, entry := range ledger.Entries {
		if err := add(key, "backup", entry.BackupPath); err != nil {
			return err
		}
	}
	for _, path := range extra {
		if err := add("new", "backup", path); err != nil {
			return err
		}
	}
	return validateDistinctPathClaims(claims)
}

func nativePathClaim(owner, kind, path string) (pathClaim, error) {
	if _, err := os.Lstat(path); err == nil {
		binding, err := captureDestination(path)
		if err != nil {
			return pathClaim{}, err
		}
		return pathClaim{owner: owner, kind: kind, resolved: binding.Resolved, target: binding.Target}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return pathClaim{}, err
	}
	resolved, err := resolveProspectivePath(path)
	return pathClaim{owner: owner, kind: kind, resolved: resolved}, err
}

func ensureNativeStateDirectory() error {
	path := nativeStateDirectory()
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateNativeStateDirectory(path)
}

func validateNativeStateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || !exactMode(info, 0o700) || !fileOwnedByCurrentUser(info) {
		return errors.New("go recovery directory must be an owned mode 0700 directory")
	}
	return nil
}

func nativeBackupPath(target string, sequence uint64) string {
	return fmt.Sprintf("%s.bak.mcp-swap-go-%020d", target, sequence)
}

func nativeUseBackupPaths(plans []nativeUsePlan) []string {
	paths := make([]string, 0, len(plans))
	for _, plan := range plans {
		if plan.Existing == nil {
			paths = append(paths, plan.BackupPath)
		}
	}
	return paths
}

func cloneNativeLedger(ledger nativeLedger) nativeLedger {
	cloned := nativeLedger{NextSequence: ledger.NextSequence, Entries: map[string]nativeLedgerEntry{}}
	for key, entry := range ledger.Entries {
		entry.Route.Nodes = append([]topologyNode(nil), entry.Route.Nodes...)
		entry.Spec.Args = append([]string{}, entry.Spec.Args...)
		entry.Spec.Env = cloneStringMap(entry.Spec.Env)
		cloned.Entries[key] = entry
	}
	return cloned
}

func sameNativeUseSpecs(before, after []nativeUsePlan) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index].Key != after[index].Key || !before[index].Spec.equal(after[index].Spec) {
			return false
		}
	}
	return true
}

func preflightNativeUse(plans []nativeUsePlan, entry entryPlan) error {
	if len(plans) == 0 {
		spec, err := processSpecFromEntry(entry.configured)
		if err != nil {
			return err
		}
		if entry.preflightCommand != "" {
			spec.command = entry.preflightCommand
		}
		if reason := preflightSpec(spec); reason != "" {
			return fmt.Errorf("preflight failed, nothing written: %s", reason)
		}
		return nil
	}
	seen := []processSpec{}
	var failures []error
	for _, plan := range plans {
		spec := plan.Spec
		if entry.preflightCommand != "" {
			spec.command = entry.preflightCommand
		}
		if slices.ContainsFunc(seen, spec.equal) {
			continue
		}
		seen = append(seen, spec)
		fmt.Fprintf(os.Stderr, "preflight: %s: %s\n", plan.Key, spec.describe())
		if reason := preflightSpec(spec); reason != "" {
			failures = append(failures, fmt.Errorf("%s: %s", plan.Key, reason))
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("preflight failed, nothing written: %w", errors.Join(failures...))
	}
	return nil
}

func printNativeUse(plans []nativeUsePlan, dryRun bool) {
	for _, plan := range plans {
		verb := "now runs"
		if dryRun {
			verb = "would run"
		}
		fmt.Printf("%-16s %s %s\n", plan.Key, verb, plan.Spec.describe())
	}
}

func printNativeRevert(plans []nativeRevertPlan, dryRun bool) {
	for _, plan := range plans {
		verb := "restored from"
		if dryRun {
			verb = "would restore"
		}
		fmt.Printf("%-16s %s %s\n", plan.Key, verb, filepath.Base(plan.Entry.BackupPath))
	}
}

func nativeLabel(client string, scope configScope) string {
	if client == "claude" {
		return nativeStateKey(client, scope)
	}
	return client
}
