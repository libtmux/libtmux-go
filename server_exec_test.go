package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.run_shell
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:args:fd422ab3a1ca
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:as_tmux_command:0fe82009cc1e
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:background:af1c44728d8a
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:background:af1c44728d8a:2
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:cwd:c8f5f1bebe8f
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:delay:0b3ab3dbe007
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:show_stderr:77ae01eb137f
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:161023f2e486
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:5bb2ac269d05
// libtmux:parity libtmux.server.Server.run_shell#warning:39bcbdbb8388
// libtmux:parity libtmux.server.Server.run_shell#warning:78d80521a5af
// libtmux:parity libtmux.server.Server.run_shell#warning:9349ea8de11f
func TestRunShellBuildsExactArguments(t *testing.T) {
	t.Parallel()

	delay := "250"
	directory := "~/workspace"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}},
		{result: tmuxcmd.Result{Stdout: []string{"alpha-beta"}}},
	}}
	server := serverWithRunner(runner)

	output, err := server.RunShell(context.Background(), RunShellRequest{
		Command:        "echo #{1}-#{2}",
		Delay:          &delay,
		AsTmuxCommand:  true,
		TargetPane:     "%9",
		StartDirectory: &directory,
		ShowStderr:     true,
		Args:           []string{"alpha", "beta"},
	})
	if err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	if !slices.Equal(output, []string{"alpha-beta"}) {
		t.Fatalf("RunShell() = %#v, want alpha-beta", output)
	}

	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and run-shell", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"run-shell", "-d", "250", "-C", "-t", "%9", "-c", "~/workspace", "-E",
		"echo #{1}-#{2}", "alpha", "beta",
	})
}

func TestRunShellBackgroundReturnsNilWithoutVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stdout: []string{"ignored"},
	}}}}
	server := serverWithRunner(runner)

	output, err := server.RunShell(context.Background(), RunShellRequest{
		Command:    "printf ignored",
		Background: true,
	})
	if err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	if output != nil {
		t.Fatalf("RunShell(background) = %#v, want nil", output)
	}
	assertServerExecArguments(t, runner, []string{"run-shell", "-b", "printf ignored"})
}

func TestNestedCommandsPreserveTerminalSeparators(t *testing.T) {
	t.Parallel()

	otherwise := "display-message otherwise;"
	tests := []struct {
		name string
		run  func(Server) error
		want []string
	}{
		{
			name: "run shell",
			run: func(server Server) error {
				_, err := server.RunShell(
					context.Background(),
					RunShellRequest{Command: "printf nested;"},
				)
				return err
			},
			want: []string{"run-shell", `printf nested\;`},
		},
		{
			name: "if shell",
			run: func(server Server) error {
				return server.IfShell(context.Background(), IfShellRequest{
					ShellCommand: "true;",
					ThenCommand:  "display-message nested;",
					ElseCommand:  &otherwise,
				})
			},
			want: []string{
				"if-shell",
				`true\;`,
				`display-message nested\;`,
				`display-message otherwise\;`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{},
			}}}
			if err := test.run(serverWithRunner(runner)); err != nil {
				t.Fatalf("nested command error = %v", err)
			}
			assertServerExecArguments(t, runner, test.want)
		})
	}
}

func TestRunShellWarnsAndOmitsUnsupportedFeatures(t *testing.T) {
	t.Parallel()

	directory := "/workspace"
	tests := []struct {
		name        string
		version     string
		request     RunShellRequest
		wantArgs    []string
		wantFeature string
		wantMinimum string
	}{
		{
			name:        "start directory before 3.4",
			version:     "3.3",
			request:     RunShellRequest{Command: "true", StartDirectory: &directory},
			wantArgs:    []string{"run-shell", "true"},
			wantFeature: "start_directory",
			wantMinimum: "3.4",
		},
		{
			name:        "stderr before 3.6",
			version:     "3.5",
			request:     RunShellRequest{Command: "true", ShowStderr: true},
			wantArgs:    []string{"run-shell", "true"},
			wantFeature: "show_stderr",
			wantMinimum: "3.6",
		},
		{
			name:        "positional arguments before 3.7",
			version:     "3.6",
			request:     RunShellRequest{Command: "true", Args: []string{"ignored"}},
			wantArgs:    []string{"run-shell", "true"},
			wantFeature: "args",
			wantMinimum: "3.7",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var warnings []Warning
			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			server := serverWithRunner(runner)
			server.connectionState().options.WarningHandler = func(warning Warning) {
				warnings = append(warnings, warning)
			}

			if _, err := server.RunShell(context.Background(), test.request); err != nil {
				t.Fatalf("RunShell() error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("runner requests = %#v, want version and run-shell", requests)
			}
			assertRequestArguments(t, requests[1], test.wantArgs)
			if len(warnings) != 1 {
				t.Fatalf("warnings = %#v, want one", warnings)
			}
			warning := warnings[0]
			if warning.Kind != WarningUnsupportedFeature ||
				warning.Subcommand != "run-shell" ||
				warning.Feature != test.wantFeature ||
				warning.CurrentVersion.String() != test.version ||
				warning.RequiredVersion.String() != test.wantMinimum {
				t.Fatalf("warning = %#v, want %s requiring %s", warning, test.wantFeature, test.wantMinimum)
			}
		})
	}
}

