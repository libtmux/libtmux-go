package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/workspace"
)

// testServer returns a server on a socket unique to the test, and kills it when
// the test ends. The workspace module cannot use the tmux module's tmuxtest
// package, because tmuxtest lives in the tmux module and this module depends on
// that module rather than the other way round.
func testServer(t *testing.T) (tmux.Server, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	server := tmux.NewServer(tmux.ServerOptions{SocketPath: socket}).WithStrictErrors()
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})
	return server, ctx
}

func TestParseAcceptsTmuxpShorthandAndLongForm(t *testing.T) {
	t.Parallel()
	document := []byte(`
session_name: shorthands
windows:
  - window_name: long form
    panes:
      - shell_command:
          - echo 'did you know'
          - echo 'you can inline'
      - shell_command: echo 'single commands'
      - echo 'for panes'
`)

	parsed, err := workspace.Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SessionName != "shorthands" || len(parsed.Windows) != 1 {
		t.Fatalf("Parse() = %#v, want one window named shorthands", parsed)
	}
	panes := parsed.Windows[0].Panes
	if len(panes) != 3 {
		t.Fatalf("panes = %d, want 3", len(panes))
	}
	for index, want := range [][]string{
		{"echo 'did you know'", "echo 'you can inline'"},
		{"echo 'single commands'"},
		{"echo 'for panes'"},
	} {
		if len(panes[index].Commands) != len(want) {
			t.Errorf("pane %d commands = %#v, want %#v", index, panes[index].Commands, want)
			continue
		}
		for position, command := range want {
			if panes[index].Commands[position].Command != command {
				t.Errorf("pane %d command %d = %q, want %q",
					index, position, panes[index].Commands[position].Command, command)
			}
		}
	}
}

func TestParseRejectsMisspelledAndIncompleteWorkspaces(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]string{
		"misspelled field": "session_nme: typo\nwindows: [{panes: [echo hi]}]\n",
		"no session name":  "windows: [{panes: [echo hi]}]\n",
		"no windows":       "session_name: empty\n",
		"bad focus":        "session_name: s\nwindows: [{focus: maybe, panes: [echo hi]}]\n",
		// A custom UnmarshalYAML receives a node, and yaml.Node.Decode does not
		// inherit the decoder's KnownFields setting, so each level re-checks its
		// own keys. Without that, a misspelling is silently dropped.
		"unknown workspace field": "session_name: s\nno_such_key: 1\nwindows: [{panes: [echo hi]}]\n",
		"unknown window field":    "session_name: s\nwindows: [{nope: 1, panes: [echo hi]}]\n",
		"unknown pane field":      "session_name: s\nwindows: [{panes: [{nope: 1, shell_command: echo hi}]}]\n",
		"unknown command field":   "session_name: s\nwindows: [{panes: [{shell_command: [{cmd: hi, nope: 1}]}]}]\n",
	} {
		if _, err := workspace.Parse([]byte(document)); !errors.Is(err, workspace.ErrInvalidWorkspace) {
			t.Errorf("%s: Parse() error = %v, want ErrInvalidWorkspace", name, err)
		}
	}
}

func TestParseAcceptsQuotedBooleans(t *testing.T) {
	t.Parallel()
	parsed, err := workspace.Parse([]byte(
		"session_name: s\nwindows: [{focus: \"true\", panes: [{focus: 'yes', shell_command: echo hi}]}]\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !bool(parsed.Windows[0].Focus) || !bool(parsed.Windows[0].Panes[0].Focus) {
		t.Fatalf("quoted booleans did not decode as true: %#v", parsed.Windows[0])
	}
}

//libtmux:real-tmux
func TestBuildCreatesTheDescribedHierarchy(t *testing.T) {
	server, ctx := testServer(t)
	parsed, err := workspace.Parse([]byte(`
session_name: build-test
windows:
  - window_name: editor
    panes:
      - echo one
      - echo two
  - window_name: shell
    panes:
      - echo three
`))
	if err != nil {
		t.Fatal(err)
	}

	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	name, ok := session.Name()
	if !ok || name != "build-test" {
		t.Fatalf("session name = (%q, %v), want build-test", name, ok)
	}

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	windows := snapshot.Windows()
	if len(windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(windows))
	}
	byName := map[string]int{}
	for _, window := range windows {
		windowName, _ := window.Name()
		byName[windowName] = len(window.Panes())
	}
	if byName["editor"] != 2 {
		t.Errorf("editor panes = %d, want 2", byName["editor"])
	}
	if byName["shell"] != 1 {
		t.Errorf("shell panes = %d, want 1", byName["shell"])
	}
}

