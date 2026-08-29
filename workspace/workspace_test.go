package workspace_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/workspace"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func mustNewServer(t testing.TB, options tmux.ServerOptions) tmux.Server {
	t.Helper()

	server, err := tmux.NewServer(options)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

// testServer returns a bare server so Build owns all asserted topology.
func testServer(t *testing.T) (tmux.Server, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{}), ctx
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
		"misspelled field":        "session_nme: typo\nwindows: [{panes: [echo hi]}]\n",
		"no session name":         "windows: [{panes: [echo hi]}]\n",
		"no windows":              "session_name: empty\n",
		"bad focus":               "session_name: s\nwindows: [{focus: maybe, panes: [echo hi]}]\n",
		"unknown workspace field": "session_name: s\nno_such_key: 1\nwindows: [{panes: [echo hi]}]\n",
		"unknown window field":    "session_name: s\nwindows: [{nope: 1, panes: [echo hi]}]\n",
		"unknown pane field":      "session_name: s\nwindows: [{panes: [{nope: 1, shell_command: echo hi}]}]\n",
		"unknown command field":   "session_name: s\nwindows: [{panes: [{shell_command: [{cmd: hi, nope: 1}]}]}]\n",
		"window_index claimed twice": "session_name: s\n" +
			"windows: [{window_index: 5, panes: [echo hi]}, " +
			"{window_index: 5, panes: [echo hi]}]\n",
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

func TestInitialSessionRequestCarriesTheFirstPane(t *testing.T) {
	described := workspace.Workspace{
		SessionName:    "project",
		StartDirectory: "/workspace",
		Windows: []workspace.Window{{
			Name:           "editor",
			StartDirectory: "/window",
			Panes: []workspace.Pane{{
				StartDirectory: "/pane",
				Shell:          "sleep 300",
			}},
		}},
	}

	request, err := described.InitialSessionRequest()
	if err != nil {
		t.Fatalf("InitialSessionRequest() error = %v", err)
	}
	if request.Name != "project" || request.WindowName != "editor" ||
		request.StartDirectory != "/pane" || request.Command != "sleep 300" {
		t.Fatalf("InitialSessionRequest() = %+v, want first pane settings", request)
	}
}

func TestInitialSessionRequestRejectsAnInvalidWorkspace(t *testing.T) {
	_, err := (workspace.Workspace{}).InitialSessionRequest()
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("InitialSessionRequest() error = %v, want ErrInvalidWorkspace", err)
	}
}

func TestBuildIntoRequiresAMaterializedSession(t *testing.T) {
	described := workspace.Workspace{
		SessionName:   "continued",
		GlobalOptions: map[string]string{"status": "off"},
		Windows:       []workspace.Window{{Name: "initial"}},
	}
	err := workspace.BuildInto(context.Background(), tmux.Session{}, described)
	if !errors.Is(err, tmux.ErrMissingTarget) {
		t.Fatalf("BuildInto() error = %v, want ErrMissingTarget", err)
	}
	if errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("BuildInto() classified a missing session as an invalid workspace: %v", err)
	}
}

//libtmux:real-tmux
func TestBuildCreatesTheSessionOnItsOwnedConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	invocations := filepath.Join(t.TempDir(), "invocations")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
transport=process
operation=other
for argument do
    if [ "$argument" = -C ]; then transport=control; fi
    if [ "$argument" = new-session ]; then operation=creation; fi
