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
	"errors"
	"flag"
	"fmt"
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
		plan, err := prepareEntry(chosen, repository)
		if err != nil {
			return err
		}
		defer plan.cleanup()
		// Preflight dry runs too so they validate the selected build.
		if !chosen.noPreflight {
			fmt.Fprintf(os.Stderr, "preflight: %s\n", describe(plan.configured))
			if reason := preflight(plan.executable); reason != "" {
				return fmt.Errorf("preflight failed, nothing written: %s", reason)
			}
		}
		if !chosen.dryRun {
			if err := plan.install(); err != nil {
				return fmt.Errorf("install build: %w", err)
			}
		}
		return useLocal(clients, plan.configured, chosen.dryRun)
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

type entryPlan struct {
	configured map[string]any
	executable map[string]any
	install    func() error
	cleanup    func()
}

func prepareEntry(chosen options, repository string) (entryPlan, error) {
	entry, err := buildEntry(chosen, repository)
	if err != nil {
		return entryPlan{}, err
	}
	plan := entryPlan{
		configured: entry,
		executable: entry,
		install:    func() error { return nil },
		cleanup:    func() {},
	}
	if chosen.mode != modeBuild || (chosen.dryRun && chosen.noPreflight) {
		return plan, nil
	}

	directory, err := os.MkdirTemp("", buildDirectoryName+"-build-")
	if err != nil {
		return entryPlan{}, err
	}
	plan.cleanup = func() { _ = os.RemoveAll(directory) }
	temporary := filepath.Join(directory, commandName)
	if err := compileAt(repository, temporary); err != nil {
		plan.cleanup()
		return entryPlan{}, err
	}
	executable := make(map[string]any, len(entry))
	for key, value := range entry {
		executable[key] = value
	}
	executable["command"] = temporary
	plan.executable = executable
	if !chosen.dryRun {
		plan.install = func() error {
			contents, err := os.ReadFile(temporary)
			if err != nil {
				return err
			}
			persistent := entryCommand(entry)
			if err := os.MkdirAll(filepath.Dir(persistent), 0o755); err != nil {
				return err
			}
			return atomicWriteFile(persistent, contents, 0o755)
		}
	}
	return plan, nil
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
		binary, err := persistentBinaryPath()
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

func persistentBinaryPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, buildDirectoryName, commandName), nil
}

// compileAt produces the binary that preflight executes. Build mode installs
// it only after preflight succeeds.
func compileAt(repository, binary string) error {
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return err
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/"+commandName)
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
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
