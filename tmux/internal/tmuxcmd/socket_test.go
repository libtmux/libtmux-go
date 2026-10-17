package tmuxcmd

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinSocketRetainsTheOriginalPrivateEndpoint(t *testing.T) {
	path := filepath.Join(socketTestDirectory(t), "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	alias, release, err := PinSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	info, err := os.Stat(filepath.Dir(alias))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("carrier permissions: %v %v", info, err)
	}
	original, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := os.Lstat(alias)
	if err != nil || !os.SameFile(original, pinned) {
		t.Fatalf("carrier inode: %v %v", pinned, err)
	}
	if err := os.Rename(path, path+".retired"); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacement.Close() }()
	accepted := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer func() { _ = connection.Close() }()
			_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
			_, err = io.WriteString(connection, "original")
		}
		accepted <- err
	}()
	connection, err := net.DialTimeout("unix", alias, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if err := release(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(connection)
	if err != nil || string(data) != "original" {
		t.Fatalf("pinned route after cleanup: %q %v", data, err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(alias)); !os.IsNotExist(err) {
		t.Fatalf("carrier directory survived: %v", err)
	}
}

func TestPinSocketRejectsAnOrdinaryFile(t *testing.T) {
	path := filepath.Join(socketTestDirectory(t), "s")
	if err := os.WriteFile(path, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias, release, err := PinSocket(path)
	if err == nil || alias != "" || release != nil {
		t.Fatalf("non-socket pin = %q %v", alias, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("source file changed: %q %v", data, err)
	}
}

func TestPinSocketRetainsAValidLongSocketPath(t *testing.T) {
	root := socketTestDirectory(t)
	padding := 100 - len(root) - 3
	if padding < 1 {
		t.Fatal("temporary root leaves no room for a native Unix socket")
	}
	directory := filepath.Join(root, strings.Repeat("x", padding))
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("valid native socket: %v", err)
	}
	defer func() { _ = listener.Close() }()
	alias, release, err := PinSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	connection, err := net.DialTimeout("unix", alias, time.Second)
	if err != nil {
		t.Fatalf("pinned long socket is unreachable: %v", err)
	}
	_ = connection.Close()
}

func TestPinSocketUsesItsPeerWhenTemporaryRootIsUnavailable(t *testing.T) {
	path := filepath.Join(socketTestDirectory(t), "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	t.Setenv("TMPDIR", filepath.Join(filepath.Dir(path), "missing"))
	alias, release, err := PinSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	connection, err := net.DialTimeout("unix", alias, time.Second)
	if err != nil {
		t.Fatalf("pinned peer socket is unreachable: %v", err)
	}
	_ = connection.Close()
}

func socketTestDirectory(t *testing.T) string {
	t.Helper()
	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	root, err := os.MkdirTemp("", "lc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
