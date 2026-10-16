package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestSocketSelectionUsesEffectiveEnvironment(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	namedDirectory := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	server := serverWithSocketSelection(t, t.TempDir(), ServerOptions{
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "LIBTMUX_SOCKET_NAME=chosen"},
	})
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if selection.Path != filepath.Join(namedDirectory, "chosen") || selection.NamedDirectory != namedDirectory {
		t.Fatalf("SocketSelection() = %#v", selection)
	}
}

func TestSocketSelectionRetainsAnUnusableTmuxTmpdir(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "missing")
	server := serverWithSocketSelection(t, t.TempDir(), ServerOptions{
		SocketName: "named", ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
	})
	want := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "named")
	if server.SocketPath() != want {
		t.Fatalf("SocketPath() = %q, want %q", server.SocketPath(), want)
	}
	if _, err := server.Cmd(t.Context(), "list-sessions"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Cmd() = %v, want missing root error", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if server.SocketPath() != want {
		t.Fatal("creating the root changed the selected endpoint")
	}
}

func TestSocketSelectionPreservesMissingParentComponents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	configured := root + "/missing/../selected"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	dependencies := testServerDependencies(t, nil)
	dependencies.executor = runner
	server, err := newServer(ServerOptions{
		SocketName: "named", ProcessEnvironment: []string{"TMUX_TMPDIR=" + configured},
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	want := configured + "/tmux-" + strconv.Itoa(os.Getuid()) + "/named"
	if server.SocketPath() != want {
		t.Errorf("SocketPath() = %q, want preserved %q", server.SocketPath(), want)
	}
	if _, err := server.Cmd(t.Context(), "list-sessions"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Cmd() = %v, want missing component failure", err)
	}
	if requests := runner.recordedRequests(); len(requests) != 0 {
		t.Errorf("invalid root launched %d requests", len(requests))
	}
	if _, err := os.Lstat(filepath.Join(selected, "tmux-"+strconv.Itoa(os.Getuid()))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("normalized root was prepared: %v", err)
	}
}

func TestNamedSocketExecutionUsesFrozenResolvedDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	dependencies := testServerDependencies(t, nil)
	dependencies.executor = runner
	server, err := newServer(ServerOptions{
		SocketName: "named", ProcessEnvironment: []string{"TMUX_TMPDIR=" + link},
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Cmd(t.Context(), "list-sessions"); err != nil {
		t.Fatal(err)
	}
	request := runner.recordedRequests()[0]
	wantPath := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "named")
	if !slices.Contains(request.Arguments, "-S"+wantPath) {
		t.Fatalf("command arguments = %q, want captured -S%s", request.Arguments, wantPath)
	}
	if captured, ok := processEnvironmentValue(request.Environment, "TMUX_TMPDIR"); !ok || captured != root {
		t.Fatalf("command TMUX_TMPDIR = (%q, %t), want %q", captured, ok, root)
	}
}

func TestZeroServerHasNoSocketSelection(t *testing.T) {
	t.Parallel()

	if _, err := (Server{}).SocketSelection(); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("zero Server.SocketSelection() error = %v, want ErrInvalidServer", err)
	}
}

func serverWithSocketSelection(
	t *testing.T,
	cwd string,
	options ServerOptions,
) Server {
	t.Helper()
	dependencies := testServerDependencies(t, nil)
	dependencies.getwd = func() (string, error) { return cwd, nil }
	server, err := newServer(options, dependencies)
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	return server
}

