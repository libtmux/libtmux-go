package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// What a swap writes is someone else's configuration file, so the parts worth
// covering are the ones that decide what lands in it: which build a mode
// names, and which spellings of a flag are understood.

func TestParseArgumentsReadsCommandsAndFlagsInAnyOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		want      options
	}{
		{
			"bare command",
			[]string{"use-local"},
			options{command: "use-local", mode: modeDev},
		},
		{
			"flag before command",
			[]string{"--dry-run", "status"},
			options{command: "status", dryRun: true, mode: modeDev},
		},
		{
			"mode as two tokens",
			[]string{"use-local", "--mode", "build"},
			options{command: "use-local", mode: modeBuild},
		},
		{
			"mode joined by equals",
			[]string{"use-local", "--mode=installed"},
			options{command: "use-local", mode: modeInstalled},
		},
		{
			"released with a ref",
			[]string{"use-local", "--mode", "released", "--ref", "v0.1.0"},
			options{command: "use-local", mode: modeReleased, ref: "v0.1.0"},
		},
		{
			"single dash is accepted too",
			[]string{"use-local", "-mode", "build", "-no-preflight"},
			options{command: "use-local", mode: modeBuild, noPreflight: true},
		},
		{
			"clients named one at a time",
			[]string{"use-local", "--client", "claude", "--client", "codex"},
			options{command: "use-local", mode: modeDev, only: []string{"claude", "codex"}},
		},
		{
			"clients joined by equals",
			[]string{"revert", "--client=claude"},
			options{command: "revert", mode: modeDev, only: []string{"claude"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseArguments(test.arguments)
			if err != nil {
				t.Fatalf("parseArguments(%q) error = %v", test.arguments, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseArguments(%q) = %+v, want %+v", test.arguments, got, test.want)
			}
		})
	}
}

// A tool whose job is editing someone else's files cannot have a spelling that
// quietly means something other than what was typed.
func TestParseArgumentsRefusesWhatItCannotHonour(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{"no command", []string{"--dry-run"}, "say status"},
		{"two commands", []string{"status", "revert"}, "say one command"},
		{"unknown flag", []string{"status", "--force"}, "not a command or a flag"},
		{"unknown mode", []string{"use-local", "--mode", "sideways"}, "is not dev, build"},
		{"mode with no value", []string{"use-local", "--mode"}, "wants a value"},
		// A ref means nothing to a build that is not a published one, and
		// silently ignoring it would swap to something other than what was
		// asked for.
		{"ref without released", []string{"use-local", "--ref", "v1"}, "only means something"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseArguments(test.arguments)
			if err == nil {
				t.Fatalf("parseArguments(%q) was accepted", test.arguments)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestBuildEntryNamesTheChosenBuild(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()

	dev, err := buildEntry(options{mode: modeDev}, repository)
	if err != nil {
		t.Fatal(err)
	}
	// "-C" rather than a "cwd" key: cwd is not something every client
	// honours, and one quietly ignored starts the server in the wrong place.
	if got := describe(dev); got != "go -C "+repository+" run ./cmd/"+commandName {
		t.Errorf("dev entry = %q", got)
	}

	installed, err := buildEntry(options{mode: modeInstalled}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(installed); got != commandName {
		t.Errorf("installed entry = %q, want just the binary name", got)
	}

	released, err := buildEntry(options{mode: modeReleased, ref: "v0.1.0"}, repository)
	if err != nil {
		t.Fatal(err)
	}
	want := "go run " + modulePath + "/cmd/" + commandName + "@v0.1.0"
	if got := describe(released); got != want {
		t.Errorf("released entry = %q, want %q", got, want)
	}

	latest, err := buildEntry(options{mode: modeReleased}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(latest); !strings.HasSuffix(got, "@latest") {
		t.Errorf("released entry without a ref = %q, want @latest", got)
	}
}

func TestDryRunBuildDoesNotCompileOrCreateCaches(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "go-was-run")
	goCommand := filepath.Join(bin, "go")
	if err := os.WriteFile(goCommand, []byte(
		"#!/bin/sh\nprintf called > \""+marker+"\"\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(directory, "cache")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("GOCACHE", filepath.Join(directory, "go-cache"))
	t.Setenv("PATH", bin)

	plan, err := prepareEntry(options{mode: modeBuild, dryRun: true}, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plan.cleanup)
	if plan.preflightCommand != "" {
		t.Fatalf("dry run prepared executable %q", plan.preflightCommand)
	}
	for _, path := range []string{marker, cache, filepath.Join(directory, "go-cache")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry run created %s: %v", filepath.Base(path), err)
		}
	}
}

// Every mode marks its entry, because a command of "go" is not by itself proof
// this tool wrote it and revert must not restore over something it did not.
func TestEveryModeMarksItsEntry(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	for _, mode := range []buildMode{modeDev, modeInstalled, modeReleased} {
		entry, err := buildEntry(options{mode: mode}, repository)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if !isLocal(entry) {
			t.Errorf("%s entry carries no marker", mode)
		}
		if got, _ := swapMode(entry); got != string(mode) {
			t.Errorf("%s entry is marked %q", mode, got)
		}
	}
}

// The released server's module path is written into client entries, so its
// source module must remain authoritative.
func TestModulePathMatchesTheReleasedServerModule(t *testing.T) {
	t.Parallel()
	repository, err := mcpModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for line := range strings.SplitSeq(string(contents), "\n") {
		if after, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			want = strings.TrimSpace(after)
			break
		}
	}
	if want == "" {
		t.Fatal("go.mod has no module line")
	}
	if modulePath != want {
		t.Errorf("modulePath = %q, but the module is %q", modulePath, want)
	}
}

// A command name that does not exist under cmd/ would produce an entry no
// client could start, which the preflight would catch and nothing else would.
func TestCommandNameExists(t *testing.T) {
	t.Parallel()
	repository, rootErr := mcpModuleRoot()
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if _, err := os.Stat(filepath.Join(repository, "cmd", commandName)); err != nil {
		t.Errorf("cmd/%s: %v", commandName, err)
	}
}

func TestSelectedNarrowsToTheClientsNamed(t *testing.T) {
	t.Parallel()
	all := knownClients("/home/someone")
	wantNames := []string{
		"claude", "codex", "cursor", "gemini", "grok", "agy", "opencode", "pi",
	}
	if got := clientNames(all); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("known clients = %v, want %v", got, wantNames)
	}
	pi := all[len(all)-1]
	if pi.path != "/home/someone/.pi/agent/mcp.json" ||
		pi.key != "mcpServers" || pi.format != formatJSONC || pi.dialect != dialectStandard {
		t.Fatalf("pi client = %+v, want the adapter's JSONC config", pi)
	}

	everything, err := selected(all, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(everything) != len(all) {
		t.Errorf("naming nothing chose %d clients, want all %d", len(everything), len(all))
	}

	some, err := selected(all, []string{"codex,claude"})
	if err != nil {
		t.Fatal(err)
	}
	// Declared order, not the order they were named, so what is written reads
	// the same however the request was spelled.
	if len(some) != 2 || some[0].name != "claude" || some[1].name != "codex" {
		t.Errorf("selected = %v, want claude then codex", clientNames(some))
	}

	agy, err := selected(all, []string{"antigravity,agy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := clientNames(agy); !reflect.DeepEqual(got, []string{"agy"}) {
		t.Errorf("antigravity alias selected %v, want one agy client", got)
	}

	// A typo that quietly wrote nothing would report success having done
	// nothing, which is the failure this refusal exists to prevent.
	if _, err := selected(all, []string{"clod"}); err == nil {
		t.Error("an unknown client was accepted")
	}
}

func TestAllEightClientsSwapAndRevertTogether(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	clients := knownClients(home)
	originals := make(map[string][]byte, len(clients))

	for _, target := range clients {
		if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
			t.Fatal(err)
		}
		var contents []byte
		if target.format == formatTOML {
			contents = []byte("[" + target.key + ".keep]\ncommand = \"keep\"\n")
		} else {
			contents = []byte("{\n  \"" + target.key + "\": {\"keep\": {\"command\": \"keep\"}}\n}\n")
		}
		if err := os.WriteFile(target.path, contents, 0o640); err != nil {
			t.Fatal(err)
		}
		originals[target.name] = contents
	}

	if err := useLocal(clients, devEntry(), false); err != nil {
		t.Fatal(err)
	}
	for _, target := range clients {
		entry, present, err := entryOf(target)
		if err != nil || !present || !isLocal(entry) {
			t.Fatalf("%s swapped entry = (%v, %t, %v)", target.name, entry, present, err)
		}
	}
	slices.Reverse(clients)
	if err := revert(clients, false); err != nil {
		t.Fatal(err)
	}
	for _, target := range clients {
		if got := []byte(readFile(t, target.path)); !bytes.Equal(got, originals[target.name]) {
			t.Errorf("%s did not restore its original bytes", target.name)
		}
		info, err := os.Stat(target.path)
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("%s restored mode = (%v, %v), want 0640", target.name, info, err)
		}
		for _, recovery := range []string{backupPath(target), recoveryStatePath(target)} {
			if _, err := os.Stat(recovery); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s retained recovery path %s: %v", target.name, recovery, err)
			}
		}
	}
}

func TestPiStatusExplainsAdapterAvailability(t *testing.T) {
	home := t.TempDir()
	clients := knownClients(home)
	pi := clients[len(clients)-1]
	if pi.name != "pi" {
		t.Fatalf("last client = %q, want pi", pi.name)
	}
	if err := os.MkdirAll(filepath.Dir(pi.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi.path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := reportTo(&output, []client{pi}); err != nil {
		t.Fatal(err)
	}
	const hint = "needs the pi-mcp-adapter package; pi has no built-in MCP client"
	if !strings.Contains(output.String(), hint) {
		t.Fatalf("pi status = %q, want adapter diagnostic", output.String())
	}

	adapter := filepath.Join(filepath.Dir(pi.path), "npm", "node_modules", "pi-mcp-adapter")
	if err := os.MkdirAll(adapter, 0o700); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := reportTo(&output, []client{pi}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), hint) {
		t.Fatalf("pi status still reports a missing adapter: %q", output.String())
	}
}

func TestHelpExitsSuccessfully(t *testing.T) {
	for _, argument := range []string{"help", "-h", "--help"} {
		t.Run(argument, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := execute([]string{argument}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "usage: mcp-swap") {
				t.Fatalf("stdout = %q, want usage", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestASecondSwapBacksUpWhatIsThereBySecondTime(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := client{
		name: "probe", path: filepath.Join(directory, "config.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	const original = `{"one":1,"mcpServers":{"tmux":{"command":"old"}}}`
	const edited = `{"one":1,"added":"after the revert","mcpServers":{"tmux":{"command":"old"}}}`
	if err := os.WriteFile(target.path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	swap := func(from string) {
		t.Helper()
		updated := strings.Replace(from,
			`{"command":"old"}`,
			`{"command":"go","env":{"LIBTMUX_MCP_SWAP":"dev"}}`, 1)
		if err := writeBesideBackup(target, []byte(from), []byte(updated)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		contents, err := os.ReadFile(target.path)
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}

	swap(original)
	if err := revert([]client{target}, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != original {
		t.Fatalf("after the first revert the file is %s, want %s", got, original)
	}
	if _, err := os.Stat(backupPath(target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revert left %s behind, and the next swap will not replace it",
			filepath.Base(backupPath(target)))
	}

	if err := os.WriteFile(target.path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	swap(edited)
	if err := revert([]client{target}, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != edited {
		t.Fatalf("the second revert restored %s and discarded the edit; want %s",
			got, edited)
	}
}

func TestWriteRefusesAnUninspectableBackup(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := client{name: "loop", path: filepath.Join(directory, "config.json")}
	const original = `{"keep":true}`
	if err := os.WriteFile(target.path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := backupPath(target)
	if err := os.Symlink(filepath.Base(backup), backup); err != nil {
		t.Fatal(err)
	}

	if err := writeBesideBackup(target, []byte(original), []byte(`{"changed":true}`)); err == nil {
		t.Fatal("write accepted a backup path it could not inspect")
	}
	if got := readFile(t, target.path); got != original {
		t.Fatalf("write changed the config without a usable backup: %s", got)
	}
}

func TestWriteReplacesTheConfigAtomically(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	target := client{name: "atomic", path: path}
	const original = `{"before":true}`
	const updated = `{"after":true}`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldFile.Close() })

	if err := writeBesideBackup(target, []byte(original), []byte(updated)); err != nil {
		t.Fatal(err)
	}
	oldContents, err := io.ReadAll(oldFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldContents) != original {
		t.Fatalf("the open pre-write file changed to %s; the write was not an atomic replacement", oldContents)
	}
	if got := readFile(t, path); got != updated {
		t.Fatalf("replacement contains %s, want %s", got, updated)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("replacement mode = %o, want 640", got)
	}
}

func TestWritePreservesAConfigSymlink(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	stored := filepath.Join(directory, "stored.json")
	linked := filepath.Join(directory, "config.json")
	const original = `{"before":true}`
	const updated = `{"after":true}`
	if err := os.WriteFile(stored, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(stored), linked); err != nil {
		t.Fatal(err)
	}
	target := client{name: "linked", path: linked}

	if err := writeBesideBackup(target, []byte(original), []byte(updated)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(linked)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("write replaced the config symlink instead of its target")
	}
	if got := readFile(t, stored); got != updated {
		t.Fatalf("symlink target contains %s, want %s", got, updated)
	}
}

func TestRevertRefusesRetargetedConfigSymlink(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	first := filepath.Join(directory, "first.json")
	second := filepath.Join(directory, "second.json")
	linked := filepath.Join(directory, "config.json")
	original := []byte(`{"mcpServers":{"tmux":{"command":"old"}}}`)
	if err := os.WriteFile(first, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(first), linked); err != nil {
		t.Fatal(err)
	}
	target := client{
		name: "linked", path: linked,
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	if err := writeEntry(target, devEntry()); err != nil {
		t.Fatal(err)
	}
	swapped := []byte(readFile(t, first))
	if err := os.WriteFile(second, swapped, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(second), linked); err != nil {
		t.Fatal(err)
	}

	if err := revert([]client{target}, false); err == nil {
		t.Fatal("revert accepted a retargeted config symlink")
	}
	if got := readFile(t, first); got != string(swapped) {
		t.Fatalf("original physical target changed: got %q, want %q", got, swapped)
	}
	if got := readFile(t, second); got != string(swapped) {
		t.Fatalf("replacement physical target changed: got %q, want %q", got, swapped)
	}
	if got, err := os.Readlink(linked); err != nil || got != filepath.Base(second) {
		t.Fatalf("config symlink = (%q, %v), want %q", got, err, filepath.Base(second))
	}
	if _, err := os.Stat(backupPath(target)); err != nil {
		t.Fatalf("revert removed recovery data after refusing: %v", err)
	}
}

func TestDryRunsValidateWithoutWriting(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	config := filepath.Join(directory, "config.json")
	if err := os.Symlink(filepath.Base(config), config); err != nil {
		t.Fatal(err)
	}
	target := client{name: "config-loop", path: config}
	if err := useLocal([]client{target}, devEntry(), true); err == nil {
		t.Error("use-local dry-run accepted a config path it could not inspect")
	}

	revertTarget := client{name: "backup-loop", path: filepath.Join(directory, "other.json")}
	backup := backupPath(revertTarget)
	if err := os.Symlink(filepath.Base(backup), backup); err != nil {
		t.Fatal(err)
	}
	if err := revert([]client{revertTarget}, true); err == nil {
		t.Error("revert dry-run accepted a backup path it could not inspect")
	}

	backupTarget := client{
		name: "backup-loop", path: filepath.Join(directory, "valid.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	if err := os.WriteFile(backupTarget.path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backup = backupPath(backupTarget)
	if err := os.Symlink(filepath.Base(backup), backup); err != nil {
		t.Fatal(err)
	}
	if err := useLocal([]client{backupTarget}, devEntry(), true); err == nil {
		t.Error("use-local dry-run accepted a backup path it could not inspect")
	}

	valid := client{
		name: "valid", path: filepath.Join(directory, "dry-run.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	original := []byte(`{"mcpServers":{"other":{"command":"keep"}}}`)
	if err := os.WriteFile(valid.path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := useLocal([]client{valid}, devEntry(), true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, valid.path); got != string(original) {
		t.Fatalf("use-local dry run changed the config: %s", got)
	}
	if _, err := os.Stat(backupPath(valid)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("use-local dry run created a backup: %v", err)
	}
	if _, err := os.Stat(recoveryStatePath(valid)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("use-local dry run created recovery state: %v", err)
	}
	if err := writeEntry(valid, devEntry()); err != nil {
		t.Fatal(err)
	}
	swapped := readFile(t, valid.path)
	if err := revert([]client{valid}, true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, valid.path); got != swapped {
		t.Fatalf("revert dry run changed the config: %s", got)
	}
	if got := readFile(t, backupPath(valid)); got != string(original) {
		t.Fatalf("revert dry run changed the backup: %s", got)
	}
}

func TestDryRunDoesNotTouchTheConfigDirectory(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := client{
		name: "dry-run", path: filepath.Join(directory, "config.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	if err := os.WriteFile(target.path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(directory, fixed, fixed); err != nil {
		t.Fatal(err)
	}

	if err := useLocal([]client{target}, devEntry(), true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(fixed) {
		t.Fatalf("dry run changed directory mtime from %s to %s", fixed, info.ModTime())
	}
}

func TestMalformedLaterClientStopsAllWrites(t *testing.T) {
	t.Parallel()

	first := jsonPreflightClient(t, "first", `{"mcpServers":{}}`)
	broken := jsonPreflightClient(t, "broken", `NOT JSON`)
	firstOriginal := readFile(t, first.path)
	brokenOriginal := readFile(t, broken.path)

	entry := map[string]any{"command": "/bin/true"}
	err := useLocal([]client{first, broken}, entry, false)
	if err == nil {
		t.Fatal("a malformed selected config reported no error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("the error does not name the client that failed: %v", err)
	}
	assertConfigWasNotWritten(t, first, firstOriginal)
	assertConfigWasNotWritten(t, broken, brokenOriginal)
}

func TestDuplicatePhysicalConfigsStopAllWrites(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	stored := filepath.Join(directory, "shared.json")
	original := []byte(`{"mcpServers":{"other":{"command":"keep"}}}`)
	if err := os.WriteFile(stored, original, 0o600); err != nil {
		t.Fatal(err)
	}
	clients := make([]client, 0, 2)
	for _, name := range []string{"first", "later"} {
		path := filepath.Join(directory, name+".json")
		if err := os.Symlink(filepath.Base(stored), path); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client{
			name: name, path: path,
			key: "mcpServers", format: formatJSON, dialect: dialectStandard,
		})
	}

	err := useLocal(clients, map[string]any{"command": "/bin/true"}, false)
	if err == nil {
		t.Fatal("two selected paths to one physical config were accepted")
	}
	if got := readFile(t, stored); got != string(original) {
		t.Fatalf("shared config changed: got %q, want %q", got, original)
	}
	for _, target := range clients {
		if _, statErr := os.Stat(backupPath(target)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s backup exists after refusal: %v", target.name, statErr)
		}
	}
}

func TestInvalidLaterBackupDestinationStopsAllWrites(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	first := jsonPreflightClient(t, "first", `{"mcpServers":{}}`)
	locked := filepath.Join(directory, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	later := client{
		name: "later", path: filepath.Join(locked, "later.json"),
		key: "mcpServers", format: formatJSON, dialect: dialectStandard,
	}
	if err := os.WriteFile(later.path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	firstOriginal := readFile(t, first.path)
	laterOriginal := readFile(t, later.path)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := useLocal([]client{first, later}, map[string]any{"command": "/bin/true"}, false)
	if err == nil {
		t.Fatal("an unusable later backup destination reported no error")
	}
	if !strings.Contains(err.Error(), "later") {
		t.Errorf("the error does not name the client that failed: %v", err)
	}
	assertConfigWasNotWritten(t, first, firstOriginal)
	if got := readFile(t, later.path); got != laterOriginal {
		t.Fatalf("later config changed: got %q, want %q", got, laterOriginal)
	}
	if _, statErr := os.Stat(backupPath(later)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("later backup exists after refusal: %v", statErr)
	}
}

func TestApplyFailureRollsBackEarlierConfigs(t *testing.T) {
	t.Parallel()

	first := jsonPreflightClient(t, "first", `{"mcpServers":{}}`)
	later := jsonPreflightClient(t, "later", `{"mcpServers":{}}`)
	firstOriginal := readFile(t, first.path)
	laterOriginal := readFile(t, later.path)
	plan := entryPlan{
		configured: map[string]any{"command": "/bin/true"},
		install: func() error {
			backup := backupPath(later)
			return os.Symlink(filepath.Base(backup), backup)
		},
		cleanup: func() {},
	}

	err := usePreparedLocal([]client{first, later}, plan, false, false)
	if err == nil {
		t.Fatal("an apply-time failure reported success")
	}
	if got := readFile(t, first.path); got != firstOriginal {
		t.Fatalf("earlier config was not rolled back: got %q, want %q", got, firstOriginal)
	}
	if got := readFile(t, later.path); got != laterOriginal {
		t.Fatalf("later config changed: got %q, want %q", got, laterOriginal)
	}
	if _, statErr := os.Stat(backupPath(first)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("earlier recovery artifact remains after a proven rollback: %v", statErr)
	}
}
