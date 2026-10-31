//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// withoutSuiteVariable tells TestMain to run a child without the suite, so the
// canary can be shown failing in a process that kept what a tmux pane sets.
const withoutSuiteVariable = "LIBTMUX_CANARY_WITHOUT_SUITE"

// recordingTmux writes an executable that appends its arguments to trace, and
// the names of the variables that select a tmux server. It reports a version
// and then that no server is running, which is all cleanup needs to finish.
// The one value it records is TMUX_TMPDIR, which is a directory and not a
// secret.
func recordingTmux(t testing.TB, trace string) string {
	t.Helper()

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	body.WriteString("{\n")
	body.WriteString("  printf 'argv %s\\n' \"$*\"\n")
	body.WriteString("  env | while IFS= read -r line; do\n")
	body.WriteString("    case $line in\n")
	body.WriteString("      TMUX_TMPDIR=*) printf 'tmpdir %s\\n' \"${line#TMUX_TMPDIR=}\";;\n")
	body.WriteString("      TMUX=*|TMUX_PANE=*|LIBTMUX_SOCKET*) printf 'env %s\\n' \"${line%%=*}\";;\n")
	body.WriteString("    esac\n")
	body.WriteString("  done\n")
	body.WriteString("} >>" + shellQuote(trace) + "\n")
	body.WriteString("case \" $* \" in *\" -V \"*) echo 'tmux 3.8'; exit 0;; esac\n")
	body.WriteString("echo 'no server running on the recorder' >&2\n")
	body.WriteString("exit 1\n")
	return writeExecutable(t, body.String())
}

// writeExecutable writes script as a file named tmux in a directory of its own
// and returns its path. A child process writes it, because a fork anywhere in
// this process holds every open descriptor until it execs, and executing a
// file some process holds open for writing fails with "text file busy".
func writeExecutable(t testing.TB, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tmux")
	write := exec.Command("/bin/sh", "-c", `umask 077 && cat >"$1" && chmod 700 "$1"`, "sh", path)
	write.Stdin = strings.NewReader(script)
	if output, err := write.CombinedOutput(); err != nil {
		t.Fatalf("write fake tmux: %v: %s", err, output)
	}
	return path
}

// The canary runs inside the suite and reports what a server-selecting client
// is handed. It is the check that fails when a pane's TMUX, TMUX_PANE or a
// socket selector survives into a test.
func TestSuiteEnvironmentIsSandboxed(t *testing.T) {
	root, err := activeSuiteRoot()
	if err != nil {
		t.Error("unsandboxed: no suite is active")
	}
	for _, finding := range sandboxFindings(os.Environ(), root) {
		t.Errorf("unsandboxed: %s", finding)
	}

	// A client that names no socket is the one the environment steers.
	trace := filepath.Join(t.TempDir(), "trace")
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: recordingTmux(t, trace)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = server.Cmd(ctx, "list-sessions")

	checkRecordedClient(t, trace, root, "a bare client")
}

// checkRecordedClient fails for each way the client that wrote trace was
// steered off root: a socket in its arguments, a selector in its environment,
// or a TMUX_TMPDIR outside root. It names variables and never their values.
func checkRecordedClient(t *testing.T, trace, root, who string) {
	t.Helper()

	recorded, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("the recording tmux was never run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "argv "):
			if slices.ContainsFunc(strings.Fields(line), func(field string) bool { return field == "-S" || field == "-L" }) {
				t.Errorf("%s was given a socket: %s", who, line)
			}
		case strings.HasPrefix(line, "tmpdir "):
			directory := strings.TrimPrefix(line, "tmpdir ")
			if findings := sandboxFindings([]string{"TMUX_TMPDIR=" + directory}, root); len(findings) != 0 {
				t.Errorf("%s was handed a TMUX_TMPDIR outside the suite: %s", who, strings.Join(findings, "; "))
			}
		default:
			t.Errorf("%s was handed %s", who, strings.TrimPrefix(line, "env "))
		}
	}
	if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "argv ") }) {
		t.Error("the recording tmux saw no command")
	}
}