func TestRunShellOrdersWarningsAndExecutesOneReducedCommand(t *testing.T) {
	t.Parallel()

	directory := "/workspace"
	var warnings []Warning
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithRunner(runner)
	server.connectionState().options.WarningHandler = func(warning Warning) {
		warnings = append(warnings, warning)
	}

	if _, err := server.RunShell(context.Background(), RunShellRequest{
		Command:        "true",
		StartDirectory: &directory,
		ShowStderr:     true,
		Args:           []string{"ignored"},
	}); err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want one version and one run-shell", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{"run-shell", "true"})
	wantFeatures := []string{"start_directory", "show_stderr", "args"}
	features := make([]string, len(warnings))
	for index, warning := range warnings {
		features[index] = warning.Feature
	}
	if !slices.Equal(features, wantFeatures) {
		t.Fatalf("warning features = %#v, want %#v", features, wantFeatures)
	}
}

func TestRunShellClonesArgsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newRunShellGateRunner()
	server := serverWithRunner(runner)
	arguments := []string{"before"}
	response := make(chan error, 1)
	go func() {
		_, err := server.RunShell(context.Background(), RunShellRequest{
			Command: "echo #{1}",
			Args:    arguments,
		})
		response <- err
	}()

	<-runner.versionStarted
	arguments[0] = "after"
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and run-shell", requests)
	}
	assertRequestArguments(t, requests[1], []string{"run-shell", "echo #{1}", "before"})
}

// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:cwd:c8f5f1bebe8f
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:delay:0b3ab3dbe007
func TestRunShellCapturesPointerFieldsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newRunShellGateRunner()
	server := serverWithRunner(runner)
	delay := "before-delay"
	directory := "before-directory"
	response := make(chan error, 1)
	go func() {
		_, err := server.RunShell(context.Background(), RunShellRequest{
			Command:        "true",
			Delay:          &delay,
			StartDirectory: &directory,
		})
		response <- err
	}()

	<-runner.versionStarted
	delay = "after-delay"
	directory = "after-directory"
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and run-shell", requests)
	}
	assertRequestArguments(t, requests[1], []string{
		"run-shell", "-d", "before-delay", "-c", "before-directory", "true",
	})
}

// libtmux:parity libtmux.server.Server.wait_for
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:lock:92e800fe2c02
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:set_flag:6fd02ea1aab4
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:unlock:8d2b1171c459
func TestWaitForBuildsExactArguments(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		mode WaitForMode
		flag string
	}{
		{name: "wait", mode: WaitForModeWait},
		{name: "signal", mode: WaitForModeSignal, flag: "-S"},
		{name: "lock", mode: WaitForModeLock, flag: "-L"},
		{name: "unlock", mode: WaitForModeUnlock, flag: "-U"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			server := serverWithRunner(runner)
			if err := server.WaitFor(
				context.Background(),
				WaitForRequest{Channel: "phase6", Mode: test.mode},
			); err != nil {
				t.Fatalf("WaitFor() error = %v", err)
			}
			want := []string{"wait-for"}
			if test.flag != "" {
				want = append(want, test.flag)
			}
			want = append(want, "phase6")
			assertServerExecArguments(t, runner, want)
		})
	}
}

// libtmux:parity libtmux.server.Server.if_shell
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:background:af1c44728d8a
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:else_command:bef5b34f0329
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.source_file
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:parse_only:656bfbd1a466
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:quiet:8573bc8befe4
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:verbose:414278ee0d55
func TestIfShellBuildsExactArguments(t *testing.T) {
	t.Parallel()

	otherwise := "set -g @branch no"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	server := serverWithRunner(runner)
	if err := server.IfShell(context.Background(), IfShellRequest{
		ShellCommand: "test -f marker",
		ThenCommand:  "set -g @branch yes",
		ElseCommand:  &otherwise,
		Background:   true,
		TargetPane:   "%4",
	}); err != nil {
		t.Fatalf("IfShell() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{
		"if-shell", "-b", "-t", "%4", "test -f marker",
		"set -g @branch yes", "set -g @branch no",
	})
}

