package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func rejectedTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("LIBTMUX_REJECTED_TMUX")
	if binary == "" {
		if os.Getenv("LIBTMUX_REQUIRE_REJECTED") != "" {
			t.Fatal("LIBTMUX_REQUIRE_REJECTED is set but LIBTMUX_REJECTED_TMUX is not")
		}
		t.Skip("set LIBTMUX_REJECTED_TMUX to an exact 3.0a, 3.1c, 3.2a or 3.3 binary for negative version-check coverage")
	}
	output, err := exec.CommandContext(t.Context(), binary, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	switch version := strings.TrimSpace(string(output)); version {
	case "tmux 3.0a", "tmux 3.1c", "tmux 3.2a", "tmux 3.3":
	default:
		t.Fatalf("rejection fixture reports %q, want tmux 3.0a, 3.1c, 3.2a or 3.3", version)
	}
	return binary
}

// pre32TmuxBinary is rejectedTmuxBinary for tests that need a tmux with no
// run-shell -C and no -N; a newer rejected fixture skips them.
func pre32TmuxBinary(t *testing.T) string {
	t.Helper()
	binary := rejectedTmuxBinary(t)
	output, err := exec.CommandContext(t.Context(), binary, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	if version := strings.TrimSpace(string(output)); version != "tmux 3.0a" && version != "tmux 3.1c" {
		if os.Getenv("LIBTMUX_REQUIRE_PRE32") != "" {
			t.Fatalf("LIBTMUX_REQUIRE_PRE32 is set but the fixture is %s", version)
		}
		t.Skipf("%s has run-shell -C; this test needs a tmux older than 3.2", version)
	}
	return binary
}

// postProbeRejectedTmuxBinary is rejectedTmuxBinary for tests that drive the
// fixture as a client, which a tmux older than 3.2 cannot be.
func postProbeRejectedTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := rejectedTmuxBinary(t)
	output, err := exec.CommandContext(t.Context(), binary, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	if version := strings.TrimSpace(string(output)); version == "tmux 3.0a" || version == "tmux 3.1c" {
		t.Skipf("%s is not a client the version check can run through", version)
	}
	return binary
}

func versionNativeCommand(t *testing.T, binary, socket string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"-f" + os.DevNull, "-S" + socket}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native fixture %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func TestCoreMinimumRejectsExplicitOlderDaemon(t *testing.T) {
	old := rejectedTmuxBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() {
		versionNativeCommand(t, old, socket, "kill-server")
	})
	versionNativeCommand(t, old, socket, "set-buffer", "-b", "sentinel", "original")
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"raw read": func() error {
			_, err := server.Cmd(t.Context(), "list-sessions")
			return err
		},
		"raw batch": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-b", "sentinel", "changed", ";", "new-session", "-d", "-s", "forbidden")
			return err
		},
		"typed write": func() error {
			return server.SetBuffer(t.Context(), tmux.SetBufferRequest{Name: new("sentinel"), Data: "changed"})
		},
		"plan": func() error {
			var plan tmux.Plan
			plan.SetBuffer("sentinel", "changed")
			_, err := plan.Run(t.Context(), server)
			return err
		},
		"new connection": func() error {
			_, connection, err := server.NewSessionConnection(t.Context(), tmux.NewSessionRequest{Name: "forbidden"}, tmux.ConnectionOptions{})
			if connection != nil {
				_ = connection.Close()
				t.Fatal("unsupported daemon acquired a control connection")
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("operation error = %v, want unsupported daemon refusal", err)
			}
			if got := versionNativeCommand(t, old, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
				t.Fatalf("unsupported operation changed the sentinel to %q", got)
			}
			if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
				t.Fatalf("unsupported operation changed the sessions to %q", got)
			}
		})
	}
}

func TestCoreMinimumFencesDaemonAppearingAtColdStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the race fixture uses a POSIX executable proxy")
	}
	old := postProbeRejectedTmuxBinary(t)
	modern, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.CommandContext(t.Context(), modern, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	socket := filepath.Join(root, "s")
	proxy := filepath.Join(root, "tmux-proxy")
	seen := filepath.Join(root, "probed")
	script := "#!/bin/sh\n" +
		"if [ \"$#\" = 1 ] && [ \"$1\" = -V ]; then\n" +
		strconv.Quote(old) + " -f /dev/null -S " + strconv.Quote(socket) + " new-session -d -s keeper 'sleep 300'\n" +
		"printf '%s\\n' " + strconv.Quote(strings.TrimSpace(string(version))) + "\nexit 0\nfi\n" +
		"if [ ! -e " + strconv.Quote(seen) + " ]; then\n" +
		"touch " + strconv.Quote(seen) + "\nprintf '%s\\n' 'no server running on fixture' >&2\nexit 1\nfi\n" +
		"exec " + strconv.Quote(modern) + " \"$@\"\n"
	if err := os.WriteFile(proxy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		versionNativeCommand(t, old, socket, "kill-server")
	})
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: proxy, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Cmd(t.Context(), "new-session", "-d", "-s", "forbidden")
	if !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("cold-start race error = %v, want atomic refusal", err)
	}
	if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
		t.Fatalf("cold-start race created a session: %q", got)
	}
}

