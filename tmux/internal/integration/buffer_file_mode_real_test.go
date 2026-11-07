//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// withOpenUmask lets every new file start from mode 0666, the widest tmux could
// hand out, so a file that comes out 0600 got that from the library and not
// from the umask of whoever runs the tests. The sequential tests of this package
// finish before its parallel ones resume, so nothing else creates files while
// it is set.
func withOpenUmask(t *testing.T) {
	t.Helper()
	previous := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(previous) })
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// The file tmux writes is created by the tmux client the library starts, under
// that process's umask. A file the library creates for a caller is private to
// the user, and a file the caller made keeps the mode the caller gave it.
//
//libtmux:real-tmux
func TestSaveBufferCreatesPrivateFilesAndKeepsExistingModes(t *testing.T) {
	withOpenUmask(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	server := tmuxtest.NewServer(ctx, t)
	name := "mode-buffer"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Name: &name, Data: "example"}); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(directory, "fresh.txt")
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: fresh, Name: &name}); err != nil {
		t.Fatalf("SaveBuffer(fresh) error = %v", err)
	}
	if got := modeOf(t, fresh); got != 0o600 {
		t.Errorf("SaveBuffer created %v, want -rw-------", got)
	}

	appended := filepath.Join(directory, "appended.txt")
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: appended, Name: &name, Append: true}); err != nil {
		t.Fatalf("SaveBuffer(append) error = %v", err)
	}
	if got := modeOf(t, appended); got != 0o600 {
		t.Errorf("SaveBuffer with Append created %v, want -rw-------", got)
	}

	for _, mode := range []os.FileMode{0o640, 0o600, 0o666} {
		existing := filepath.Join(directory, "existing-"+mode.String()+".txt")
		if err := os.WriteFile(existing, []byte("old"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(existing, mode); err != nil {
			t.Fatal(err)
		}
		if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: existing, Name: &name}); err != nil {
			t.Fatalf("SaveBuffer(existing %v) error = %v", mode, err)
		}
		if got := modeOf(t, existing); got != mode {
			t.Errorf("SaveBuffer changed an existing %v file to %v", mode, got)
		}
		if data, err := os.ReadFile(existing); err != nil || string(data) != "example" {
			t.Errorf("existing file = (%q, %v), want it replaced by the buffer", data, err)
		}
	}
}

// A relative path is the server's to resolve against the working directory it
// was built in, and the library makes the file there.
//
//libtmux:real-tmux
func TestSaveBufferCreatesPrivateRelativeFiles(t *testing.T) {
	withOpenUmask(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	t.Chdir(directory)
	server := tmuxtest.NewServer(ctx, t)
	name := "mode-relative-buffer"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Name: &name, Data: "example"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("nested", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative.txt", filepath.Join("nested", "..", "dotted.txt")} {
		if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: path, Name: &name}); err != nil {
			t.Fatalf("SaveBuffer(%q) error = %v", path, err)
		}
		if got := modeOf(t, filepath.Join(directory, path)); got != 0o600 {
			t.Errorf("SaveBuffer created %q as %v, want -rw-------", path, got)
		}
	}
}

// A save that fails must not leave the empty file the library made for it.
//
//libtmux:real-tmux
func TestSaveBufferFailureRemovesTheFileItCreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	server := tmuxtest.NewServer(ctx, t)
	missing := "no-such-buffer"

	failed := filepath.Join(directory, "failed.txt")
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: failed, Name: &missing}); err == nil {
		t.Fatal("SaveBuffer(missing buffer) succeeded")
	}
	if _, err := os.Lstat(failed); !os.IsNotExist(err) {
		t.Errorf("a failed SaveBuffer left %s behind (stat error %v)", failed, err)
	}

	kept := filepath.Join(directory, "kept.txt")
	if err := os.WriteFile(kept, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: kept, Name: &missing}); err == nil {
		t.Fatal("SaveBuffer(missing buffer, existing file) succeeded")
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "old" {
		t.Errorf("a failed SaveBuffer changed the caller's file: (%q, %v)", data, err)
	}
}

//libtmux:real-tmux
func TestCaptureToFileCreatesPrivateFilesOverBothTransports(t *testing.T) {
	withOpenUmask(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	loose, err := sessions[0].SearchPanes(ctx, nil)
	if err != nil || len(loose) == 0 {
		t.Fatalf("panes = (%v, %v)", loose, err)
	}
	_, connection, err := server.NewSessionConnection(
		ctx, tmux.NewSessionRequest{Name: "mode"}, tmux.ConnectionOptions{Lanes: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	bound, err := connection.Session().SearchPanes(ctx, nil)
	if err != nil || len(bound) == 0 {
		t.Fatalf("connection panes = (%v, %v)", bound, err)
	}

	for _, transport := range []struct {
		name string
		pane tmux.Pane
	}{{"subprocess", loose[0]}, {"connection", bound[0]}} {
		t.Run(transport.name, func(t *testing.T) {
			fresh := filepath.Join(directory, transport.name+"-fresh.txt")
			if _, err := transport.pane.CaptureToFile(ctx, fresh, tmux.CapturePaneRequest{}); err != nil {
				t.Fatalf("CaptureToFile(fresh) error = %v", err)
			}
			if got := modeOf(t, fresh); got != 0o600 {
				t.Errorf("CaptureToFile created %v, want -rw-------", got)
			}

			semicolon := filepath.Join(directory, transport.name+"-ends-with;")
			if _, err := transport.pane.CaptureToFile(ctx, semicolon, tmux.CapturePaneRequest{}); err != nil {
				t.Fatalf("CaptureToFile(trailing semicolon) error = %v", err)
			}
			if got := modeOf(t, semicolon); got != 0o600 {
				t.Errorf("CaptureToFile created the stepwise path %v, want -rw-------", got)
			}

			existing := filepath.Join(directory, transport.name+"-existing.txt")
			if err := os.WriteFile(existing, nil, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(existing, 0o640); err != nil {
				t.Fatal(err)
			}
			if _, err := transport.pane.CaptureToFile(ctx, existing, tmux.CapturePaneRequest{}); err != nil {
				t.Fatalf("CaptureToFile(existing) error = %v", err)
			}
			if got := modeOf(t, existing); got != 0o640 {
				t.Errorf("CaptureToFile changed an existing 0640 file to %v", got)
			}
		})
	}
}
