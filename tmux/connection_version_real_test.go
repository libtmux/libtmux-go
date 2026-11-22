package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configuredLiveTestTmux() (string, bool) {
	for _, variable := range []string{"LIBTMUX_TEST_TMUX", "LIBTMUX_TMUX"} {
		if value := os.Getenv(variable); strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

// OpenControl checks the daemon before it attaches and binds the checked
// handle, so a connection carries the version it was checked at even when the
// session value came from a server that never probed one. The literal-command
// gates read that version; without it they have nothing to compare.
func TestOpenControlBindsTheVersionItChecked(t *testing.T) {
	candidate, explicit := configuredLiveTestTmux()
	if !explicit {
		candidate = "tmux"
	}
	binary, err := exec.LookPath(candidate)
	if err != nil {
		if explicit {
			t.Fatal(err)
		}
		t.Skip("tmux is not installed")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	version, err := ParseVersion(strings.TrimPrefix(strings.TrimSpace(string(output)), "tmux "))
	if err != nil {
		t.Skipf("unrecognised tmux %q", output)
	}
	if !version.meetsMinimum(controlMinimumVersion) {
		t.Skipf("control connections need tmux %s, not %s", controlMinimumVersion, version)
	}

	socket := filepath.Join(t.TempDir(), "s")
	if err := exec.Command(binary, "-S", socket, "-f", os.DevNull, "new-session", "-d", "-s", "keeper", "sleep 300").Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(binary, "-S", socket, "kill-server").Run() })
	server, err := NewServer(ServerOptions{Binary: binary, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if server.daemon != nil {
		t.Fatal("a server that never probed carries a daemon identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := (Session{server: server, sessionID: "$0"}).OpenControl(ctx, ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if connection.Server().daemon == nil {
		t.Fatal("the connection's server carries no daemon version, so its literal-command gates have nothing to compare")
	}
}