func TestCoreMinimumBoundHandleDoesNotStartOlderExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the configuration marker uses a POSIX shell")
	}
	old := postProbeRejectedTmuxBinary(t)
	modern := os.Getenv("LIBTMUX_FLOOR_TMUX")
	if modern == "" {
		var err error
		modern, err = exec.LookPath("tmux")
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	socket := filepath.Join(root, "s")
	marker := filepath.Join(root, "unexpected-startup")
	config := filepath.Join(root, "tmux.conf")
	contents := "run-shell " + strconv.Quote("printf started > "+strconv.Quote(marker)) + "\n"
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	versionNativeCommand(t, modern, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, old, "-N", "-S"+socket, "kill-server").Run()
	})
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: old, SocketPath: socket, ConfigFile: config})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stale := snapshot.Sessions()[0]
	bound := stale.Server()
	versionNativeCommand(t, modern, socket, "kill-server")
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := exec.CommandContext(t.Context(), modern, "-N", "-S"+socket, "list-sessions").Run()
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture daemon did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := bound.Cmd(t.Context(), "new-session", "-d", "-s", "forbidden")
	if err == nil && result.ExitCode == 0 {
		t.Fatal("a materialized handle started another daemon")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an older executable loaded configuration before refusal: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := bound.OpenControl(ctx, stale)
	if client != nil {
		_ = client.Close()
		t.Fatal("a stale handle acquired control on another daemon")
	}
	if err == nil {
		t.Fatal("control attachment to an absent daemon succeeded")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control startup loaded an older executable's configuration: %v", err)
	}
}

func TestCoreMinimumColdVersionCheckIgnoresCachedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable replacement fixture uses a POSIX shell")
	}
	old := postProbeRejectedTmuxBinary(t)
	modern, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	socket := filepath.Join(root, "s")
	marker := filepath.Join(root, "started")
	configuration := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(configuration, []byte("set -s exit-empty off\nrun-shell "+strconv.Quote("printf started > "+strconv.Quote(marker))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(root, "tmux-proxy")
	selectBinary := func(binary string) {
		t.Helper()
		if err := os.WriteFile(proxy, []byte("#!/bin/sh\nexec "+strconv.Quote(binary)+" \"$@\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selectBinary(modern)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: proxy, SocketPath: socket, ConfigFile: configuration})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion(tmux.MinimumSupportedVersion)
	if err != nil || !cached.AtLeast(minimum) {
		t.Fatalf("modern fixture = %s %v", cached, err)
	}
	selectBinary(old)
	t.Cleanup(func() { _ = exec.Command(old, "-S", socket, "kill-server").Run() })
	_, err = server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "forbidden", Command: "sleep 300"})
	if _, markerErr := os.Stat(marker); !errors.Is(markerErr, os.ErrNotExist) {
		t.Fatalf("cached executable version check started old configuration: marker=%v operation=%v", markerErr, err)
	}
	if !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("cached executable version check error = %v, want version refusal", err)
	}
	if _, socketErr := os.Stat(socket); !errors.Is(socketErr, os.ErrNotExist) {
		t.Fatalf("cached executable version check created daemon socket: %v", socketErr)
	}
}

func TestCoreMinimumColdFeatureRequirementUsesFreshExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable replacement fixture uses a POSIX shell")
	}
	olderSupported := os.Getenv("LIBTMUX_FLOOR_TMUX")
	if olderSupported == "" {
		t.Skip("set LIBTMUX_FLOOR_TMUX to exact tmux 3.3a")
	}
	modern, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	proxy := filepath.Join(root, "tmux-proxy")
	selectBinary := func(binary string) {
		t.Helper()
		if err := os.WriteFile(proxy, []byte("#!/bin/sh\nexec "+strconv.Quote(binary)+" \"$@\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selectBinary(modern)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: proxy, SocketPath: filepath.Join(root, "s"), ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	required, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}
	if !cached.AtLeast(required) {
		t.Skip("this counterexample needs a configured executable at least 3.7")
	}
	selectBinary(olderSupported)
	err = server.RequireVersion(t.Context(), required)
	var refusal *tmux.VersionTooLowError
	if !errors.As(err, &refusal) || refusal.Current.String() != "3.3a" || refusal.Minimum.String() != "3.7" {
		t.Fatalf("cold feature requirement reused cached executable: %v", err)
	}
	// The independent diagnostic cache remains a cache; the version check does not
	// invalidate or rewrite an unrelated observer's reported executable.
	if diagnostic, err := server.Version(t.Context()); err != nil || diagnostic != cached {
		t.Fatalf("cold version check changed diagnostic cache: %s %v", diagnostic, err)
	}
}

func TestCoreMinimumOpaqueExecutableOn33a(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the opaque executable route uses Linux byte paths")
	}
	binary := os.Getenv("LIBTMUX_FLOOR_TMUX")
	if binary == "" {
		t.Skip("set LIBTMUX_FLOOR_TMUX to exact tmux 3.3a")
	}
	directory := t.TempDir()
	opaque := filepath.Join(directory, "tmux'"+string([]byte{0xff}))
	if err := os.Symlink(binary, opaque); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "s")
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: opaque, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(binary, "-S", socket, "kill-server").Run() })
	version, err := server.Version(t.Context())
	if err != nil || version.String() != "3.3a" {
		t.Fatalf("native opaque executable version = %s %v", version, err)
	}
	session, err := server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "opaque", Command: "sleep 300"})
	if err != nil || session.ID() == "" {
		t.Fatalf("native opaque executable startup = %v %v", session, err)
	}
	if version, err := session.Server().Version(t.Context()); err != nil || version.String() != "3.3a" {
		t.Fatalf("opaque daemon qualification = %s %v", version, err)
	}
}

