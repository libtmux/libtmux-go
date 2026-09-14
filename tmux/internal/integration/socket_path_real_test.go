//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestLayoutPreflightPreservesLiveSocketPermissionFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	server := tmuxtest.NewServer(t.Context(), t)
	sessions, err := server.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(server.SocketPath())
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, info.Mode().Perm()) })
	if err := os.Chmod(directory, 0); err != nil {
		t.Fatal(err)
	}
	query, err := server.Cmd(t.Context(), "display-message", "-p", "tmux #{version}")
	if err != nil || query.ExitCode != 1 || !strings.Contains(strings.Join(query.Stderr, "\n"), "Permission denied") {
		t.Fatalf("permission fixture did not bite: %+v %v", query, err)
	}
	err = server.ValidateLayouts(t.Context(), func(yield func(string, int) bool) { yield("main-h", 1) })
	command, ok := errors.AsType[*tmux.CommandError](err)
	if !ok || !slices.Equal(command.Result.Stderr, query.Stderr) {
		t.Fatalf("layout validation lost original permission failure: %v", err)
	}
	if err := os.Chmod(directory, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions[0].Refresh(t.Context()); err != nil {
		t.Fatalf("permission probe lost keeper identity: %v", err)
	}
}

// tmux keeps its sockets in TMUX_TMPDIR/tmux-<uid> and refuses that directory
// if others can reach it. tmux 3.2a reports "error creating"; 3.3a and newer use
// three other diagnostics, so the test exercises the installed version.
//
//libtmux:real-tmux
func TestADirectoryTmuxRefusesReadsAsNoServer(t *testing.T) {
	t.Parallel()

	// Group permission is allowed and would not provoke the refusal at all.
	for name, mode := range map[string]os.FileMode{
		"other can read and execute": 0o755,
		"other can read":             0o705,
		"other can execute":          0o701,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			root := t.TempDir()
			sockets := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
			if err := os.Mkdir(sockets, mode); err != nil {
				t.Fatalf("Mkdir(%q) = %v", sockets, err)
			}
			// Mkdir applies the umask, so the mode has to be set outright for
			// the test to be asking what it means to ask.
			if err := os.Chmod(sockets, mode); err != nil {
				t.Fatalf("Chmod(%q) = %v", sockets, err)
			}

			server, err := tmux.NewServer(tmux.ServerOptions{
				SocketName:         "refused",
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "PATH=" + os.Getenv("PATH")},
			})
			if err != nil {
				t.Fatalf("NewServer() error = %v", err)
			}

			alive, err := server.IsAlive(ctx)
			if err != nil {
				t.Fatalf("IsAlive() = (%t, %v), want no error: tmux refused the "+
					"directory and the refusal was not recognised", alive, err)
			}
			if alive {
				t.Fatalf("IsAlive() = true for a directory tmux will not use")
			}

			_, err = server.Sessions(ctx)
			if !errors.Is(err, tmux.ErrNoServer) {
				t.Fatalf("Sessions() = %v, want ErrNoServer", err)
			}
			// The reason survives classification, because a caller who cannot
			// read it has to guess between a socket that is absent and a
			// directory they need to chmod.
			if got := err.Error(); got == "" {
				t.Fatal("Sessions() error has no text")
			}
		})
	}
}

// TestAnAbsentSocketDirectoryStillReadsAsNoServer keeps the ordinary case
// beside the refused one: nothing there at all is the same answer.
//
//libtmux:real-tmux
func TestAnAbsentSocketDirectoryStillReadsAsNoServer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "absent",
		ProcessEnvironment: []string{
			"TMUX_TMPDIR=" + filepath.Join(t.TempDir(), "nothing-here"),
			"PATH=" + os.Getenv("PATH"),
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	alive, err := server.IsAlive(ctx)
	if err != nil || alive {
		t.Fatalf("IsAlive() = (%t, %v), want (false, nil)", alive, err)
	}
}

//libtmux:real-tmux
func TestNamedSocketExecutionKeepsConstructorFallback(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	missingRoot := filepath.Join(t.TempDir(), "appears-later")
	name := "frozen-" + filepath.Base(t.TempDir())
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: name,
		ProcessEnvironment: []string{
			"TMUX_TMPDIR=" + missingRoot,
			"PATH=" + os.Getenv("PATH"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := server.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(missingRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "frozen-root"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = server.Kill(cleanupCtx)
	})
	result, err := server.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
		t.Fatalf("display-message = %#v, %v", result, err)
	}
	if result.Stdout[0] != selection.Path {
		t.Fatalf(
			"tmux socket path = %q, want frozen selection %q",
			result.Stdout[0],
			selection.Path,
		)
	}
}