// libtmux:parity libtmux._internal.types.StrPath
func TestSourceFileBuildsExactArgumentsAndExpandsCurrentUser(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	server := serverWithRunner(runner)
	if err := server.SourceFile(context.Background(), SourceFileRequest{
		Path:      "~/tmux.conf",
		Quiet:     true,
		ParseOnly: true,
		Verbose:   true,
	}); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{
		"source-file", "-q", "-n", "-v", "--", filepath.Join(home, "tmux.conf"),
	})
}

func TestSourceFilePreservesBackslashAtEndOfPOSIXHome(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("POSIX permits backslash in path components")
	}
	home := filepath.Join(t.TempDir(), `home\`)
	t.Setenv("HOME", home)
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	if err := serverWithRunner(runner).SourceFile(
		context.Background(),
		SourceFileRequest{Path: "~/tmux.conf"},
	); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{
		"source-file", "--", home + "/tmux.conf",
	})
}

func TestSourceFilePreservesDoubleSlashPOSIXHome(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("POSIX reserves exactly two leading separators")
	}
	t.Setenv("HOME", "//")
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	if err := serverWithRunner(runner).SourceFile(
		context.Background(),
		SourceFileRequest{Path: "~/tmux.conf"},
	); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{
		"source-file", "--", "//tmux.conf",
	})
}

func TestSourceFileNormalizesHarmlessLexicalPathComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{path: "relative/", want: "relative"},
		{path: "relative//child", want: filepath.Join("relative", "child")},
		{path: "relative/./child", want: filepath.Join("relative", "child")},
		{path: "./-", want: "." + string(os.PathSeparator) + "-"},
		{
			path: "link/../config",
			want: "link" + string(os.PathSeparator) + ".." +
				string(os.PathSeparator) + "config",
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			server := serverWithRunner(runner)
			if err := server.SourceFile(
				context.Background(),
				SourceFileRequest{Path: test.path},
			); err != nil {
				t.Fatalf("SourceFile() error = %v", err)
			}
			assertServerExecArguments(
				t,
				runner,
				[]string{"source-file", "--", test.want},
			)
		})
	}
}

func TestSourceFileEscapesTerminalSemicolonPath(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	server := serverWithRunner(runner)
	if err := server.SourceFile(context.Background(), SourceFileRequest{Path: `config\;`}); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{"source-file", "--", `config\\;`})
}

func TestSourceFileTreatsLeadingDashPathAsPositional(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
	server := serverWithRunner(runner)
	if err := server.SourceFile(context.Background(), SourceFileRequest{Path: "-config"}); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	assertServerExecArguments(t, runner, []string{"source-file", "--", "-config"})
}

func TestSourceFileRejectsNULPathBeforeExecution(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	err := serverWithRunner(runner).SourceFile(
		context.Background(),
		SourceFileRequest{Path: "config\x00suffix"},
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("SourceFile(NUL) error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("SourceFile(NUL) calls = %d, want 0", runner.callCount())
	}
}

func TestServerExecRequestsValidateBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) error
		want      error
		field     string
	}{
		{
			name: "run-shell command",
			operation: func(server Server) error {
				_, err := server.RunShell(context.Background(), RunShellRequest{})
				return err
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Command",
		},
		{
			name: "run-shell pane target",
			operation: func(server Server) error {
				_, err := server.RunShell(context.Background(), RunShellRequest{
					Command:    "true",
					TargetPane: "pane",
					ShowStderr: true,
				})
				return err
			},
			want: ErrInvalidTarget,
		},
		{
			name: "wait-for channel",
			operation: func(server Server) error {
				return server.WaitFor(context.Background(), WaitForRequest{})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Channel",
		},
		{
			name: "wait-for mode",
			operation: func(server Server) error {
				return server.WaitFor(context.Background(), WaitForRequest{
					Channel: "phase6",
					Mode:    WaitForMode(99),
				})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Mode",
		},
		{
			name: "if-shell shell command",
			operation: func(server Server) error {
				return server.IfShell(context.Background(), IfShellRequest{ThenCommand: "display"})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "ShellCommand",
		},
		{
			name: "if-shell then command",
			operation: func(server Server) error {
				return server.IfShell(context.Background(), IfShellRequest{ShellCommand: "true"})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "ThenCommand",
		},
		{
			name: "if-shell pane target",
			operation: func(server Server) error {
				return server.IfShell(context.Background(), IfShellRequest{
					ShellCommand: "true",
					ThenCommand:  "display",
					TargetPane:   "pane",
				})
			},
			want: ErrInvalidTarget,
		},
		{
			name: "source-file path",
			operation: func(server Server) error {
				return server.SourceFile(context.Background(), SourceFileRequest{})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Path",
		},
		{
			name: "source-file stdin",
			operation: func(server Server) error {
				return server.SourceFile(context.Background(), SourceFileRequest{Path: "-"})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Path",
		},
		{
			name: "source-file named user",
			operation: func(server Server) error {
				return server.SourceFile(context.Background(), SourceFileRequest{Path: "~other/tmux.conf"})
			},
			want:  ErrInvalidServerCommandRequest,
			field: "Path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.operation(serverWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("operation error = %v, want %v", err, test.want)
			}
			if test.field != "" {
				var requestError *ServerCommandRequestError
				if !errors.As(err, &requestError) || requestError.Field != test.field {
					t.Fatalf("operation error = %#v, want %s ServerCommandRequestError", err, test.field)
				}
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestServerExecMethodsRedactCompletedStderr(t *testing.T) {
	t.Parallel()

	for _, test := range serverExecOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"tmux diagnostic"}, ExitCode: 0,
			}}}}
			err := test.operation(serverWithRunner(runner))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("%s error = %v, want ErrCommand", test.name, err)
			}
			var commandError *CommandError
			if !errors.As(err, &commandError) ||
				commandError.Subcommand != test.subcommand ||
				commandError.Result.ExitCode != 0 ||
				commandError.Result.Command != nil || commandError.Result.Stdout != nil ||
				commandError.Result.Stderr != nil {
				t.Fatalf("%s error = %#v, want exit-code-only CommandError", test.name, err)
			}
		})
	}
}

func TestRunShellRejectsNULBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	directory := "directory"
	runner := &versionQueueRunner{}
	_, err := serverWithRunner(runner).RunShell(context.Background(), RunShellRequest{
		Command:        "secret\x00command",
		StartDirectory: &directory,
	})
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("RunShell() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("RunShell() error retained command: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before version probe", runner.callCount())
	}
}

func TestServerExecMethodsPreserveExitOnlyFailuresAsData(t *testing.T) {
	t.Parallel()

	for _, test := range serverExecOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stdout:   []string{"partial"},
				ExitCode: 7,
			}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("%s error = %v, want nil for exit-only completion", test.name, err)
			}
		})
	}
}

func TestServerExecMethodsPropagateTransportErrors(t *testing.T) {
	t.Parallel()

	for _, test := range serverExecOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{err: context.DeadlineExceeded}}}
			err := test.operation(serverWithRunner(runner))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s error = %v, want context deadline", test.name, err)
			}
		})
	}
}

type serverExecOperation struct {
	name       string
	subcommand string
	operation  func(Server) error
}

func serverExecOperations() []serverExecOperation {
	return []serverExecOperation{
		{
			name:       "run-shell",
			subcommand: "run-shell",
			operation: func(server Server) error {
				_, err := server.RunShell(context.Background(), RunShellRequest{Command: "true"})
				return err
			},
		},
		{
			name:       "wait-for",
			subcommand: "wait-for",
			operation: func(server Server) error {
				return server.WaitFor(context.Background(), WaitForRequest{
					Channel: "phase6",
					Mode:    WaitForModeSignal,
				})
			},
		},
		{
			name:       "if-shell",
			subcommand: "if-shell",
			operation: func(server Server) error {
				return server.IfShell(context.Background(), IfShellRequest{
					ShellCommand: "true",
					ThenCommand:  "display-message ok",
				})
			},
		},
		{
			name:       "source-file",
			subcommand: "source-file",
			operation: func(server Server) error {
				return server.SourceFile(context.Background(), SourceFileRequest{Path: "tmux.conf"})
			},
		},
	}
}

func assertServerExecArguments(t *testing.T, runner *versionQueueRunner, want []string) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	assertRequestArguments(t, requests[0], want)
}

type runShellGateRunner struct {
	versionStarted chan struct{}
	releaseVersion chan struct{}

	mu       sync.Mutex
	requests []tmuxcmd.Request
}

func newRunShellGateRunner() *runShellGateRunner {
	return &runShellGateRunner{
		versionStarted: make(chan struct{}),
		releaseVersion: make(chan struct{}),
	}
}

func (r *runShellGateRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	call := len(r.requests)
	r.mu.Unlock()
	if call == 1 {
		close(r.versionStarted)
		<-r.releaseVersion
		return tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}, nil
	}
	return tmuxcmd.Result{}, nil
}

func (r *runShellGateRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}
