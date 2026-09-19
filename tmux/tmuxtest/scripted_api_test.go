package tmuxtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// A consumer has to be able to unit-test code that drives this package
// without a tmux for it to drive.
func TestScriptedTmuxAnswersWithoutARealTmux(t *testing.T) {
	t.Parallel()

	binary := tmuxtest.ScriptedTmux(t,
		tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
		tmuxtest.ScriptedCommand{
			Contains: []string{"kill-pane"},
			Stderr:   "can't find pane: %7\n",
			ExitCode: 1,
		},
		tmuxtest.ScriptedCommand{Contains: []string{"list-sessions"}, Stdout: "$5\n"},
	)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ctx := context.Background()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version.String() != "3.7" {
		t.Errorf("Version() = %s, want 3.7", version)
	}

	alive, err := server.IsAlive(ctx)
	if err != nil {
		t.Fatalf("IsAlive() error = %v", err)
	}
	if !alive {
		t.Error("IsAlive() = false, want the scripted listing to answer")
	}

	// The point of scripting a failure is that a caller can assert on how
	// their own code classifies it.
	result, err := server.Cmd(ctx, "kill-pane", "-t", "%7")
	if err != nil {
		t.Fatalf("Cmd() error = %v, want a completed failure", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}

// An unmatched invocation has to say so rather than look like success.
func TestScriptedTmuxRefusesWhatItWasNotGiven(t *testing.T) {
	t.Parallel()

	binary := tmuxtest.ScriptedTmux(t,
		tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
	)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	err = server.CheckAlive(context.Background())
	if !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("CheckAlive() error = %v, want ErrCommand", err)
	}
}
