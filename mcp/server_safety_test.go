package mcp_test

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestIsCallerHasThreeAnswers(t *testing.T) {
	// The pane ids a fresh server hands out are predictable, which is what lets
	// the environment name one before the server that reports it exists.
	const ownPane = "%0"

	read := func(t *testing.T) []struct {
		ID       string `json:"id"`
		IsCaller *bool  `json:"isCaller"`
	} {
		t.Helper()
		session, _, ctx := connect(t)
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: identity\nwindows:\n  - panes:\n      - {}\n",
		}, nil)
		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		if len(listed.Panes) == 0 {
			t.Fatal("no panes to identify")
		}
		return listed.Panes
	}

	t.Run("outside tmux the question has no answer", func(t *testing.T) {
		t.Setenv("TMUX_PANE", "")
		t.Setenv("TMUX", "")
		for _, pane := range read(t) {
			if pane.IsCaller != nil {
				t.Errorf("pane %s answered %v while the server is not inside tmux",
					pane.ID, *pane.IsCaller)
			}
		}
	})

	t.Run("on this socket exactly one pane is the caller", func(t *testing.T) {
		// The socket is not known until the server exists, and the server reads
		// the environment when it is first asked rather than when it starts, so
		// naming the socket the harness is about to use is enough.
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx := connect(t)
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: identity\nwindows:\n  - panes:\n      - {}\n",
		}, nil)
		// The socket is only knowable once tmux is running, and the server works
		// out its own pane the first time something asks rather than at startup,
		// so naming it here still reaches the listing below.
		socket, err := target.Cmd(
			ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		if len(listed.Panes) == 0 {
			t.Fatal("no panes to identify")
		}
		for _, pane := range listed.Panes {
			if pane.IsCaller == nil {
				t.Fatalf("pane %s gave no answer while the server is inside tmux", pane.ID)
			}
			if want := pane.ID == ownPane; *pane.IsCaller != want {
				t.Errorf("pane %s answered %v, want %v", pane.ID, *pane.IsCaller, want)
			}
		}
	})

	t.Run("the same pane id on another socket is somebody else", func(t *testing.T) {
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", filepath.Join(t.TempDir(), "other.sock")+",1234,0")
		for _, pane := range read(t) {
			if pane.IsCaller == nil || *pane.IsCaller {
				t.Errorf("pane %s claimed the caller across sockets", pane.ID)
			}
		}
	})
}

//libtmux:real-tmux
func TestInstructionsNameTheCallersPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("TMUX", "/tmp/example.sock,1234,0")
	session, _, _ := connect(t)

	instructions := session.InitializeResult().Instructions
	if !strings.Contains(instructions, "%7") {
		t.Errorf("instructions do not name the caller's pane: %q", instructions)
	}
	if !strings.Contains(instructions, "isCaller") {
		t.Errorf("instructions do not explain the pane flag: %q", instructions)
	}

	// The instructions decide whether a client reaches for this server at all
	// and which of two overlapping tools it picks, so they carry more than the
	// caller's pane.
	for _, expected := range []string{
		"DO NOT USE THIS FOR", // the word "window" is a browser tab elsewhere
		"WAIT, DO NOT POLL",   // the loop every tool description warns about
		"run_command",         // and where each waiting case should go
		"wait_for_text",
		"wait_for_channel",
		"snapshot_pane", // prefer one response when both state and content are needed
	} {
		if !strings.Contains(instructions, expected) {
			t.Errorf("instructions omit %q", expected)
		}
	}
}

