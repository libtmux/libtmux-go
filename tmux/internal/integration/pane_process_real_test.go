//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestPaneRespawnPasteAndPipeAgainstRealTmux(t *testing.T) {
	baseServer := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 1)
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         baseServer.SocketPath(),
		ConfigFile:         baseServer.ConfigFile(),
		ProcessEnvironment: baseServer.ProcessEnvironment(),
		Unsupported:        tmux.DegradeUnsupported,
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, beta, _, betaWindow := newLinkedProcessWindow(ctx, t, server, "pane-process")
	if err := beta.SetEnvironment(ctx, "RESPAWN_CONTEXT", "beta", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	pane := relatedPanes(t, betaWindow)[0]
	workDirectory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	respawnOutput := filepath.Join(workDirectory, "pane-respawn-context")
	respawnCommand := "printf '%s|%s' \"$RESPAWN_CONTEXT\" \"$REQUEST_VALUE\" > " +
		strconv.Quote(respawnOutput) + "; sleep 30"
	respawned, err := pane.Respawn(ctx, tmux.RespawnRequest{
		Command:     &respawnCommand,
		Environment: map[string]string{"REQUEST_VALUE": "request"},
		Kill:        true,
	})
	if err != nil {
		t.Fatalf("Respawn() error = %v", err)
	}
	if respawned.ID() != pane.ID() {
		t.Fatalf("Respawn() PaneID = %s, want stable %s", respawned.ID(), pane.ID())
	}
	if got := waitForProcessFile(ctx, t, respawnOutput); got != "beta|request" {
		t.Fatalf("pane respawn context = %q, want beta|request", got)
	}

	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	pane = relatedPanes(t, betaWindow)[0]
	readyPath := filepath.Join(workDirectory, "paste-ready")
	pasteOutput := filepath.Join(workDirectory, "paste-byte")
	readerCommand := "printf ready > " + strconv.Quote(readyPath) +
		"; stty raw; od -An -tx1 -N1 > " + strconv.Quote(pasteOutput) + "; sleep 30"
	if _, err := pane.Respawn(ctx, tmux.RespawnRequest{
		Command: &readerCommand,
		Kill:    true,
	}); err != nil {
		t.Fatalf("Respawn(reader) error = %v", err)
	}
	if got := waitForProcessFile(ctx, t, readyPath); got != "ready" {
		t.Fatalf("reader ready marker = %q", got)
	}
	nulPath := filepath.Join(workDirectory, "nul-buffer")
	if err := os.WriteFile(nulPath, []byte{0}, 0o600); err != nil {
		t.Fatal(err)
	}
	bufferName := "raw-nul"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{
		Path: nulPath,
		Name: &bufferName,
	}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version37, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}

	// NoVis needs tmux 3.7. This server omits what it cannot carry, so below
	// the floor the paste runs without the flag and says so.
	warnings = nil
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName:  &bufferName,
		DeleteAfter: true,
		NoVis:       true,
	}); err != nil {
		t.Fatalf("PasteBuffer() error = %v", err)
	}
	wantWarning := !version.AtLeast(version37)
	if (len(warnings) == 1) != wantWarning {
		t.Fatalf("PasteBuffer() warnings = %#v, want warning %t on %s", warnings, wantWarning, version)
	}
	if wantWarning && (warnings[0].Kind != tmux.WarningUnsupportedFeature ||
		warnings[0].Subcommand != "paste-buffer" || warnings[0].Feature != "no_vis") {
		t.Fatalf("PasteBuffer() warning = %#v", warnings[0])
	}
	if got := strings.TrimSpace(waitForProcessFile(ctx, t, pasteOutput)); got != "00" {
		t.Fatalf("raw pasted byte = %q, want 00", got)
	}
	if _, err := server.ShowBuffer(ctx, &bufferName); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("ShowBuffer(deleted) error = %v, want ErrCommand", err)
	}

	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	pane = relatedPanes(t, betaWindow)[0]
	pipeOutput := filepath.Join(workDirectory, "pipe-context")
	pipeCommand := "echo \"#{session_name}\" > " + strconv.Quote(pipeOutput) + "; sleep 30"
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	if got := strings.TrimSpace(waitForProcessFile(ctx, t, pipeOutput)); got != "pane-process-beta" {
		t.Fatalf("pipe format context = %q, want pane-process-beta", got)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{
		Command: &pipeCommand,
		Toggle:  true,
	}); err != nil {
		t.Fatalf("Pipe(toggle) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe(reopen) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	emptyCommand := ""
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &emptyCommand}); err != nil {
		t.Fatalf("Pipe(empty) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &pipeCommand}); err != nil {
		t.Fatalf("Pipe(second reopen) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{}); err != nil {
		t.Fatalf("Pipe(nil) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	failingCommand := "exit 42"
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: &failingCommand}); err != nil {
		t.Fatalf("Pipe(failing child) error = %v, want installation success", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
	if err := pane.Pipe(ctx, tmux.PipePaneRequest{}); err != nil {
		t.Fatalf("Pipe(clean up failed child) error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")
}

func waitForPanePipeState(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	pane tmux.Pane,
	want string,
) {
	t.Helper()
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := server.Cmd(ctx, "display-message", "-p", "-t", target, "#{pane_pipe}")
		if err == nil && result.ExitCode == 0 && len(result.Stderr) == 0 &&
			len(result.Stdout) == 1 && result.Stdout[0] == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane_pipe did not become %s before deadline: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}

// TestPipeTreatsLeadingDashCommandAsPositionalAgainstRealTmux proves the
// pipe-pane "--" guard against a live tmux: without it, tmux's own parser
// reads a Command beginning with "-" as pipe-pane's own flags and refuses it
// before opening a pipe, which is what the raw control below reproduces.
// pipe-pane exposes no readback of the exact command text, so this checks
// that the guarded call actually opens a pipe (#{pane_pipe} becomes "1")
// rather than being refused at the same parse step.
func TestPipeTreatsLeadingDashCommandAsPositionalAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	panes, err := session.SearchPanes(ctx, nil)
	if err != nil || len(panes) != 1 {
		t.Fatalf("SearchPanes() = (%#v, %v), want one pane", panes, err)
	}
	pane := panes[0]
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()

	const command = "-not-a-flag"
	raw, err := server.Cmd(ctx, "pipe-pane", "-t", target, command)
	if err != nil || raw.ExitCode == 0 {
		t.Fatalf("unguarded pipe-pane = (%#v, %v), want a parse failure", raw, err)
	}
	waitForPanePipeState(ctx, t, server, pane, "0")

	if err := pane.Pipe(ctx, tmux.PipePaneRequest{Command: tmux.Ptr(command)}); err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	waitForPanePipeState(ctx, t, server, pane, "1")
}

//libtmux:real-tmux
func TestManagedProgramPreservesArguments(t *testing.T) {
	ctx := t.Context()
	base := tmuxtest.NewServer(ctx, t)
	var launches atomic.Int32
	var badTrace atomic.Bool
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: base.SocketPath(), ConfigFile: base.ConfigFile(), ProcessEnvironment: base.ProcessEnvironment(),
		CommandObserver: func(trace tmux.CommandTrace) {
			if trace.Subcommand == "respawn-pane" {
				launches.Add(1)
				if trace.ExitCode != 0 || trace.Err != nil {
					badTrace.Store(true)
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	session := sessions[0]
	capability, err := server.Capability(ctx, "program-argv")
	if err != nil || !capability.Supported || capability.Since.String() != tmux.MinimumSupportedVersion {
		t.Fatalf("program capability = %#v, %v", capability, err)
	}
	lifetime, err := server.Capability(ctx, "program-lifetime")
	if err != nil {
		t.Fatal(err)
	}
	before, err := server.Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		executable string
		arguments  []string
	}{{"", nil}, {"sh", []string{"nul\x00argument"}}} {
		if _, err := session.StartProgram(ctx, invalid.executable, invalid.arguments, tmux.RunOptions{}); err == nil {
			t.Fatal("StartProgram accepted invalid argv")
		}
	}
	if !lifetime.Supported {
		if _, err := session.StartProgram(ctx, "cat", nil, tmux.RunOptions{}); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("StartProgram below lifetime floor = %v", err)
		}
	}
	after, err := server.Windows(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("refused requests changed windows: %v, %v", after, err)
	}
	if !lifetime.Supported {
		legacy, err := session.Run(ctx, "printf legacy", tmux.RunOptions{})
		if err != nil || !slices.Equal(legacy.Lines, []string{"legacy"}) {
			t.Fatalf("legacy Run at core floor = %#v, %v", legacy, err)
		}
		return
	}
	directory := t.TempDir()
	output := filepath.Join(directory, "argv")
	cwdOutput := filepath.Join(directory, "cwd")
	values := []string{"a b", "", "$HOME", "$(exit 99)", "-n", ";", "tail;", "\\;", "line\nbreak", "雪"}
	arguments := append([]string{"-c", `printf '%s\0' "$@" > "$ARGV_FILE"; pwd > "$CWD_FILE"; printf 'last-line'; exit 7`, "argv"}, values...)
	result, err := session.RunProgram(ctx, "sh", arguments, tmux.RunOptions{
		StartDirectory: directory, Environment: map[string]string{"ARGV_FILE": output, "CWD_FILE": cwdOutput},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 7 || strings.Join(result.Lines, "\n") != "last-line" {
		t.Fatalf("result = %#v", result)
	}
	bytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(bytes), "\x00"), "\x00"); !slices.Equal(got, values) {
		t.Fatalf("argv = %q, want %q", got, values)
	}
	cwd, err := os.ReadFile(cwdOutput)
	if err != nil || strings.TrimSpace(string(cwd)) != directory {
		t.Fatalf("program cwd = %q, %v; want %s", cwd, err, directory)
	}
	for _, executable := range []struct{ name, output string }{
		{"-literal;$HOME", "singleton"}, {"-literal;$HOME\n", "newline"},
	} {
		if err := os.WriteFile(filepath.Join(directory, executable.name), []byte("#!/bin/sh\nprintf "+executable.output+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{directory, ""} {
			singleton, err := session.RunProgram(ctx, executable.name, nil, tmux.RunOptions{
				StartDirectory: directory, Environment: map[string]string{"PATH": path},
			})
			if err != nil || singleton.Status != 0 || !slices.Equal(singleton.Lines, []string{executable.output}) {
				t.Fatalf("singleton executable %q with PATH %q = %#v, %v", executable.name, path, singleton, err)
			}
		}
	}
	running, err := session.StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := running.Pane().SendKeys(ctx, tmux.SendKeysRequest{Command: new("literal-input"), Literal: true}); err != nil {
		t.Fatal(err)
	}
	if err := running.Pane().SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-d"}}); err != nil {
		t.Fatal(err)
	}
	finished, err := running.Wait(ctx)
	if err != nil || finished.Status != 0 {
		t.Fatalf("singleton Wait() = %#v, %v", finished, err)
	}
	if !slices.Contains(finished.Lines, "literal-input") {
		t.Fatalf("singleton output = %q", finished.Lines)
	}
	if got := launches.Load(); got != 6 || badTrace.Load() {
		t.Fatalf("program launch traces = %d, invalid outcome %v", got, badTrace.Load())
	}
}

//libtmux:real-tmux
func TestManagedProgramRefusesRespawnedProcess(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, "3.8")
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	running, err := sessions[0].StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.Pane().Respawn(ctx, tmux.RespawnRequest{Command: new("cat"), Kill: true}); err != nil {
		t.Fatal(err)
	}
	if err := running.Kill(ctx); !errors.Is(err, tmux.ErrProgramReplaced) {
		t.Fatalf("Kill after replacement = %v", err)
	}
	if _, err := running.Wait(ctx); !errors.Is(err, tmux.ErrProgramReplaced) {
		t.Fatalf("Wait after replacement = %v", err)
	}
	replacement, err := running.Pane().Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead, _ := replacement.Dead(); dead {
		t.Fatal("replacement was killed")
	}
}

//libtmux:real-tmux
func TestManagedProgramKeepReleasesWitnessAfterConcurrentWaits(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, "3.8")
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	before, err := server.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	running, err := sessions[0].StartProgram(ctx, "cat", nil, tmux.RunOptions{Keep: true})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := running.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wait = %v", err)
	}
	if err := running.Pane().SendKeys(ctx, tmux.SendKeysRequest{Command: new("retained-input"), Literal: true}); err != nil {
		t.Fatal(err)
	}
	if err := running.Pane().SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-d"}}); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		result tmux.RunResult
		err    error
	}
	completed := make(chan completion, 2)
	for range 2 {
		go func() {
			result, waitErr := running.Wait(ctx)
			completed <- completion{result, waitErr}
		}()
	}
	for range 2 {
		got := <-completed
		if got.err != nil || got.result.Status != 0 || !slices.Contains(got.result.Lines, "retained-input") {
			t.Fatalf("Wait = %#v, %v", got.result, got.err)
		}
	}
	after, err := server.Clients(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("clients after Keep = %v, %v; before %d", after, err, len(before))
	}
	pane, err := running.Pane().Refresh(ctx)
	if dead, _ := pane.Dead(); err != nil || !dead {
		t.Fatalf("kept pane = %#v, %v", pane, err)
	}
}

//libtmux:real-tmux
func TestManagedProgramCleanupPreservesAddedPane(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, "3.8")
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	running, err := sessions[0].StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	added, err := running.Pane().Split(ctx, tmux.SplitPaneRequest{Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := running.Pane().SendKeySequence(ctx, tmux.SendKeySequenceRequest{Keys: []string{"C-d"}}); err != nil {
		t.Fatal(err)
	}
	if result, err := running.Wait(ctx); err != nil || result.Status != 0 {
		t.Fatalf("Wait = %#v, %v", result, err)
	}
	if _, err := added.Refresh(ctx); err != nil {
		t.Fatalf("cleanup removed added pane: %v", err)
	}
}

//libtmux:real-tmux
func TestManagedProgramRefusesRespawnAtDispatch(t *testing.T) {
	ctx := t.Context()
	base := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, base, "3.8")
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	proxy := filepath.Join(directory, "tmux")
	targetFile := filepath.Join(directory, "target")
	doneFile := filepath.Join(directory, "respawned")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
for argument do
    case "$argument" in __libtmux_program_moved_*) protected=1;; esac
done
if [ "$protected" = 1 ] && [ ! -e "$RACE_DONE" ]; then
    IFS= read -r target < "$RACE_TARGET" || exit
    : > "$RACE_DONE"
    "$RACE_REAL" -S "$RACE_SOCKET" respawn-pane -k -t "$target" cat || exit
fi
exec "$RACE_REAL" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: proxy, SocketPath: base.SocketPath(), ConfigFile: base.ConfigFile(),
		ProcessEnvironment: append(base.ProcessEnvironment(), "RACE_REAL="+realBinary,
			"RACE_SOCKET="+base.SocketPath(), "RACE_TARGET="+targetFile, "RACE_DONE="+doneFile),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v, %v", sessions, err)
	}
	running, err := sessions[0].StartProgram(ctx, "cat", nil, tmux.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte(running.Pane().ID().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := running.Kill(ctx); !errors.Is(err, tmux.ErrProgramReplaced) {
		t.Fatalf("Kill across dispatch-boundary respawn = %v", err)
	}
	if _, err := os.Stat(doneFile); err != nil {
		t.Fatal("dispatch-boundary respawn did not execute:", err)
	}
	replacement, err := running.Pane().Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead, _ := replacement.Dead(); dead {
		t.Fatal("dispatch killed the replacement")
	}
}

func TestManagedProgramCaptureExcludesHookOutput(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, "3.8")
	borrowed := "managed-capture-borrowed"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Name: &borrowed, Data: "untouched"}); err != nil {
		t.Fatal(err)
	}
	for _, bound := range []bool{false, true} {
		t.Run(strconv.FormatBool(bound), func(t *testing.T) {
			var session tmux.Session
			if bound {
				_, connection, err := server.NewSessionConnection(ctx,
					tmux.NewSessionRequest{Name: "capture-bound"}, tmux.ConnectionOptions{Lanes: 1})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = connection.Close() }()
				session = connection.Session()
			} else {
				var err error
				session, err = server.NewSession(ctx, tmux.NewSessionRequest{Name: "capture-process"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := session.SetHook(ctx, "after-capture-pane",
				"display-message -p hook-capture-only ; set-option -g @managed_capture_hook yes"); err != nil {
				t.Fatal(err)
			}
			before, err := filepath.Glob(filepath.Join(os.TempDir(), "libtmux-go-run-*"))
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"application", "%begin 1 2 3", "%end 1 2 3", "quote'\\雪"}
			arguments := append([]string{"%s\n"}, want...)
			result, err := session.RunProgram(ctx, "printf", arguments, tmux.RunOptions{Keep: true})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(result.Lines, want) {
				t.Fatalf("captured screen = %q, want %q", result.Lines, want)
			}
			if value, present, err := server.GlobalSessionScope().RawOption(ctx, "@managed_capture_hook"); err != nil || !present || value != "yes" {
				t.Fatalf("capture hook execution = %q, %t, %v", value, present, err)
			}
			if content, err := server.ShowBuffer(ctx, &borrowed); err != nil || content != "untouched" {
				t.Fatalf("borrowed buffer = %q, %v", content, err)
			}
			buffers, err := server.ListBuffers(ctx, tmux.ListBuffersRequest{Format: new("#{buffer_name}")})
			if err != nil || len(buffers) != 1 {
				t.Fatalf("buffers after capture = %v, %v", buffers, err)
			}
			after, err := filepath.Glob(filepath.Join(os.TempDir(), "libtmux-go-run-*"))
			if err != nil || !slices.Equal(before, after) {
				t.Fatalf("capture files changed: %v -> %v, %v", before, after, err)
			}
		})
	}
}