// A harness server is handed an environment the caller replaced, and tmux
// children must still see none of the selectors in it and still see
// TMUX_TMPDIR.
func TestHarnessServersKeepTMUXTMPDIRAndDropSelectors(t *testing.T) {
	root, err := activeSuiteRoot()
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := NewServerWithOptions(ctx, t, ServerOptions{
		Binary: recordingTmux(t, trace),
		ProcessEnvironment: []string{
			"PATH=" + os.Getenv("PATH"),
			"TMUX_TMPDIR=" + os.Getenv("TMUX_TMPDIR"),
			"TMUX=/tmp/decoy,1,0",
			"TMUX_PANE=%9",
			"LIBTMUX_SOCKET=decoy",
			"LIBTMUX_SOCKET_PATH=/tmp/decoy/path",
		},
	})
	_, _ = server.Cmd(ctx, "list-sessions")

	recorded, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("the recording tmux was never run: %v", err)
	}
	if !strings.Contains(string(recorded), "tmpdir "+root+"\n") {
		t.Error("the server child lost TMUX_TMPDIR")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "LIBTMUX_SOCKET", "LIBTMUX_SOCKET_PATH"} {
		if strings.Contains(string(recorded), "env "+name+"\n") {
			t.Errorf("the server child was handed %s", name)
		}
	}

	environment, err := controlProcessEnvironment(server)
	if err != nil {
		t.Fatal(err)
	}
	if got := ambientTmuxNames(environment); len(got) != 0 {
		t.Errorf("the control client was handed %s", strings.Join(got, ", "))
	}
	if !slices.Contains(environment, "TMUX_TMPDIR="+root) {
		t.Error("the control client lost TMUX_TMPDIR")
	}
}

// A canary that cannot fail proves nothing, so this runs it in a child that
// skips the suite and keeps the variables a tmux pane sets.
func TestSandboxCanaryFailsWhereTheSuiteIsAbsent(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestSuiteEnvironmentIsSandboxed$", "-test.count=1")
	command.Env = append(os.Environ(),
		withoutSuiteVariable+"=1",
		"TMUX=/tmp/decoy/sock,1,0",
		"TMUX_PANE=%9",
		"LIBTMUX_SOCKET=decoy",
		"LIBTMUX_SOCKET_PATH=/tmp/decoy/path",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("the canary passed in a process holding a decoy TMUX:\n%s", output)
	}
	for _, want := range []string{
		"unsandboxed: no suite is active",
		"unsandboxed: TMUX is set",
		"unsandboxed: TMUX_PANE is set",
		"unsandboxed: LIBTMUX_SOCKET is set",
		"unsandboxed: LIBTMUX_SOCKET_PATH is set",
		"a bare client was handed TMUX\n",
		"a bare client was handed TMUX_PANE\n",
		"a bare client was handed LIBTMUX_SOCKET\n",
		"a bare client was handed LIBTMUX_SOCKET_PATH\n",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("canary output lacks %q:\n%s", want, output)
		}
	}
}

// The variable that lets the canary's control run without the suite must not
// let anything else: a child that keeps it and runs a second test is inside the
// suite, so the canary passes there.
func TestTheCanaryBypassCoversOnlyTheCanary(t *testing.T) {
	command := exec.Command(os.Args[0],
		"-test.run=^(TestSuiteEnvironmentIsSandboxed|TestAssertSandboxedNamesVariablesAndNeverValues)$",
		"-test.count=1")
	command.Env = append(os.Environ(), withoutSuiteVariable+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("a child holding the bypass variable ran outside the suite:\n%s", output)
	}
}

