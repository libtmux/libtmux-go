//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
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

			server := tmux.NewServer(tmux.ServerOptions{
				SocketName:         "refused",
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "PATH=" + os.Getenv("PATH")},
			})

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

	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "absent",
		ProcessEnvironment: []string{
			"TMUX_TMPDIR=" + filepath.Join(t.TempDir(), "nothing-here"),
			"PATH=" + os.Getenv("PATH"),
		},
	})

	alive, err := server.IsAlive(ctx)
	if err != nil || alive {
		t.Fatalf("IsAlive() = (%t, %v), want (false, nil)", alive, err)
	}
}
