//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

//libtmux:real-tmux
func TestNamedSocketLaunchesUseCapturedPath(t *testing.T) {
	for _, control := range []bool{false, true} {
		for _, mode := range []os.FileMode{0, 0o700, 0o770} {
			t.Run(fmt.Sprintf("control=%t/mode=%o", control, mode), func(t *testing.T) {
				root := endpointTestRoot(t)
				directory := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
				if mode != 0 {
					if err := os.Mkdir(directory, mode); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(directory, mode); err != nil {
						t.Fatal(err)
					}
				}
				server, err := tmux.NewServer(tmux.ServerOptions{
					ConfigFile:         "/dev/null",
					ProcessEnvironment: []string{"TMUX_TMPDIR=" + root, "LIBTMUX_SOCKET_NAME=named", "PATH=" + os.Getenv("PATH"), "TMUX=ignored", "TMUX_PANE=%9"},
				})
				if err != nil {
					t.Fatal(err)
				}
				// An environment edit must retain named-directory preparation as
				// well as the endpoint, even before the first command.
				server, err = server.WithProcessEnvironmentValue("TMUX_TMPDIR", filepath.Join(root, "ignored"))
				if err != nil {
					t.Fatal(err)
				}
				cleanupEndpoint(t, server)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				if control {
					_, connection, err := server.NewSessionConnection(ctx, tmux.NewSessionRequest{Command: "cat"}, tmux.ConnectionOptions{})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := connection.Close(); err != nil {
							t.Errorf("close connection: %v", err)
						}
					})
				} else if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Command: "cat"}); err != nil {
					t.Fatal(err)
				}
				wantPath := filepath.Join(directory, "named")
				result := mustRealCommand(t, server, "display-message", "-p", "#{socket_path}")
				if len(result.Stdout) != 1 || result.Stdout[0] != wantPath {
					t.Fatalf("daemon socket = %q, want %q", result.Stdout, wantPath)
				}
				info, err := os.Lstat(directory)
				if err != nil {
					t.Fatal(err)
				}
				if mode == 0 {
					mode = 0o700
				}
				if !info.IsDir() || info.Mode().Perm() != mode {
					t.Fatalf("socket directory mode = %v, want %o", info.Mode(), mode)
				}
			})
		}
	}
}

//libtmux:real-tmux
func TestNamedSocketInvalidRootsNeverLaunchElsewhere(t *testing.T) {
	for _, control := range []bool{false, true} {
		for _, condition := range []string{"missing", "removed", "other permissions", "symlink", "file", "wrong owner"} {
			t.Run(fmt.Sprintf("control=%t/%s", control, condition), func(t *testing.T) {
				root := endpointTestRoot(t)
				selected := filepath.Join(root, "selected")
				if condition != "missing" {
					if err := os.Mkdir(selected, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				directory := filepath.Join(selected, "tmux-"+strconv.Itoa(os.Getuid()))
				switch condition {
				case "other permissions", "wrong owner":
					if err := os.Mkdir(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					if condition == "wrong owner" {
						if os.Getuid() != 0 {
							t.Skip("changing directory ownership requires root")
						}
						if err := os.Chown(directory, 1, -1); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Chmod(directory, 0o701); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(root, directory); err != nil {
						t.Fatal(err)
					}
				case "file":
					if err := os.WriteFile(directory, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				name := "refused-" + rand.Text()
				server, err := tmux.NewServer(tmux.ServerOptions{
					ConfigFile: "/dev/null", SocketName: name,
					ProcessEnvironment: []string{"TMUX_TMPDIR=" + selected, "PATH=" + os.Getenv("PATH")},
				})
				if err != nil {
					t.Fatal(err)
				}
				if condition == "removed" {
					if err := os.Remove(selected); err != nil {
						t.Fatal(err)
					}
				}
				want := filepath.Join(directory, name)
				if server.SocketPath() != want {
					t.Fatalf("endpoint = %q, want %q", server.SocketPath(), want)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				if control {
					_, connection, startErr := server.NewSessionConnection(ctx, tmux.NewSessionRequest{Command: "cat"}, tmux.ConnectionOptions{})
					if connection != nil {
						if err := connection.Close(); err != nil {
							t.Error(err)
						}
					}
					err = startErr
				} else {
					_, err = server.NewSession(ctx, tmux.NewSessionRequest{Command: "cat"})
				}
				if err == nil {
					t.Fatal("invalid root launched a session")
				}
				if !strings.Contains(err.Error(), "socket directory") {
					t.Fatalf("lost directory failure: %v", err)
				}
				if _, err := os.Lstat(want); !errors.Is(err, os.ErrNotExist) && condition != "file" {
					t.Fatalf("unexpected endpoint after refused launch: %v", err)
				}
				fallback := filepath.Join("/tmp", "tmux-"+strconv.Itoa(os.Getuid()), name)
				if _, err := os.Lstat(fallback); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("fallback socket exists: %v", err)
				}
			})
		}
	}
}

//libtmux:real-tmux
func TestExplicitSocketDoesNotCreateParent(t *testing.T) {
	root := endpointTestRoot(t)
	parent := filepath.Join(root, "missing")
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: filepath.Join(parent, "socket"), ConfigFile: "/dev/null"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.NewSession(t.Context(), tmux.NewSessionRequest{Command: "cat"}); err == nil {
		t.Fatal("created session below missing parent")
	}
	if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parent was created: %v", err)
	}
}

func endpointTestRoot(t *testing.T) string {
	t.Helper()
	const base = "/tmp/libtmux-go-test"
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:usetesting // Unix sockets need a short path in the private test namespace.
	root, err := os.MkdirTemp(base, "endpoint-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove private root: %v", err)
		}
	})
	return root
}

func cleanupEndpoint(t *testing.T, server tmux.Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pid, _ := server.Cmd(ctx, "display-message", "-p", "#{pid}")
		var daemonPID int
		if pid.ExitCode == 0 && len(pid.Stdout) == 1 {
			daemonPID, _ = strconv.Atoi(pid.Stdout[0])
		}
		if err := server.Kill(ctx); err != nil && !errors.Is(err, tmux.ErrNoServer) {
			t.Errorf("kill private daemon: %v", err)
		}
		if daemonPID > 0 {
			if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, error) {
				return errors.Is(syscall.Kill(daemonPID, 0), syscall.ESRCH), nil
			}); err != nil {
				t.Errorf("private daemon process survived cleanup: %v", err)
			}
		}
		if alive, err := server.IsAlive(ctx); err != nil || alive {
			t.Errorf("daemon remains after cleanup: alive=%t, error=%v", alive, err)
		}
	})
}