func TestSocketDefaultEnvironmentContract(t *testing.T) {
	t.Parallel()
	namedDirectory := filepath.Join("/tmp", "tmux-"+strconv.Itoa(os.Getuid()))
	for _, testCase := range []struct {
		name        string
		options     ServerOptions
		environment []string
		wantPath    string
		wantError   bool
	}{
		{
			name:        "ordinary constructor uses environment path",
			environment: []string{"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/selected.sock"},
			wantPath:    "/tmp/libtmux-go-test/selected.sock",
		},
		{
			name:        "path default wins over invalid lower selectors",
			environment: []string{"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/selected.sock", "LIBTMUX_SOCKET_NAME=../invalid", "TMUX=malformed", "TMUX_TMPDIR=relative"},
			wantPath:    "/tmp/libtmux-go-test/selected.sock",
		},
		{
			name:        "name default wins over tmux context",
			environment: []string{"LIBTMUX_SOCKET_NAME=selected", "TMUX=malformed"},
			wantPath:    filepath.Join(namedDirectory, "selected"),
		},
		{
			name:        "explicit name wins over environment path",
			options:     ServerOptions{SocketName: "explicit"},
			environment: []string{"LIBTMUX_SOCKET_PATH=relative", "TMUX=malformed"},
			wantPath:    filepath.Join(namedDirectory, "explicit"),
		},
		{
			name:        "explicit path ignores lower selectors",
			options:     ServerOptions{SocketPath: "/tmp/libtmux-go-test/explicit.sock"},
			environment: []string{"LIBTMUX_SOCKET_PATH=relative", "LIBTMUX_SOCKET_NAME=../invalid", "TMUX=malformed"},
			wantPath:    "/tmp/libtmux-go-test/explicit.sock",
		},
		{
			name:        "empty environment defaults use tmux context with commas",
			environment: []string{"LIBTMUX_SOCKET_PATH=", "LIBTMUX_SOCKET_NAME=", "TMUX=/tmp/libtmux-go-test/with,commas.sock,123,0"},
			wantPath:    "/tmp/libtmux-go-test/with,commas.sock",
		},
		{
			name:        "tmux job without session",
			environment: []string{"TMUX=/tmp/libtmux-go-test/job.sock,123,-1"},
			wantPath:    "/tmp/libtmux-go-test/job.sock",
		},
		{
			name:        "tmux session with dollar sigil",
			environment: []string{"TMUX=/tmp/libtmux-go-test/context.sock,123,$0"},
			wantPath:    "/tmp/libtmux-go-test/context.sock",
		},
		{
			name:        "empty selector environment uses default",
			environment: []string{"LIBTMUX_SOCKET_PATH=", "LIBTMUX_SOCKET_NAME=", "TMUX="},
			wantPath:    filepath.Join(namedDirectory, "default"),
		},
		{name: "both explicit selectors", options: ServerOptions{SocketPath: "/tmp/socket", SocketName: "named"}, wantError: true},
		{name: "relative explicit path", options: ServerOptions{SocketPath: "relative"}, wantError: true},
		{name: "relative environment path", environment: []string{"LIBTMUX_SOCKET_PATH=relative"}, wantError: true},
		{name: "invalid selected name", environment: []string{"LIBTMUX_SOCKET_NAME=../invalid"}, wantError: true},
		{name: "relative named root", environment: []string{"LIBTMUX_SOCKET_NAME=named", "TMUX_TMPDIR=relative"}, wantError: true},
		{name: "tmux has no fields", environment: []string{"TMUX=/tmp/socket"}, wantError: true},
		{name: "tmux has zero pid", environment: []string{"TMUX=/tmp/socket,0,1"}, wantError: true},
		{name: "tmux has signed pid", environment: []string{"TMUX=/tmp/socket,+12,1"}, wantError: true},
		{name: "tmux has invalid session", environment: []string{"TMUX=/tmp/socket,12,-2"}, wantError: true},
		{name: "tmux has relative path", environment: []string{"TMUX=relative,12,1"}, wantError: true},
		{name: "tmux has blank pid", environment: []string{"TMUX=/tmp/socket,,1"}, wantError: true},
		{name: "tmux has pid whitespace", environment: []string{"TMUX=/tmp/socket, 12,1"}, wantError: true},
		{name: "tmux has pid suffix", environment: []string{"TMUX=/tmp/socket,12x,1"}, wantError: true},
		{name: "tmux has unicode digits", environment: []string{"TMUX=/tmp/socket,１２,1"}, wantError: true},
		{name: "tmux has empty session", environment: []string{"TMUX=/tmp/socket,12,"}, wantError: true},
		{name: "tmux has signed session", environment: []string{"TMUX=/tmp/socket,12,+1"}, wantError: true},
		{name: "tmux has session suffix", environment: []string{"TMUX=/tmp/socket,12,1x"}, wantError: true},
		{name: "tmux has session whitespace", environment: []string{"TMUX=/tmp/socket,12,1 "}, wantError: true},
		{name: "tmux has repeated session sigil", environment: []string{"TMUX=/tmp/socket,12,$$1"}, wantError: true},
		{name: "tmux has sigil on job sentinel", environment: []string{"TMUX=/tmp/socket,12,$-1"}, wantError: true},
		{name: "tmux preserves path whitespace and commas", environment: []string{"TMUX=/tmp/libtmux-go-test/ space ,comma ,12,$1"}, wantPath: "/tmp/libtmux-go-test/ space ,comma "},
		{name: "configured environment replaces host selectors", options: ServerOptions{ProcessEnvironment: []string{}}, environment: []string{"LIBTMUX_SOCKET_PATH=invalid", "TMUX=invalid"}, wantPath: filepath.Join(namedDirectory, "default")},
		{name: "configured environment selects endpoint", options: ServerOptions{ProcessEnvironment: []string{"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/child.sock"}}, environment: []string{"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/host.sock"}, wantPath: "/tmp/libtmux-go-test/child.sock"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server, err := newServer(testCase.options, testServerDependencies(t, testCase.environment))
			if testCase.wantError {
				if !errors.Is(err, ErrInvalidServerOptions) {
					t.Fatalf("newServer() error = %v, want ErrInvalidServerOptions", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := server.SocketPath(); got != testCase.wantPath {
				t.Fatalf("SocketPath() = %q, want %q", got, testCase.wantPath)
			}
		})
	}
}

func TestDefaultSocketLaunchPinsPathWithoutRetargeting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := []string{"TMUX_TMPDIR=" + root, "KEEP=before"}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	dependencies := testServerDependencies(t, parent)
	dependencies.executor = runner
	server, err := newServer(ServerOptions{}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	parent[0] = "TMUX_TMPDIR=" + t.TempDir()
	derived := server
	for _, edit := range [][2]string{
		{"KEEP", "after"},
		{"KEEP", "last"},
		{"NEW", "value"},
		{"TMUX_TMPDIR", t.TempDir()},
		{"LIBTMUX_SOCKET_PATH", "/ignored.sock"},
		{"TMUX", "invalid"},
		{"TMUX_PANE", "%9"},
	} {
		derived, err = derived.WithProcessEnvironmentValue(edit[0], edit[1])
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := derived.Cmd(t.Context(), "list-sessions"); err != nil {
		t.Fatal(err)
	}
	request := runner.requests[0]
	if !slices.Equal(request.Arguments, []string{"-u", "-S" + wantPath, "list-sessions"}) {
		t.Fatalf("raw default launch arguments = %q", request.Arguments)
	}
	if value, _ := processEnvironmentValue(request.Environment, "KEEP"); value != "last" {
		t.Fatalf("derived KEEP = %q", value)
	}
	if value, _ := processEnvironmentValue(server.state.config.processEnvironment, "KEEP"); value != "before" {
		t.Fatalf("base KEEP = %q", value)
	}
	for _, key := range []string{"TMUX", "TMUX_PANE"} {
		if _, found := processEnvironmentValue(request.Environment, key); found {
			t.Fatalf("derived launch retained %s", key)
		}
	}
	if _, err := server.WithProcessEnvironmentValue("BAD=KEY", "value"); !errors.Is(err, ErrInvalidServerOptions) {
		t.Fatalf("invalid edit = %v", err)
	}
	if server.SocketPath() != wantPath || derived.SocketPath() != wantPath {
		t.Fatal("environment edit redirected an endpoint")
	}
	for _, path := range []string{"", "relative"} {
		if _, err := server.WithSocketPath(path); !errors.Is(err, ErrInvalidServerOptions) {
			t.Fatalf("WithSocketPath(%q) = %v", path, err)
		}
	}
}

func TestSocketDefaultsRemoveOnlyChildTmuxContext(t *testing.T) {
	t.Parallel()
	parent := []string{"LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/selected.sock", "TMUX=/tmp/other.sock,12,0", "TMUX_PANE=%9", "KEEP=value"}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	dependencies := testServerDependencies(t, parent)
	dependencies.executor = runner
	server, err := newServer(ServerOptions{}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if parent[1] != "TMUX=/tmp/other.sock,12,0" || parent[2] != "TMUX_PANE=%9" {
		t.Fatal("constructor changed parent environment")
	}
	parent[0] = "LIBTMUX_SOCKET_PATH=/tmp/libtmux-go-test/changed.sock"
	if _, err := server.Cmd(t.Context(), "list-sessions"); err != nil {
		t.Fatal(err)
	}
	request := runner.recordedRequests()[0]
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		if _, present := processEnvironmentValue(request.Environment, name); present {
			t.Fatalf("child environment still contains %s", name)
		}
	}
	if got := server.SocketPath(); got != "/tmp/libtmux-go-test/selected.sock" {
		t.Fatalf("SocketPath() after parent mutation = %q", got)
	}
	if value, present := processEnvironmentValue(request.Environment, "KEEP"); !present || value != "value" {
		t.Fatalf("unrelated child environment changed: %q, %t", value, present)
	}
}
