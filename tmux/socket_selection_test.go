package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	namedDirectory := filepath.Join(
		relativeRoot,
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
	server := serverWithSocketSelection(t, cwd, ServerOptions{
		SocketName:         "named",
		ProcessEnvironment: []string{"TMUX_TMPDIR=socket-root"},
	})
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(realRoot, "tmux-"+strconv.Itoa(os.Getuid()))
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
	dependencies.executor = runner
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
