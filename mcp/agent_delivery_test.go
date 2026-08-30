package mcp_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestPasteTextDeliversWhatSendKeysWouldEat(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: pasting\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// A word tmux claims as a key, in the middle of text that is not keys.
	result := call(ctx, t, session, "paste_text", map[string]any{
		"paneId": pane, "text": "echo one Escape two",
	}, nil)
	if result.IsError {
		t.Fatalf("paste_text: %#v", result.Content)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
		if strings.Contains(strings.Join(shown.Lines, "\n"), "Escape two") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the pasted text did not arrive intact")
}

//libtmux:real-tmux
func TestKillingTheLastPaneSaysTheWindowWentWithIt(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: ending\nwindows:\n"+
			"  - window_name: first\n    panes:\n      - {}\n      - {}\n"+
			"  - window_name: second\n    panes:\n      - {}\n")

	var listed struct {
		Panes []struct {
			ID       string `json:"id"`
			Window   string `json:"window"`
			WindowID string `json:"windowId"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)

	var lone string
	var loneWindow string
	for _, pane := range listed.Panes {
		if pane.Window == "second" {
			lone = pane.ID
			loneWindow = pane.WindowID
		}
	}
	if lone == "" {
		t.Fatalf("the workspace has no lone pane: %+v", listed.Panes)
	}

	var killed struct {
		Killed      string `json:"killed"`
		WindowEnded bool   `json:"windowEnded"`
	}
	result := call(ctx, t, session, "kill_pane", map[string]any{"paneId": lone}, &killed)
	if result.IsError {
		t.Fatalf("kill_pane: %#v", result.Content)
	}
	if !killed.WindowEnded {
		t.Errorf("killing window %s's only pane did not report the window ending", loneWindow)
	}
}

//libtmux:real-tmux
func TestABatchIsCheckedWholeBeforeAnythingRuns(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: batched\nwindows:\n  - panes:\n      - {}\n")
	before := paneIDs(ctx, t, session)

	// A split that would have worked, followed by a kill the tier forbids.
	result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{"paneId": before[0]}},
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "batched"}},
		},
	}, nil)
	if !result.IsError {
		t.Fatal("a mutating batch containing a kill was allowed")
	}
	if after := paneIDs(ctx, t, session); len(after) != len(before) {
		t.Errorf("the refused batch still ran its first call: %d panes became %d",
			len(before), len(after))
	}

	unknown := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{{"tool": "no_such_tool"}},
	}, nil)
	if !unknown.IsError {
		t.Error("a batch naming a tool this server does not serve was accepted")
	}

	// The same calls at the destructive tier are allowed.
	allowed := call(ctx, t, session, "call_destructive_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "batched"}},
		},
	}, nil)
	if allowed.IsError {
		t.Errorf("a destructive batch was refused: %#v", allowed.Content)
	}
}

//libtmux:real-tmux
func TestRunCommandReportsWhatTheCommandPrinted(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: running\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var ran struct {
		ExitStatus *int     `json:"exitStatus"`
		TimedOut   bool     `json:"timedOut"`
		Output     []string `json:"output"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo PRINTED-BY-THE-COMMAND", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command: %#v", result.Content)
	}
	if ran.TimedOut || ran.ExitStatus == nil || *ran.ExitStatus != 0 {
		t.Fatalf("run_command reported %+v", ran)
	}
	joined := strings.Join(ran.Output, "\n")
	if !strings.Contains(joined, "PRINTED-BY-THE-COMMAND") {
		t.Errorf("the command's output was not reported: %q", joined)
	}
	// The wrapper this tool runs the command inside is its own bookkeeping and
	// has no business in the answer.
	if strings.Contains(joined, "wait-for") {
		t.Errorf("the wrapper's echo reached the caller: %q", joined)
	}

	var failed struct {
		ExitStatus *int `json:"exitStatus"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "exit 3", "timeoutSeconds": 30,
	}, &failed)
	if failed.ExitStatus == nil || *failed.ExitStatus != 3 {
		t.Errorf("exit status = %v, want 3", failed.ExitStatus)
	}
}

// Buffer tools normalize names into the libtmux-mcp- namespace.
//
//libtmux:real-tmux
func TestBuffersStayInTheMCPNamespace(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: buffers\nwindows:\n  - panes:\n      - {}\n")

	// An unprefixed buffer stands in for a person's copy.
	if err := target.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "a private copy",
		Name: new("someones-own"),
	}); err != nil {
		t.Fatalf("stage a foreign buffer: %v", err)
	}

	var staged struct {
		Name  string `json:"name"`
		Bytes int    `json:"bytes"`
	}
	result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "staged by the test", "name": "notes",
	}, &staged)
	if result.IsError {
		t.Fatalf("load_buffer: %#v", result.Content)
	}

	var read struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "show_buffer", map[string]any{"name": staged.Name}, &read)
	if strings.Join(read.Lines, "\n") != "staged by the test" {
		t.Errorf("read back %q", strings.Join(read.Lines, "\n"))
	}

	// The foreign buffer is not reachable because its name is prefixed on lookup.
	foreign := call(ctx, t, session, "show_buffer", map[string]any{"name": "someones-own"}, nil)
	if !foreign.IsError {
		t.Error("an unprefixed buffer was readable")
	}

	if result := call(ctx, t, session, "delete_buffer", map[string]any{
		"name": staged.Name,
	}, nil); result.IsError {
		t.Errorf("delete_buffer: %#v", result.Content)
	}
}

//libtmux:real-tmux
func TestBufferNamesPreserveEmbeddedWhitespace(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: spaced-buffer\nwindows:\n  - panes:\n      - {}\n")

	const name = "release notes"
	var staged struct {
		Name string `json:"name"`
	}
	result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "payload", "name": name,
	}, &staged)
	if result.IsError {
		t.Fatalf("load_buffer(%q): %#v", name, result.Content)
	}
	if !strings.HasSuffix(staged.Name, name) {
		t.Errorf("load_buffer(%q) returned name %q", name, staged.Name)
	}

	var read struct {
		Lines []string `json:"lines"`
	}
	result = call(ctx, t, session, "show_buffer", map[string]any{
		"name": staged.Name,
	}, &read)
	if result.IsError {
		t.Fatalf("show_buffer(%q): %#v", staged.Name, result.Content)
	}
	if got := strings.Join(read.Lines, "\n"); got != "payload" {
		t.Errorf("show_buffer(%q) = %q, want payload", staged.Name, got)
	}
}

// tmux 3.7 normalizes displayed buffer names differently from lookup. A
// successful load must return a name that later buffer operations can use.
//
//libtmux:real-tmux
func TestALoadedBufferIsReachableByTheNameItAnswersWith(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: handles\nwindows:\n  - panes:\n      - {}\n")

	for _, name := range []string{`a\b`, `\`, `back\\slash`} {
		var staged struct {
			Name string `json:"name"`
		}
		result := call(ctx, t, session, "load_buffer", map[string]any{
			"text": "payload", "name": name,
		}, &staged)
		if !result.IsError {
			// Accepting it is only sound if the handle works, so hold it to that.
			read := call(ctx, t, session, "show_buffer", map[string]any{
				"name": staged.Name,
			}, nil)
			if read.IsError {
				t.Errorf("load_buffer(%q) answered with %q, which show_buffer cannot "+
					"reach: %#v", name, staged.Name, read.Content)
			}
			call(ctx, t, session, "delete_buffer", map[string]any{"name": staged.Name}, nil)
			continue
		}
		if text, ok := result.Content[0].(*sdk.TextContent); ok &&
			!strings.Contains(text.Text, `\`) {
			t.Errorf("load_buffer(%q) was refused without naming the character: %s",
				name, text.Text)
		}
	}
}

//libtmux:real-tmux
func TestSendKeysBatchDrivesAProgramThatReadsKeys(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: keys\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready")
	received := filepath.Join(directory, "received")
	send(ctx, t, session, pane,
		"printf ready > "+strconv.Quote(ready)+
			"; IFS= read -r first; printf '%s\\n' \"$first\" > "+strconv.Quote(received))
	waitForDeliveryPath(ctx, t, ready)

	var sent struct {
		Sent   int    `json:"sent"`
		PaneID string `json:"paneId"`
	}
	result := call(ctx, t, session, "send_keys_batch", map[string]any{
		"paneId": pane,
		"keys":   []string{"e", "c", "h", "o", "Space", "K", "E", "Y", "S", "Enter"},
	}, &sent)
	if result.IsError {
		t.Fatalf("send_keys_batch: %#v", result.Content)
	}
	if sent.Sent != 10 {
		t.Errorf("sent %d keys, want 10", sent.Sent)
	}

	waitForDeliveryPath(ctx, t, received)
	written, err := os.ReadFile(received)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "echo KEYS\n" {
		t.Errorf("the key reader got %q, want %q", written, "echo KEYS\n")
	}
}

func waitForDeliveryPath(ctx context.Context, t *testing.T, path string) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s was not published: %v", filepath.Base(path), ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

//libtmux:real-tmux
func TestADetachedCommandReturnsBeforeItFinishes(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: detached\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started struct {
		JobID    string `json:"jobId"`
		Detached bool   `json:"detached"`
	}
	sent := time.Now()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "sleep 5; echo FINISHED", "detach": true,
	}, &started)
	if result.IsError {
		t.Fatalf("run_command: %#v", result.Content)
	}
	if !started.Detached || started.JobID == "" {
		t.Fatalf("a detached run reported %+v", started)
	}
	if elapsed := time.Since(sent); elapsed > 4*time.Second {
		t.Errorf("detaching took %v, so it waited for the command", elapsed)
	}

	// Unfinished, and honest about it rather than reporting a status.
	var checked struct {
		Finished   bool `json:"finished"`
		ExitStatus *int `json:"exitStatus"`
	}
	call(ctx, t, session, "get_job", map[string]any{"jobId": started.JobID}, &checked)
	if checked.Finished || checked.ExitStatus != nil {
		t.Errorf("a command still running reported %+v", checked)
	}

	var collected struct {
		Finished   bool     `json:"finished"`
		ExitStatus *int     `json:"exitStatus"`
		Output     []string `json:"output"`
	}
	call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &collected)
	if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 {
		t.Fatalf("collecting the job reported %+v", collected)
	}
	if !slices.ContainsFunc(collected.Output, func(line string) bool {
		return strings.Contains(line, "FINISHED")
	}) {
		t.Errorf("the collected output was %q, want the command's own", collected.Output)
	}
}

//libtmux:real-tmux
func TestAJobAnswersTheSameWayTwice(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: onceonly\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started struct {
		JobID string `json:"jobId"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "true", "detach": true,
	}, &started)

	type collection struct {
		Finished   bool     `json:"finished"`
		ExitStatus *int     `json:"exitStatus"`
		Output     []string `json:"output"`
	}
	var first collection
	call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &first)
	if !first.Finished || first.ExitStatus == nil {
		t.Fatalf("the job never finished: %+v", first)
	}

	var again collection
	result := call(ctx, t, session, "get_job", map[string]any{"jobId": started.JobID}, &again)
	if result.IsError {
		t.Fatalf("asking about a collected job failed: %#v", result.Content)
	}
	if !again.Finished || again.ExitStatus == nil || *again.ExitStatus != *first.ExitStatus {
		t.Errorf("the second reading said %+v, want the same as %+v", again, first)
	}
}

