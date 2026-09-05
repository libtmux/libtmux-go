package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const piAdapterHint = "needs the pi-mcp-adapter package; pi has no built-in MCP client"

func report(clients []client) error {
	return reportTo(os.Stdout, clients)
}

func reportTo(output io.Writer, clients []client) error {
	for _, c := range clients {
		caveat := clientCaveat(c)
		entry, present, err := entryOf(c)
		var line string
		switch {
		case errors.Is(err, os.ErrNotExist):
			line = fmt.Sprintf("%-12s not installed%s", c.name, caveat)
		case err != nil:
			line = fmt.Sprintf("%-12s unreadable: %v%s", c.name, err, caveat)
		default:
			switch {
			case !present:
				line = fmt.Sprintf("%-12s no %q server%s", c.name, serverName, caveat)
			case isLocal(entry):
				mode, _ := swapMode(entry)
				line = fmt.Sprintf("%-12s %s: %s%s", c.name, mode, describe(entry), caveat)
			default:
				line = fmt.Sprintf("%-12s %s%s", c.name, describe(entry), caveat)
			}
		}
		if _, err := fmt.Fprintln(output, line); err != nil {
			return err
		}
	}
	return nil
}

func clientCaveat(c client) string {
	if c.name != "pi" {
		return ""
	}
	adapter := filepath.Join(filepath.Dir(c.path), "npm", "node_modules", "pi-mcp-adapter")
	if info, err := os.Stat(adapter); err == nil && info.IsDir() {
		return ""
	}
	return " -- " + piAdapterHint
}

// useLocal plans the complete client selection before applying any change.
func useLocal(clients []client, entry map[string]any, dryRun bool) error {
	return usePreparedLocal(clients, entryPlan{
		configured: entry,
		install:    func() error { return nil },
		cleanup:    func() {},
	}, dryRun, false)
}

