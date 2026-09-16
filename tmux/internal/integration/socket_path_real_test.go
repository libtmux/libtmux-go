//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

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
	// t.TempDir()'s own basename is not fit for this: within one test it
	// increments from a fixed start, so a test's Nth call names the same
	// directory on every run and every machine (this call, the second in
	// this test, is always "002"). That makes the socket name machine-wide
	// constant, which collides with any server a previous run leaked under
	// the same name. rand.Text() is unique per call instead.
	name := "frozen-" + rand.Text()
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

// t.TempDir()'s own basename is deterministic per call within a test - the
// Nth call in a given test always ends the same way, on every run and every
// machine - so a socket name derived from it (the bug this replaces) was
// machine-wide constant, not unique. This confirms that premise directly,
// then pins the replacement's actual property: two calls in the same
// process, the case that would be identical either way, differ.
func TestFrozenSocketNameIsNotAFixedConstant(t *testing.T) {
	t.Parallel()

	// Not pinned to Go's exact counter format, which this project does not
	// control: two independent *testing.T instances taking the same second
	// call reproduce the same basename as each other, which is the
	// replaced scheme's actual premise (deterministic, not unique) without
	// depending on what that basename literally is.
	outer := secondTempDirCallBasename(t)
	t.Run("reproduced independently", func(t *testing.T) {
		if got := secondTempDirCallBasename(t); got != outer {
			t.Fatalf(
				"second call's basename = %q here, %q in the sibling instance above, "+
					"want them identical - that repetition is what made the replaced scheme collide",
				got, outer,
			)
		}
	})

	first := "frozen-" + rand.Text()
	second := "frozen-" + rand.Text()
	if first == second {
		t.Fatalf("two successive frozen socket names are both %q, want distinct", first)
	}
}

func secondTempDirCallBasename(t *testing.T) string {
	t.Helper()
	_ = t.TempDir()
	return filepath.Base(t.TempDir())
}
