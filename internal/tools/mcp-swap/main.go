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
// handshake before any write. Every selected config and backup destination is
// prepared before the first update, and each config is backed up once.
package main

import (
	"errors"
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

// modulePath is kept in step with the released server's go.mod by a test.
const modulePath = "github.com/libtmux/libtmux-go/mcp"

type buildMode string

type configScope string

const (
	modeDev       buildMode = "dev"
	modeBuild     buildMode = "build"
	modeInstalled buildMode = "installed"
	modeReleased  buildMode = "released"

	scopeUser    configScope = "user"
	scopeProject configScope = "project"
)

// client describes one supported global configuration.
type client struct {
	name       string
	binary     string
	path       string
	key        string
	format     configFormat
	dialect    entryDialect
	scope      configScope
	repository string
}

func knownClients(home string) []client {
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	return []client{
		{name: "claude", binary: "claude", path: filepath.Join(home, ".claude.json"), key: "mcpServers", format: formatJSON, dialect: dialectClaude},
		{name: "codex", binary: "codex", path: filepath.Join(home, ".codex", "config.toml"), key: "mcp_servers", format: formatTOML, dialect: dialectStandard},
		{name: "cursor", binary: "cursor-agent", path: filepath.Join(home, ".cursor", "mcp.json"), key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		{name: "gemini", binary: "gemini", path: filepath.Join(home, ".gemini", "settings.json"), key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		{name: "grok", binary: "grok", path: filepath.Join(home, ".grok", "config.toml"), key: "mcp_servers", format: formatTOML, dialect: dialectStandard},
		{name: "agy", binary: "agy", path: filepath.Join(home, ".gemini", "config", "mcp_config.json"), key: "mcpServers", format: formatJSON, dialect: dialectStandard},
		{name: "opencode", binary: "opencode", path: filepath.Join(config, "opencode", "opencode.jsonc"), key: "mcp", format: formatJSONC, dialect: dialectOpencode},
		{name: "pi", binary: "pi", path: filepath.Join(home, ".pi", "agent", "mcp.json"), key: "mcpServers", format: formatJSONC, dialect: dialectStandard},
	}
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(arguments []string, stdout, stderr io.Writer) int {
	chosen, err := parseArguments(arguments)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "mcp-swap:", err)
		printUsage(stderr)
		return 2
	}
	if chosen.help {
		printUsage(stdout)
		return 0
	}

	if err := run(chosen); err != nil {
		_, _ = fmt.Fprintln(stderr, "mcp-swap:", err)
		return 1
	}
	return 0
}

func printUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output,
		"usage: mcp-swap detect|status|use|use-local|revert|doctor [--dry-run]"+
			" [--mode dev|build|installed|released] [--ref VERSION]"+
			" [--client NAME] [--scope user|project] [--env KEY=VALUE]"+
			" [--no-preflight]")
}

type options struct {
	command     string
	dryRun      bool
	mode        buildMode
	ref         string
	noPreflight bool
	only        []string
	scope       configScope
	environment map[string]string
	help        bool
}

// parseArguments accepts flags after the command and rejects unknown tokens,
// so --dry-run cannot silently become a write.
func parseArguments(arguments []string) (options, error) {
	chosen := options{mode: modeDev}
	expecting := ""
	refProvided := false
	provided := map[string]bool{}
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
			case "--scope", "--env":
				if err := assign(&chosen, expecting, argument); err != nil {
					return options{}, err
				}
			}
			expecting = ""
			continue
		}
		name, value, assigned := strings.Cut(argument, "=")
		if assigned {
			argument = name
		}
		switch argument {
		case "help", "-h", "--help":
			chosen.help = true
		case "--dry-run", "-dry-run":
			provided["--dry-run"] = true
			chosen.dryRun = true
		case "--no-preflight", "-no-preflight":
			provided["--no-preflight"] = true
			chosen.noPreflight = true
		case "--mode", "-mode", "--ref", "-ref", "--client", "-client",
			"--scope", "-scope", "--env", "-env":
			expecting = "--" + strings.TrimLeft(argument, "-")
			provided[expecting] = true
			if expecting == "--ref" {
				refProvided = true
			}
			if assigned {
				remembered := expecting
				expecting = ""
				if err := assign(&chosen, remembered, value); err != nil {
					return options{}, err
				}
			}
		case "detect", "status", "use", "use-local", "revert", "doctor":
			if argument == "use" {
				argument = "use-local"
			}
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
	if chosen.help {
		return chosen, nil
	}
	if chosen.command == "" {
		return options{}, errors.New("say status, use-local, or revert")
	}
	if err := validateProvidedOptions(chosen.command, provided); err != nil {
		return options{}, err
	}
	if chosen.ref != "" && chosen.mode != modeReleased {
		return options{}, errors.New("--ref only means something with --mode released")
	}
	if refProvided && chosen.ref == "" {
		return options{}, errors.New("--ref must be a safe module version")
	}
	if chosen.ref != "" && !safeModuleVersion(chosen.ref) {
		return options{}, errors.New("--ref must be a safe module version")
	}
	if len(chosen.environment) != 0 && chosen.command != "use-local" {
		return options{}, errors.New("--env only means something with use")
	}
	if len(chosen.only) != 0 {
		hasClient := false
		for _, raw := range chosen.only {
			for part := range strings.SplitSeq(raw, ",") {
				hasClient = hasClient || strings.TrimSpace(part) != ""
			}
		}
		if !hasClient {
			return options{}, errors.New("client selection is empty")
		}
	}
	return chosen, nil
}

