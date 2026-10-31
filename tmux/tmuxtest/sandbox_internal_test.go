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

// recordingTmux writes an executable that appends its arguments and the
// variables that select a tmux server to trace, and answers nothing. A child
// process writes it, for the reason ScriptedTmux does.
func recordingTmux(t testing.TB, trace string) string {
	t.Helper()

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	body.WriteString("{\n")
	body.WriteString("  printf 'argv %s\\n' \"$*\"\n")
	body.WriteString("  env | while IFS= read -r line; do\n")
	body.WriteString("    case $line in TMUX=*|TMUX_*|LIBTMUX_SOCKET*) printf 'env %s\\n' \"$line\";; esac\n")
	body.WriteString("  done\n")
	body.WriteString("} >>" + shellQuote(trace) + "\n")
	body.WriteString("exit 0\n")

	path := filepath.Join(t.TempDir(), "tmux")
	write := exec.Command("/bin/sh", "-c", `umask 077 && cat >"$1" && chmod 700 "$1"`, "sh", path)
	write.Stdin = strings.NewReader(body.String())
	if output, err := write.CombinedOutput(); err != nil {
		t.Fatalf("write recording tmux: %v: %s", err, output)
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

	recorded, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("the recording tmux was never run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "argv "):
			if slices.ContainsFunc(strings.Fields(line), func(field string) bool { return field == "-S" || field == "-L" }) {
				t.Errorf("a bare client was given a socket: %s", line)
			}
		case strings.HasPrefix(line, "env TMUX_TMPDIR="):
			directory := strings.TrimPrefix(line, "env TMUX_TMPDIR=")
			if findings := sandboxFindings([]string{"TMUX_TMPDIR=" + directory}, root); len(findings) != 0 {
				t.Errorf("a bare client was handed a TMUX_TMPDIR outside the suite: %s", strings.Join(findings, "; "))
			}
		default:
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "env "), "=")
			t.Errorf("a bare client was handed %s", name)
		}
	}
	if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "argv ") }) {
		t.Error("the recording tmux saw no command")
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

	t.Run("no root", func(t *testing.T) {
		t.Parallel()
		got := sandboxFindings(clean, "")
		if want := []string{"the default socket is outside the suite root"}; !slices.Equal(got, want) {
			t.Errorf("sandboxFindings() = %q, want %q", got, want)
		}
	})
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
