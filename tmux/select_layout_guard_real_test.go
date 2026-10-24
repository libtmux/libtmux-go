package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// tmux 3.3a exits the server when select-layout cannot parse its operand, and
// when it is given -o. Literal select-layout is refused there before it is
// sent, in every spelling tmux accepts and inside a command list.
//
//libtmux:real-tmux
func TestRawSelectLayoutIsRefusedWhereItKillsTheServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.String() != "3.3a" {
		t.Skipf("select-layout only kills tmux 3.3a, not %s", version)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "anchor"})
	if err != nil {
		t.Fatal(err)
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := window.ID().String()
	if result, err := server.Cmd(ctx, "split-window", "-d", "-t", target); err != nil || result.ExitCode != 0 {
		t.Fatalf("split-window = (%+v, %v)", result, err)
	}
	result, err := server.Cmd(ctx, "display-message", "-p", "-t", target, "#{window_layout}")
	if err != nil || len(result.Stdout) != 1 {
		t.Fatalf("window_layout = (%+v, %v)", result, err)
	}
	classic := result.Stdout[0]

	alive := func(label string) {
		t.Helper()
		if result, err := server.Cmd(ctx, "list-sessions"); err != nil || result.ExitCode != 0 {
			t.Fatalf("%s: the server is gone: (%+v, %v)", label, result, err)
		}
	}
	for _, verb := range []string{"select-layout", "selectl", "select-l", "select-layo"} {
		for _, operands := range [][]string{
			{"garbage"},
			{""},
			{"main-"},
			{"-o"},
			{"-o", "tiled"},
			{"--", "-o"},
			{"{}"},
		} {
			args := append([]string{verb, "-t", target}, operands...)
			if operands[0] == "-o" && len(operands) == 1 {
				args = []string{verb, "-o", "-t", target}
			}
			_, err := server.Cmd(ctx, args...)
			if !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Errorf("%q = %v, want a refusal", args, err)
			}
			alive("alone")
			list := append([]string{"list-windows", ";"}, args...)
			if _, err := server.Cmd(ctx, list...); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Errorf("%q = %v, want a refusal", list, err)
			}
			alive("in a list")
		}
	}
	for _, args := range [][]string{
		{"select-layout", "-t", target, "tiled"},
		{"selectl", "-t", target, "even-h"},
		{"select-layout", "-t", target, classic},
		{"select-layout", "-t", target, "-n"},
		{"select-layout", "-t", target, "-E"},
		{"select-layout", "-t", target},
	} {
		if result, err := server.Cmd(ctx, args...); err != nil || result.ExitCode != 0 {
			t.Errorf("%q = (%+v, %v), want it sent and accepted", args, result, err)
		}
	}
	alive("supported forms")
}