//libtmux:real-tmux
func TestUnknownJobHandlesDoNotRevealAnotherScope(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: handles\nwindows:\n  - panes:\n      - {}\n")

	foreign := "libtmux-mcp-opaque-foreign-handle"
	result := call(ctx, t, session, "get_job", map[string]any{"jobId": foreign}, nil)
	if !result.IsError {
		t.Fatal("an unknown handle was accepted")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || !strings.Contains(text.Text, "not a job owned by this MCP session") {
		t.Errorf("the refusal disclosed or misclassified the handle: %#v", result.Content)
	}
}

//libtmux:real-tmux
func TestAPaneKeepsItsIdentityWhenItMoves(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: moving\nwindows:\n"+
		"  - window_name: from\n    panes:\n      - {}\n      - {}\n"+
		"  - window_name: to\n    panes:\n      - {}\n")

	var before listing
	call(ctx, t, session, "list_panes", map[string]any{}, &before)
	mover := before.Panes[1]
	var destination string
	for _, pane := range before.Panes {
		if pane.WindowID != mover.WindowID {
			destination = pane.WindowID
			break
		}
	}
	if destination == "" {
		t.Fatal("the workspace did not build a second window")
	}

	var joined struct {
		PaneID    string `json:"paneId"`
		WindowID  string `json:"windowId"`
		BrokenOut bool   `json:"brokenOut"`
	}
	result := call(ctx, t, session, "move_pane", map[string]any{
		"paneId": mover.ID, "toWindowId": destination,
	}, &joined)
	if result.IsError {
		t.Fatalf("move_pane: %#v", result.Content)
	}
	if joined.PaneID != mover.ID {
		t.Errorf("the pane came back as %s, want its own id %s", joined.PaneID, mover.ID)
	}
	if joined.WindowID != destination {
		t.Errorf("the pane landed in %s, want %s", joined.WindowID, destination)
	}

	var broken struct {
		PaneID    string `json:"paneId"`
		WindowID  string `json:"windowId"`
		BrokenOut bool   `json:"brokenOut"`
	}
	call(ctx, t, session, "move_pane", map[string]any{"paneId": mover.ID}, &broken)
	if !broken.BrokenOut {
		t.Error("breaking a pane out did not report that it did")
	}
	if broken.WindowID == destination || broken.WindowID == "" {
		t.Errorf("the broken-out pane is in %q, want a window of its own", broken.WindowID)
	}
}

