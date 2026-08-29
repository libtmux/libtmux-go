package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

// The module path is written down because an installed binary cannot read the
// go.mod it was built from, which makes it the one constant here that can
// drift without anything noticing.
func TestModulePathMatchesTheModuleThisWasBuiltFrom(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
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
	if _, err := os.Stat(filepath.Join("..", commandName)); err != nil {
		t.Errorf("cmd/%s: %v", commandName, err)
	}
}

func TestPreflightReportsACommandThatCannotLaunch(t *testing.T) {
	t.Parallel()
	entry := map[string]any{
		"command": "libtmux-mcp-does-not-exist",
		"args":    []any{},
	}
	if reason := preflight(entry); !strings.Contains(reason, "could not launch") {
		t.Errorf("preflight said %q, want it to name the launch failure", reason)
	}
}

// TestSelectedNarrowsToTheClientsNamed covers trying a build in one client on a
// machine where the others deliberately run a different server.
func TestSelectedNarrowsToTheClientsNamed(t *testing.T) {
	t.Parallel()
	all := knownClients("/home/someone")

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

	// A typo that quietly wrote nothing would report success having done
	// nothing, which is the failure this refusal exists to prevent.
	if _, err := selected(all, []string{"clod"}); err == nil {
		t.Error("an unknown client was accepted")
	}
}

// TestASecondSwapBacksUpWhatIsThereBySecondTime covers the file this tool is
// most able to damage: someone else's global agent configuration.
//
// A backup is written only when none exists, which is what makes swapping an
// already-swapped client still revert to the original. Left in place after a
// revert, that same rule turns into data loss: the file is edited, swapped
// again, and the revert restores the copy from before the edit.
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

// TestOneUnwritableClientDoesNotStopTheRest covers a swap across several CLIs
// where one of them has a config this cannot edit.
//
// Returning at the first failure leaves the clients before it swapped and the
// ones after it untouched, which is a mixed state nothing reports: the CLIs
// that were never reached look exactly like the ones that were, and the next
// run has to be made after fixing the broken one. Every client is attempted
// and every failure is named instead.
func TestOneUnwritableClientDoesNotStopTheRest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	write := func(name, contents string) client {
		t.Helper()
		target := client{
			name: name, path: filepath.Join(directory, name+".json"),
			key: "mcpServers", format: formatJSON, dialect: dialectStandard,
		}
		if err := os.WriteFile(target.path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return target
	}
	first := write("first", `{"mcpServers":{}}`)
	broken := write("broken", `NOT JSON`)
	last := write("last", `{"mcpServers":{}}`)

	entry := map[string]any{"command": "/bin/true"}
	err := useLocal([]client{first, broken, last}, entry, false)
	if err == nil {
		t.Fatal("a client that could not be written reported no error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("the error does not name the client that failed: %v", err)
	}

	for _, target := range []client{first, last} {
		contents, readErr := os.ReadFile(target.path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(contents), "tmux") {
			t.Errorf("%s was not swapped, so one broken config stopped the rest: %s",
				target.name, contents)
		}
	}
	// The one that could not be parsed is left exactly as it was.
	contents, readErr := os.ReadFile(broken.path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != "NOT JSON" {
		t.Errorf("the unreadable config was rewritten: %s", contents)
	}
}
