package mcp_test

import (
	"context"
	"io"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// waitScenario is a server whose first pane prints a line every tenth of a
// second, beside a second pane that keeps the window alive when the first is
// killed. Below tmux 3.7 wait_for_text polls the pane and from 3.7 it follows
// the control stream, so the same scenario runs on both paths in whichever
// lane installs the tmux.
type waitScenario struct {
	target  tmux.Server
	client  *sdk.ClientSession
	pane    string
	control bool
	binary  string
}

func newWaitScenario(ctx context.Context, t *testing.T, loop string) waitScenario {
	t.Helper()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	result, err := target.Cmd(ctx, "new-session", "-d", "-s", "waits", "-x", "80", "-y", "24", "--", "sh", "-c", loop)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("new-session = (%+v, %v)", result, err)
	}
	if result, err = target.Cmd(ctx, "split-window", "-d", "-t", "waits:0", "--", "sleep", "300"); err != nil || result.ExitCode != 0 {
		t.Fatalf("split-window = (%+v, %v)", result, err)
	}
	listed, err := target.Cmd(ctx, "list-panes", "-t", "waits:0", "-F", "#{pane_id}")
	if err != nil || len(listed.Stdout) != 2 {
		t.Fatalf("list-panes = (%+v, %v)", listed, err)
	}
	client, closeClient, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	version, err := target.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion(tmux.MinimumConnectionVersion)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	return waitScenario{
		target: target, client: client, pane: listed.Stdout[0],
		control: version.AtLeast(minimum), binary: binary,
	}
}

type waitReply struct {
	Outcome string   `json:"outcome"`
	Found   bool     `json:"found"`
	Lines   []string `json:"lines"`
	EndNote string   `json:"endNote"`
	Elapsed float64  `json:"elapsedSeconds"`
}

// start begins a wait in the background and returns what it will answer.
func (s waitScenario) start(ctx context.Context, t *testing.T, patterns ...string) <-chan waitReply {
	t.Helper()
	replies := make(chan waitReply, 1)
	go func() {
		arguments := map[string]any{"pane_id": s.pane, "timeout": 25}
		if len(patterns) > 0 {
			arguments["patterns"] = patterns
		}
		result, err := s.client.CallTool(ctx, &sdk.CallToolParams{Name: "wait_for_text", Arguments: arguments})
		var reply waitReply
		if err != nil {
			reply.Outcome = "call failed: " + err.Error()
		} else if err := decodeStructured(result, &reply); err != nil {
			reply.Outcome = "tool error: " + err.Error() + " " + surfaceResultText(result)
		}
		replies <- reply
	}()
	return replies
}

// attached returns once the wait is reading the pane. The control path owns a
// client of its own, which is listed; the polling path has nothing to list, so
// it is given the first reads.
func (s waitScenario) attached(ctx context.Context, t *testing.T) {
	t.Helper()
	if !s.control {
		time.Sleep(time.Second)
		return
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		listed, err := s.target.Cmd(ctx, "list-clients")
		if err != nil {
			return false, err
		}
		return len(listed.Stdout) >= 2, nil
	}); err != nil {
		t.Fatalf("the wait never attached: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
}

func (s waitScenario) answer(t *testing.T, replies <-chan waitReply, within time.Duration) waitReply {
	t.Helper()
	select {
	case reply := <-replies:
		return reply
	case <-time.After(within):
		t.Fatalf("wait_for_text did not answer within %s", within)
		return waitReply{}
	}
}

func hasLine(lines []string, text string) bool {
	return slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, text) })
}

const tickLoop = "while :; do echo tick-output; sleep 0.1; done"

// A match split across a wrapped row is found. The line is longer than the
// pane is wide, and the command that prints it is built so that its own echo
// does not contain the pattern.
//
//libtmux:real-tmux
func TestWaitForTextMatchesAcrossAWrappedRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	loop := `while :; do printf '%s\n' "$(head -c 120 /dev/zero | tr '\0' A)ZZEND"; sleep 0.2; done`
	s := newWaitScenario(ctx, t, loop)
	pattern := strings.Repeat("A", 120) + "ZZEND"
	replies := s.start(ctx, t, pattern)
	reply := s.answer(t, replies, 20*time.Second)
	if reply.Outcome != "matched" || !reply.Found {
		t.Fatalf("wait_for_text = %+v, want matched across the wrap (control path %t)", reply, s.control)
	}
	if !hasLine(reply.Lines, pattern) {
		t.Fatalf("lines = %q, want one joined line holding the pattern", reply.Lines)
	}
}

// A pane killed mid-wait ends the wait as paneDied with what it wrote first.
//
//libtmux:real-tmux
func TestWaitForTextReportsAKilledPaneAsDied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := newWaitScenario(ctx, t, tickLoop)
	replies := s.start(ctx, t, "NEVER-WRITTEN")
	s.attached(ctx, t)
	time.Sleep(time.Second)
	if result, err := s.target.Cmd(ctx, "kill-pane", "-t", s.pane); err != nil || result.ExitCode != 0 {
		t.Fatalf("kill-pane = (%+v, %v)", result, err)
	}
	reply := s.answer(t, replies, 15*time.Second)
	if reply.Outcome != "paneDied" || reply.Found {
		t.Fatalf("wait_for_text = %+v, want paneDied (control path %t)", reply, s.control)
	}
	if !hasLine(reply.Lines, "tick-output") || reply.EndNote == "" {
		t.Fatalf("paneDied carries lines %q and note %q, want the output so far and a note", reply.Lines, reply.EndNote)
	}
}

