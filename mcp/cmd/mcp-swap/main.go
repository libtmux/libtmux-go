// Command mcp-swap points supported agent CLIs at a selected libtmux-mcp build
// and restores their prior entries.
//
//	mcp-swap status
//	mcp-swap use-local --dry-run
//	mcp-swap use-local --mode build
//	mcp-swap use-local --mode released --ref v0.1.0
//	mcp-swap use-local --client claude
//	mcp-swap revert
//
// Unless --no-preflight is set, the selected server must complete an MCP
// handshake before any write. Each config is backed up once; a write failure
// for one client does not stop updates to the others.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serverName = "tmux"

const commandName = "libtmux-mcp"

// buildDirectoryName keeps configured binaries in the user cache so they
// outlive this process without adding artifacts to repository generation checks.
const buildDirectoryName = "libtmux-mcp"

// modulePath is kept in step with this command's go.mod by a test.
const modulePath = "github.com/libtmux/libtmux-go/mcp"

type buildMode string

const (
	modeDev       buildMode = "dev"
	modeBuild     buildMode = "build"
	modeInstalled buildMode = "installed"
	modeReleased  buildMode = "released"
)

// client describes one supported global configuration.
type client struct {
	name    string
	path    string
	key     string
	format  configFormat
	dialect entryDialect
}

func knownClients(home string) []client {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return []client{
		{"claude", filepath.Join(home, ".claude.json"), "mcpServers", formatJSON, dialectStandard},
		{"cursor", filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", formatJSON, dialectStandard},
		{"gemini", filepath.Join(home, ".gemini", "settings.json"), "mcpServers", formatJSON, dialectStandard},
		{"antigravity", filepath.Join(home, ".gemini", "config", "mcp_config.json"), "mcpServers", formatJSON, dialectStandard},
		{"codex", filepath.Join(home, ".codex", "config.toml"), "mcp_servers", formatTOML, dialectStandard},
		{"grok", filepath.Join(home, ".grok", "config.toml"), "mcp_servers", formatTOML, dialectStandard},
		{"opencode", filepath.Join(config, "opencode", "opencode.jsonc"), "mcp", formatJSONC, dialectOpencode},
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: mcp-swap status|use-local|revert [--dry-run]"+
				" [--mode dev|build|installed|released] [--ref VERSION]"+
				" [--client NAME] [--no-preflight]\n\n")
		flag.PrintDefaults()
	}
	chosen, err := parseArguments(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-swap:", err)
		flag.Usage()
		os.Exit(2)
	}

	if err := run(chosen); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-swap:", err)
		os.Exit(1)
	}
}

type options struct {
	command     string
	dryRun      bool
	mode        buildMode
	ref         string
	noPreflight bool
	only        []string
}

// parseArguments accepts flags after the command and rejects unknown tokens,
// so --dry-run cannot silently become a write.
func parseArguments(arguments []string) (options, error) {
	chosen := options{mode: modeDev}
	expecting := ""
	for _, argument := range arguments {
		if expecting != "" {
			switch expecting {
			case "--mode":
				mode := buildMode(argument)
				switch mode {
				case modeDev, modeBuild, modeInstalled, modeReleased:
					chosen.mode = mode
				default:
					return options{}, fmt.Errorf(
						"%q is not dev, build, installed, or released", argument)
				}
			case "--ref":
				chosen.ref = argument
			case "--client":
				chosen.only = append(chosen.only, argument)
			}
			expecting = ""
			continue
		}
		name, value, assigned := strings.Cut(argument, "=")
		if assigned {
			argument = name
		}
		switch argument {
		case "--dry-run", "-dry-run":
			chosen.dryRun = true
		case "--no-preflight", "-no-preflight":
			chosen.noPreflight = true
		case "--mode", "-mode", "--ref", "-ref", "--client", "-client":
			expecting = "--" + strings.TrimLeft(argument, "-")
			if assigned {
				remembered := expecting
				expecting = ""
				if err := assign(&chosen, remembered, value); err != nil {
					return options{}, err
				}
			}
		case "status", "use-local", "revert":
			if chosen.command != "" {
				return options{}, fmt.Errorf(
					"say one command, not %q and %q", chosen.command, argument)
			}
			chosen.command = argument
		default:
			return options{}, fmt.Errorf("%q is not a command or a flag", argument)
		}
	}
	if expecting != "" {
		return options{}, fmt.Errorf("%s wants a value", expecting)
	}
	if chosen.command == "" {
		return options{}, errors.New("say status, use-local, or revert")
	}
	if chosen.ref != "" && chosen.mode != modeReleased {
		return options{}, errors.New("--ref only means something with --mode released")
	}
	return chosen, nil
}

func assign(chosen *options, flagName, value string) error {
	switch flagName {
	case "--mode":
		mode := buildMode(value)
		switch mode {
		case modeDev, modeBuild, modeInstalled, modeReleased:
			chosen.mode = mode
			return nil
		default:
			return fmt.Errorf("%q is not dev, build, installed, or released", value)
		}
	case "--ref":
		chosen.ref = value
		return nil
	case "--client":
		chosen.only = append(chosen.only, value)
		return nil
	}
	return fmt.Errorf("%s takes no value", flagName)
}