done
printf '%s %s\n' "$transport" "$operation" >> "$LIBTMUX_WORKSPACE_INVOCATIONS"
exec "$LIBTMUX_WORKSPACE_REAL_TMUX" "$@"
`), 0o700); err != nil {
		t.Fatalf("write tmux proxy: %v", err)
	}
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Binary: proxy,
		ProcessEnvironment: append(os.Environ(),
			"LIBTMUX_WORKSPACE_INVOCATIONS="+invocations,
			"LIBTMUX_WORKSPACE_REAL_TMUX="+realBinary,
		),
	})

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	described := workspace.Workspace{
		SessionName: "owned-connection",
		Windows: []workspace.Window{
			{Name: "editor", Panes: []workspace.Pane{{Shell: "sleep 300"}}},
			{Name: "shell", Panes: []workspace.Pane{{Shell: "sleep 300"}}},
		},
	}
	session, err := workspace.Build(ctx, server, described)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	recorded, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatalf("read tmux invocations: %v", err)
	}
	minimum, err := tmux.ParseVersion(tmux.MinimumConnectionVersion)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	if version.AtLeast(minimum) {
		if !slices.Contains(lines, "control creation") {
			t.Fatalf("tmux invocations = %q, want control-mode session creation", lines)
		}
		if slices.Contains(lines, "process creation") {
			t.Fatalf("tmux invocations = %q, want no process session creation", lines)
		}
	} else if !slices.Contains(lines, "process creation") {
		t.Fatalf("tmux invocations = %q, want process fallback on tmux %s", lines, version)
	}
	if session.Server().ConnectionBound() {
		t.Fatal("Build() returned a session bound to its closed connection")
	}
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		t.Fatalf("returned Session.SearchWindows() error = %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("returned session has %d windows, want 2", len(windows))
	}
	clients, err := server.SearchClients(ctx, nil)
	if err != nil {
		t.Fatalf("SearchClients() error = %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("Build() left %d owned clients open", len(clients))
	}
}

//libtmux:real-tmux
func TestBuildIntoUsesTheMaterializedSessionsTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.Symlink(realBinary, proxy); err != nil {
		t.Fatalf("link tmux proxy: %v", err)
	}
	configuration := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(configuration, nil, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := mustNewServer(t, tmux.ServerOptions{
		Binary:     proxy,
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		ConfigFile: configuration,
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})
	described := workspace.Workspace{
		SessionName: "continued",
		Windows: []workspace.Window{
			{Name: "editor", Panes: []workspace.Pane{{Shell: "sleep 300"}}},
			{Name: "shell", Panes: []workspace.Pane{{Shell: "sleep 300"}}},
		},
	}
	request, err := described.InitialSessionRequest()
	if err != nil {
		t.Fatalf("InitialSessionRequest() error = %v", err)
	}
	_, connection, err := server.NewSessionConnection(
		ctx,
		request,
		tmux.ConnectionOptions{},
	)
	if err != nil {
		t.Fatalf("NewSessionConnection() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	disabled := proxy + ".disabled"
	if err := os.Rename(proxy, disabled); err != nil {
		t.Fatalf("disable tmux proxy: %v", err)
	}
	proxyDisabled := true
	t.Cleanup(func() {
		if proxyDisabled {
			_ = os.Rename(disabled, proxy)
		}
	})
	if err := workspace.BuildInto(ctx, connection.Session(), described); err != nil {
		t.Fatalf("BuildInto() error = %v", err)
	}

	windows, err := connection.Session().SearchWindows(ctx, nil)
	if err != nil {
		t.Fatalf("SearchWindows() error = %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("BuildInto() left %d windows, want 2", len(windows))
	}
	if err := os.Rename(disabled, proxy); err != nil {
		t.Fatalf("restore tmux proxy: %v", err)
	}
	proxyDisabled = false
}

// Long-lived pane commands prevent teardown from racing topology assertions.
//
//libtmux:real-tmux
func TestBuildCreatesTheDescribedHierarchy(t *testing.T) {
	server, ctx := testServer(t)
	parsed, err := workspace.Parse([]byte(`
session_name: build-test
windows:
  - window_name: editor
    panes:
      - sleep 300
      - sleep 300
  - window_name: shell
    panes:
      - sleep 300
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
		panes, ok := window.Panes()
		if !ok {
			t.Fatalf("window %q carries no panes", windowName)
		}
		byName[windowName] = len(panes)
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
      - sleep 300
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