// A pane whose program exited stays in its window under remain-on-exit, and
// reading it raises no error. The wait still ends as paneDied, with the output
// the program wrote first, instead of running to its timeout.
//
//libtmux:real-tmux
func TestWaitForTextReportsAnExitedRemainedPaneAsDied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := newWaitScenario(ctx, t, "sleep 2; echo tick-output; sleep 3")
	if result, err := s.target.Cmd(ctx, "set-option", "-g", "remain-on-exit", "on"); err != nil || result.ExitCode != 0 {
		t.Fatalf("set-option = (%+v, %v)", result, err)
	}
	replies := s.start(ctx, t, "NEVER-WRITTEN")
	s.attached(ctx, t)
	reply := s.answer(t, replies, 15*time.Second)
	if reply.Outcome != "paneDied" || reply.Found {
		t.Fatalf("wait_for_text = %+v, want paneDied (control path %t)", reply, s.control)
	}
	if !hasLine(reply.Lines, "tick-output") || reply.EndNote == "" {
		t.Fatalf("paneDied carries lines %q and note %q, want the output so far and a note", reply.Lines, reply.EndNote)
	}
	// The program exits about five seconds in and the watch asks once a
	// second, so the answer lands within a couple of seconds of the exit. A
	// longer interval would miss this bound.
	if reply.Elapsed > 7 {
		t.Fatalf("the exit was noticed after %.1fs, want within 7s", reply.Elapsed)
	}
}

// A killed server answers like a dead pane, not with a transport error.
//
//libtmux:real-tmux
func TestWaitForTextReportsAKilledServerAsDied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := newWaitScenario(ctx, t, tickLoop)
	replies := s.start(ctx, t, "NEVER-WRITTEN")
	s.attached(ctx, t)
	time.Sleep(time.Second)
	_, _ = s.target.Cmd(ctx, "kill-server")
	reply := s.answer(t, replies, 15*time.Second)
	if reply.Outcome != "paneDied" || reply.Found {
		t.Fatalf("wait_for_text = %+v, want paneDied (control path %t)", reply, s.control)
	}
	if !hasLine(reply.Lines, "tick-output") {
		t.Fatalf("paneDied carries lines %q, want the output so far", reply.Lines)
	}
}

// tmux stays up after kill-server until every client has left, so a second
// control client that is stopped keeps the server alive while the wait's own
// stream ends. The answer must come from asking about the pane, and arrive
// without waiting for the stopped client.
//
//libtmux:real-tmux
func TestWaitForTextReportsDiedWhileAStoppedClientHoldsTheServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := newWaitScenario(ctx, t, "sleep 300")
	second := exec.CommandContext(ctx, s.binary, "-S", s.target.SocketPath(), "-C", "attach", "-f", "no-output", "-t", "waits")
	stdin, err := second.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	second.Stdout = io.Discard
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = second.Process.Signal(syscall.SIGCONT)
		_ = stdin.Close()
		_ = second.Process.Kill()
		_ = second.Wait()
	})
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		listed, err := s.target.Cmd(ctx, "list-clients")
		if err != nil {
			return false, err
		}
		return len(listed.Stdout) >= 1, nil
	}); err != nil {
		t.Fatalf("the second client never attached: %v", err)
	}
	replies := s.start(ctx, t, "NEVER-WRITTEN")
	s.attached(ctx, t)
	if err := second.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	_, _ = s.target.Cmd(ctx, "kill-server")
	reply := s.answer(t, replies, 10*time.Second)
	if reply.Outcome != "paneDied" || reply.Found {
		t.Fatalf("wait_for_text = %+v, want paneDied (control path %t)", reply, s.control)
	}
}

// A control stream that ends while the pane lives is a lost stream, not a dead
// pane. Only the control path has a stream to lose.
//
//libtmux:real-tmux
func TestWaitForTextReportsADroppedStreamOnALivePane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	probe := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, probe, tmux.MinimumConnectionVersion)
	s := newWaitScenario(ctx, t, tickLoop)
	replies := s.start(ctx, t, "NEVER-WRITTEN")
	s.attached(ctx, t)
	time.Sleep(time.Second)
	clients, err := s.target.Cmd(ctx, "list-clients", "-F", "#{client_name}")
	if err != nil || len(clients.Stdout) == 0 {
		t.Fatalf("list-clients = (%+v, %v)", clients, err)
	}
	for _, name := range clients.Stdout {
		if result, err := s.target.Cmd(ctx, "detach-client", "-t", name); err != nil || result.ExitCode != 0 {
			t.Fatalf("detach-client %s = (%+v, %v)", name, result, err)
		}
	}
	reply := s.answer(t, replies, 15*time.Second)
	if reply.Outcome != "streamLost" || reply.Found {
		t.Fatalf("wait_for_text = %+v, want streamLost", reply)
	}
	if !hasLine(reply.Lines, "tick-output") || reply.EndNote == "" {
		t.Fatalf("streamLost carries lines %q and note %q", reply.Lines, reply.EndNote)
	}
	alive, err := s.target.Cmd(ctx, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil || alive.ExitCode != 0 || !slices.Contains(alive.Stdout, s.pane) {
		t.Fatalf("the pane did not survive: (%+v, %v)", alive, err)
	}
}