func validateProvidedOptions(command string, provided map[string]bool) error {
	allowed := map[string]map[string]bool{
		"--client":       {"detect": true, "status": true, "use-local": true, "revert": true},
		"--dry-run":      {"use-local": true, "revert": true},
		"--env":          {"use-local": true},
		"--mode":         {"use-local": true},
		"--no-preflight": {"use-local": true},
		"--ref":          {"use-local": true},
		"--scope":        {"status": true, "use-local": true, "revert": true},
	}
	for flag := range provided {
		if !allowed[flag][command] {
			return fmt.Errorf("%s does not apply to %s", flag, command)
		}
	}
	return nil
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
	case "--scope":
		switch configScope(value) {
		case scopeUser, scopeProject:
			chosen.scope = configScope(value)
			return nil
		default:
			return fmt.Errorf("%q is not user or project", value)
		}
	case "--env":
		name, environmentValue, found := strings.Cut(value, "=")
		if !found || name == "" {
			return fmt.Errorf("--env expects KEY=VALUE, got %q", value)
		}
		if name == "LIBTMUX_SAFETY" {
			return errors.New("LIBTMUX_SAFETY is retired; use LIBTMUX_TOOLSETS")
		}
		if chosen.environment == nil {
			chosen.environment = map[string]string{}
		}
		chosen.environment[name] = environmentValue
		return nil
	}
	return fmt.Errorf("%s takes no value", flagName)
}

func safeModuleVersion(value string) bool {
	if value == "latest" {
		return true
	}
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("-+.", character) {
			continue
		}
		return false
	}
	return true
}

func run(chosen options) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(home) {
		return errors.New("home directory must be absolute")
	}

	allClients := knownClients(home)
	clients, err := selected(allClients, chosen.only)
	if err != nil {
		return err
	}

	if chosen.command == "detect" {
		return detectTo(os.Stdout, clients)
	}
	moduleRoot, err := mcpModuleRoot()
	if err != nil {
		return err
	}
	repository := filepath.Dir(moduleRoot)

	switch chosen.command {
	case "status":
		return nativeStatusTo(os.Stdout, clients, chosen.scope, repository)
	case "revert":
		return nativeRevert(allClients, clients, chosen, repository)
	case "doctor":
		return nativeDoctorTo(os.Stdout, allClients, repository)
	case "use-local":
		plan, err := prepareEntry(chosen, moduleRoot)
		if err != nil {
			return err
		}
		defer plan.cleanup()
		return nativeUse(allClients, clients, plan, chosen, repository)
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
			part = canonicalClientName(strings.TrimSpace(part))
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

func canonicalClientName(name string) string {
	if name == "antigravity" {
		return "agy"
	}
	return name
}

func clientNames(clients []client) []string {
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.name)
	}
	return names
}

func scopedClients(clients []client, requested configScope, repository string) ([]client, error) {
	absolute, err := filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	chosen := make([]client, len(clients))
	for index, target := range clients {
		target.repository = absolute
		target.scope = scopeUser
		if target.name == "claude" {
			target.scope = requested
			if target.scope == "" {
				target.scope = scopeProject
			}
		}
		chosen[index] = target
	}
	return chosen, nil
}

type entryPlan struct {
	configured       map[string]any
	preflightCommand string
	install          func() error
	cleanup          func()
}

func prepareEntry(chosen options, repository string) (entryPlan, error) {
	entry, err := buildEntry(chosen, repository)
	if err != nil {
		return entryPlan{}, err
	}
	plan := entryPlan{
		configured: entry,
		install:    func() error { return nil },
		cleanup:    func() {},
	}
	if chosen.mode != modeBuild || chosen.dryRun {
		return plan, nil
	}

	var directory string
	plan.cleanup = func() {
		if directory != "" {
			_ = os.RemoveAll(directory)
		}
	}
	plan.install = func() error {
		var err error
		directory, err = os.MkdirTemp("", buildDirectoryName+"-build-")
		if err != nil {
			return err
		}
		temporary := filepath.Join(directory, commandName)
		if err := compileAt(repository, temporary); err != nil {
			return err
		}
		contents, err := os.ReadFile(temporary)
		if err != nil {
			return err
		}
		persistent := entryCommand(entry)
		if err := os.MkdirAll(filepath.Dir(persistent), 0o755); err != nil {
			return err
		}
		if info, err := os.Lstat(persistent); err == nil && !info.Mode().IsRegular() {
			return errors.New("persistent build destination is not a regular file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return atomicWriteFileExact(persistent, contents, 0o755)
	}
	return plan, nil
}

// buildEntry uses go -C for dev mode because not every client honors cwd.
func buildEntry(chosen options, repository string) (map[string]any, error) {
	environment := map[string]any{"LIBTMUX_MCP_SWAP": string(chosen.mode)}
	for name, value := range chosen.environment {
		environment[name] = value
	}
	entry := map[string]any{
		// Ownership marker for status and safe revert.
		"env": environment,
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

// mcpModuleRoot finds this checkout's released MCP module.
func mcpModuleRoot() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for directory := working; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "mcp", "go.mod")
		if contents, readErr := os.ReadFile(candidate); readErr == nil {
			for line := range strings.Lines(string(contents)) {
				if strings.TrimSpace(line) == "module "+modulePath {
					return filepath.Dir(candidate), nil
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return "", readErr
		}
		if parent := filepath.Dir(directory); parent == directory {
			return "", errors.New("run this from inside the libtmux-go checkout")
		}
	}
}
