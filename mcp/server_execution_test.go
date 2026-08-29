package mcp_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestSendKeysReachesThePane(t *testing.T) {
	session, _, ctx := connect(t)

	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: keys-test\nwindows:\n  - window_name: only\n    panes:\n      - true\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) != 1 {
		t.Fatalf("list_panes = %d panes, want 1", len(listed.Panes))
	}
	paneID := listed.Panes[0].ID

	call(ctx, t, session, "send_keys", map[string]any{
		"paneId":  paneID,
		"command": "printf 'mcp ready\\n'",
	}, nil)

	// Poll against the context's own budget rather than a second, shorter
	// deadline: a shell can take longer to start when the suite is running many
	// tmux servers at once, and a private timeout turns that into a flake.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var captured struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": paneID}, &captured)
		if slices.Contains(captured.Lines, "mcp ready") {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane never showed the command output: %#v", captured.Lines)
		case <-ticker.C:
		}
	}
}

//libtmux:real-tmux
func TestRunCommandReportsExitStatusWithoutReadingTheScreen(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: runner\nwindows:\n  - panes:\n      - {}\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to run a command in")
	}
	pane := listed.Panes[0].ID

	for _, testCase := range []struct {
		name    string
		command string
		want    int
	}{
		{"success", "sleep 1; printf 'DONE\\n'", 0},
		{"failure after printing the marker", "printf 'DONE\\n'; sleep 1; exit 7", 7},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var out struct {
				ExitStatus *int `json:"exitStatus"`
				TimedOut   bool `json:"timedOut"`
			}
			result := call(ctx, t, session, "run_command", map[string]any{
				"paneId": pane, "command": testCase.command, "timeoutSeconds": 30,
			}, &out)
			if result.IsError {
				t.Fatalf("run_command failed: %#v", result.Content)
			}
			if out.TimedOut {
				t.Fatal("run_command timed out")
			}
			if out.ExitStatus == nil || *out.ExitStatus != testCase.want {
				t.Errorf("exit status = %v, want %d", out.ExitStatus, testCase.want)
			}
		})
	}
}