// ranCommand is what the run_command and get_job assertions below read back.
type ranCommand struct {
	ExitStatus *int     `json:"exitStatus"`
	TimedOut   bool     `json:"timedOut"`
	Output     []string `json:"output"`
	JobID      string   `json:"jobId"`
}

//libtmux:real-tmux
func TestRunCommandKeepsATabInItsCommand(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: tabbed\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// The shell counts the bytes it received rather than printing them. A
	// terminal renders a tab by moving to the next tab stop, and whether the
	// captured row holds a tab or the spaces it painted differs by tmux
	// version; how many bytes reached the shell does not.
	var ran ranCommand
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "printf '%s' 'a\tb' | wc -c", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command failed: %#v", result.Content)
	}
	if ran.TimedOut {
		t.Fatal("a command carrying a tab wedged the pane")
	}
	if len(ran.Output) != 1 || strings.TrimSpace(ran.Output[0]) != "3" {
		t.Errorf("output = %q, want one line reading 3, so the shell was given "+
			"a, a tab, and b rather than whatever a completion inserted",
			ran.Output)
	}
}

//libtmux:real-tmux
func TestDetachedRunCommandReportsOnlyTheCommandsOutput(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: detached\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var started ranCommand
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo ONLY-THIS", "detach": true,
	}, &started)
	if started.JobID == "" {
		t.Fatal("a detached run returned no handle")
	}

	// Collect only once the shell has certainly drawn its prompt into the row
	// below the output, which is the case a detached run is for. Reading the
	// closing mark's row inclusively returns that prompt as command output.
	time.Sleep(time.Second)

	var collected ranCommand
	result := call(ctx, t, session, "get_job", map[string]any{
		"jobId": started.JobID, "timeoutSeconds": 30,
	}, &collected)
	if result.IsError {
		t.Fatalf("get_job failed: %#v", result.Content)
	}
	if len(collected.Output) != 1 || collected.Output[0] != "ONLY-THIS" {
		t.Errorf("output = %q, want exactly [ONLY-THIS] with no prompt", collected.Output)
	}
}

