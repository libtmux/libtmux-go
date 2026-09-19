package tmuxtest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func ExampleScriptedTmux() {
	// A real test passes its own *testing.T. This example asserts rather than
	// only compiling, so it needs one that works outside a test: a bare T
	// records the helper's cleanup without ever running it, which costs the
	// one temporary directory the scripted executable is written into.
	t := &testing.T{}
	binary := tmuxtest.ScriptedTmux(t,
		tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
	)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	version, err := server.Version(context.Background())
	if err != nil {
		fmt.Println("version:", err)
		return
	}
	fmt.Println(version)
	// Output: 3.7
}

// Matching is per argument, so a needle cannot match across a boundary, and
// text a shell would otherwise treat specially is carried verbatim.
func TestScriptedTmuxMatchesWholeArgumentsAndQuotesOutput(t *testing.T) {
	t.Parallel()

	binary := tmuxtest.ScriptedTmux(t,
		tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
		tmuxtest.ScriptedCommand{
			Contains: []string{"display-message"},
			Stdout:   "it's a $HOME * [quoted] value\n",
		},
	)
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	// "display" alone must not match "display-message", and a target holding
	// a space must not let a needle match across two arguments.
	result, err := server.Cmd(context.Background(), "has-session", "-t", "display message")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Errorf("a needle matched across an argument boundary: %#v", result)
	}

	result, err = server.Cmd(context.Background(), "display-message", "-p", "#{pane_id}")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if want := []string{"it's a $HOME * [quoted] value"}; !slices.Equal(result.Stdout, want) {
		t.Errorf("Stdout = %#v, want %#v", result.Stdout, want)
	}
}
