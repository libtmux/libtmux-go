//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listenAt listens on a Unix socket at path and closes it with the test. The
// socket is a server as far as the scan can tell: something answers on it.
func listenAt(t *testing.T, path string) *net.UnixListener {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

// shortDirectory makes a directory whose name leaves room for a socket path.
func shortDirectory(t *testing.T) string {
	t.Helper()

	// t.TempDir names the test, and a socket path may hold only 103 bytes.
	directory, err := os.MkdirTemp("", "lk") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

// stoppingTmux is a tmux whose kill-server stops the server by removing its
// socket, and records the arguments it was given.
func stoppingTmux(t *testing.T, record string) string {
	t.Helper()

	return writeExecutable(t, "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >>"+shellQuote(record)+"\n"+
		"[ \"$3\" = kill-server ] && rm -f \"$2\"\n"+
		"exit 0\n")
}

func TestScanStopsAServerOnAnySocketInTheRoot(t *testing.T) {
	t.Parallel()

	root := shortDirectory(t)
	deep := filepath.Join(root, "a", "b", "custom.sock")
	listenAt(t, deep)
	record := filepath.Join(shortDirectory(t), "argv")

	report := stopLeakedServers(root, stoppingTmux(t, record))

	if report != (leakReport{elsewhere: 1}) {
		t.Fatalf("stopLeakedServers() = %+v, want one server on another socket", report)
	}
	if report.keepsRoot() {
		t.Error("the report keeps a root whose server stopped")
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("kill-server was never run: %v", err)
	}
	if want := "-S " + deep + " kill-server\n"; string(got) != want {
		t.Errorf("tmux was run with %q, want %q", got, want)
	}
}

func TestScanCountsTheDefaultSocketApart(t *testing.T) {
	t.Parallel()

	root := shortDirectory(t)
	listenAt(t, filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "default"))
	listenAt(t, filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "named"))

	report := stopLeakedServers(root, stoppingTmux(t, filepath.Join(shortDirectory(t), "argv")))

	if want := (leakReport{onDefault: 1, elsewhere: 1}); report != want {
		t.Fatalf("stopLeakedServers() = %+v, want %+v", report, want)
	}
}

func TestScanIgnoresASocketFileNothingListensOn(t *testing.T) {
	t.Parallel()

	root := shortDirectory(t)
	listener := listenAt(t, filepath.Join(root, "dead.sock"))
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	if report := stopLeakedServers(root, ""); report != (leakReport{}) {
		t.Fatalf("stopLeakedServers() = %+v, want nothing for a dead socket file", report)
	}
}

func TestScanKeepsTheRootWhileAServerStillAnswers(t *testing.T) {
	t.Parallel()

	root := shortDirectory(t)
	listenAt(t, filepath.Join(root, "stubborn.sock"))
	ignoring := writeExecutable(t, "#!/bin/sh\nexit 0\n")

	for name, tmuxPath := range map[string]string{
		"a kill-server that changes nothing": ignoring,
		"no tmux to ask":                     "",
	} {
		report := stopLeakedServers(root, tmuxPath)
		if report.survivors != 1 || !report.keepsRoot() {
			t.Errorf("%s: stopLeakedServers() = %+v, want one survivor and the root kept", name, report)
		}
	}
}

func TestScanKeepsTheRootItCouldNotRead(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("a superuser reads any directory")
	}
	root := shortDirectory(t)
	hidden := filepath.Join(root, "hidden")
	if err := os.Mkdir(hidden, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hidden, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o700) })

	report := stopLeakedServers(root, "")

	if report.unscanned != 1 || !report.keepsRoot() {
		t.Fatalf("stopLeakedServers() = %+v, want one unscanned entry and the root kept", report)
	}
}

func TestLeakMessagesGiveCountsAndNeverPaths(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		report leakReport
		want   string
	}{
		{"nothing", leakReport{}, ""},
		{
			"stopped",
			leakReport{onDefault: 1, elsewhere: 2},
			"tmuxtest: the suite left tmux servers running: 1 on the default socket in TMUX_TMPDIR, 2 on another socket; each was stopped with kill-server",
		},
		{
			"survivor",
			leakReport{elsewhere: 1, survivors: 1},
			"tmuxtest: the suite left tmux servers running: 0 on the default socket in TMUX_TMPDIR, 1 on another socket; 1 did not stop after kill-server, so the suite root was kept",
		},
		{
			"unscanned",
			leakReport{unscanned: 2},
			"tmuxtest: 2 entries in the suite root could not be scanned for servers, so the root was kept",
		},
	} {
		if got := test.report.message(); got != test.want {
			t.Errorf("%s: message() = %q, want %q", test.name, got, test.want)
		}
	}
}

// stubbornListener is referenced for the life of the process, because the
// garbage collector closes a listener nothing refers to and the socket would
// stop answering before the run ends.
var stubbornListener *net.UnixListener

