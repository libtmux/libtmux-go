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

func report(clients []client) error {
	for _, c := range clients {
		entry, present, err := entryOf(c)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Printf("%-12s not installed\n", c.name)
			continue
		case err != nil:
			fmt.Printf("%-12s unreadable: %v\n", c.name, err)
			continue
		}
		switch {
		case !present:
			fmt.Printf("%-12s no %q server\n", c.name, serverName)
		case isLocal(entry):
			mode, _ := swapMode(entry)
			fmt.Printf("%-12s %s: %s\n", c.name, mode, describe(entry))
		default:
			fmt.Printf("%-12s %s\n", c.name, describe(entry))
		}
	}
	return nil
}

// useLocal attempts every client and joins named failures.
func useLocal(clients []client, entry map[string]any, dryRun bool) error {
	var failures []error
	for _, c := range clients {
		change, err := planEntryChange(c, entry)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not changed: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		if dryRun {
			fmt.Printf("%-12s would run %s\n", c.name, describe(entry))
			continue
		}
		if err := change.apply(); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not changed: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		fmt.Printf("%-12s now runs %s\n", c.name, describe(entry))
	}
	return errors.Join(failures...)
}

// revert attempts every backed-up client and joins named failures.
func revert(clients []client, dryRun bool) error {
	var failures []error
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
		if dryRun {
			fmt.Printf("%-12s would restore %s\n", c.name, filepath.Base(change.backup))
			continue
		}
		if err := atomicWriteFile(c.path, change.restored, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		// Remove the restored backup so the next swap captures current state.
		if err := os.Remove(change.backup); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s restored, but its backup remains: %v\n",
				c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		fmt.Printf("%-12s restored from %s\n", c.name, filepath.Base(change.backup))
	}
	return errors.Join(failures...)
}

type restoreChange struct {
	backup   string
	restored []byte
}

func planRestore(c client) (restoreChange, bool, error) {
	backup := backupPath(c)
	exists, err := regularFileExists(backup)
	if err != nil || !exists {
		return restoreChange{}, exists, err
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		return restoreChange{}, false, err
	}
	current, err := os.ReadFile(c.path)
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
	restored, err := restoreEntry(c, current, original)
	if err != nil {
		return restoreChange{}, false, err
	}
	return restoreChange{backup: backup, restored: restored}, true, nil
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
}

func planEntryChange(c client, entry map[string]any) (entryChange, error) {
	contents, err := os.ReadFile(c.path)
	if err != nil {
		return entryChange{}, err
	}
	updated, err := renderEntryChange(c, contents, entry)
	if err != nil {
		return entryChange{}, err
	}
	if _, err := regularFileExists(backupPath(c)); err != nil {
		return entryChange{}, fmt.Errorf("inspect backup: %w", err)
	}
	return entryChange{target: c, original: contents, updated: updated}, nil
}

func (c entryChange) apply() error {
	return writeBesideBackup(c.target, c.original, c.updated)
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
	backup := backupPath(c)
	exists, err := regularFileExists(backup)
	if err != nil {
		return fmt.Errorf("inspect backup: %w", err)
	}
	if !exists {
		if err := atomicWriteFile(backup, original, 0o600); err != nil {
			return err
		}
	}
	return atomicWriteFile(c.path, updated, 0o600)
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