func run(chosen options) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	clients, err := selected(knownClients(home), chosen.only)
	if err != nil {
		return err
	}

	switch chosen.command {
	case "status":
		return report(clients)
	case "revert":
		return revert(clients, chosen.dryRun)
	case "use-local":
		// Only checkout-backed modes require a repository root.
		repository := ""
		if chosen.mode == modeDev || chosen.mode == modeBuild {
			if repository, err = repositoryRoot(); err != nil {
				return err
			}
		}
		entry, err := buildEntry(chosen, repository)
		if err != nil {
			return err
		}
		// Preflight dry runs too so they validate the selected build.
		if !chosen.noPreflight {
			fmt.Fprintf(os.Stderr, "preflight: %s\n", describe(entry))
			if reason := preflight(entry); reason != "" {
				return fmt.Errorf("preflight failed, nothing written: %s", reason)
			}
		}
		return useLocal(clients, entry, chosen.dryRun)
	default:
		return fmt.Errorf("%q is not a command", chosen.command)
	}
}

// selected preserves declaration order and rejects unknown client names.
func selected(clients []client, only []string) ([]client, error) {
	if len(only) == 0 {
		return clients, nil
	}
	known := map[string]bool{}
	for _, c := range clients {
		known[c.name] = true
	}
	wanted := map[string]bool{}
	for _, name := range only {
		for part := range strings.SplitSeq(name, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !known[part] {
				return nil, fmt.Errorf("%q is not a client this knows: %s",
					part, strings.Join(clientNames(clients), ", "))
			}
			wanted[part] = true
		}
	}
	chosen := make([]client, 0, len(wanted))
	for _, c := range clients {
		if wanted[c.name] {
			chosen = append(chosen, c)
		}
	}
	return chosen, nil
}

func clientNames(clients []client) []string {
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.name)
	}
	return names
}

// buildEntry uses go -C for dev mode because not every client honors cwd.
func buildEntry(chosen options, repository string) (map[string]any, error) {
	entry := map[string]any{
		// Ownership marker for status and safe revert.
		"env": map[string]any{"LIBTMUX_MCP_SWAP": string(chosen.mode)},
	}
	switch chosen.mode {
	case modeInstalled:
		entry["command"] = commandName
		entry["args"] = []any{}
	case modeReleased:
		ref := chosen.ref
		if ref == "" {
			ref = "latest"
		}
		entry["command"] = "go"
		entry["args"] = []any{
			"run", fmt.Sprintf("%s/cmd/%s@%s", modulePath, commandName, ref),
		}
	case modeBuild:
		binary, err := compile(repository)
		if err != nil {
			return nil, err
		}
		entry["command"] = binary
		entry["args"] = []any{}
	case modeDev:
		entry["command"] = "go"
		entry["args"] = []any{"-C", repository, "run", "./cmd/" + commandName}
	default:
		return nil, fmt.Errorf("%q is not a build mode", chosen.mode)
	}
	return entry, nil
}

// compile writes a persistent binary because the config outlives this process.
func compile(repository string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	binary := filepath.Join(cache, buildDirectoryName, commandName)
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return "", err
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/"+commandName)
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build failed: %s", strings.TrimSpace(string(output)))
	}
	return binary, nil
}

func entryCommand(entry map[string]any) string {
	command, _ := entry["command"].(string)
	return command
}

func entryArguments(entry map[string]any) []string {
	list, _ := entry["args"].([]any)
	arguments := make([]string, 0, len(list))
	for _, argument := range list {
		arguments = append(arguments, fmt.Sprint(argument))
	}
	return arguments
}

// repositoryRoot finds the nearest ancestor with a go.mod whose path ends in mcp.
func repositoryRoot() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for directory := working; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			if strings.HasSuffix(directory, "mcp") {
				return directory, nil
			}
		}
		if parent := filepath.Dir(directory); parent == directory {
			return "", errors.New("run this from inside the mcp module")
		}
	}
}

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
		_, err := os.Stat(c.path)
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
		if err := writeEntry(c, entry); err != nil {
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
		backup := backupPath(c)
		exists, err := regularFileExists(backup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		if !exists {
			continue
		}
		if dryRun {
			fmt.Printf("%-12s would restore %s\n", c.name, filepath.Base(backup))
			continue
		}
		contents, err := os.ReadFile(backup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		current, err := os.ReadFile(c.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		entry, present, err := entryFromContents(c, current)
		if err != nil || !present || !isLocal(entry) {
			if err == nil {
				err = errors.New("the current server entry is no longer the one mcp-swap wrote")
			}
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		restored, err := restoreEntry(c, current, contents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		if err := atomicWriteFile(c.path, restored, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		// Remove the restored backup so the next swap captures current state.
		if err := os.Remove(backup); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s restored, but its backup remains: %v\n",
				c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		fmt.Printf("%-12s restored from %s\n", c.name, filepath.Base(backup))
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

// writeEntry splices TOML and JSONC so unrelated settings and comments survive.
func writeEntry(c client, entry map[string]any) error {
	contents, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}

	switch c.format {
	case formatTOML:
		table := c.key + "." + serverName
		if err := validateTOMLPreservation(contents, table); err != nil {
			return err
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
		return writeBesideBackup(c, contents, updated)
	case formatJSONC:
		previous := map[string]any{}
		if decoded, err := readJSONC(contents); err == nil {
			if existing, found := serverEntry(decoded, c.key); found {
				previous = existing
			}
		}
		shaped := mergeWithExisting(previous, renderEntry(entry, c.dialect))
		updated, err := setJSONCMember(contents, []string{c.key, serverName}, shaped, "  ")
		if err != nil {
			return err
		}
		return writeBesideBackup(c, contents, updated)
	case formatJSON:
		fallthrough
	default:
		var decoded map[string]any
		if err := json.Unmarshal(contents, &decoded); err != nil {
			return err
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
			return err
		}
		return writeBesideBackup(c, contents, append(updated, '\n'))
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