// missingShell identifies examples that require an unavailable interpreter.
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

// unsetEnvironmentReference identifies examples requiring an unset variable.
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

// TestGlobalOptionsAcceptEveryTableTmuxAccepts covers tmuxp's untyped mapping.
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

// TestGlobalOptionsRejectAnOptionNoTableDeclares preserves the original name.
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

// TestFirstPaneShellRunsAsTheWindowCommand covers the pane created with a window.
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
	// tmux briefly reports the shell before exec replaces it, so poll.
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

// TestWindowShellAndFirstPaneShellConflict rejects two commands for one pane.
func TestWindowShellAndFirstPaneShellConflict(t *testing.T) {
	_, err := workspace.Parse([]byte(
		"session_name: conflict\nwindows:\n  - window_name: probe\n" +
			"    window_shell: cat\n    panes:\n      - shell: sleep 60\n",
	))
	if !errors.Is(err, workspace.ErrInvalidWorkspace) {
		t.Fatalf("got %v, want ErrInvalidWorkspace", err)
	}
}

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

// TestACommandReportsItsOwnFailure preserves the inner cause and source line.
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

// TestPaneIsConstructibleInGo guards Bool's exported construction contract.
func TestPaneIsConstructibleInGo(t *testing.T) {
	enter := workspace.Bool(false)
	pane := workspace.Pane{Focus: true, Enter: &enter, SuppressHistory: &enter}
	if !bool(pane.Focus) || bool(*pane.Enter) {
		t.Fatalf("constructed pane does not hold what it was given: %+v", pane)
	}
}

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

// TestARefusedFieldReadsDifferentlyFromATypo distinguishes unsupported syntax.
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

// TestPanesLandInTheOrderTheFileListsThem guards split target ordering.
func TestPanesLandInTheOrderTheFileListsThem(t *testing.T) {
	server, ctx := testServer(t)
	var document strings.Builder
	document.WriteString("session_name: order\nwindows:\n  - window_name: w\n    panes:\n")
	for _, marker := range []string{"A", "B", "C", "D"} {
		document.WriteString("      - shell: sh -c 'printf " + marker + "; sleep 60'\n")
	}
	described, err := workspace.Parse([]byte(document.String()))
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

// TestTheFirstPaneStartsWhereItsFileSays covers the pane created with a window.
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
		// Before tmux 3.3, use current_path for this idle pane because
		// pane_start_path is unavailable.
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

// TestSessionOptionsAcceptWhatTmuxAccepts covers tmuxp's untyped mapping.
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

// TestEnvironmentIsTheSessionsRatherThanThePanes guards last-write behavior.
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

// TestAnUnknownLayoutIsRefusedBeforeAnythingIsBuilt guards preflight validation.
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

// TestAFirstWindowMayAskForTheIndexItAlreadyHas avoids a redundant tmux move.
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
			server := mustNewServer(t, tmux.ServerOptions{
				SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
				ConfigFile: configuration,
			})
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

// TestAValidationFailureNamesItsLine covers errors found after decoding.
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

// TestValidateReportsEveryProblemAtOnce guards joined validation errors.
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

// TestMissingDirectoriesReportsWhatTmuxWouldIgnore exposes tmux's fallback.
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

// TestAFailureWithNoLineDoesNotClaimLineZero covers pre-node parse errors.
func TestAFailureWithNoLineDoesNotClaimLineZero(t *testing.T) {
	for name, document := range map[string]string{
		"empty":     "",
		"not a map": "[unclosed\n  windows: nonsense: : :\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := workspace.Parse([]byte(document))
			if err == nil {
				t.Fatal("the document was accepted")
			}
			if strings.Contains(err.Error(), "line 0") {
				t.Errorf("the rejection claims a line that cannot exist: %v", err)
			}
		})
	}
}