// usePreparedLocal renders every client entry and checks every new backup
// destination before preflight. This validates the command, arguments, and
// environment that each client will actually use.
func usePreparedLocal(clients []client, plan entryPlan, dryRun, check bool) error {
	var failures []error
	changes := make([]entryChange, 0, len(clients))
	for _, c := range clients {
		change, err := planEntryChange(c, plan.configured)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not changed: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		changes = append(changes, change)
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	if err := validateDistinctTargets(changes); err != nil {
		return err
	}

	if check && !dryRun {
		if err := preflightEntryChanges(changes, plan); err != nil {
			failures = append(failures, err)
			return errors.Join(failures...)
		}
	}
	if !dryRun {
		if err := plan.install(); err != nil {
			return fmt.Errorf("install build: %w", err)
		}
		if err := validateEntryChanges(changes); err != nil {
			return err
		}
	}

	if !dryRun {
		if err := applyEntryChanges(changes); err != nil {
			return err
		}
	}
	for _, change := range changes {
		c := change.target
		if dryRun {
			fmt.Printf("%-12s would run %s\n", c.name, change.spec.describe())
			continue
		}
		fmt.Printf("%-12s now runs %s\n", c.name, change.spec.describe())
	}
	return errors.Join(failures...)
}

func preflightEntryChanges(changes []entryChange, plan entryPlan) error {
	if len(changes) == 0 {
		spec, err := processSpecFromEntry(plan.configured)
		if err != nil {
			return fmt.Errorf("preflight failed, nothing written: %w", err)
		}
		if plan.preflightCommand != "" {
			spec.command = plan.preflightCommand
		}
		fmt.Fprintf(os.Stderr, "preflight: %s\n", spec.describe())
		if reason := preflightSpec(spec); reason != "" {
			return fmt.Errorf("preflight failed, nothing written: %s", reason)
		}
		return nil
	}

	var failures []error
	for _, candidate := range distinctProcessSpecs(changes, plan.preflightCommand) {
		fmt.Fprintf(os.Stderr, "preflight: %s: %s\n",
			strings.Join(candidate.clients, ", "), candidate.spec.describe())
		if reason := preflightSpec(candidate.spec); reason != "" {
			failures = append(failures, fmt.Errorf(
				"%s preflight failed, nothing written: %s",
				strings.Join(candidate.clients, ", "), reason,
			))
		}
	}
	return errors.Join(failures...)
}

type clientProcessSpec struct {
	clients []string
	spec    processSpec
}

func distinctProcessSpecs(changes []entryChange, command string) []clientProcessSpec {
	distinct := make([]clientProcessSpec, 0, len(changes))
	for _, change := range changes {
		spec := change.spec
		if command != "" {
			spec.command = command
		}
		matched := false
		for index := range distinct {
			if distinct[index].spec.equal(spec) {
				distinct[index].clients = append(distinct[index].clients, change.target.name)
				matched = true
				break
			}
		}
		if !matched {
			distinct = append(distinct, clientProcessSpec{
				clients: []string{change.target.name},
				spec:    spec,
			})
		}
	}
	return distinct
}

// revert plans the complete selection before restoring any configuration.
func revert(clients []client, dryRun bool) error {
	var failures []error
	changes := make([]restoreChange, 0, len(clients))
	for _, c := range clients {
		change, exists, err := planRestore(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		if !exists {
			continue
		}
		changes = append(changes, change)
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	if err := validateDistinctRestoreTargets(changes); err != nil {
		return err
	}
	if dryRun {
		for _, change := range changes {
			fmt.Printf("%-12s would restore %s\n",
				change.target.name, filepath.Base(change.recovery.backup))
		}
		return nil
	}
	if err := applyRestoreChanges(changes); err != nil {
		return err
	}
	for _, change := range changes {
		fmt.Printf("%-12s restored from %s\n",
			change.target.name, filepath.Base(change.recovery.backup))
	}
	return nil
}

type restoreChange struct {
	target            client
	current, restored []byte
	binding           destinationBinding
	recovery          recoveryPlan
}

func planRestore(c client) (restoreChange, bool, error) {
	backup := backupPath(c)
	state := recoveryStatePath(c)
	backupExists, err := regularFileExists(backup)
	if err != nil {
		return restoreChange{}, false, err
	}
	stateExists, err := regularFileExists(state)
	if err != nil {
		return restoreChange{}, false, err
	}
	if !backupExists && !stateExists {
		return restoreChange{}, false, nil
	}
	if backupExists != stateExists {
		return restoreChange{}, false, errors.New("backup and recovery state are incomplete")
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		return restoreChange{}, false, err
	}
	current, binding, err := readBoundFile(c.path)
	if err != nil {
		return restoreChange{}, false, err
	}
	recovery, err := recoveryFor(c, current, binding)
	if err != nil {
		return restoreChange{}, false, err
	}
	entry, present, err := entryFromContents(c, current)
	if err != nil {
		return restoreChange{}, false, err
	}
	if !present || !isLocal(entry) {
		return restoreChange{}, false,
			errors.New("the current server entry is no longer the one mcp-swap wrote")
	}
	// A normal revert can restore the recovery artifact verbatim. If something
	// else changed after the swap, retain the existing entry-only restore so the
	// neighbouring edit survives.
	expected, err := renderEntryChange(c, original, entry)
	if err != nil {
		return restoreChange{}, false, fmt.Errorf("reconstruct swapped configuration: %w", err)
	}
	restored := original
	if !bytes.Equal(current, expected) {
		restored, err = restoreEntry(c, current, original)
	}
	if err != nil {
		return restoreChange{}, false, err
	}
	return restoreChange{
		target: c, current: current, restored: restored,
		binding: binding, recovery: recovery,
	}, true, nil
}

func validateDistinctRestoreTargets(changes []restoreChange) error {
	for index, change := range changes {
		for previous := range changes[:index] {
			if change.binding.Target == changes[previous].binding.Target {
				return fmt.Errorf(
					"%s and %s select the same physical configuration",
					changes[previous].target.name, change.target.name,
				)
			}
		}
	}
	return nil
}

func applyRestoreChanges(changes []restoreChange) error {
	applied := make([]restoreChange, 0, len(changes))
	for _, change := range changes {
		committed, err := change.commit()
		if err != nil {
			return rollbackRestoreChanges(applied,
				fmt.Errorf("%s: %w", change.target.name, err))
		}
		applied = append(applied, committed)
	}
	var failures []error
	for _, change := range applied {
		if err := os.Remove(change.recovery.backup); err != nil {
			failures = append(failures, fmt.Errorf(
				"%s: remove backup: %w", change.target.name, err,
			))
			continue
		}
		if err := os.Remove(change.recovery.state); err != nil {
			failures = append(failures, fmt.Errorf(
				"%s: remove recovery state: %w", change.target.name, err,
			))
		}
	}
	return errors.Join(failures...)
}

func (c restoreChange) commit() (restoreChange, error) {
	if err := validateBoundContents(c.target.path, c.current, c.binding); err != nil {
		return restoreChange{}, err
	}
	if err := atomicWriteFile(c.binding.Resolved, c.restored, 0o600); err != nil {
		return restoreChange{}, fmt.Errorf("restore configuration: %w", err)
	}
	after, err := captureDestination(c.target.path)
	if err == nil && !sameTopology(c.binding, after) {
		err = errors.New("configuration topology changed during restore")
	}
	if err == nil {
		err = validateBoundContents(c.target.path, c.restored, after)
	}
	if err != nil {
		rollbackErr := c.restoreCurrent()
		return restoreChange{}, errors.Join(err, rollbackErr)
	}
	c.binding = after
	return c, nil
}

func (c restoreChange) restoreCurrent() error {
	if err := atomicWriteFile(c.binding.Resolved, c.current, 0o600); err != nil {
		return fmt.Errorf("rollback restored configuration: %w", err)
	}
	after, err := captureDestination(c.target.path)
	if err != nil {
		return fmt.Errorf("verify restore rollback: %w", err)
	}
	if !sameTopology(c.binding, after) {
		return errors.New("verify restore rollback: configuration topology changed")
	}
	if err := validateBoundContents(c.target.path, c.current, after); err != nil {
		return fmt.Errorf("verify restore rollback: %w", err)
	}
	record := c.recovery.record
	record.Destination = after
	if err := writeRecoveryState(c.recovery.state, record); err != nil {
		return fmt.Errorf("record restore rollback: %w", err)
	}
	return nil
}

func rollbackRestoreChanges(applied []restoreChange, cause error) error {
	failures := []error{cause}
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		if err := change.restoreCurrent(); err != nil {
			failures = append(failures, fmt.Errorf(
				"%s: rollback restore: %w", change.target.name, err,
			))
		}
	}
	return errors.Join(failures...)
}

// entryOf may decode freely because it never writes the result back.
func entryOf(c client) (map[string]any, bool, error) {
	contents, err := os.ReadFile(c.path)
	if err != nil {
		return nil, false, err
	}
	return entryFromContents(c, contents)
}

func entryFromContents(c client, contents []byte) (map[string]any, bool, error) {
	switch c.format {
	case formatTOML:
		entry, found := readTOMLEntry(contents, c.key+"."+serverName)
		return entry, found, nil
	case formatJSONC:
		decoded, err := readJSONC(contents)
		if err != nil {
			return nil, false, err
		}
		entry, found := serverEntry(decoded, c.key)
		return openCodeEntry(entry), found, nil
	case formatJSON:
		fallthrough
	default:
		var decoded map[string]any
		if err := json.Unmarshal(contents, &decoded); err != nil {
			return nil, false, err
		}
		entry, found := serverEntry(decoded, c.key)
		return entry, found, nil
	}
}

func restoreEntry(c client, current, original []byte) ([]byte, error) {
	path := []string{c.key, serverName}
	switch c.format {
	case formatTOML:
		table := c.key + "." + serverName
		currentStart, currentEnd, found := tomlTableSpan(current, table)
		if !found {
			return nil, fmt.Errorf("current configuration has no %s table", table)
		}
		originalStart, originalEnd, originallyPresent := tomlTableSpan(original, table)
		replacement := []byte(nil)
		if originallyPresent {
			replacement = original[originalStart:originalEnd]
		} else {
			// Remove the separator that writeEntry added with the table.
			prefix := []byte("\n")
			if !bytes.HasSuffix(original, []byte("\n")) {
				prefix = []byte("\n\n")
			}
			if currentStart >= len(prefix) &&
				bytes.Equal(current[currentStart-len(prefix):currentStart], prefix) {
				currentStart -= len(prefix)
			}
		}
		return replaceBytes(current, currentStart, currentEnd, replacement), nil
	case formatJSONC, formatJSON:
		currentSpan, ok := findJSONCMember(blankComments(current), path)
		if !ok || !currentSpan.present {
			return nil, errors.New("current configuration has no server entry")
		}
		originalSpan, ok := findJSONCMember(blankComments(original), path)
		if ok && originalSpan.present {
			return replaceBytes(
				current,
				currentSpan.valueStart,
				currentSpan.valueEnd,
				original[originalSpan.valueStart:originalSpan.valueEnd],
			), nil
		}
		return removeJSONCMember(current, currentSpan)
	default:
		return nil, fmt.Errorf("unknown configuration format %d", c.format)
	}
}

func replaceBytes(text []byte, start, end int, replacement []byte) []byte {
	updated := make([]byte, 0, len(text)-(end-start)+len(replacement))
	updated = append(updated, text[:start]...)
	updated = append(updated, replacement...)
	return append(updated, text[end:]...)
}

// openCodeEntry normalizes opencode's entry dialect.
func openCodeEntry(entry map[string]any) map[string]any {
	if entry == nil {
		return nil
	}
	command, ok := entry["command"].([]any)
	if !ok || len(command) == 0 {
		return entry
	}
	flattened := map[string]any{"command": fmt.Sprint(command[0])}
	if len(command) > 1 {
		flattened["args"] = command[1:]
	}
	if environment, ok := entry["environment"].(map[string]any); ok {
		flattened["env"] = environment
	}
	return flattened
}

type entryChange struct {
	target            client
	original, updated []byte
	spec              processSpec
	binding           destinationBinding
	recovery          recoveryPlan
}

func planEntryChange(c client, entry map[string]any) (entryChange, error) {
	contents, binding, err := readBoundFile(c.path)
	if err != nil {
		return entryChange{}, err
	}
	updated, err := renderEntryChange(c, contents, entry)
	if err != nil {
		return entryChange{}, err
	}
	recovery, err := recoveryFor(c, contents, binding)
	if err != nil {
		return entryChange{}, err
	}
	finalEntry, present, err := entryFromContents(c, updated)
	if err != nil {
		return entryChange{}, fmt.Errorf("read rendered entry: %w", err)
	}
	if !present {
		return entryChange{}, errors.New("rendered configuration has no server entry")
	}
	spec, err := processSpecFromEntry(finalEntry)
	if err != nil {
		return entryChange{}, fmt.Errorf("rendered server entry: %w", err)
	}
	return entryChange{
		target: c, original: contents, updated: updated, spec: spec,
		binding: binding, recovery: recovery,
	}, nil
}

func validateDistinctTargets(changes []entryChange) error {
	for index, change := range changes {
		for previous := range changes[:index] {
			if change.binding.Target == changes[previous].binding.Target {
				return fmt.Errorf(
					"%s and %s select the same physical configuration",
					changes[previous].target.name, change.target.name,
				)
			}
		}
	}
	return nil
}

func (c entryChange) apply() error {
	return applyEntryChanges([]entryChange{c})
}

func applyEntryChanges(changes []entryChange) error {
	prepared := make([]entryChange, 0, len(changes))
	for _, change := range changes {
		created, err := change.prepareBackup()
		if err != nil {
			return errors.Join(
				fmt.Errorf("%s: prepare backup: %w", change.target.name, err),
				removePreparedBackups(prepared),
			)
		}
		if created {
			prepared = append(prepared, change)
		}
	}

	applied := make([]entryChange, 0, len(changes))
	for _, change := range changes {
		committed, err := change.commit()
		if err != nil {
			return rollbackEntryChanges(applied, prepared,
				fmt.Errorf("%s: %w", change.target.name, err))
		}
		applied = append(applied, committed)
	}
	return nil
}

func (c entryChange) prepareBackup() (bool, error) {
	if c.recovery.exists {
		return false, nil
	}
	if err := atomicWriteFile(c.recovery.backup, c.original, 0o600); err != nil {
		return false, err
	}
	if err := writeRecoveryState(c.recovery.state, c.recovery.record); err != nil {
		return false, errors.Join(err, os.Remove(c.recovery.backup))
	}
	return true, nil
}

func (c entryChange) commit() (entryChange, error) {
	if err := c.validate(); err != nil {
		return entryChange{}, err
	}
	if err := atomicWriteFile(c.binding.Resolved, c.updated, 0o600); err != nil {
		return entryChange{}, fmt.Errorf("write configuration: %w", err)
	}
	after, err := captureDestination(c.target.path)
	if err == nil && !sameTopology(c.binding, after) {
		err = errors.New("configuration topology changed during write")
	}
	if err == nil {
		err = validateBoundContents(c.target.path, c.updated, after)
	}
	if err == nil {
		record := c.recovery.record
		record.Destination = after
		err = writeRecoveryState(c.recovery.state, record)
		if err == nil {
			c.binding = after
			c.recovery.record = record
			return c, nil
		}
	}
	rollbackErr := c.restoreOriginal()
	return entryChange{}, errors.Join(err, rollbackErr)
}

func (c entryChange) restoreOriginal() error {
	if err := atomicWriteFile(c.binding.Resolved, c.original, 0o600); err != nil {
		return fmt.Errorf("rollback configuration: %w", err)
	}
	after, err := captureDestination(c.target.path)
	if err != nil {
		return fmt.Errorf("verify rollback: %w", err)
	}
	if !sameTopology(c.binding, after) {
		return errors.New("verify rollback: configuration topology changed")
	}
	if err := validateBoundContents(c.target.path, c.original, after); err != nil {
		return fmt.Errorf("verify rollback: %w", err)
	}
	record := c.recovery.record
	record.Destination = after
	if err := writeRecoveryState(c.recovery.state, record); err != nil {
		return fmt.Errorf("record rollback: %w", err)
	}
	return nil
}

func rollbackEntryChanges(applied, prepared []entryChange, cause error) error {
	var failures []error
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		if err := change.restoreOriginal(); err != nil {
			failures = append(failures, fmt.Errorf("%s: rollback: %w", change.target.name, err))
		}
	}
	if len(failures) == 0 {
		if err := removePreparedBackups(prepared); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(append([]error{cause}, failures...)...)
}

func removePreparedBackups(prepared []entryChange) error {
	var failures []error
	for index := len(prepared) - 1; index >= 0; index-- {
		change := prepared[index]
		if err := os.Remove(change.recovery.backup); err != nil {
			failures = append(failures, fmt.Errorf(
				"%s: remove prepared backup: %w", change.target.name, err,
			))
			continue
		}
		if err := os.Remove(change.recovery.state); err != nil {
			failures = append(failures, fmt.Errorf(
				"%s: remove prepared recovery state: %w", change.target.name, err,
			))
		}
	}
	return errors.Join(failures...)
}

func (c entryChange) validate() error {
	return validateBoundContents(c.target.path, c.original, c.binding)
}

func validateEntryChanges(changes []entryChange) error {
	var failures []error
	for _, change := range changes {
		if err := change.validate(); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", change.target.name, err))
		}
	}
	return errors.Join(failures...)
}

// writeEntry splices TOML and JSONC so unrelated settings and comments survive.
func writeEntry(c client, entry map[string]any) error {
	change, err := planEntryChange(c, entry)
	if err != nil {
		return err
	}
	return change.apply()
}

func renderEntryChange(c client, contents []byte, entry map[string]any) ([]byte, error) {
	switch c.format {
	case formatTOML:
		table := c.key + "." + serverName
		if err := validateTOMLPreservation(contents, table); err != nil {
			return nil, err
		}
		previous := tomlPreserved(contents, table)
		if environment := tomlEnvironment(contents, table); environment != nil {
			previous["env"] = environment
		}
		shaped := mergeWithExisting(previous, renderEntry(entry, c.dialect))
		start, end, found := tomlTableSpan(contents, table)
		header := "[" + table + "]"
		if found {
			header = tomlHeaderAt(contents, start)
		}
		rendered := renderTOMLTable(table, header, shaped)
		var updated []byte
		if found {
			updated = append(append(append([]byte{}, contents[:start]...),
				[]byte(rendered)...), contents[end:]...)
		} else {
			separator := "\n"
			if bytes.HasSuffix(contents, []byte("\n")) {
				separator = ""
			}
			updated = append(append([]byte{}, contents...),
				[]byte(separator+"\n"+rendered)...)
		}
		return updated, nil
	case formatJSONC:
		previous := map[string]any{}
		decoded, err := readJSONC(contents)
		if err != nil {
			return nil, err
		}
		if existing, found := serverEntry(decoded, c.key); found {
			previous = existing
		}
		shaped := mergeWithExisting(previous, renderEntry(entry, c.dialect))
		updated, err := setJSONCMember(contents, []string{c.key, serverName}, shaped, "  ")
		if err != nil {
			return nil, err
		}
		return updated, nil
	case formatJSON:
		fallthrough
	default:
		var decoded map[string]any
		if err := json.Unmarshal(contents, &decoded); err != nil {
			return nil, err
		}
		servers, _ := decoded[c.key].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		previous, _ := servers[serverName].(map[string]any)
		servers[serverName] = mergeWithExisting(previous, renderEntry(entry, c.dialect))
		decoded[c.key] = servers
		updated, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(updated, '\n'), nil
	}
}

// writeBesideBackup retains the first pre-swap copy across repeated swaps.
func writeBesideBackup(c client, original, updated []byte) error {
	current, binding, err := readBoundFile(c.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("configuration changed after it was planned")
	}
	recovery, err := recoveryFor(c, original, binding)
	if err != nil {
		return err
	}
	return applyEntryChanges([]entryChange{
		{
			target: c, original: original, updated: updated,
			binding: binding, recovery: recovery,
		},
	})
}

func backupPath(c client) string { return c.path + ".mcp-swap-backup" }

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	return true, nil
}