//libtmux:real-tmux
func TestBuildAppliesEnvironmentAndOptions(t *testing.T) {
	server, ctx := testServer(t)
	parsed, err := workspace.Parse([]byte(`
session_name: options-test
environment:
  LIBTMUX_WORKSPACE_PROBE: applied
options:
  base-index: "1"
windows:
  - window_name: only
    panes:
      - echo hi
`))
	if err != nil {
		t.Fatal(err)
	}

	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	value, ok, err := session.GetEnvironment(ctx, "LIBTMUX_WORKSPACE_PROBE")
	if err != nil || !ok || value.Removed || value.Value != "applied" {
		t.Fatalf("GetEnvironment() = (%#v, %v, %v), want applied", value, ok, err)
	}
	raw, ok, err := session.RawOption(ctx, "base-index")
	if err != nil || !ok || raw != "1" {
		t.Fatalf("RawOption(base-index) = (%q, %v, %v), want 1", raw, ok, err)
	}
}

//libtmux:real-tmux
func TestBuildRunsEveryTmuxpExampleThatThisModuleSupports(t *testing.T) {
	examples, err := filepath.Glob(os.ExpandEnv("$HOME/work/python/tmuxp/examples/*.yaml"))
	if err != nil || len(examples) == 0 {
		t.Skip("tmuxp examples are not available on this machine")
	}

	var parsed, built int
	for _, path := range examples {
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		workspaceValue, err := workspace.Parse(document)
		if err != nil {
			// Unsupported tmuxp features are expected; this test measures how far
			// the supported subset reaches, not full compatibility.
			continue
		}
		parsed++
		if shell, ok := missingShell(workspaceValue); ok {
			t.Logf("%s: skipped, %s is not installed here", filepath.Base(path), shell)
			continue
		}
		if reference, ok := unsetEnvironmentReference(document); ok {
			t.Logf("%s: skipped, %s is not set here", filepath.Base(path), reference)
			continue
		}

		server, ctx := testServer(t)
		if _, err := workspace.Build(ctx, server, workspaceValue); err != nil {
			t.Errorf("%s: Build() error = %v", filepath.Base(path), err)
			continue
		}
		built++
	}
	t.Logf("tmuxp examples: %d parsed, %d built, of %d total", parsed, built, len(examples))
	if built == 0 {
		t.Fatal("no tmuxp example built; the supported subset reaches nothing")
	}
}

// missingShell reports an absolute shell path a workspace names that this
// machine does not have. Some tmuxp examples pin interpreters by path, and a
// pane whose command cannot start takes its session with it, which is an
// environment fact rather than a builder defect.
func missingShell(described workspace.Workspace) (string, bool) {
	for _, window := range described.Windows {
		for _, shell := range append([]string{window.Shell}, paneShells(window)...) {
			if shell == "" || !filepath.IsAbs(shell) {
				continue
			}
			if _, err := os.Stat(strings.Fields(shell)[0]); err != nil {
				return shell, true
			}
		}
	}
	return "", false
}

func paneShells(window workspace.Window) []string {
	shells := make([]string, 0, len(window.Panes))
	for _, pane := range window.Panes {
		shells = append(shells, pane.Shell)
	}
	return shells
}

// unsetEnvironmentReference reports a ${VAR} the document depends on that this
// machine does not define. tmuxp expands those before building; this module
// passes values through, so an unexpanded reference reaches tmux and is
// rejected. That is an environment fact rather than a builder defect.
func unsetEnvironmentReference(document []byte) (string, bool) {
	for _, match := range environmentReference.FindAllSubmatch(document, -1) {
		name := string(match[1])
		if _, ok := os.LookupEnv(name); !ok {
			return "${" + name + "}", true
		}
	}
	return "", false
}

var environmentReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// TestGlobalOptionsAcceptEveryTableTmuxAccepts covers the canonical tmuxp
// entry: a workspace writes mode-keys, a window option, into global_options
// because tmux's own set-option -g resolves a name against whichever table
// declares it.
func TestGlobalOptionsAcceptEveryTableTmuxAccepts(t *testing.T) {
	for _, testCase := range []struct{ name, option, value string }{
		{"session scope", "status-style", "bg=red"},
		{"window scope", "mode-keys", "vi"},
		{"server scope", "exit-empty", "off"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, ctx := testServer(t)
			described, err := workspace.Parse([]byte(
				"session_name: scopes\nglobal_options:\n  " +
					testCase.option + ": " + testCase.value +
					"\nwindows:\n  - window_name: probe\n",
			))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := workspace.Build(ctx, server, described); err != nil {
				t.Fatalf("build with %q: %v", testCase.option, err)
			}
		})
	}
}

// TestGlobalOptionsRejectAnOptionNoTableDeclares proves the scope fallback
// reports the name the file got wrong rather than swallowing it after three
// tries.
func TestGlobalOptionsRejectAnOptionNoTableDeclares(t *testing.T) {
	server, ctx := testServer(t)
	described, err := workspace.Parse([]byte(
		"session_name: unknown-option\nglobal_options:\n  no-such-option: x\n" +
			"windows:\n  - window_name: probe\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := workspace.Build(ctx, server, described); err == nil ||
		!strings.Contains(err.Error(), "no-such-option") {
		t.Fatalf("got %v, want an error naming no-such-option", err)
	}
}

// TestFirstPaneShellRunsAsTheWindowCommand covers a shell written on every
// pane of a window, where tmux creates the first pane with the window and so
// gives it no creation call of its own to carry a command.
func TestFirstPaneShellRunsAsTheWindowCommand(t *testing.T) {
	server, ctx := testServer(t)
	described, err := workspace.Parse([]byte(
		"session_name: first-pane-shell\nwindows:\n  - window_name: probe\n" +
			"    panes:\n      - shell: cat\n      - shell: cat\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	panes, err := session.SearchPanes(ctx, nil)
	if err != nil {
		t.Fatalf("search panes: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(panes))
	}
	// tmux reports the shell until the pane's command has replaced it, so the
	// command is waited for rather than read once. Reading once passed only
	// while the build was slow enough to hide the gap.
	for index, pane := range panes {
		if err := tmux.Poll(ctx, 10*time.Millisecond, func(
			ctx context.Context,
		) (bool, error) {
			fresh, err := server.Pane(ctx, pane.ID())
			if err != nil {
				return false, err
			}
			command, ok := fresh.Formats().PaneCurrentCommand()
			return ok && command == "cat", nil
		}); err != nil {
			t.Errorf("pane %d never ran cat: %v", index, err)
		}
	}
}

// TestWindowShellAndFirstPaneShellConflict keeps the module's promise that a
// field is rejected rather than silently ignored: both name the same pane's
// command, so a file setting both is asking for two.
func TestWindowShellAndFirstPaneShellConflict(t *testing.T) {
	_, err := workspace.Parse([]byte(
		"session_name: conflict\nwindows:\n  - window_name: probe\n" +
			"    window_shell: cat\n    panes:\n      - shell: sleep 60\n",
	))
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
}

// TestQuotedBooleansAreAcceptedWhereverABooleanIs covers tmuxp's quoted
// spellings on a command, which are the same key a pane accepts.
func TestQuotedBooleansAreAcceptedWhereverABooleanIs(t *testing.T) {
	for _, testCase := range []struct{ name, document string }{
		{
			name: "on a pane",
			document: "session_name: a\nwindows:\n  - panes:\n" +
				"      - shell_command: [echo hi]\n        enter: \"true\"\n",
		},
		{
			name: "on a command",
			document: "session_name: a\nwindows:\n  - panes:\n" +
				"      - shell_command:\n          - cmd: echo hi\n            enter: \"true\"\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := workspace.Parse([]byte(testCase.document)); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}

// TestACommandReportsItsOwnFailure keeps a command's decode failure from being
// restated as the shape of the list holding it, which named the wrong line and
// the wrong mistake.
func TestACommandReportsItsOwnFailure(t *testing.T) {
	_, err := workspace.Parse([]byte(
		"session_name: a\nwindows:\n  - panes:\n      - shell_command:\n" +
			"          - cmd: echo hi\n            enter: maybe\n",
	))
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
	message := err.Error()
	if !strings.Contains(message, "line 6") || !strings.Contains(message, "not a boolean") {
		t.Errorf("error does not name the failing line and cause: %v", err)
	}
	if strings.Contains(message, "is not a list of commands") {
		t.Errorf("a command's failure was restated as the list's shape: %v", err)
	}
	if strings.Count(message, workspace.ErrInvalidWorkspace.Error()) != 1 {
		t.Errorf("error is wrapped more than once: %v", err)
	}
}

// TestPaneIsConstructibleInGo pins the reason Bool is exported: an optional
// setting is a *Bool, and a caller outside this package cannot make a pointer
// to a type it cannot name.
func TestPaneIsConstructibleInGo(t *testing.T) {
	enter := workspace.Bool(false)
	pane := workspace.Pane{Focus: true, Enter: &enter, SuppressHistory: &enter}
	if !bool(pane.Focus) || bool(*pane.Enter) {
		t.Fatalf("constructed pane does not hold what it was given: %+v", pane)
	}
}

// TestWindowIndexIsHonoredOnEveryWindow covers the first window, which tmux
// creates with the session and so cannot carry an index into its creation.
func TestWindowIndexIsHonoredOnEveryWindow(t *testing.T) {
	server, ctx := testServer(t)
	described, err := workspace.Parse([]byte(
		"session_name: idx\nwindows:\n  - window_name: first\n    window_index: 7\n" +
			"  - window_name: second\n    window_index: 9\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		t.Fatalf("search windows: %v", err)
	}
	found := map[string]int{}
	for _, window := range windows {
		name, _ := window.Formats().WindowName()
		index, _ := window.Formats().WindowIndex()
		found[name] = index
	}
	for name, want := range map[string]int{"first": 7, "second": 9} {
		if found[name] != want {
			t.Errorf("window %q is at index %d, want %d", name, found[name], want)
		}
	}
}

// TestSleepIsRejectedWhereverItIsNegative covers a delay tmux could never
// wait for, on a pane as well as on a command.
func TestSleepIsRejectedWhereverItIsNegative(t *testing.T) {
	for _, testCase := range []struct{ name, document string }{
		{
			name: "on a pane",
			document: "session_name: a\nwindows:\n  - panes:\n" +
				"      - shell_command: [echo hi]\n        sleep_before: -1\n",
		},
		{
			name: "on a command",
			document: "session_name: a\nwindows:\n  - panes:\n" +
				"      - shell_command:\n          - cmd: echo hi\n            sleep_before: -1\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := workspace.Parse([]byte(testCase.document)); !errors.Is(
				err, workspace.ErrInvalidWorkspace,
			) {
				t.Fatalf("got %v, want ErrInvalidWorkspace", err)
			}
		})
	}
}

// TestSleepIsReadInSeconds pins the unit, which a time.Duration field would
// otherwise invite a caller to write as a duration string.
func TestSleepIsReadInSeconds(t *testing.T) {
	described, err := workspace.Parse([]byte(
		"session_name: a\nwindows:\n  - panes:\n" +
			"      - shell_command: [echo hi]\n        sleep_before: 0.5\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := described.Windows[0].Panes[0].SleepBefore; got != 500*time.Millisecond {
		t.Fatalf("sleep_before: 0.5 became %v, want 500ms", got)
	}
}

// TestARefusedFieldReadsDifferentlyFromATypo covers the two tmuxp fields this
// module will not grow. Reporting them as unknown reads as a misspelling and
// sends a reader looking for the spelling that works.
func TestARefusedFieldReadsDifferentlyFromATypo(t *testing.T) {
	for _, testCase := range []struct{ name, document, want string }{
		{
			name:     "plugins",
			document: "session_name: a\nplugins: [x]\nwindows:\n  - window_name: w\n",
			want:     "not supported",
		},
		{
			name:     "before_script",
			document: "session_name: a\nbefore_script: ./x.sh\nwindows:\n  - window_name: w\n",
			want:     "not supported",
		},
		{
			name:     "a misspelling",
			document: "session_name: a\nsession_nme: b\nwindows:\n  - window_name: w\n",
			want:     "unknown field",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := workspace.Parse([]byte(testCase.document))
			if !errors.Is(err, workspace.ErrInvalidWorkspace) {
				t.Fatalf("got %v, want ErrInvalidWorkspace", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error %q does not say %q", err, testCase.want)
			}
		})
	}
}

// TestBuildLeavesAChosenTransportAlone covers the way a caller declines the
// control connection Build otherwise opens. A connection is a tmux client: it
// appears in list-clients, counts toward session_attached, and fires a
// client-attached hook, so a caller whose tmux reacts to attachment needs a
// way to say no.
func TestBuildLeavesAChosenTransportAlone(t *testing.T) {
	server, ctx := testServer(t)
	described, err := workspace.Parse([]byte(
		"session_name: chosen\nwindows:\n  - window_name: w\n    panes:\n      - {}\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Saying so with the engine that runs everything as a tmux process.
	onProcesses := server.WithEngine(server.SubprocessEngine())
	session, err := workspace.Build(ctx, onProcesses, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	clients, err := server.SearchClients(ctx, nil)
	if err != nil {
		t.Fatalf("search clients: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("build attached %d clients despite a chosen transport", len(clients))
	}
	attached, ok := session.Formats().SessionAttached()
	if ok && attached != 0 {
		t.Errorf("session_attached = %d after the build, want 0", attached)
	}
}

// TestPanesLandInTheOrderTheFileListsThem covers the order a reader compares
// against tmuxp. Splitting the window targets whichever pane tmux considers
// current, which puts every new pane beside the first and reverses the rest,
// so a layout that reads correctly in the file comes out scrambled.
func TestPanesLandInTheOrderTheFileListsThem(t *testing.T) {
	server, ctx := testServer(t)
	document := "session_name: order\nwindows:\n  - window_name: w\n    panes:\n"
	for _, marker := range []string{"A", "B", "C", "D"} {
		document += "      - shell: sh -c 'printf " + marker + "; sleep 60'\n"
	}
	described, err := workspace.Parse([]byte(document))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	panes, err := session.SearchPanes(ctx, nil)
	if err != nil {
		t.Fatalf("search panes: %v", err)
	}
	if len(panes) != 4 {
		t.Fatalf("built %d panes, want 4", len(panes))
	}
	for position, want := range []string{"A", "B", "C", "D"} {
		pane := panes[position]
		index, _ := pane.Formats().PaneIndex()
		if index != position {
			t.Fatalf("pane %d reported index %d", position, index)
		}
		if err := tmux.Poll(ctx, 10*time.Millisecond, func(
			ctx context.Context,
		) (bool, error) {
			fresh, err := server.Pane(ctx, pane.ID())
			if err != nil {
				return false, err
			}
			lines, err := fresh.Capture(ctx, tmux.CapturePaneRequest{})
			if err != nil {
				return false, err
			}
			return len(lines) != 0 && strings.Contains(lines[0], want), nil
		}); err != nil {
			t.Errorf("pane at index %d never showed %q: %v", position, want, err)
		}
	}
}

// TestTheFirstPaneStartsWhereItsFileSays covers a directory on a window's
// first pane, which tmux creates with the window and which therefore has no
// creation call of its own to carry one.
func TestTheFirstPaneStartsWhereItsFileSays(t *testing.T) {
	server, ctx := testServer(t)
	first := t.TempDir()
	second := t.TempDir()
	described, err := workspace.Parse([]byte(
		"session_name: dirs\nwindows:\n  - window_name: w\n    panes:\n" +
			"      - start_directory: " + first + "\n" +
			"      - start_directory: " + second + "\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	panes, err := session.SearchPanes(ctx, nil)
	if err != nil {
		t.Fatalf("search panes: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("built %d panes, want 2", len(panes))
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	minimum33, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{first, second} {
		// tmux gained pane_start_path in 3.3. Before that the directory a pane
		// was created in is only readable as the directory it is currently in,
		// which is the same thing for a pane nothing has run in yet.
		got, ok := panes[index].Formats().PaneStartPath()
		format := "pane_start_path"
		if !version.AtLeast(minimum33) {
			got, ok = panes[index].Formats().PaneCurrentPath()
			format = "pane_current_path"
		}
		if !ok || got != want {
			t.Errorf("pane %d %s = %q (present=%v), want %q on tmux %s",
				index, format, got, ok, want, version)
		}
	}
}

// TestSessionOptionsAcceptWhatTmuxAccepts covers a window option written
// beside session ones, which is what tmuxp's own examples do and what tmux
// itself resolves without complaint.
func TestSessionOptionsAcceptWhatTmuxAccepts(t *testing.T) {
	for _, testCase := range []struct{ name, option, value string }{
		{"session table", "history-limit", "5000"},
		{"window table", "main-pane-height", "10"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, ctx := testServer(t)
			described, err := workspace.Parse([]byte(
				"session_name: opts\noptions:\n  " + testCase.option + ": " + testCase.value +
					"\nwindows:\n  - window_name: w\n",
			))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := workspace.Build(ctx, server, described); err != nil {
				t.Fatalf("build with %q: %v", testCase.option, err)
			}
		})
	}
}

// TestEnvironmentIsTheSessionsRatherThanThePanes pins what a file gets when
// two panes name the same variable. tmux keeps one environment per session, so
// the last value written is what every later process sees.
func TestEnvironmentIsTheSessionsRatherThanThePanes(t *testing.T) {
	server, ctx := testServer(t)
	described, err := workspace.Parse([]byte(
		"session_name: env\nenvironment:\n  PORT: '8000'\nwindows:\n  - window_name: w\n" +
			"    panes:\n      - environment:\n          PORT: '8001'\n" +
			"      - environment:\n          PORT: '8002'\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	value, ok, err := session.GetEnvironment(ctx, "PORT")
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	if !ok || value.Value != "8002" {
		t.Fatalf("PORT = %q (present=%v), want the last value written", value.Value, ok)
	}
}

// TestAnUnknownLayoutIsRefusedBeforeAnythingIsBuilt covers a misspelled layout,
// which otherwise parsed and validated clean and then failed partway through
// the build, leaving a session half made.
func TestAnUnknownLayoutIsRefusedBeforeAnythingIsBuilt(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		layout  string
		refused bool
	}{
		{"a misspelling", "main-verticle", true},
		{"a name tmux knows", "main-vertical", false},
		{"a layout string tmux printed", "8466,80x24,0,0{78x24,0,0,0}", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := workspace.Parse([]byte(
				"session_name: layouts\nwindows:\n  - window_name: w\n    layout: " +
					testCase.layout + "\n",
			))
			if testCase.refused {
				if !errors.Is(err, workspace.ErrInvalidWorkspace) {
					t.Fatalf("got %v, want ErrInvalidWorkspace", err)
				}
				if !strings.Contains(err.Error(), testCase.layout) {
					t.Errorf("error does not name the layout: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("a layout tmux accepts was refused: %v", err)
			}
		})
	}
}

// TestAFirstWindowMayAskForTheIndexItAlreadyHas covers a file that numbers its
// windows explicitly, which is a common tmuxp idiom. tmux refuses to move a
// window to the index it already occupies, so asking for the server's
// base-index failed the whole build.
func TestAFirstWindowMayAskForTheIndexItAlreadyHas(t *testing.T) {
	for _, base := range []int{0, 1} {
		t.Run("base-index "+strconv.Itoa(base), func(t *testing.T) {
			configuration := filepath.Join(t.TempDir(), "tmux.conf")
			if err := os.WriteFile(configuration, []byte(
				"set -g base-index "+strconv.Itoa(base)+"\n",
			), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			server := tmux.NewServer(tmux.ServerOptions{
				SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
				ConfigFile: configuration,
			}).WithStrictErrors()
			t.Cleanup(func() {
				killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer killCancel()
				_ = server.Kill(killCtx)
			})

			described, err := workspace.Parse([]byte(
				"session_name: indexed\nwindows:\n  - window_name: w\n    window_index: " +
					strconv.Itoa(base) + "\n",
			))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			session, err := workspace.Build(ctx, server, described)
			if err != nil {
				t.Fatalf("build asking for the index it already has: %v", err)
			}
			windows, err := session.SearchWindows(ctx, nil)
			if err != nil {
				t.Fatalf("search windows: %v", err)
			}
			if len(windows) != 1 {
				t.Fatalf("built %d windows, want 1", len(windows))
			}
			if index, _ := windows[0].Formats().WindowIndex(); index != base {
				t.Errorf("window is at index %d, want %d", index, base)
			}
		})
	}
}

// TestAValidationFailureNamesItsLine covers the half of a rejection that
// arrives after decoding. A decode failure already carried a line; a
// validation failure named only the window's position in a list, which is not
// where a reader has to go to fix it.
func TestAValidationFailureNamesItsLine(t *testing.T) {
	_, err := workspace.Parse([]byte(
		"session_name: positioned\nwindows:\n  - window_name: first\n" +
			"  - window_name: second\n    layout: main-verticle\n",
	))
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error does not name the failing window's line: %v", err)
	}

	// A workspace built in Go has no document, so it says nothing rather than
	// pointing at a line that does not exist.
	built := workspace.Workspace{
		SessionName: "in-go",
		Windows:     []workspace.Window{{Name: "w", Layout: "main-verticle"}},
	}
	err = built.Validate()
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
	if strings.Contains(err.Error(), "line") {
		t.Errorf("a workspace with no document reported a line: %v", err)
	}
}

// TestValidateReportsEveryProblemAtOnce covers fixing a file: reporting one
// complaint per run makes the number of runs the number of mistakes.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	_, err := workspace.Parse([]byte(
		"session_name: many\nwindows:\n" +
			"  - window_name: first\n    layout: main-verticle\n" +
			"  - window_name: second\n    window_index: -3\n" +
			"  - window_name: third\n    window_shell: cat\n    panes:\n      - shell: top\n",
	))
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
	message := err.Error()
	for _, want := range []string{
		"main-verticle",         // the first window's layout
		"negative window_index", // the second window's index
		"window_shell",          // the third window's conflicting command
	} {
		if !strings.Contains(message, want) {
			t.Errorf("report omits %q: %v", want, err)
		}
	}
	if lines := strings.Count(message, "\n") + 1; lines != 3 {
		t.Errorf("reported %d problems, want 3: %v", lines, err)
	}
}

// TestMissingDirectoriesReportsWhatTmuxWouldIgnore covers the silence a
// caller can turn into a message: tmux starts a pane in the home directory
// when the one it was given does not exist, and reports success.
func TestMissingDirectoriesReportsWhatTmuxWouldIgnore(t *testing.T) {
	present := t.TempDir()
	absent := filepath.Join(t.TempDir(), "not-created")
	described, err := workspace.Parse([]byte(
		"session_name: dirs\nstart_directory: " + present + "\nwindows:\n" +
			"  - window_name: w\n    start_directory: " + absent + "\n" +
			"    panes:\n      - start_directory: " + present + "\n" +
			"      - start_directory: " + absent + "\n",
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// A workspace still loads: a directory a shell_command_before creates is
	// ordinary, so this is a report rather than a rejection.
	missing := described.MissingDirectories()
	if len(missing) != 1 || missing[0] != absent {
		t.Fatalf("MissingDirectories() = %v, want exactly %q once", missing, absent)
	}

	if none := (workspace.Workspace{
		SessionName: "clean",
		Windows:     []workspace.Window{{Name: "w", StartDirectory: present}},
	}).MissingDirectories(); len(none) != 0 {
		t.Errorf("a workspace whose directories exist reported %v", none)
	}
}