//libtmux:real-tmux
func TestRunCommandRejoinsOutputTheTerminalWrapped(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: wrapped\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)
	call(ctx, t, session, "resize_window", map[string]any{
		"sessionName": "wrapped", "width": 40, "height": 24,
	}, nil)

	var ran ranCommand
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "printf 'W%.0s' $(seq 1 100); echo", "timeoutSeconds": 30,
	}, &ran)
	if result.IsError {
		t.Fatalf("run_command failed: %#v", result.Content)
	}
	if len(ran.Output) != 1 || len(ran.Output[0]) != 100 {
		t.Errorf("output = %d lines of %v, want one line of 100 characters",
			len(ran.Output), lineLengths(ran.Output))
	}
}

// lineLengths reports each line's length, for a failure message that says how
// the output was split rather than printing a hundred identical characters.
func lineLengths(lines []string) []int {
	lengths := make([]int, 0, len(lines))
	for _, line := range lines {
		lengths = append(lengths, len(line))
	}
	return lengths
}

//libtmux:real-tmux
func TestRunCommandRefusesAPaneWhoseShellHasExited(t *testing.T) {
	// A fixed shell leaves on the first "exit"; an inherited one may still be
	// starting when the keys arrive, which is time this test spends waiting.
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: exited\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	call(ctx, t, session, "set_option", map[string]any{
		"scope": "pane", "paneId": pane, "name": "remain-on-exit", "value": "on",
	}, nil)
	call(ctx, t, session, "send_keys", map[string]any{"paneId": pane, "command": "exit"}, nil)
	waitForDeadPane(ctx, t, session, pane)

	began := time.Now()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo into-the-void", "timeoutSeconds": 20,
	}, nil)
	if !result.IsError {
		t.Fatal("running a command in a pane whose shell has exited was accepted")
	}
	if waited := time.Since(began); waited > 10*time.Second {
		t.Errorf("refusing a dead pane took %s, so it waited rather than checked", waited)
	}
}