//libtmux:real-tmux
func TestSafetyLevelWithholdsTools(t *testing.T) {
	for _, testCase := range []struct {
		level    string
		kill     bool
		split    bool
		listOnly bool
	}{
		{"readonly", false, false, true},
		{"", false, true, false},
		{"destructive", true, true, false},
		// A level nobody meant to write is the one case where guessing wrong
		// hands out more than was asked for, so it reads as the lowest.
		{"readonyl", false, false, true},
	} {
		t.Run("level "+testCase.level, func(t *testing.T) {
			t.Setenv("LIBTMUX_SAFETY", testCase.level)
			session, _, ctx := connect(t)
			listed, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			advertised := map[string]bool{}
			for _, tool := range listed.Tools {
				advertised[tool.Name] = true
			}
			if advertised["kill_session"] != testCase.kill {
				t.Errorf("kill_session advertised = %v, want %v",
					advertised["kill_session"], testCase.kill)
			}
			if advertised["split_window"] != testCase.split {
				t.Errorf("split_window advertised = %v, want %v",
					advertised["split_window"], testCase.split)
			}
			if !advertised["list_panes"] {
				t.Error("list_panes is withheld at every level")
			}

			// A batch must not reach a tool the level hid. The mutating batch
			// is itself withheld at the readonly level, which is correct and
			// leaves nothing to test there.
			if !testCase.kill && !testCase.listOnly {
				var out struct {
					Completed int `json:"completed"`
				}
				call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
					"calls": []map[string]any{{
						"tool":      "kill_session",
						"arguments": map[string]any{"sessionName": "anything"},
					}},
				}, &out)
				if out.Completed != 0 {
					t.Error("a batch reached a tool the safety level withheld")
				}
			}
		})
	}
}

func TestReadOnlyWithholdsArbitraryFormatExpansion(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "display_message" {
			t.Fatal("readonly advertised display_message, whose #() formats run shell commands")
		}
	}
}

//libtmux:real-tmux
func TestEveryToolAgreesWithEverySafetyLevel(t *testing.T) {
	everything := func(t *testing.T) map[string]bool {
		t.Helper()
		t.Setenv("LIBTMUX_SAFETY", "destructive")
		session, _, ctx := connect(t)
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range listed.Tools {
			names[tool.Name] = true
		}
		return names
	}(t)

	for _, level := range []string{"readonly", "mutating"} {
		t.Run(level, func(t *testing.T) {
			t.Setenv("LIBTMUX_SAFETY", level)
			session, _, ctx := connect(t)
			listed, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			offered := map[string]bool{}
			for _, tool := range listed.Tools {
				offered[tool.Name] = true
			}
			withheld := 0
			for name := range everything {
				if offered[name] {
					continue
				}
				withheld++
				// Unknown rather than refused: a withheld tool is not a tool
				// this server serves, and saying anything else tells a client
				// the bound is a preference.
				_, err := session.CallTool(ctx, &sdk.CallToolParams{
					Name: name, Arguments: map[string]any{},
				})
				if err == nil {
					t.Errorf("%s is withheld at %s and answered anyway", name, level)
					continue
				}
				if !strings.Contains(err.Error(), "unknown tool") {
					t.Errorf("%s at %s was refused as %v, want unknown", name, level, err)
				}
			}
			if withheld == 0 {
				t.Errorf("%s withholds nothing, which is not what it is for", level)
			}

			// The other half of the cross-product: a tool a level does offer
			// has to work there. Listing one and then failing it because of
			// the level is the same lie as withholding one and dispatching it,
			// told the other way round.
			// A tool whose contract is to wait blocks until its own deadline
			// when it is called with nothing, which measures the clock rather
			// than the level and spends the budget every later call needs.
			waits := map[string]bool{"wait_for_text": true, "wait_for_channel": true}
			for _, name := range slices.Sorted(maps.Keys(offered)) {
				if waits[name] {
					continue
				}
				// Bounded on its own, so a tool that blocks fails itself
				// rather than every tool after it.
				callCtx, endCall := context.WithTimeout(ctx, 10*time.Second)
				result, err := session.CallTool(callCtx, &sdk.CallToolParams{
					Name: name, Arguments: map[string]any{},
				})
				endCall()
				if err != nil {
					t.Errorf("%s is offered at %s and the call failed: %v", name, level, err)
					continue
				}
				// Most refuse for want of a required argument, which is the
				// tool answering rather than the level withholding it. What
				// must not appear is the safety refusal.
				if result.IsError && strings.Contains(resultText(result), "safety") {
					t.Errorf("%s is offered at %s and refused for safety: %s",
						name, level, resultText(result))
				}
			}
			t.Logf("%s: %d withheld and unreachable, %d offered and none refused "+
				"for safety (%d that only wait were not called)",
				level, withheld, len(offered)-len(waits), len(waits))
		})
	}
}