//libtmux:real-tmux
func TestRunCommandReportsATimeoutRatherThanFailing(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: slow\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes")
	}

	var out struct {
		ExitStatus *int `json:"exitStatus"`
		TimedOut   bool `json:"timedOut"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": listed.Panes[0].ID, "command": "sleep 30", "timeoutSeconds": 1,
	}, &out)
	if result.IsError {
		t.Fatalf("a timeout must not be a tool error: %#v", result.Content)
	}
	if !out.TimedOut {
		t.Fatal("a command that outlasted the wait was not reported as timed out")
	}
	// Zero is what a successful command reports, so a timeout must not carry a
	// status at all rather than one that reads as success.
	if out.ExitStatus != nil {
		t.Errorf("a timeout reported exit status %d", *out.ExitStatus)
	}
}

//libtmux:real-tmux
func TestWaitForTextReadsTheStreamRatherThanTheScreen(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: watcher\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'sleep 1; printf \"SERVICE READY\\n\"; sleep 60'\n",
	}, nil)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to watch")
	}

	var found struct {
		Found          bool     `json:"found"`
		Outcome        string   `json:"outcome"`
		Matched        string   `json:"matched"`
		MatchedAtEntry bool     `json:"matchedAtEntry"`
		ElapsedSeconds float64  `json:"elapsedSeconds"`
		Lines          []string `json:"lines"`
	}
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         listed.Panes[0].ID,
		"patterns":       []string{"SERVICE READY"},
		"timeoutSeconds": 30,
	}, &found)
	if result.IsError {
		t.Fatalf("wait_for_text failed: %#v", result.Content)
	}
	if !found.Found {
		t.Fatalf("the announcement was not seen; pane wrote %q", strings.Join(found.Lines, "\n"))
	}
	if found.Outcome != "matched" || found.Matched != "SERVICE READY" {
		t.Errorf("outcome = %q matched = %q, want matched/SERVICE READY",
			found.Outcome, found.Matched)
	}
	// How long the call took proves nothing about the wait. The program's own
	// delay starts when the workspace is built, not when the wait begins, so on
	// a machine that took a moment to build it the announcement lands within
	// milliseconds of the wait starting — or before it, which the reply reports
	// as a match at entry. What is worth asserting is that the wait reports its
	// own elapsed time and that a pattern which never appears still times out,
	// which is the half below.
	if found.ElapsedSeconds < 0 {
		t.Errorf("elapsedSeconds = %v, want the time the wait actually took", found.ElapsedSeconds)
	}

	var missing struct {
		Found   bool   `json:"found"`
		Outcome string `json:"outcome"`
	}
	result = call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         listed.Panes[0].ID,
		"patterns":       []string{"NEVER PRINTED"},
		"timeoutSeconds": 1,
	}, &missing)
	if result.IsError {
		t.Fatalf("a wait that timed out must not be a tool error: %#v", result.Content)
	}
	if missing.Found || missing.Outcome != "timeout" {
		t.Errorf("text that was never written reported found=%v outcome=%q",
			missing.Found, missing.Outcome)
	}
}

// TestWaitForChannelIsReleasedBySignalling covers coordinating with anything
// that signals a tmux channel, which run_command cannot: that one waits on a
// command it started itself.
//
//libtmux:real-tmux
func TestWaitForChannelIsReleasedBySignalling(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: channels\nwindows:\n  - panes:\n      - {}\n",
	}, nil)

	waited := make(chan bool, 1)
	go func() {
		var out struct {
			Signalled bool `json:"signalled"`
		}
		call(ctx, t, session, "wait_for_channel", map[string]any{
			"channel": "ready for work", "timeoutSeconds": 30,
		}, &out)
		waited <- out.Signalled
	}()

	// The waiter is released by a signal, not by its own deadline.
	deadline := time.After(20 * time.Second)
	for {
		call(ctx, t, session, "signal_channel", map[string]any{
			"channel": "ready for work",
		}, nil)
		select {
		case signalled := <-waited:
			if !signalled {
				t.Fatal("the wait ended without being signalled")
			}
			return
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("the wait was never released")
		}
	}
}

// TestChannelNamesAreValidated covers a name tmux would read as something
// other than a channel.
//
//libtmux:real-tmux
func TestChannelNamesAreValidated(t *testing.T) {
	session, _, ctx := connect(t)
	for _, channel := range []string{"", "-L"} {
		if result := call(ctx, t, session, "signal_channel", map[string]any{
			"channel": channel,
		}, nil); !result.IsError {
			t.Errorf("channel %q was accepted", channel)
		}
	}
}

//libtmux:real-tmux
func TestBatchCanCreateTheFirstSessionThenReadIt(t *testing.T) {
	session, _, ctx := connect(t)
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var batch struct {
		Completed int `json:"completed"`
		Results   []struct {
			Tool   string         `json:"tool"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		} `json:"results"`
	}
	result := call(callCtx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "create_session", "arguments": map[string]any{"name": "first-in-batch"}},
			{"tool": "list_sessions"},
		},
	}, &batch)
	if result.IsError {
		t.Fatalf("first-session batch failed: %s", resultText(result))
	}
	if batch.Completed != 2 || len(batch.Results) != 2 {
		t.Fatalf("batch = %#v, want two completed calls", batch)
	}
	for _, item := range batch.Results {
		if item.Error != "" {
			t.Fatalf("%s failed after first creation: %s", item.Tool, item.Error)
		}
	}
	sessions, ok := batch.Results[1].Result["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("list_sessions result = %#v, want the created session", batch.Results[1].Result)
	}
}

//libtmux:real-tmux
func TestBatchesRunInOrderAndRefuseMutations(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: batched\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var readOnly struct {
		Results []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
		Completed int `json:"completed"`
	}
	result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "find_pane_by_position", "arguments": map[string]any{
				"paneId": panes[0], "direction": "below",
			}},
		},
	}, &readOnly)
	if result.IsError {
		t.Fatalf("read-only batch: %#v", result.Content)
	}
	if readOnly.Completed != 2 {
		t.Fatalf("completed %d of 2 calls: %+v", readOnly.Completed, readOnly.Results)
	}

	// A mutating call refuses the whole batch, and nothing runs.
	before := len(paneIDs(ctx, t, session))
	if result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "list_panes"},
			{"tool": "split_window", "arguments": map[string]any{"paneId": panes[0]}},
		},
	}, nil); !result.IsError {
		t.Error("a read-only batch accepted a mutating call")
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a refused batch changed the panes: %d then %d", before, after)
	}

	// The mutating batch runs the same call.
	var mutating struct {
		Completed int `json:"completed"`
	}
	if result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{"paneId": panes[0]}},
			{"tool": "list_panes"},
		},
	}, &mutating); result.IsError {
		t.Fatalf("mutating batch: %#v", result.Content)
	}
	if mutating.Completed != 2 {
		t.Errorf("mutating batch completed %d of 2", mutating.Completed)
	}
	if after := len(paneIDs(ctx, t, session)); after != before+1 {
		t.Errorf("panes = %d, want %d", after, before+1)
	}
}