// waitForDeadPane blocks until tmux reports the pane's process has exited,
// which happens a moment after the shell is told to leave.
func waitForDeadPane(
	ctx context.Context, t *testing.T, session *sdk.ClientSession, pane string,
) {
	t.Helper()
	for range 100 {
		var info struct {
			Dead bool `json:"dead"`
		}
		call(ctx, t, session, "get_pane_info", map[string]any{"paneId": pane}, &info)
		if info.Dead {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pane's shell never exited")
}

//libtmux:real-tmux
func TestRunCommandSaysWhyThereIsNoOutput(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session,
		"session_name: busy\nwindows:\n  - panes:\n      - shell: sleep 300\n")
	pane := firstPane(ctx, t, session)

	var ran struct {
		OutputUnavailable string `json:"outputUnavailable"`
		TimedOut          bool   `json:"timedOut"`
		Running           string `json:"running"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo never-runs", "timeoutSeconds": 3,
	}, &ran)

	if ran.OutputUnavailable == "" {
		t.Fatal("a pane that never ran the command reported no reason")
	}
	if strings.Contains(ran.OutputUnavailable, "/tmp/") ||
		strings.Contains(ran.OutputUnavailable, "no such file") {
		t.Errorf("the reason is this server's own bookkeeping, not the cause: %q",
			ran.OutputUnavailable)
	}
	if !strings.Contains(ran.OutputUnavailable, "never ran") {
		t.Errorf("the reason does not say the command never ran: %q",
			ran.OutputUnavailable)
	}
	if !strings.Contains(ran.OutputUnavailable, "respawn_pane") {
		t.Errorf("the reason names no way out: %q", ran.OutputUnavailable)
	}
}

//libtmux:real-tmux
func TestEveryDeliveryRefusesAPaneWithNoProcess(t *testing.T) {
	session, _, ctx := connect(t)
	pane := deadPaneHeldOpen(ctx, t, session, "corpse")

	if result := call(ctx, t, session, "load_buffer", map[string]any{
		"text": "x", "name": "corpse",
	}, nil); result.IsError {
		t.Fatalf("load_buffer: %#v", result.Content)
	}

	for _, delivery := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"send_keys", map[string]any{"paneId": pane, "command": "echo LEAK"}},
		{"send_keys_batch", map[string]any{"paneId": pane, "keys": []string{"a"}}},
		{"paste_text", map[string]any{"paneId": pane, "text": "LEAK"}},
		{"paste_buffer", map[string]any{"paneId": pane, "name": "corpse"}},
		{"run_command", map[string]any{"paneId": pane, "command": "echo LEAK", "timeoutSeconds": 5}},
	} {
		t.Run(delivery.tool, func(t *testing.T) {
			result := call(ctx, t, session, delivery.tool, delivery.arguments, nil)
			if !result.IsError {
				t.Fatalf("%s reported success against a pane with no process", delivery.tool)
			}
			said := resultText(result)
			if !strings.Contains(said, "respawn_pane") {
				t.Errorf("the refusal does not name the way out: %q", said)
			}
			if strings.Contains(said, "exited 1") {
				t.Errorf("the refusal is tmux's exit code rather than a reason: %q", said)
			}
		})
	}
}

//libtmux:real-tmux
func TestADeadPaneIsStillReadableAndRestartable(t *testing.T) {
	session, _, ctx := connect(t)
	pane := deadPaneHeldOpen(ctx, t, session, "readable")

	for _, allowed := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"enter_copy_mode", map[string]any{"paneId": pane}},
		{"exit_copy_mode", map[string]any{"paneId": pane}},
		{"clear_pane", map[string]any{"paneId": pane}},
		{"respawn_pane", map[string]any{"paneId": pane, "command": "sleep 60"}},
	} {
		if result := call(ctx, t, session, allowed.tool, allowed.arguments, nil); result.IsError {
			t.Errorf("%s was refused on a dead pane: %#v", allowed.tool, result.Content)
		}
	}
}

// deadPaneHeldOpen makes a pane whose process has exited and which tmux keeps.
func deadPaneHeldOpen(
	ctx context.Context,
	t *testing.T,
	session *sdk.ClientSession,
	name string,
) string {
	t.Helper()
	var made struct {
		PaneID   string `json:"paneId"`
		WindowID string `json:"windowId"`
	}
	if result := call(ctx, t, session, "create_session", map[string]any{
		"name": name, "command": "sleep 300",
	}, nil); result.IsError {
		t.Fatalf("create_session: %#v", result.Content)
	}
	// A second window, so the pane going does not take the session with it
	// while the assertions are still running.
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": name, "name": "held", "command": "sleep 300",
	}, &made); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}
	if result := call(ctx, t, session, "set_option", map[string]any{
		"name": "remain-on-exit", "value": "on",
		"scope": "window", "windowId": made.WindowID,
	}, nil); result.IsError {
		t.Fatalf("set_option: %#v", result.Content)
	}
	if result := call(ctx, t, session, "respawn_pane", map[string]any{
		"paneId": made.PaneID, "command": "true", "kill": true,
	}, nil); result.IsError {
		t.Fatalf("respawn_pane: %#v", result.Content)
	}
	// The pane stays; what this waits for is the process ending.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var listed struct {
			Panes []struct {
				ID     string `json:"id"`
				Status struct {
					Dead bool `json:"dead"`
				} `json:"status"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{
			"sessionName": name, "detail": "full",
		}, &listed)
		for _, pane := range listed.Panes {
			if pane.ID == made.PaneID && pane.Status.Dead {
				return made.PaneID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reported its command as finished", made.PaneID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