func preflightBackupDestination(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("backup parent is not a directory")
	}
	if info.Mode().Perm()&0o222 == 0 {
		return errors.New("backup parent is not writable")
	}
	return nil
}

// atomicWriteFile replaces path only after its complete contents are durable
// in a sibling temporary file. Existing symlinks continue to point at their
// targets rather than being replaced by the rename.
func atomicWriteFile(path string, contents []byte, defaultMode os.FileMode) error {
	target := path
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		target = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("resolve destination: %w", err)
	} else if info, lstatErr := os.Lstat(path); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("destination is a dangling symlink")
	} else if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", lstatErr)
	}

	mode := defaultMode
	if info, statErr := os.Stat(target); statErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("destination is not a regular file")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", statErr)
	}

	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".mcp-swap-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	written, err := temporary.Write(contents)
	if err != nil {
		return err
	}
	if written != len(contents) {
		return io.ErrShortWrite
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func serverEntry(configuration map[string]any, key string) (map[string]any, bool) {
	servers, ok := configuration[key].(map[string]any)
	if !ok {
		return nil, false
	}
	entry, ok := servers[serverName].(map[string]any)
	return entry, ok
}

func isLocal(entry map[string]any) bool {
	_, swapped := swapMode(entry)
	return swapped
}

func swapMode(entry map[string]any) (string, bool) {
	environment, ok := entry["env"].(map[string]any)
	if !ok {
		return "", false
	}
	mode, ok := environment["LIBTMUX_MCP_SWAP"].(string)
	return mode, ok && mode != ""
}

func describe(entry map[string]any) string {
	command, _ := entry["command"].(string)
	parts := []string{command}
	if arguments, ok := entry["args"].([]any); ok {
		for _, argument := range arguments {
			parts = append(parts, fmt.Sprint(argument))
		}
	}
	if directory, ok := entry["cwd"].(string); ok && directory != "" {
		parts = append(parts, "in "+directory)
	}
	return strings.Join(parts, " ")
}
