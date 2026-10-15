package mcp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestRunShellCommandIgnoresInheritedShellState(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup string
	}{
		{name: "printf function", setup: "printf() { :; }"},
		{name: "printf alias", setup: "alias printf=:"},
		{name: "shell options", setup: "set -eu"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			request := tmux.NewSessionRequest{Name: "work"}
			target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
				FixedShell: true, InitialSession: &request,
			})
			panes, err := target.Panes(ctx)
			if err != nil || len(panes) != 1 {
				t.Fatalf("Panes() = (%v, %v), want one pane", panes, err)
			}
			tmuxtest.WaitForShellReady(ctx, t, panes[0])
			if err := panes[0].SendKeys(ctx, tmux.SendKeysRequest{
				Command: &test.setup, Literal: true,
			}); err != nil {
				t.Fatal(err)
			}

			result := callInputTool(ctx, t, inputTestClient(ctx, t, target, nil),
				"run_shell_command", map[string]any{
					"pane_id": panes[0].ID().String(), "command": "exit 23", "timeout": 5,
				})
			if result.IsError {
				t.Fatalf("run_shell_command refusal = %q", callToolResultText(result))
			}
			if got := structuredMap(t, result)["exit_status"]; got != float64(23) {
				t.Fatalf("exit_status = %#v, want 23", got)
			}
		})
	}
}

// A shell with no line editor draws its prompt after the echo of keys typed
// before it was ready, so the wrapper starts on a row holding the prompt.
// The output must not carry it.
//
//libtmux:real-tmux
func TestRunShellCommandLeavesOutAPromptDrawnAfterTheKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The pane reports the waiting sh as its command, so the keys go in at
	// once, and the interactive shell prints its prompt after their echo.
	request := tmux.NewSessionRequest{
		Name:    "late",
		Command: "sleep 0.5; exec env ENV= PS1='" + tmuxtest.ShellPrompt + "' /bin/sh -i",
	}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	panes, err := target.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%v, %v), want one pane", panes, err)
	}

	result := callInputTool(ctx, t, inputTestClient(ctx, t, target, nil),
		"run_shell_command", map[string]any{
			"pane_id": panes[0].ID().String(), "command": "echo hi", "timeout": 5,
		})
	if result.IsError {
		t.Fatalf("run_shell_command refusal = %q", callToolResultText(result))
	}
	if got := structuredMap(t, result)["output"]; !reflect.DeepEqual(got, []any{"hi"}) {
		t.Fatalf("output = %#v, want [hi]; screen:\n%s", got,
			strings.Join(tmuxtest.Screen(ctx, t, panes[0]), "\n"))
	}
}
