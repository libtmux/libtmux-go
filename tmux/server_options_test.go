package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestNewServerSnapshotsEnvironmentAndResolverInputs(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/parent/bin", "PARENT=before", "SYSTEMROOT=/parent/windows"}
	configured := []string{
		"PATH=/first",
		"PRIVATE=first",
		"PATH=/frozen/bin",
		"KEEP=value",
		"PRIVATE=last",
		"SYSTEMROOT=/frozen/windows",
	}
	var environCalls, getwdCalls, resolverCalls int
	var resolvedName, resolvedCWD string
	var resolvedEnvironment []string
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server, err := newServer(ServerOptions{
		Binary:             "configured-tmux",
		ProcessEnvironment: configured,
	}, serverDependencies{
		environ: func() []string {
			environCalls++
			return parent
		},
		getwd: func() (string, error) {
			getwdCalls++
			return filepath.Dir(testExecutable(t)), nil
		},
		resolveExecutable: func(name string, environment []string, cwd string) (string, error) {
			resolverCalls++
			resolvedName = name
			resolvedEnvironment = slices.Clone(environment)
			resolvedCWD = cwd
			return testExecutable(t), nil
		},
		executor: runner,
	})
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	configured[3] = "KEEP=mutated"
	parent[0] = "PATH=/mutated-parent"

	wantEnvironment := []string{
		"PATH=/frozen/bin",
		"KEEP=value",
		"PRIVATE=last",
		"SYSTEMROOT=/frozen/windows",
	}
	if environCalls != 1 || getwdCalls != 1 || resolverCalls != 1 {
		t.Fatalf(
			"constructor calls = (environ %d, getwd %d, resolver %d), want (1, 1, 1)",
			environCalls,
			getwdCalls,
			resolverCalls,
		)
	}
	if resolvedName != "configured-tmux" || resolvedCWD != filepath.Dir(testExecutable(t)) ||
		!slices.Equal(resolvedEnvironment, wantEnvironment) {
		t.Fatalf(
			"resolver inputs = (%q, %#v, %q), want (%q, %#v, %q)",
			resolvedName,
			resolvedEnvironment,
			resolvedCWD,
			"configured-tmux",
			wantEnvironment,
			filepath.Dir(testExecutable(t)),
		)
	}
	if server.state.config.executable != testExecutable(t) || server.state.executor != runner {
		t.Fatalf("server binding = %#v, want resolved executable and supplied executor", server.state)
	}
	got := server.ProcessEnvironment()
	if !slices.Equal(got, wantEnvironment) {
		t.Fatalf("ProcessEnvironment() = %#v, want %#v", got, wantEnvironment)
	}
	got[0] = "PATH=/caller-mutation"
	if again := server.ProcessEnvironment(); !slices.Equal(again, wantEnvironment) {
		t.Fatalf("ProcessEnvironment() after caller mutation = %#v, want %#v", again, wantEnvironment)
	}
	if _, err := server.Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	requests := runner.recordedRequests()
	wantExecutionEnvironment := append(
		slices.Clone(wantEnvironment),
		"TMUX_TMPDIR="+filepath.Dir(server.state.config.socketSelection.NamedDirectory),
	)
	if len(requests) != 1 || requests[0].Binary != testExecutable(t) ||
		requests[0].Directory != filepath.Dir(testExecutable(t)) ||
		!slices.Equal(requests[0].Environment, wantExecutionEnvironment) {
		t.Fatalf("Cmd() request = %#v, want frozen executable, directory, and environment", requests)
	}
}

func TestNewServerDistinguishesInheritedAndEmptyEnvironment(t *testing.T) {
	t.Parallel()

	parent := []string{
		"PATH=/inherited",
		"KEEP=value",
		"SYSTEMROOT=/inherited/windows",
	}
	dependencies := testServerDependencies(t, parent)
	inherited, err := newServer(ServerOptions{}, dependencies)
	if err != nil {
		t.Fatalf("newServer(inherited) error = %v", err)
	}
	wantInherited := append(
		slices.Clone(parent),
		"TMUX_TMPDIR="+filepath.Dir(inherited.state.config.socketSelection.NamedDirectory),
	)
	parent[1] = "KEEP=mutated"
	if got := inherited.ProcessEnvironment(); got != nil {
		t.Fatalf("inherited ProcessEnvironment() = %#v, want private nil", got)
	}
	if got := inherited.state.config.processEnvironment; !slices.Equal(got, wantInherited) {
		t.Fatalf("private inherited environment = %#v, want %#v", got, wantInherited)
	}

	empty, err := newServer(
		ServerOptions{ProcessEnvironment: []string{}},
		dependencies,
	)
	if err != nil {
		t.Fatalf("newServer(empty) error = %v", err)
	}
	if got, want := empty.ProcessEnvironment(), addCriticalProcessEnvironment([]string{}, parent); got == nil || !slices.Equal(got, want) {
		t.Fatalf("empty ProcessEnvironment() = %#v, want %#v", got, want)
	}
}

