package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

var authenticationEnvironment = []struct {
	name   string
	client string
}{
	{"ANTHROPIC_API_KEY", "claude"},
	{"OPENAI_API_KEY", "codex"},
	{"GEMINI_API_KEY", "gemini"},
	{"GOOGLE_API_KEY", "gemini"},
	{"XAI_API_KEY", "grok"},
	{"GROK_API_KEY", "grok"},
}

func nativeStatusTo(
	output io.Writer,
	clients []client,
	requested configScope,
	repository string,
) error {
	for _, base := range clients {
		scopes := []configScope{scopeUser}
		if base.name == "claude" {
			scopes = []configScope{scopeUser, scopeProject}
			if requested != "" {
				scopes = []configScope{requested}
			}
		}
		for _, scope := range scopes {
			target := base
			target.scope = scope
			target.repository = repository
			label := nativeLabel(target.name, scope)
			contents, _, err := readNativeConfig(target.path)
			var entry map[string]any
			present := false
			if err == nil {
				entry, present, err = entryFromContents(target, contents)
			}
			var line string
			switch {
			case errors.Is(err, os.ErrNotExist):
				line = fmt.Sprintf("[%s] no config%s", label, clientCaveat(target))
			case err != nil:
				line = fmt.Sprintf("[%s] unreadable: %v%s", label, err, clientCaveat(target))
			case !present:
				line = fmt.Sprintf("[%s] no entry for %q%s", label, serverName, clientCaveat(target))
			default:
				mode := "other"
				if isLocal(entry) {
					mode, _ = swapMode(entry)
				}
				line = fmt.Sprintf("[%s] %s = %s (%s)%s",
					label, serverName, describe(entry), mode, clientCaveat(target))
			}
			if _, err := fmt.Fprintln(output, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeDoctorTo(output io.Writer, clients []client, repository string) error {
	if _, err := fmt.Fprintln(output, "mcp-swap doctor"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "  repository: %s\n", repository); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "  server: %s\n", serverName); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "  configurations:"); err != nil {
		return err
	}
	for _, target := range clients {
		contents, binding, err := readNativeConfig(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if _, writeErr := fmt.Fprintf(output, "    %s: unreadable: %v\n", target.name, err); writeErr != nil {
				return writeErr
			}
			continue
		}
		if _, err := fmt.Fprintf(output, "    %s: %d bytes, mode %04o\n",
			target.name, len(contents), binding.Mode); err != nil {
			return err
		}
		if err := reportRetiredSafety(output, target, contents, repository); err != nil {
			if _, writeErr := fmt.Fprintf(output,
				"    %s: entry unreadable: %v\n", target.name, err); writeErr != nil {
				return writeErr
			}
		}
	}

	ledger, _, err := loadNativeLedger()
	if err != nil {
		return fmt.Errorf("recovery state: %w", err)
	}
	if len(ledger.Entries) != 0 {
		if _, err := fmt.Fprintln(output, "  outstanding swaps:"); err != nil {
			return err
		}
		keys := make([]string, 0, len(ledger.Entries))
		for key := range ledger.Entries {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(left, right int) bool {
			return ledger.Entries[keys[left]].Sequence < ledger.Entries[keys[right]].Sequence
		})
		for _, key := range keys {
			entry := ledger.Entries[key]
			detail := ""
			if _, err := readNativeBackup(entry); err != nil {
				detail = " -- backup invalid: " + err.Error()
			}
			if _, err := fmt.Fprintf(output, "    %s: sequence %d, backup %s%s\n",
				key, entry.Sequence, entry.BackupPath, detail); err != nil {
				return err
			}
		}
	}

	referenced := map[string]bool{}
	for _, entry := range ledger.Entries {
		referenced[entry.BackupPath] = true
	}
	orphans, bytes, err := nativeOrphanedBackups(clients, referenced)
	if err != nil {
		return err
	}
	if len(orphans) != 0 {
		if _, err := fmt.Fprintf(output,
			"  orphaned backups: %d file(s), %d bytes not tracked by Go recovery state\n",
			len(orphans), bytes); err != nil {
			return err
		}
	}

	for _, variable := range authenticationEnvironment {
		if _, present := os.LookupEnv(variable.name); !present {
			continue
		}
		if _, err := fmt.Fprintf(output,
			"  ! %s overrides %s's stored login\n", variable.name, variable.client); err != nil {
			return err
		}
	}
	return nil
}

func reportRetiredSafety(
	output io.Writer,
	target client,
	contents []byte,
	repository string,
) error {
	scopes := []configScope{scopeUser}
	if target.name == "claude" {
		scopes = []configScope{scopeUser, scopeProject}
	}
	for _, scope := range scopes {
		target.scope = scope
		target.repository = repository
		entry, present, err := entryFromContents(target, contents)
		if err != nil {
			return fmt.Errorf("%s: %w", nativeLabel(target.name, scope), err)
		}
		if !present {
			continue
		}
		if _, retired := entryEnvironment(entry)["LIBTMUX_SAFETY"]; retired {
			if _, err := fmt.Fprintf(output,
				"    ! %s retains retired LIBTMUX_SAFETY; replace it explicitly with LIBTMUX_TOOLSETS\n",
				nativeLabel(target.name, scope)); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeOrphanedBackups(
	clients []client,
	referenced map[string]bool,
) ([]string, int64, error) {
	seen := map[string]bool{}
	var paths []string
	var total int64
	for _, target := range clients {
		base := target.path
		if _, err := os.Lstat(base); err == nil {
			resolved, err := filepath.EvalSymlinks(base)
			if err != nil {
				return nil, 0, err
			}
			base = resolved
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, 0, err
		}
		pattern := base + ".bak.mcp-swap-go-*"
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, 0, err
		}
		for _, path := range matches {
			path = filepath.Clean(path)
			if seen[path] || referenced[path] {
				continue
			}
			seen[path] = true
			info, err := os.Lstat(path)
			if err != nil {
				return nil, 0, err
			}
			if !info.Mode().IsRegular() {
				return nil, 0, fmt.Errorf("orphaned backup is not a regular file: %s", path)
			}
			paths = append(paths, path)
			total += info.Size()
		}
	}
	sort.Strings(paths)
	return paths, total, nil
}
