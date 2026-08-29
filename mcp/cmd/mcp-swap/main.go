// Command mcp-swap points the agent CLIs on this machine at a local build of
// this server, and puts them back.
//
// An MCP server cannot be exercised without a client, so the development loop
// is to rewrite every client's configuration to run the working tree, try it,
// and restore what was there. Doing that by hand across half a dozen config
// files is why it does not get done.
//
//	mcp-swap status
//	mcp-swap use-local --dry-run
//	mcp-swap use-local --mode build
//	mcp-swap use-local --mode released --ref v0.1.0
//	mcp-swap use-local --client claude
//	mcp-swap revert
//
// --mode chooses which build the clients are pointed at. "dev" runs the
// working tree, so an edit is live for the next call and nothing has to be
// rebuilt. "build" compiles once and points at the binary, trading that for a
// plain exec. "installed" points at whatever libtmux-mcp is on PATH. "released"
// runs a published version out of the module cache, which is the one mode that
// does not involve this checkout at all.
//
// --client narrows the swap to the clients named, given more than once or as
// one comma-separated list. Without it every client is written, which is the
// right default when they all run the same server; naming one is for a machine
// where they do not.
//
// Before writing anything, the chosen build is started once and asked to
// complete an MCP handshake. A build error, a missing binary, or a version the
// module proxy has never heard of otherwise lands in every config at once and
// surfaces later as a server that will not start, separately, in each client.
//
// A client whose configuration cannot be read is named and left exactly as it
// is, and the others are still written: stopping at the first would leave the
// clients before it swapped and the ones after it untouched, with nothing
// saying which was which.
//
// It writes only global configuration, only the one server entry, and only
// after copying the file beside itself. Swapping something already swapped
// keeps the first backup, so revert lands on what was there before any of it,
// and revert removes the backup so the next swap starts from the file as it is
// then.
package main

import (
	"bufio"
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
	"time"
)

// serverName is the key this server is written under, which is what a person
// types after the client's own prefix.
const serverName = "tmux"

// commandName is the command under cmd/, which is both the package go run is
// given and the binary go build produces.
const commandName = "libtmux-mcp"

// buildDirectoryName is where --mode build puts its binary, under the user's
// cache directory.
//
// Outside the repository on purpose. A config entry outlives this process, so
// the path it names has to still hold a binary tomorrow, which rules out a
// temporary directory; and the generate check lists untracked files without
// excluding ignored ones, so a build artifact inside the tree fails it whether
// or not it is git-ignored.
const buildDirectoryName = "libtmux-mcp"

// modulePath is where a released build comes from. It is the module line of
// this command's own go.mod, which cannot be read at runtime from an installed
// binary, so it is written here and kept in step by a test.
const modulePath = "github.com/libtmux/libtmux-go/mcp"

// buildMode names which build of the server the clients are pointed at.
type buildMode string

const (
	// modeDev runs the working tree; every launch compiles.
	modeDev buildMode = "dev"
	// modeBuild compiles once and points at the binary.
	modeBuild buildMode = "build"
	// modeInstalled points at libtmux-mcp on PATH.
	modeInstalled buildMode = "installed"
	// modeReleased runs a published version from the module cache.
	modeReleased buildMode = "released"
)

// preflightTimeout bounds the handshake. Generous, because a released mode
// downloads a module and a cold dev mode compiles one.
const preflightTimeout = 180 * time.Second

// client is one agent CLI's global configuration.
//
// Every CLI here is written by default, not only the ones keeping JSON: the
// entry has one name across all of them, so swapping some by accident leaves
// two different servers answering to it and nothing saying which client got
// which. --client narrows it on purpose, which is what a machine running one
// implementation in some clients and another elsewhere needs in order to try a
// build in one of them without disturbing the rest.
type client struct {
	name string
	path string
	// key is the object holding the servers, which the CLIs spell differently.
	key string
	// format is how the file is written, and so how it has to be edited.
	format configFormat
	// dialect is the shape this CLI expects one entry to take.
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

// parseArguments reads the command and its flags in either order.
//
// Go's flag package stops at the first argument that is not a flag, so
// "use-local --dry-run" would leave the flag unparsed and silently write the
// configuration a person asked to preview. A tool whose whole job is editing
// someone else's files cannot have a spelling that quietly means the opposite,
// so anything unrecognised is refused rather than ignored.
// options is what one invocation was asked to do.
type options struct {
	command     string
	dryRun      bool
	mode        buildMode
	ref         string
	noPreflight bool
	// only narrows the swap to the clients named, empty meaning all of them.
	only []string
}

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
			// Both spellings, because a person who finds one form does not
			// work reaches for the other rather than for the usage line.
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

// assign applies one --flag=value pair.
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
		// Only the modes that run this checkout need to find it. A released
		// or installed swap is about a build that exists elsewhere, and
		// refusing it outside the repo would be ceremony.
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
		// Runs under --dry-run too: starting the server once is the only
		// signal a dry run can give about whether the swap would work.
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

// selected narrows the clients to the ones named, keeping the declared order so
// what is written reads the same however the names were given.
//
// An unknown name is refused rather than skipped, because a typo would
// otherwise report success having written nothing.
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

// clientNames lists what --client accepts, for a refusal that says so.
func clientNames(clients []client) []string {
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.name)
	}
	return names
}