func TestNewServerPreservesOpaqueEnvironmentEntries(t *testing.T) {
	t.Parallel()

	configured := []string{
		"OPAQUE",
		"=FIRST",
		"=SECOND",
		"=C:=C:\\work",
		"NAME=first",
		"NAME=last",
		"",
	}
	server, err := newServer(
		ServerOptions{ProcessEnvironment: configured},
		testServerDependencies(t, []string{"SYSTEMROOT=/windows"}),
	)
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	want := addCriticalProcessEnvironment(
		[]string{"OPAQUE", "=SECOND", "=C:=C:\\work", "NAME=last"},
		[]string{"SYSTEMROOT=/windows"},
	)
	if got := server.ProcessEnvironment(); !slices.Equal(got, want) {
		t.Fatalf("ProcessEnvironment() = %#v, want %#v", got, want)
	}
}

func TestNewServerResolvesTheDefaultBinaryOnce(t *testing.T) {
	t.Parallel()

	dependencies := testServerDependencies(t, nil)
	var calls int
	dependencies.resolveExecutable = func(name string, _ []string, _ string) (string, error) {
		calls++
		if name != "tmux" {
			t.Fatalf("resolver name = %q, want tmux", name)
		}
		return testExecutable(t), nil
	}
	if _, err := newServer(ServerOptions{}, dependencies); err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
}

func TestNewServerRejectsInvalidConfigurationBeforeUse(t *testing.T) {
	t.Parallel()

	workingDirectoryFailure := errors.New("working directory failed")
	resolutionFailure := errors.New("resolution failed")
	tests := []struct {
		name         string
		options      ServerOptions
		mutate       func(*serverDependencies)
		want         error
		wantContains string
		wantOptions  bool
	}{
		{name: "color", options: ServerOptions{Colors: ColorMode(16)}, want: ErrUnknownColor, wantOptions: true},
		{name: "unsupported policy", options: ServerOptions{
			Unsupported: UnsupportedPolicy(255),
		}, want: ErrInvalidServerOptions, wantContains: "Unsupported", wantOptions: true},
		{name: "working directory", mutate: func(dependencies *serverDependencies) {
			dependencies.getwd = func() (string, error) { return "relative", nil }
		}, wantContains: "working directory"},
		{name: "working directory failure", mutate: func(dependencies *serverDependencies) {
			dependencies.getwd = func() (string, error) { return "", workingDirectoryFailure }
		}, want: workingDirectoryFailure},
		{name: "resolution", mutate: func(dependencies *serverDependencies) {
			dependencies.resolveExecutable = func(string, []string, string) (string, error) {
				return "", resolutionFailure
			}
		}, want: resolutionFailure},
		{name: "relative resolution", mutate: func(dependencies *serverDependencies) {
			dependencies.resolveExecutable = func(string, []string, string) (string, error) {
				return "relative/tmux", nil
			}
		}, wantContains: "resolver"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dependencies := testServerDependencies(t, nil)
			if test.mutate != nil {
				test.mutate(&dependencies)
			}
			server, err := newServer(test.options, dependencies)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("newServer() error = %v, want errors.Is(_, %v)", err, test.want)
			}
			if test.wantOptions && !errors.Is(err, ErrInvalidServerOptions) {
				t.Fatalf("newServer() error = %v, want ErrInvalidServerOptions", err)
			}
			if test.wantContains != "" && (err == nil || !strings.Contains(err.Error(), test.wantContains)) {
				t.Fatalf("newServer() error = %v, want text %q", err, test.wantContains)
			}
			if err == nil || server.state != nil {
				t.Fatalf("newServer() = (%#v, %v), want zero server and error", server, err)
			}
		})
	}
}

func TestNewServerAppliesPlatformNULEnvironmentPolicy(t *testing.T) {
	t.Parallel()

	entry := "path=one\x00two"
	server, err := newServer(
		ServerOptions{ProcessEnvironment: []string{entry}},
		testServerDependencies(t, nil),
	)
	if processEnvironmentNULAllowed {
		if err != nil || !slices.Equal(server.ProcessEnvironment(), []string{entry}) {
			t.Fatalf("newServer(NUL environment) = (%#v, %v), want preserved entry", server, err)
		}
		return
	}
	if !errors.Is(err, ErrInvalidServerOptions) || server.state != nil {
		t.Fatalf("newServer(NUL environment) = (%#v, %v), want invalid options", server, err)
	}
}

func TestNewServerRejectsIncompletePrivateDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*serverDependencies)
	}{
		{name: "environment", mutate: func(dependencies *serverDependencies) {
			dependencies.environ = nil
		}},
		{name: "working directory", mutate: func(dependencies *serverDependencies) {
			dependencies.getwd = nil
		}},
		{name: "resolver", mutate: func(dependencies *serverDependencies) {
			dependencies.resolveExecutable = nil
		}},
		{name: "executor", mutate: func(dependencies *serverDependencies) {
			dependencies.executor = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dependencies := testServerDependencies(t, nil)
			test.mutate(&dependencies)
			server, err := newServer(ServerOptions{}, dependencies)
			if err == nil || !strings.Contains(err.Error(), "incomplete constructor dependencies") ||
				server.state != nil {
				t.Fatalf("newServer() = (%#v, %v), want zero server and dependency error", server, err)
			}
		})
	}
}