//libtmux:real-tmux
func TestWaitForTextSeesWhatThePaneAlreadyShowed(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: already\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'printf \"ANNOUNCED-ONCE\\n\"; sleep 120'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}

	// Let the announcement land before the wait starts, which is the case a
	// stream-only wait misses.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var shown struct {
			Lines []string `json:"lines"`
		}
		call(ctx, t, session, "capture_pane", map[string]any{"paneId": panes[0]}, &shown)
		if strings.Contains(strings.Join(shown.Lines, "\n"), "ANNOUNCED-ONCE") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	var found struct {
		Found          bool `json:"found"`
		MatchedAtEntry bool `json:"matchedAtEntry"`
	}
	result := call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": panes[0], "patterns": []string{"ANNOUNCED-ONCE"}, "timeoutSeconds": 5,
	}, &found)
	if result.IsError {
		t.Fatalf("wait_for_text: %#v", result.Content)
	}
	if !found.Found {
		t.Fatal("text already on the pane was not found, so the announcement was missed")
	}
	if !found.MatchedAtEntry {
		t.Error("a match that was already on the screen was not reported as one")
	}

	// sinceEntry is the other reading of the same question: whether the pane
	// says it again, rather than whether it has said it.
	var again struct {
		Found   bool   `json:"found"`
		Outcome string `json:"outcome"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId":         panes[0],
		"patterns":       []string{"ANNOUNCED-ONCE"},
		"sinceEntry":     true,
		"timeoutSeconds": 1,
	}, &again)
	if again.Found || again.Outcome != "timeout" {
		t.Errorf("sinceEntry matched what was already shown: found=%v outcome=%q",
			again.Found, again.Outcome)
	}
}

//libtmux:real-tmux
func TestABatchToldToContinueRunsTheCallsAfterAFailure(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: continuing\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	// A failure between two calls that would each succeed on their own.
	calls := []map[string]any{
		{"tool": "get_pane_info", "arguments": map[string]any{"paneId": pane}},
		{"tool": "get_pane_info", "arguments": map[string]any{"paneId": "%9000"}},
		{"tool": "list_panes", "arguments": map[string]any{}},
	}
	type report struct {
		Results []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
		Completed int      `json:"completed"`
		Failed    int      `json:"failed"`
		Skipped   []string `json:"skipped"`
	}

	var stopped report
	call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls}, &stopped)
	if stopped.Completed != 1 || len(stopped.Results) != 2 {
		t.Errorf("stopping ran %d and reported %d results, want 1 and 2",
			stopped.Completed, len(stopped.Results))
	}
	if !slices.Equal(stopped.Skipped, []string{"list_panes"}) {
		t.Errorf("stopping skipped %q, want the call after the failure", stopped.Skipped)
	}

	var continued report
	call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls, "onError": "continue"}, &continued)
	if continued.Completed != 2 {
		t.Errorf("continuing ran %d of the two that work", continued.Completed)
	}
	if continued.Failed != 1 {
		t.Errorf("continuing reported %d failures, want 1", continued.Failed)
	}
	if len(continued.Results) != 3 {
		t.Fatalf("continuing reported %d results, want one per call", len(continued.Results))
	}
	if len(continued.Skipped) != 0 {
		t.Errorf("continuing skipped %q, and it skips nothing", continued.Skipped)
	}
	if continued.Results[1].Error == "" {
		t.Error("the failing call is not reported as failed")
	}
	if continued.Results[2].Tool != "list_panes" || continued.Results[2].Error != "" {
		t.Errorf("the call after the failure did not run: %+v", continued.Results[2])
	}

	// An unknown value is refused rather than read as the default.
	if result := call(ctx, t, session, "call_readonly_tools_batch",
		map[string]any{"calls": calls[:1], "onError": "carry-on"}, nil); !result.IsError {
		t.Error("a batch took an onError it does not have")
	}
}

//libtmux:real-tmux
func TestBatchArgumentsAreCheckedLikeAnyCall(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: strict\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	before := len(panes)

	var batch struct {
		Results []struct {
			Error string `json:"error"`
		} `json:"results"`
		Completed int `json:"completed"`
	}
	call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "split_window", "arguments": map[string]any{
				"paneId": panes[0], "directon": "right",
			}},
		},
	}, &batch)
	if batch.Completed != 0 {
		t.Errorf("a misspelled field was accepted: completed=%d", batch.Completed)
	}
	if len(batch.Results) == 0 || batch.Results[0].Error == "" {
		t.Fatalf("a misspelled field reported no error: %+v", batch.Results)
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a rejected call still split a pane: %d then %d", before, after)
	}

	// The closed sets are part of the same schema, so a batch has to hold a
	// value to them too, and by the schema rather than by whatever the handler
	// happens to tolerate.
	for _, arguments := range []map[string]any{
		{"paneId": panes[0], "direction": "sideways"},
		// Accepted by the handler, which folds case, and outside the set the
		// schema publishes.
		{"paneId": panes[0], "direction": "RIGHT"},
	} {
		batch.Completed, batch.Results = 0, nil
		call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
			"calls": []map[string]any{{"tool": "split_window", "arguments": arguments}},
		}, &batch)
		if batch.Completed != 0 {
			t.Errorf("a batch took direction %q", arguments["direction"])
		}
		if len(batch.Results) == 0 || !strings.Contains(batch.Results[0].Error, "enum") {
			t.Errorf("direction %q refused for the wrong reason: %+v",
				arguments["direction"], batch.Results)
		}
	}
	if after := len(paneIDs(ctx, t, session)); after != before {
		t.Errorf("a rejected direction still split a pane: %d then %d", before, after)
	}
}

//libtmux:real-tmux
func TestACommandThatPrintedBlankLinesSaysSo(t *testing.T) {
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: blanks\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	for _, printed := range []struct {
		command string
		want    []string
	}{
		{"echo", []string{""}},
		{`printf '\n\n'`, []string{"", ""}},
		{`printf '\n\nx\n'`, []string{"", "", "x"}},
		// Still distinguishable from a command that printed nothing at all.
		{"true", nil},
	} {
		t.Run(printed.command, func(t *testing.T) {
			var reported struct {
				Output     []string `json:"output"`
				ExitStatus *int     `json:"exitStatus"`
			}
			call(ctx, t, session, "run_command", map[string]any{
				"paneId": pane, "command": printed.command, "timeoutSeconds": 15,
			}, &reported)
			if reported.ExitStatus == nil || *reported.ExitStatus != 0 {
				t.Fatalf("exit status = %v, want 0", reported.ExitStatus)
			}
			if !slices.Equal(reported.Output, printed.want) {
				t.Errorf("output = %q, want %q", reported.Output, printed.want)
			}
		})
	}
}

//libtmux:real-tmux
func TestATimedOutRunLeavesNothingOfItsOwnInThePane(t *testing.T) {
	// A plain POSIX shell, which is what the wrapper is written for, and a
	// prompt that is one character rather than whoever's shell the suite
	// inherited.
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})
	workspace(ctx, t, session, "session_name: outlived\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var timedOut struct {
		TimedOut bool `json:"timedOut"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "sleep 3", "timeoutSeconds": 1,
	}, &timedOut)
	if !timedOut.TimedOut {
		t.Fatal("the command did not outlast its wait")
	}

	// Wait for the pane to go quiet, which is after the command has finished
	// and the wrapper behind it has run its own lines. Waiting for the command
	// to leave instead returns before those lines are written.
	var quiet struct {
		Outcome string `json:"outcome"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": pane, "idleSeconds": 2, "timeoutSeconds": 30,
	}, &quiet)
	if quiet.Outcome != "idle" {
		t.Fatalf("the pane settled as %q, not idle", quiet.Outcome)
	}

	var shown struct {
		Lines []string `json:"lines"`
	}
	call(ctx, t, session, "capture_pane", map[string]any{"paneId": pane}, &shown)
	whole := strings.Join(shown.Lines, "\n")
	for _, leaked := range []string{"cannot create", "Directory nonexistent", "status"} {
		if strings.Contains(whole, leaked) {
			t.Errorf("the pane holds %q after a timed-out run:\n%s", leaked, whole)
		}
	}

	// And the next run reads its own output rather than the leftovers.
	var next struct {
		Output []string `json:"output"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": "echo after-the-timeout",
	}, &next)
	if len(next.Output) != 1 || next.Output[0] != "after-the-timeout" {
		t.Errorf("the next run read %q", next.Output)
	}
}

