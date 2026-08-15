package tmux_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

// recordingEngine records every request one transport was asked to carry and
// answers from a queue, so a routing decision is observable without tmux.
type recordingEngine struct {
	mu        sync.Mutex
	kinds     []tmux.CommandKind
	arguments [][]string
	supports  map[tmux.CommandKind]bool
	result    tmux.CommandResult
	err       error
}

func (e *recordingEngine) Supports(kind tmux.CommandKind) bool { return e.supports[kind] }

func (e *recordingEngine) Run(
	_ context.Context,
	kind tmux.CommandKind,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kinds = append(e.kinds, kind)
	e.arguments = append(e.arguments, slices.Clone(request.Arguments))
	return e.result, e.err
}

func (e *recordingEngine) recorded() ([]tmux.CommandKind, [][]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.kinds), slices.Clone(e.arguments)
}

// recordingRunner counts the tmux processes a server would have started.
type recordingRunner struct {
	mu        sync.Mutex
	arguments [][]string
	result    tmux.CommandResult
}

func (r *recordingRunner) Run(
	_ context.Context,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arguments = append(r.arguments, slices.Clone(request.Arguments))
	return r.result, nil
}

func (r *recordingRunner) recorded() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.arguments)
}

func TestWithEngineRoutesServerCommandsWithoutClientSelectors(t *testing.T) {
	t.Parallel()

	engine := &recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
		result:   tmux.CommandResult{Stdout: []string{"$0 work"}},
	}
	runner := &recordingRunner{}
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "engine-socket",
		Colors:     tmux.Color256,
		Runner:     runner,
	}).WithEngine(engine)

	result, err := server.Cmd(context.Background(), "list-sessions", "-F", "#{session_id}")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if !slices.Equal(result.Stdout, []string{"$0 work"}) {
		t.Fatalf("Cmd() stdout = %#v, want the engine result", result.Stdout)
	}
	kinds, arguments := engine.recorded()
	if !slices.Equal(kinds, []tmux.CommandKind{tmux.CommandServer}) {
		t.Fatalf("engine kinds = %v, want one server command", kinds)
	}
	want := []string{"list-sessions", "-F", "#{session_id}"}
	if len(arguments) != 1 || !slices.Equal(arguments[0], want) {
		t.Fatalf("engine arguments = %#v, want %#v without client selectors", arguments, want)
	}
	if started := runner.recorded(); len(started) != 0 {
		t.Fatalf("started %d tmux processes, want none", len(started))
	}
}

func TestWithEngineKeepsUnsupportedKindsOnATmuxProcess(t *testing.T) {
	t.Parallel()

	engine := &recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
	}
	runner := &recordingRunner{
		result: tmux.CommandResult{Stdout: []string{"tmux 3.7b"}},
	}
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "engine-socket",
		Runner:     runner,
	}).WithEngine(engine)

	version, err := server.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version.String() != "3.7b" {
		t.Fatalf("Version() = %q, want 3.7b", version)
	}
	if kinds, _ := engine.recorded(); len(kinds) != 0 {
		t.Fatalf("engine carried %v, want the version probe on a tmux process", kinds)
	}
	started := runner.recorded()
	if len(started) != 1 || !slices.Equal(started[0], []string{"-V"}) {
		t.Fatalf("process arguments = %#v, want one tmux -V", started)
	}
}

func TestSubprocessEngineRestoresClientSelectorsAndProcessExecution(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	base := tmux.NewServer(tmux.ServerOptions{
		SocketPath: "/tmp/engine.sock",
		ConfigFile: "/tmp/engine.conf",
		Runner:     runner,
	})
	connected := base.WithEngine(&recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
	})

	if _, err := connected.WithEngine(base.SubprocessEngine()).Cmd(
		context.Background(),
		"kill-window",
		"-t",
		"@1",
	); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	started := runner.recorded()
	want := []string{"-f/tmp/engine.conf", "-S/tmp/engine.sock", "kill-window", "-t", "@1"}
	if len(started) != 1 || !slices.Equal(started[0], want) {
		t.Fatalf("process arguments = %#v, want %#v", started, want)
	}
}

func TestWithEngineNilRestoresProcessExecution(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	engine := &recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
	}
	server := tmux.NewServer(tmux.ServerOptions{Runner: runner}).WithEngine(engine)

	if _, err := server.WithEngine(nil).Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if kinds, _ := engine.recorded(); len(kinds) != 0 {
		t.Fatalf("engine carried %v after WithEngine(nil)", kinds)
	}
	if started := runner.recorded(); len(started) != 1 {
		t.Fatalf("started %d tmux processes, want one", len(started))
	}
}

func TestEngineTransportFailureStaysDetectable(t *testing.T) {
	t.Parallel()

	want := errors.New("engine unavailable")
	server := tmux.NewServer(tmux.ServerOptions{}).WithEngine(&recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
		result:   tmux.CommandResult{ExitCode: -1},
		err:      want,
	})

	if _, err := server.Cmd(context.Background(), "list-sessions"); !errors.Is(err, want) {
		t.Fatalf("Cmd() error = %v, want %v", err, want)
	}
	sessions, err := server.Sessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("Sessions() = (%#v, %v), want a lenient empty collection", sessions, err)
	}
}

func TestCommandKindStringsAreStable(t *testing.T) {
	t.Parallel()

	for kind, want := range map[tmux.CommandKind]string{
		tmux.CommandServer:  "server",
		tmux.CommandProcess: "process",
		tmux.CommandKind(9): "CommandKind(9)",
	} {
		if got := kind.String(); got != want {
			t.Errorf("CommandKind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
}

func TestEngineRequestsDoNotAliasServerConfiguration(t *testing.T) {
	t.Parallel()

	environment := []string{"LANG=C"}
	engine := &recordingEngine{
		supports: map[tmux.CommandKind]bool{tmux.CommandServer: true},
	}
	var captured tmux.CommandRequest
	mutating := tmux.CommandRunnerFunc(
		func(_ context.Context, request tmux.CommandRequest) (tmux.CommandResult, error) {
			captured = request
			return tmux.CommandResult{}, nil
		},
	)
	server := tmux.NewServer(tmux.ServerOptions{
		ProcessEnvironment: environment,
		Runner:             mutating,
	})
	if _, err := server.WithEngine(engine).Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if _, err := server.Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	captured.Environment[0] = "LANG=changed"
	if got := server.ProcessEnvironment(); !slices.Equal(got, environment) {
		t.Fatalf("transport mutated server environment: %#v", got)
	}
	if _, arguments := engine.recorded(); len(arguments) != 1 ||
		strings.Join(arguments[0], " ") != "list-sessions" {
		t.Fatalf("engine arguments = %#v", arguments)
	}
}