// TestSuiteChildHelper is the test a child process runs for the tests below:
// it passes, leaving what its mode says behind.
func TestSuiteChildHelper(t *testing.T) {
	mode := os.Getenv("LIBTMUX_SUITE_CHILD")
	if mode == "" {
		return
	}
	record := ""
	switch mode {
	case "default":
		out, err := exec.Command("tmux", "-f", "/dev/null", "new-session", "-d", "-s", "leak", "-P", "-F", "#{pid}").Output()
		if err != nil {
			t.Fatalf("start the leaked server: %v", err)
		}
		record = strings.TrimSpace(string(out))
	case "custom":
		socket := filepath.Join(os.Getenv("TMUX_TMPDIR"), "custom.sock")
		out, err := exec.Command("tmux", "-f", "/dev/null", "-S", socket, "new-session", "-d", "-s", "leak", "-P", "-F", "#{pid}").Output()
		if err != nil {
			t.Fatalf("start the leaked server: %v", err)
		}
		record = strings.TrimSpace(string(out))
	case "survivor":
		// Something answers here that the fake tmux on PATH cannot stop. It
		// stays open until the process exits.
		address, err := net.ResolveUnixAddr("unix", filepath.Join(os.Getenv("TMUX_TMPDIR"), "stubborn.sock"))
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.ListenUnix("unix", address)
		if err != nil {
			t.Fatal(err)
		}
		stubbornListener = listener
		record = os.Getenv("TMUX_TMPDIR")
	}
	if err := os.WriteFile(os.Getenv("LIBTMUX_SUITE_CHILD_RECORD"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A test that leaves a server running passes, so only the run can fail for it.
// The child here is a whole suite whose one test leaks, once on the default
// socket and once on an explicit one; the run has to fail, stop the server,
// and say how many without saying where.
func TestRunFailsWhenAServerIsLeft(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH")
	}
	for _, test := range []struct {
		mode string
		want string
	}{
		{"default", "1 on the default socket in TMUX_TMPDIR, 0 on another socket; each was stopped with kill-server"},
		{"custom", "0 on the default socket in TMUX_TMPDIR, 1 on another socket; each was stopped with kill-server"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()

			output, record := runLeakingChild(t, test.mode, nil)

			if !strings.HasPrefix(output, "PASS\n") {
				t.Errorf("the child's tests did not all pass, which is the case under test:\n%s", output)
			}
			if !strings.Contains(output, test.want) {
				t.Errorf("the child's output lacks %q:\n%s", test.want, output)
			}
			pid, err := strconv.Atoi(record)
			if err != nil {
				t.Fatalf("the child recorded no server: %v", err)
			}
			deadline := time.Now().Add(cleanupTimeout)
			for processAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if processAlive(pid) {
				t.Errorf("the leaked server (pid %d) is still running", pid)
			}
		})
	}
}

// The root is the one place a server that will not stop can still be found, so
// the run leaves it and says so.
func TestRunKeepsTheRootOfAServerItCannotStop(t *testing.T) {
	t.Parallel()

	ignoring := writeExecutable(t, "#!/bin/sh\nexit 0\n")
	output, root := runLeakingChild(t, "survivor", []string{"PATH=" + filepath.Dir(ignoring) + ":" + os.Getenv("PATH")})
	t.Cleanup(func() {
		if root != "" && filepath.IsAbs(root) {
			_ = os.RemoveAll(root)
		}
	})

	if want := "1 did not stop after kill-server, so the suite root was kept"; !strings.Contains(output, want) {
		t.Errorf("the child's output lacks %q:\n%s", want, output)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the suite root of a server that kept answering is gone: %v", err)
	}
}

// runSuiteChild runs this binary as a suite whose one test leaves mode behind,
// with decoy selectors in its environment and extra after the rest of it. It
// returns what the child printed, which must name no value, what the helper
// recorded, and how the child exited.
func runSuiteChild(t *testing.T, mode string, extra []string) (output, record string, exit int) {
	t.Helper()

	const marker = "private-leak-marker"
	recordFile := filepath.Join(shortDirectory(t), "record")
	command := exec.Command(os.Args[0], "-test.run=^TestSuiteChildHelper$", "-test.count=1")
	command.Env = append(os.Environ(),
		"LIBTMUX_SUITE_CHILD="+mode,
		"LIBTMUX_SUITE_CHILD_RECORD="+recordFile,
		"LIBTMUX_SOCKET="+marker,
		"LIBTMUX_SOCKET_PATH=/"+marker+"/path",
	)
	command.Env = append(command.Env, extra...)
	combined, err := command.CombinedOutput()
	if exited, ok := errors.AsType[*exec.ExitError](err); ok {
		exit = exited.ExitCode()
	} else if err != nil {
		t.Fatalf("run the child suite: %v", err)
	}
	if strings.Contains(string(combined), marker) {
		t.Errorf("the child's output carries a selector value:\n%s", combined)
	}
	if tmpdir := os.Getenv("TMPDIR"); tmpdir != "" && strings.Contains(string(combined), tmpdir) {
		t.Errorf("the child's output carries a path:\n%s", combined)
	}
	recorded, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatalf("the helper test never ran:\n%s", combined)
	}
	// A run that failed to stop the server it was meant to must not leave it
	// behind: the pid is the one the child's own server reported.
	if pid, err := strconv.Atoi(string(recorded)); err == nil {
		t.Cleanup(func() {
			if process, err := os.FindProcess(pid); err == nil && processAlive(pid) {
				_ = process.Kill()
			}
		})
	}
	return string(combined), string(recorded), exit
}

// runLeakingChild is runSuiteChild for a child that must fail for what it left.
func runLeakingChild(t *testing.T, mode string, extra []string) (output, record string) {
	t.Helper()

	output, record, exit := runSuiteChild(t, mode, extra)
	if exit != 1 {
		t.Fatalf("the child exited %d, want status 1 for the leak:\n%s", exit, output)
	}
	return output, record
}