//libtmux:real-tmux
func TestSendKeysRecoversAPaneLeftBusy(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: wedged\nwindows:\n  - panes:\n      - {}\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var timedOut struct {
		TimedOut bool `json:"timedOut"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "sleep 120", "timeoutSeconds": 1,
	}, &timedOut)
	if !timedOut.TimedOut {
		t.Fatal("the command did not outlast its wait")
	}

	// tmux reads the key name, so this interrupts rather than typing letters.
	call(ctx, t, session, "send_keys", map[string]any{
		"paneId": panes[0], "command": "C-c",
	}, nil)

	var recovered struct {
		ExitStatus *int `json:"exitStatus"`
		TimedOut   bool `json:"timedOut"`
	}
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "printf 'back'", "timeoutSeconds": 20,
	}, &recovered)
	if result.IsError {
		t.Fatalf("run_command after recovery: %#v", result.Content)
	}
	if recovered.TimedOut || recovered.ExitStatus == nil || *recovered.ExitStatus != 0 {
		t.Fatalf("the pane did not recover: timedOut=%v status=%v",
			recovered.TimedOut, recovered.ExitStatus)
	}
}

//libtmux:real-tmux
func TestATimeoutNamesWhatThePaneWasRunning(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: busy\nwindows:\n" +
			"  - panes:\n      - shell: sh -c 'while read line; do :; done'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)

	var out struct {
		TimedOut bool   `json:"timedOut"`
		Running  string `json:"running"`
	}
	call(ctx, t, session, "run_command", map[string]any{
		"paneId": panes[0], "command": "printf 'never runs'", "timeoutSeconds": 2,
	}, &out)
	if !out.TimedOut {
		t.Fatal("a command typed into a busy pane did not time out")
	}
	if out.Running == "" {
		t.Fatal("a timeout did not say what the pane was running")
	}
}

//libtmux:real-tmux
func TestATimeoutIsLoggedToAClientThatAsked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "logged"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	logged := make(chan *sdk.LoggingMessageParams, 8)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "listener"}, &sdk.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, request *sdk.LoggingMessageRequest) {
			logged <- request.Params
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.SetLoggingLevel(ctx, &sdk.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatalf("set logging level: %v", err)
	}

	var panes struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &panes)
	if len(panes.Panes) == 0 {
		t.Fatal("no panes")
	}

	// A wait that ends with nothing says why in the log, not in the result.
	call(ctx, t, session, "wait_for_text", map[string]any{
		"paneId": panes.Panes[0].ID, "patterns": []string{"NEVER-WRITTEN"}, "timeoutSeconds": 1,
	}, nil)

	select {
	case message := <-logged:
		if message.Logger != "libtmux" {
			t.Errorf("logger = %q, want libtmux", message.Logger)
		}
		if !strings.Contains(fmt.Sprint(message.Data), "wait_for_text timed out") {
			t.Errorf("log says %v, want the timeout it describes", message.Data)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a client that asked for logs heard nothing about a timeout")
	}
}

//libtmux:real-tmux
func TestABatchNamesTheCallsItSkipped(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: batched\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var batch struct {
		Completed int      `json:"completed"`
		Skipped   []string `json:"skipped"`
		Results   []struct {
			Tool  string `json:"tool"`
			Error string `json:"error"`
		} `json:"results"`
	}
	result := call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": pane, "title": "ran"}},
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": "%999", "title": "fails"}},
			{"tool": "set_pane_title", "arguments": map[string]any{"paneId": pane, "title": "skipped"}},
			{"tool": "select_pane", "arguments": map[string]any{"paneId": pane}},
		},
	}, &batch)
	if result.IsError {
		t.Fatalf("the batch itself failed: %#v", result.Content)
	}
	if batch.Completed != 1 || len(batch.Results) != 2 {
		t.Fatalf("completed = %d with %d results, want 1 and 2",
			batch.Completed, len(batch.Results))
	}
	want := []string{"set_pane_title", "select_pane"}
	if !slices.Equal(batch.Skipped, want) {
		t.Errorf("skipped = %q, want %q", batch.Skipped, want)
	}
}

//libtmux:real-tmux
func TestABatchSaysWhyItCannotBatchABatch(t *testing.T) {
	session, _, ctx := connect(t)

	result := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "call_readonly_tools_batch", "arguments": map[string]any{}},
		},
	}, nil)
	if !result.IsError {
		t.Fatal("a batch inside a batch was accepted")
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("the refusal carried %T", result.Content[0])
	}
	if strings.Contains(text.Text, "is not a tool this server serves") {
		t.Errorf("the refusal denies a tool that is advertised: %s", text.Text)
	}
	if !strings.Contains(text.Text, "cannot be called from inside a batch") {
		t.Errorf("the refusal does not say why: %s", text.Text)
	}
}
