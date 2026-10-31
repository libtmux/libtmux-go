package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestSocketSelectionUsesFrozenTmuxPrecedenceAndWorkingDirectory(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	relativeRoot := filepath.Join(cwd, "socket-root")
	if err := os.Mkdir(relativeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(relativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	namedDirectory := filepath.Join(
		resolvedRoot,
		"tmux-"+strconv.Itoa(os.Getuid()),
	)

	for _, testCase := range []struct {
		name        string
		options     ServerOptions
		wantPath    string
		wantNamedIn string
	}{
		{
			name: "explicit path wins",
			options: ServerOptions{
				SocketPath:         "relative/selected.sock",
				SocketName:         "ignored",
				ProcessEnvironment: []string{"TMUX=ignored,1,2", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(cwd, "relative/selected.sock"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "socket name wins over environment",
			options: ServerOptions{
				SocketName:         "named",
				ProcessEnvironment: []string{"TMUX=ignored,1,2", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(namedDirectory, "named"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "tmux environment wins over default",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX=relative/environment.sock,1,2", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(cwd, "relative/environment.sock"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "bare tmux environment path",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX=relative/bare.sock", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(cwd, "relative/bare.sock"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "tmux uses its first comma",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX=relative/with,comma,1,2", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(cwd, "relative/with"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "leading comma does not select a path",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX=,1,2", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(namedDirectory, "default"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "empty tmux environment uses default",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX=", "TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(namedDirectory, "default"),
			wantNamedIn: namedDirectory,
		},
		{
			name: "unset tmux environment uses default",
			options: ServerOptions{
				ProcessEnvironment: []string{"TMUX_TMPDIR=socket-root"},
			},
			wantPath:    filepath.Join(namedDirectory, "default"),
			wantNamedIn: namedDirectory,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := serverWithSocketSelection(t, cwd, testCase.options)
			selection, err := server.SocketSelection()
			if err != nil {
				t.Fatalf("SocketSelection() error = %v", err)
			}
			if selection.Path != testCase.wantPath ||
				selection.NamedDirectory != testCase.wantNamedIn {
				t.Fatalf(
					"SocketSelection() = %#v, want path %q and named directory %q",
					selection,
					testCase.wantPath,
					testCase.wantNamedIn,
				)
			}
		})
	}
}

func TestSocketSelectionFallsBackFromAnUnusableTmuxTmpdir(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	server := serverWithSocketSelection(t, cwd, ServerOptions{
		SocketName:         "named",
		ProcessEnvironment: []string{"TMUX_TMPDIR=missing"},
	})
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatalf("SocketSelection() error = %v", err)
	}
	fallback, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		fallback = "/tmp"
	}
	wantDirectory := filepath.Join(fallback, "tmux-"+strconv.Itoa(os.Getuid()))
	if selection.NamedDirectory != wantDirectory ||
		selection.Path != filepath.Join(wantDirectory, "named") {
		t.Fatalf("SocketSelection() = %#v, want fallback directory %q", selection, wantDirectory)
	}
}

func TestSocketSelectionResolvesTmuxTmpdirSymlinks(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	realRoot := t.TempDir()
	link := filepath.Join(cwd, "socket-root")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	server := serverWithSocketSelection(t, cwd, ServerOptions{
		SocketName:         "named",
		ProcessEnvironment: []string{"TMUX_TMPDIR=socket-root"},
	})
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(resolvedRoot, "tmux-"+strconv.Itoa(os.Getuid()))
	if selection.NamedDirectory != wantDirectory ||
		selection.Path != filepath.Join(wantDirectory, "named") {
		t.Fatalf("SocketSelection() = %#v, want symlink-resolved root %q", selection, realRoot)
	}
}

func TestSocketSelectionIsFrozenAcrossTmuxTmpdirFilesystemChanges(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	server := serverWithSocketSelection(t, cwd, ServerOptions{
		SocketName:         "named",
		ProcessEnvironment: []string{"TMUX_TMPDIR=appears-later"},
	})
	before, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "appears-later")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("SocketSelection() changed from %#v to %#v after creating %q",
			before, after, root)
	}
}

func TestNamedSocketExecutionUsesFrozenResolvedDirectory(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	dependencies := testServerDependencies(t, nil)
	dependencies.getwd = func() (string, error) { return cwd, nil }
	dependencies.executor = &qualifiedCommandRunner{inner: runner}
	server, err := newServer(ServerOptions{
		SocketName:         "named",
		ProcessEnvironment: []string{"TMUX_TMPDIR=appears-later"},
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cwd, "appears-later"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatal(err)
	}
	request := runner.recordedRequests()[0]
	wantRoot := filepath.Dir(selection.NamedDirectory)
	if root, ok := processEnvironmentValue(request.Environment, "TMUX_TMPDIR"); !ok || root != wantRoot {
		t.Fatalf(
			"command TMUX_TMPDIR = (%q, %t), want frozen root %q",
			root,
			ok,
			wantRoot,
		)
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

// A server that no one pointed at a socket passes tmux neither -L nor -S, so
// tmux picks the server itself.
func TestServerPassesNoSocketUnlessOneIsSelected(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	for name, environment := range map[string][]string{
		"nothing set":     {"TMUX_TMPDIR=" + cwd},
		"inside tmux":     {"TMUX_TMPDIR=" + cwd, "TMUX=" + filepath.Join(cwd, "inherited") + ",1,2"},
		"unrelated names": {"TMUX_TMPDIR=" + cwd, "LIBTMUX_SOCKET_NAME=ignored", "LIBTMUX_SOCKETS=ignored"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := serverWithSocketSelection(t, cwd, ServerOptions{ProcessEnvironment: environment})
			got := server.commandArguments([]string{"list-sessions"})
			if want := []string{"-u", "list-sessions"}; !slices.Equal(got, want) {
				t.Fatalf("commandArguments() = %#v, want %#v", got, want)
			}
			selection, err := server.SocketSelection()
			if err != nil || selection.Source != SocketSourceTmux {
				t.Fatalf("SocketSelection() = (%#v, %v), want source %q", selection, err, SocketSourceTmux)
			}
		})
	}
}

// LIBTMUX_SOCKET_PATH is -S and LIBTMUX_SOCKET is -L. When both are set the
// path wins and the name is dropped without an error, as tmux's own -S does.
func TestEnvironmentVariablesSelectTheSocket(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	namedDirectory := filepath.Join(resolvedDirectory(t, cwd), "tmux-"+strconv.Itoa(os.Getuid()))
	for _, test := range []struct {
		name        string
		environment []string
		wantArgs    []string
		wantSource  SocketSource
		wantPath    string
	}{
		{
			name:        "the name alone",
			environment: []string{"LIBTMUX_SOCKET=named"},
			wantArgs:    []string{"-u", "-Lnamed", "list-sessions"},
			wantSource:  SocketSourceEnvironmentName,
			wantPath:    filepath.Join(namedDirectory, "named"),
		},
		{
			name:        "the path alone",
			environment: []string{"LIBTMUX_SOCKET_PATH=/run/chosen.sock"},
			wantArgs:    []string{"-u", "-S/run/chosen.sock", "list-sessions"},
			wantSource:  SocketSourceEnvironmentPath,
			wantPath:    "/run/chosen.sock",
		},
		{
			name:        "both, so the path wins",
			environment: []string{"LIBTMUX_SOCKET=named", "LIBTMUX_SOCKET_PATH=/run/chosen.sock"},
			wantArgs:    []string{"-u", "-S/run/chosen.sock", "list-sessions"},
			wantSource:  SocketSourceEnvironmentPath,
			wantPath:    "/run/chosen.sock",
		},
		{
			name:        "a relative path reaches what tmux -S reaches from the working directory",
			environment: []string{"LIBTMUX_SOCKET_PATH=relative/chosen.sock"},
			wantArgs:    []string{"-u", "-Srelative/chosen.sock", "list-sessions"},
			wantSource:  SocketSourceEnvironmentPath,
			wantPath:    filepath.Join(cwd, "relative/chosen.sock"),
		},
		{
			name: "a variable beats the server TMUX names",
			environment: []string{
				"LIBTMUX_SOCKET=named", "TMUX=" + filepath.Join(cwd, "inherited") + ",1,2",
			},
			wantArgs:   []string{"-u", "-Lnamed", "list-sessions"},
			wantSource: SocketSourceEnvironmentName,
			wantPath:   filepath.Join(namedDirectory, "named"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := serverWithSocketSelection(t, cwd, ServerOptions{
				ProcessEnvironment: append([]string{"TMUX_TMPDIR=" + cwd}, test.environment...),
			})
			if got := server.commandArguments([]string{"list-sessions"}); !slices.Equal(got, test.wantArgs) {
				t.Fatalf("commandArguments() = %#v, want %#v", got, test.wantArgs)
			}
			selection, err := server.SocketSelection()
			if err != nil || selection.Source != test.wantSource || selection.Path != test.wantPath {
				t.Fatalf("SocketSelection() = (%#v, %v), want source %q and path %q",
					selection, err, test.wantSource, test.wantPath)
			}
		})
	}
}

// A socket the caller names is never overridden, and then neither variable is
// read.
func TestAnExplicitSocketComesBeforeTheEnvironment(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	environment := []string{
		"TMUX_TMPDIR=" + cwd, "LIBTMUX_SOCKET=envname", "LIBTMUX_SOCKET_PATH=/run/env.sock",
	}
	for _, test := range []struct {
		name     string
		options  ServerOptions
		wantArgs []string
	}{
		{"a name", ServerOptions{SocketName: "mine"}, []string{"-u", "-Lmine", "list-sessions"}},
		{"a path", ServerOptions{SocketPath: "/run/mine.sock"}, []string{"-u", "-S/run/mine.sock", "list-sessions"}},
		{
			"both, so the path wins",
			ServerOptions{SocketName: "mine", SocketPath: "/run/mine.sock"},
			[]string{"-u", "-S/run/mine.sock", "list-sessions"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			test.options.ProcessEnvironment = environment
			server := serverWithSocketSelection(t, cwd, test.options)
			if got := server.commandArguments([]string{"list-sessions"}); !slices.Equal(got, test.wantArgs) {
				t.Fatalf("commandArguments() = %#v, want %#v", got, test.wantArgs)
			}
			selection, err := server.SocketSelection()
			if err != nil || selection.Source != SocketSourceExplicit {
				t.Fatalf("SocketSelection() = (%#v, %v), want source %q", selection, err, SocketSourceExplicit)
			}
		})
	}
}

// A variable that is empty or only whitespace is unset, each one alone, and a
// value with spaces around it names a socket with those spaces.
func TestBlankVariablesCountAsUnsetOneByOne(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	for _, test := range []struct {
		name        string
		environment []string
		wantArgs    []string
		wantSource  SocketSource
	}{
		{"both empty", []string{"LIBTMUX_SOCKET=", "LIBTMUX_SOCKET_PATH="}, []string{"-u", "list-sessions"}, SocketSourceTmux},
		{"both blank", []string{"LIBTMUX_SOCKET= \t", "LIBTMUX_SOCKET_PATH=  "}, []string{"-u", "list-sessions"}, SocketSourceTmux},
		{
			"an empty path leaves the name in force",
			[]string{"LIBTMUX_SOCKET=named", "LIBTMUX_SOCKET_PATH="},
			[]string{"-u", "-Lnamed", "list-sessions"},
			SocketSourceEnvironmentName,
		},
		{
			"a blank path leaves the name in force",
			[]string{"LIBTMUX_SOCKET=named", "LIBTMUX_SOCKET_PATH= "},
			[]string{"-u", "-Lnamed", "list-sessions"},
			SocketSourceEnvironmentName,
		},
		{
			"an empty name leaves the path in force",
			[]string{"LIBTMUX_SOCKET=", "LIBTMUX_SOCKET_PATH=/run/chosen.sock"},
			[]string{"-u", "-S/run/chosen.sock", "list-sessions"},
			SocketSourceEnvironmentPath,
		},
		{
			"padding is kept",
			[]string{"LIBTMUX_SOCKET= named "},
			[]string{"-u", "-L named ", "list-sessions"},
			SocketSourceEnvironmentName,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := serverWithSocketSelection(t, cwd, ServerOptions{
				ProcessEnvironment: append([]string{"TMUX_TMPDIR=" + cwd}, test.environment...),
			})
			if got := server.commandArguments([]string{"list-sessions"}); !slices.Equal(got, test.wantArgs) {
				t.Fatalf("commandArguments() = %#v, want %#v", got, test.wantArgs)
			}
			selection, err := server.SocketSelection()
			if err != nil || selection.Source != test.wantSource {
				t.Fatalf("SocketSelection() = (%#v, %v), want source %q", selection, err, test.wantSource)
			}
		})
	}
}

// The variables are read from the environment the server's tmux children get:
// the supplied ProcessEnvironment when there is one, otherwise the process's.
func TestTheVariablesAreReadFromTheServersEnvironment(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	dependencies := testServerDependencies(t, []string{"LIBTMUX_SOCKET=fromprocess"})
	dependencies.getwd = func() (string, error) { return cwd, nil }

	inherited, err := newServer(ServerOptions{}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if got := inherited.commandArguments([]string{"x"}); !slices.Equal(got, []string{"-u", "-Lfromprocess", "x"}) {
		t.Errorf("a server that snapshots the process got %#v", got)
	}

	supplied, err := newServer(ServerOptions{ProcessEnvironment: []string{}}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if got := supplied.commandArguments([]string{"x"}); !slices.Equal(got, []string{"-u", "x"}) {
		t.Errorf("a server given an empty environment got %#v, want no socket", got)
	}
}

// Asking for the TMUX socket is the caller's choice, and the variables do not
// override it.
func TestNewServerFromEnvKeepsTheSocketTMUXNames(t *testing.T) {
	t.Parallel()

	server, err := newServerFromEnvironmentSnapshot(
		map[string]string{"TMUX": "/run/inherited.sock,1,2"},
		[]string{"PATH=" + os.Getenv("PATH"), "LIBTMUX_SOCKET=envname", "LIBTMUX_SOCKET_PATH=/run/env.sock"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := server.commandArguments([]string{"x"}); !slices.Equal(got, []string{"-u", "-S/run/inherited.sock", "x"}) {
		t.Fatalf("commandArguments() = %#v, want the socket TMUX names", got)
	}
	selection, err := server.SocketSelection()
	if err != nil || selection.Source != SocketSourceExplicit {
		t.Fatalf("SocketSelection() = (%#v, %v), want source %q", selection, err, SocketSourceExplicit)
	}
}

// WithSocketPath names a socket, and an empty path hands the choice back to
// tmux whatever the environment holds.
func TestWithSocketPathReportsWhoChoseTheSocket(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	server := serverWithSocketSelection(t, cwd, ServerOptions{
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + cwd, "LIBTMUX_SOCKET=envname"},
	})
	named, err := server.WithSocketPath("/run/named.sock")
	if err != nil {
		t.Fatal(err)
	}
	if selection, _ := named.SocketSelection(); selection.Source != SocketSourceExplicit {
		t.Errorf("WithSocketPath(path) source = %q, want %q", selection.Source, SocketSourceExplicit)
	}
	cleared, err := named.WithSocketPath("")
	if err != nil {
		t.Fatal(err)
	}
	selection, _ := cleared.SocketSelection()
	if selection.Source != SocketSourceTmux {
		t.Errorf("WithSocketPath(\"\") source = %q, want %q", selection.Source, SocketSourceTmux)
	}
	if got := cleared.commandArguments([]string{"x"}); !slices.Equal(got, []string{"-u", "x"}) {
		t.Errorf("WithSocketPath(\"\") got %#v, want no socket", got)
	}
}

func resolvedDirectory(t *testing.T, directory string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