func TestSandboxFindingsNameEachWayOutOfTheSuite(t *testing.T) {
	t.Parallel()

	const root = "/tmp/ltg-canary"
	clean := []string{
		"PATH=/usr/bin",
		"TMUX_TMPDIR=" + root,
		// Other libtmux and tmux variables select no server.
		"LIBTMUX_SUITE_ROOT_TAG=tag",
		"LIBTMUX_OPTIONAL_OLD_TMUX=/opt/tmux/bin/tmux",
		"TMUX_GO_TEST=value",
	}
	with := func(entries ...string) []string { return append(slices.Clone(clean), entries...) }
	without := func(prefix string) []string {
		return slices.DeleteFunc(slices.Clone(clean), func(entry string) bool { return strings.HasPrefix(entry, prefix) })
	}

	for _, test := range []struct {
		name        string
		environment []string
		want        []string
	}{
		{"a sandboxed environment", clean, nil},
		{"a directory inside the root", append(without("TMUX_TMPDIR="), "TMUX_TMPDIR="+root+"/nested"), nil},
		{"TMUX", with("TMUX=/tmp/decoy,1,0"), []string{"TMUX is set"}},
		{"an empty TMUX", with("TMUX="), []string{"TMUX is set"}},
		{"TMUX_PANE", with("TMUX_PANE=%3"), []string{"TMUX_PANE is set"}},
		{"LIBTMUX_SOCKET", with("LIBTMUX_SOCKET=decoy"), []string{"LIBTMUX_SOCKET is set"}},
		{"LIBTMUX_SOCKET_PATH", with("LIBTMUX_SOCKET_PATH=/tmp/decoy"), []string{"LIBTMUX_SOCKET_PATH is set"}},
		{"a selector the ports may add", with("LIBTMUX_SOCKET_DIR=/tmp/decoy"), []string{"LIBTMUX_SOCKET_DIR is set"}},
		{
			"every selector at once",
			with("TMUX=/tmp/decoy,1,0", "TMUX_PANE=%3", "LIBTMUX_SOCKET=a", "LIBTMUX_SOCKET_PATH=/b"),
			[]string{"TMUX is set", "TMUX_PANE is set", "LIBTMUX_SOCKET is set", "LIBTMUX_SOCKET_PATH is set"},
		},
		{"no TMUX_TMPDIR", without("TMUX_TMPDIR="), []string{"TMUX_TMPDIR is not an absolute path"}},
		{"a relative TMUX_TMPDIR", append(without("TMUX_TMPDIR="), "TMUX_TMPDIR=ltg-canary"), []string{"TMUX_TMPDIR is not an absolute path"}},
		{"a directory elsewhere", append(without("TMUX_TMPDIR="), "TMUX_TMPDIR=/tmp"), []string{"the default socket is outside the suite root"}},
		{"a sibling sharing the prefix", append(without("TMUX_TMPDIR="), "TMUX_TMPDIR="+root+"-other"), []string{"the default socket is outside the suite root"}},
		{"a directory climbing out", append(without("TMUX_TMPDIR="), "TMUX_TMPDIR="+root+"/../other"), []string{"the default socket is outside the suite root"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := sandboxFindings(test.environment, root)
			if !slices.Equal(got, test.want) {
				t.Errorf("sandboxFindings() = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("a default socket path too long to use", func(t *testing.T) {
		t.Parallel()
		long := "/tmp/ltg-" + strings.Repeat("x", maxDefaultSocketBytes)
		got := sandboxFindings([]string{"TMUX_TMPDIR=" + long}, long)
		if want := []string{"the default socket path is too long"}; !slices.Equal(got, want) {
			t.Errorf("sandboxFindings() = %q, want %q", got, want)
		}
	})

	t.Run("no root", func(t *testing.T) {
		t.Parallel()
		got := sandboxFindings(clean, "")
		if want := []string{"the default socket is outside the suite root"}; !slices.Equal(got, want) {
			t.Errorf("sandboxFindings() = %q, want %q", got, want)
		}
	})
}

func TestPrivateRootFindings(t *testing.T) {
	t.Parallel()

	directory := func(mode os.FileMode) string {
		path := filepath.Join(t.TempDir(), "root")
		if err := os.Mkdir(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(directory(0o700), link); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		root string
		want []string
	}{
		{"a private directory", directory(0o700), nil},
		{"a directory others read", directory(0o755), []string{"the suite root is not private"}},
		{"a directory the group writes", directory(0o770), []string{"the suite root is not private"}},
		{"a file", file, []string{"the suite root is not a directory"}},
		{"a missing path", filepath.Join(t.TempDir(), "missing"), []string{"the suite root is not a directory"}},
		{"a symbolic link to a directory", link, []string{"the suite root is not a directory"}},
	} {
		if got := privateRootFindings(test.root); !slices.Equal(got, test.want) {
			t.Errorf("%s: privateRootFindings() = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestAssertSandboxedRefusesARootOthersCanUse(t *testing.T) {
	root := filepath.Join(shortDirectory(t), "open")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", root)

	err := assertSandboxed(root)

	if err == nil || !strings.Contains(err.Error(), "the suite root is not private") {
		t.Fatalf("assertSandboxed() = %v, want a refusal naming the root as not private", err)
	}
}

func TestAssertSandboxedNamesVariablesAndNeverValues(t *testing.T) {
	root, err := activeSuiteRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := assertSandboxed(root); err != nil {
		t.Fatalf("assertSandboxed() inside the suite = %v, want nil", err)
	}

	t.Setenv("TMUX", "/tmp/private-decoy,1,0")
	err = assertSandboxed(root)
	if err == nil {
		t.Fatal("assertSandboxed() with TMUX set = nil, want an error")
	}
	if !strings.Contains(err.Error(), "TMUX is set") || strings.Contains(err.Error(), "private-decoy") {
		t.Errorf("assertSandboxed() = %q, want TMUX named and its value withheld", err)
	}
}