// buildEntry produces the config entry for the chosen mode.
//
// The dev entry uses "go -C <module>" rather than a "cwd" key, because cwd is
// not something every client honours and a working directory quietly ignored
// starts the server in the wrong place rather than reporting anything.
func buildEntry(chosen options, repository string) (map[string]any, error) {
	entry := map[string]any{
		// The marker is what revert and status recognise, since a command of
		// "go" is not by itself proof this wrote the entry.
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

// compile builds the command and returns the binary's path.
//
// Built into the module rather than a temporary directory: a config entry
// outlives this process, so the path it names has to still hold a binary
// tomorrow.
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

// preflight starts the entry and completes one MCP handshake, returning an
// empty string when the server answered.
//
// stdin is held open until the answer arrives rather than closed with the
// frame. A stdio server is entitled to read end-of-input as its client hanging
// up, and this one does: closing stdin first gets "server is closing: EOF" and
// a nonzero exit instead of a reply, which would report every healthy server
// as broken.
func preflight(entry map[string]any) string {
	process := exec.Command(entryCommand(entry), entryArguments(entry)...)
	input, err := process.StdinPipe()
	if err != nil {
		return err.Error()
	}
	output, err := process.StdoutPipe()
	if err != nil {
		return err.Error()
	}
	var complaints strings.Builder
	process.Stderr = &complaints
	if err := process.Start(); err != nil {
		return fmt.Sprintf("could not launch %s: %v", entryCommand(entry), err)
	}
	defer func() {
		_ = input.Close()
		_ = process.Process.Kill()
		_ = process.Wait()
	}()

	frame := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"mcp-swap-preflight","version":"1"}}}` + "\n"
	if _, err := io.WriteString(input, frame); err != nil {
		return fmt.Sprintf("%s closed its input: %v", entryCommand(entry), err)
	}

	answered := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var message struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				continue
			}
			if message.ID == 1 && len(message.Result) > 0 {
				answered <- true
				return
			}
		}
		answered <- false
	}()

	select {
	case ok := <-answered:
		if ok {
			return ""
		}
	case <-time.After(preflightTimeout):
		return fmt.Sprintf("no MCP response within %s", preflightTimeout)
	}
	if tail := strings.TrimSpace(complaints.String()); tail != "" {
		return tail
	}
	return "server exited without answering initialize"
}

// entryCommand and entryArguments read an entry back as something runnable.
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

// repositoryRoot finds the module this command was built from, so use-local
// points at the checkout rather than at wherever it happens to be run.
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

// useLocal points every installed client at the entry.
//
// One client that cannot be written does not stop the others. Returning at the
// first failure left the clients before it swapped and the ones after it
// untouched, and nothing said which was which: a CLI that was never reached
// looks exactly like one that was. Every failure is collected and named
// instead, so one run says everything that needs fixing.
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

// revert restores every client that has a backup.
//
// As with useLocal, one client that cannot be restored does not strand the
// others: a half-reverted set is the state hardest to reason about, and the
// one a person reaches for revert to get out of.
func revert(clients []client, dryRun bool) error {
	var failures []error
	for _, c := range clients {
		backup := backupPath(c)
		_, err := os.Stat(backup)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
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
		if err := os.WriteFile(c.path, restored, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "%-12s not restored: %v\n", c.name, err)
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		// Removed, so the next swap takes a backup of what is there then. A
		// kept one is stale the moment the file is edited afterwards, and
		// writeBesideBackup declines to replace an existing backup -- so
		// leaving it means a later revert restores a version from before the
		// edit and discards it.
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

// entryOf reports the server entry a client currently holds.
//
// Reading may decode freely — nothing is written back from it — which is why
// this is much shorter than the writing path.
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
			// writeEntry separates an appended table from the preceding
			// configuration. That separator belongs to the table it added,
			// so removing the table removes the separator as well.
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

// openCodeEntry turns opencode's array command back into the shape describe
// and the swap marker expect.
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

// writeEntry puts one server entry into a client's config.
//
// The TOML and JSONC paths splice bytes rather than re-serializing the file,
// because everything around the entry is somebody else's — other servers,
// their settings, the comments explaining why. A decode-and-write loses all of
// it, quietly.
func writeEntry(c client, entry map[string]any) error {
	contents, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}

	switch c.format {
	case formatTOML:
		table := c.key + "." + serverName
		previous := tomlPreserved(contents, table)
		if environment := tomlEnvironment(contents, table); environment != nil {
			previous["env"] = environment
		}
		shaped := mergeWithExisting(previous, renderEntry(entry, c.dialect))
		rendered := renderTOMLTable(table, shaped)
		start, end, found := tomlTableSpan(contents, table)
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

// writeBesideBackup keeps the first pre-swap copy and writes the new contents.
//
// The first copy rather than the latest: swapping something already swapped
// should still leave revert landing on what was there before any of this,
// which is the whole point of a tool that switches one entry back and forth.
func writeBesideBackup(c client, original, updated []byte) error {
	backup := backupPath(c)
	_, err := os.Stat(backup)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("inspect backup: %w", err)
	}
	return os.WriteFile(c.path, updated, 0o600)
}

func backupPath(c client) string { return c.path + ".mcp-swap-backup" }

func serverEntry(configuration map[string]any, key string) (map[string]any, bool) {
	servers, ok := configuration[key].(map[string]any)
	if !ok {
		return nil, false
	}
	entry, ok := servers[serverName].(map[string]any)
	return entry, ok
}

// isLocal reports whether this tool wrote the entry.
func isLocal(entry map[string]any) bool {
	_, swapped := swapMode(entry)
	return swapped
}

// swapMode reads the mode marker off an entry, which is also what status
// reports so a person can see which build a client is on.
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
