package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	tmux "github.com/libtmux/libtmux-go"
)

func TestNewServerAcceptsCommandRunner(t *testing.T) {
	t.Parallel()

	type runnerContextKey struct{}
	contextKey := runnerContextKey{}
	ctx := context.WithValue(context.Background(), contextKey, "runner-context")
	environment := []string{"LANG=C"}
	source := tmux.CommandResult{
		Command:   []string{"fake-tmux", "display-message"},
		Stdout:    []string{"output"},
		RawStdout: []byte("output\n"),
		Stderr:    []string{"diagnostic"},
		ExitCode:  7,
	}
	var request tmux.CommandRequest
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:             "fake-tmux",
		SocketName:         "runner-socket",
		ProcessEnvironment: environment,
		Runner: tmux.CommandRunnerFunc(func(gotContext context.Context, got tmux.CommandRequest) (tmux.CommandResult, error) {
			if gotContext.Value(contextKey) != "runner-context" {
				t.Fatal("runner did not receive the command context")
			}
			request = got
			return source, nil
		}),
	})

	result, err := server.Cmd(ctx, "display-message", "-p", "hello")
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := []string{"-Lrunner-socket", "display-message", "-p", "hello"}
	if request.Binary != "fake-tmux" || !slices.Equal(request.Arguments, wantArguments) {
		t.Fatalf("runner request = %#v, want binary and arguments %#v", request, wantArguments)
	}
	if !slices.Equal(request.Environment, environment) {
		t.Fatalf("runner environment = %#v, want %#v", request.Environment, environment)
	}
	if result.ExitCode != 7 || !bytes.Equal(result.RawStdout, []byte("output\n")) {
		t.Fatalf("Cmd() = %#v, want runner result", result)
	}

	request.Environment[0] = "LANG=changed"
	source.Command[0] = "changed"
	source.Stdout[0] = "changed"
	source.RawStdout[0] = 'X'
	source.Stderr[0] = "changed"
	if got := server.ProcessEnvironment(); !slices.Equal(got, environment) {
		t.Fatalf("runner mutated server environment: %#v", got)
	}
	if result.Command[0] != "fake-tmux" || result.Stdout[0] != "output" ||
		result.RawStdout[0] != 'o' || result.Stderr[0] != "diagnostic" {
		t.Fatalf("runner result aliases caller storage: %#v", result)
	}
}

func TestCommandRunnerTransportErrorRemainsDetectable(t *testing.T) {
	t.Parallel()

	want := errors.New("runner unavailable")
	server := tmux.NewServer(tmux.ServerOptions{
		Runner: tmux.CommandRunnerFunc(func(context.Context, tmux.CommandRequest) (tmux.CommandResult, error) {
			return tmux.CommandResult{ExitCode: -1}, want
		}),
	})

	result, err := server.Cmd(context.Background(), "list-sessions")
	if !errors.Is(err, want) {
		t.Fatalf("Cmd() error = %v, want wrapped runner error", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("Cmd() result = %#v, want runner result", result)
	}
}

// TestSubprocessRunnerMatchesTheDefaultRunner pins the reason SubprocessRunner
// is exported: a wrapper delegates to it instead of reimplementing execution,
// which is only safe while it produces what a nil Runner produces.
//
//libtmux:real-tmux
func TestSubprocessRunnerMatchesTheDefaultRunner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	options := tmux.ServerOptions{SocketPath: socket}
	implicit := tmux.NewServer(options)
	explicit := tmux.NewServer(tmux.ServerOptions{
		SocketPath: socket,
		Runner:     tmux.SubprocessRunner(),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = implicit.Kill(killCtx)
	})

	if _, err := implicit.NewSession(ctx, tmux.NewSessionRequest{Name: "runner"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// A successful read and a failing one, so the comparison covers the exit
	// code and stderr as well as decoded and exact stdout.
	for _, arguments := range [][]string{
		{"display-message", "-p", "#{session_name}"},
		{"kill-session", "-t", "=no-such-session"},
	} {
		want, wantErr := implicit.Cmd(ctx, arguments...)
		got, gotErr := explicit.Cmd(ctx, arguments...)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("%v: errors differ: %v vs %v", arguments, wantErr, gotErr)
		}
		if got.ExitCode != want.ExitCode {
			t.Errorf("%v: exit code %d, want %d", arguments, got.ExitCode, want.ExitCode)
		}
		if !slices.Equal(got.Stdout, want.Stdout) {
			t.Errorf("%v: stdout %q, want %q", arguments, got.Stdout, want.Stdout)
		}
		if !bytes.Equal(got.RawStdout, want.RawStdout) {
			t.Errorf("%v: raw stdout %q, want %q", arguments, got.RawStdout, want.RawStdout)
		}
		if !slices.Equal(got.Stderr, want.Stderr) {
			t.Errorf("%v: stderr %q, want %q", arguments, got.Stderr, want.Stderr)
		}
	}
}