func TestZeroServerIsInvalidAndSafeToInspect(t *testing.T) {
	t.Parallel()

	var server Server
	if server.String() != "Server(invalid)" || server.Equal(Server{}) {
		t.Fatalf("zero Server identity = (%q, %t), want invalid and unequal", server, server.Equal(Server{}))
	}
	if server.Executable() != "" || server.SocketPath() != "" || server.ConfigFile() != "" ||
		server.ProcessEnvironment() != nil {
		t.Fatalf("zero Server accessors returned configured values")
	}
	result, err := server.Cmd(context.Background(), "list-sessions")
	if !errors.Is(err, ErrInvalidServer) || result.ExitCode != -1 {
		t.Fatalf("zero Server.Cmd() = (%#v, %v), want exit -1 and ErrInvalidServer", result, err)
	}
	if _, err := server.Version(context.Background()); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("zero Server.Version() error = %v, want ErrInvalidServer", err)
	}
	if _, err := NewPlan().Run(context.Background(), server); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("empty Plan.Run(zero) error = %v, want ErrInvalidServer", err)
	}
	if _, err := server.OpenControl(context.Background(), Session{}); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("zero Server.OpenControl() error = %v, want ErrInvalidServer", err)
	}
}

func TestNewServerAcceptsAnAbsoluteExecutable(t *testing.T) {
	t.Parallel()

	executable := testExecutable(t)
	server, err := NewServer(ServerOptions{Binary: executable})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if server.Executable() != filepath.Clean(executable) {
		t.Fatalf("resolved executable = %q, want %q", server.Executable(), executable)
	}
}

func TestNewServerRejectsMissingExecutableEagerly(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-tmux")
	server, err := NewServer(ServerOptions{Binary: missing})
	if err == nil || server.state != nil {
		t.Fatalf("NewServer() = (%#v, %v), want zero server and resolution error", server, err)
	}
	var executableError *exec.Error
	if !errors.As(err, &executableError) || executableError.Name != missing {
		t.Fatalf("NewServer() error = %#v, want *exec.Error for %q", err, missing)
	}
}

func TestServerWithSocketPathPreservesFrozenExecutionBinding(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	base := serverWithOptionsAndRunner(ServerOptions{
		SocketName:         "original",
		ProcessEnvironment: []string{"PATH=/frozen"},
	}, runner)
	derived, err := base.WithSocketPath("/tmp/sibling.sock")
	if err != nil {
		t.Fatalf("WithSocketPath() error = %v", err)
	}
	if derived.state == base.state || derived.state.shared == base.state.shared {
		t.Fatal("WithSocketPath() retained original daemon-scoped state")
	}
	if derived.state.executor != base.state.executor ||
		derived.state.config.executable != base.state.config.executable ||
		derived.Executable() != base.Executable() ||
		derived.state.config.directory != base.state.config.directory ||
		!slices.Equal(
			derived.state.config.processEnvironment,
			base.state.config.processEnvironment,
		) ||
		!slices.Equal(derived.ProcessEnvironment(), base.ProcessEnvironment()) {
		t.Fatal("WithSocketPath() changed the frozen execution binding")
	}
	if derived.SocketPath() != "/tmp/sibling.sock" ||
		derived.state.config.socketName != "" {
		t.Fatalf("derived selector = %s, want only /tmp/sibling.sock", derived)
	}
	if base.SocketPath() == "" || base.state.config.socketName != "original" {
		t.Fatalf("WithSocketPath() mutated base selector: %s", base)
	}
	if _, err := derived.Cmd(context.Background(), "list-sessions"); err != nil {
		t.Fatalf("derived Cmd() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 || requests[0].Binary != base.state.config.executable ||
		requests[0].Directory != base.state.config.directory ||
		!slices.Equal(requests[0].Environment, base.state.config.processEnvironment) ||
		!slices.Equal(requests[0].Arguments, []string{"-S/tmp/sibling.sock", "list-sessions"}) {
		t.Fatalf("derived Cmd() request = %#v, want frozen binding and sibling selector", requests)
	}
}

func TestServerWithSocketPathRejectsInvalidReceiverAndPath(t *testing.T) {
	t.Parallel()

	if _, err := (Server{}).WithSocketPath("/tmp/sibling.sock"); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("zero Server.WithSocketPath() error = %v, want ErrInvalidServer", err)
	}
	base := serverWithOptionsAndRunner(ServerOptions{}, &versionQueueRunner{})
	if _, err := base.WithSocketPath("private\x00socket"); !errors.Is(err, ErrInvalidServerOptions) ||
		!errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("WithSocketPath(NUL) error = %v, want option and command-request classification", err)
	}
}

func testServerDependencies(t *testing.T, parentEnvironment []string) serverDependencies {
	t.Helper()

	return serverDependencies{
		environ: func() []string { return parentEnvironment },
		getwd:   os.Getwd,
		resolveExecutable: func(string, []string, string) (string, error) {
			return testExecutable(t), nil
		},
		executor: &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{ExitCode: 0},
		}}},
	}
}

func testExecutable(t *testing.T) string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return executable
}