// A daemon older than 3.2 cannot answer the probe, and a newer client cannot
// even speak to a 3.0 one. Both are a refused tmux, and neither run changes it.
func TestCoreMinimumRefusesDaemonBeforeProbeExisted(t *testing.T) {
	old := pre32TmuxBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() {
		versionNativeCommand(t, old, socket, "kill-server")
	})
	versionNativeCommand(t, old, socket, "set-buffer", "-b", "sentinel", "original")
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Cmd(t.Context(), "set-buffer", "-b", "sentinel", "changed")
	var refused *tmux.VersionTooLowError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "older than 3.2") {
		t.Fatalf("command against a pre-3.2 daemon = %v, want the version refusal", err)
	}
	if got := versionNativeCommand(t, old, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
		t.Fatalf("refused command changed the sentinel to %q", got)
	}
}

// A client older than 3.2 rejects -N, which the probe sends, so the executable
// is refused whether or not a daemon is running.
func TestCoreMinimumRefusesExecutableBeforeProbeExisted(t *testing.T) {
	old := pre32TmuxBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: old, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Cmd(t.Context(), "new-session", "-d", "-s", "forbidden")
	reported, versionErr := exec.CommandContext(t.Context(), old, "-V").Output()
	if versionErr != nil {
		t.Fatal(versionErr)
	}
	release := strings.TrimPrefix(strings.TrimSpace(string(reported)), "tmux ")
	var refused *tmux.VersionTooLowError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "installed "+release+",") {
		t.Fatalf("command through a pre-3.2 client = %v, want the refusal naming installed %s", err, release)
	}
	if _, statErr := os.Stat(socket); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused command created a daemon socket: %v", statErr)
	}
}

// A tmux before 3.2 keeps the client's stdout pipe open after the client has
// exited. The refusal must come back as soon as the process is done, not when
// a command timeout fires or the pipe closes. Output captured through pipes,
// which an unwritable temporary directory forces, is the case that depends on
// the runner's wait delay.
func TestCoreMinimumRefusesPre32DaemonPromptly(t *testing.T) {
	old := pre32TmuxBinary(t)
	for _, test := range []struct {
		name     string
		tmpdir   bool
		deadline time.Duration
	}{
		{name: "temporary files", deadline: 5 * time.Second},
		{name: "unwritable TMPDIR", tmpdir: true, deadline: 5 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "s")
			absent := filepath.Join(t.TempDir(), "absent")
			versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
			t.Cleanup(func() {
				versionNativeCommand(t, old, socket, "kill-server")
			})
			server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: socket, ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			if test.tmpdir {
				t.Setenv("TMPDIR", absent)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			started := time.Now()
			_, err = server.Cmd(ctx, "list-sessions")
			if elapsed := time.Since(started); elapsed > test.deadline {
				t.Fatalf("refusal took %s, want it within %s and long before the %s command timeout", elapsed, test.deadline, time.Minute)
			}
			if !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("command against a pre-3.2 daemon = %v, want the version refusal", err)
			}
		})
	}
}
