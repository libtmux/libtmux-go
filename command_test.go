package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.cmd
// libtmux:parity libtmux.server.Server.cmd#parameter-branch:target:3fc216416bbd
// libtmux:parity libtmux.common.tmux_cmd
func TestServerCmdReturnsNonzeroTmuxExitAsData(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Binary: os.Args[0]})
	result, err := server.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"failure",
	)
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if result.ExitCode != 9 {
		t.Errorf("ExitCode = %d, want 9", result.ExitCode)
	}
	if want := []string{"output"}; !slices.Equal(result.Stdout, want) {
		t.Errorf("Stdout = %#v, want %#v", result.Stdout, want)
	}
	if want := []string{"failure"}; !slices.Equal(result.Stderr, want) {
		t.Errorf("Stderr = %#v, want %#v", result.Stderr, want)
	}
}

func TestServerCmdReturnsExactRawStdout(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Binary: os.Args[0]})
	result, err := server.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"raw-output",
	)
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if want := []string{`\xffA`}; !slices.Equal(result.Stdout, want) {
		t.Errorf("Stdout = %#v, want %#v", result.Stdout, want)
	}
	if want := []byte{0xff, 'A', '\n', '\n'}; !slices.Equal(result.RawStdout, want) {
		t.Errorf("RawStdout = %q, want %q", result.RawStdout, want)
	}
}

// libtmux:parity libtmux.exc.UnknownColorOption
// libtmux:parity libtmux.exc.UnknownColorOption.__init__
func TestServerCmdRejectsUnknownColorMode(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Binary: os.Args[0], Colors: ColorMode(16)})
	result, err := server.Cmd(context.Background(), "ignored")
	if !errors.Is(err, ErrUnknownColor) {
		t.Fatalf("Cmd() error = %v, want ErrUnknownColor", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("Cmd() ExitCode = %d, want -1 before process start", result.ExitCode)
	}
}

// libtmux:parity libtmux.server.Server
// libtmux:parity libtmux.server.Server.__init__
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:colors:7fb6bc016235
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:config_file:40b07a1bff37
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:on_init:e39d1da35264
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:socket_name:e12e46656294
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:socket_name_factory:fd647ea9004b
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:socket_path:efa7d1c8b9f2
// libtmux:parity libtmux.server.Server.__init__#parameter-branch:tmux_bin:547408be225d
// libtmux:parity libtmux.server.Server.colors
// libtmux:parity libtmux.server.Server.config_file
// libtmux:parity libtmux.server.Server.tmux_bin
// libtmux:parity libtmux.common.tmux_cmd.__init__
// libtmux:parity libtmux.common.tmux_cmd.__init__#parameter-branch:args,tmux_bin:57c855e4829d
// libtmux:parity libtmux.common.tmux_cmd.__init__#parameter-branch:tmux_bin:e28268d2d456
func TestServerBuildsTmuxGlobalArguments(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{
		SocketName: "ignored-name",
		SocketPath: "/tmp/libtmux.sock",
		ConfigFile: "/tmp/libtmux.conf",
		Colors:     Color256,
	})
	got := server.commandArguments([]string{"list-sessions", "-F", "#{session_id}"})
	want := []string{
		"-2",
		"-f/tmp/libtmux.conf",
		"-S/tmp/libtmux.sock",
		"list-sessions", "-F", "#{session_id}",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("commandArguments() = %#v, want %#v", got, want)
	}
}

func TestLiteralCmdEscapesEachTerminalSeparatorExactlyOnce(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	_, err := server.literalCmd(
		context.Background(),
		"if-shell",
		"true;",
		"display-message nested;",
		`already\;`,
		"middle;value",
	)
	if err != nil {
		t.Fatalf("literalCmd() error = %v", err)
	}
	want := []string{
		"if-shell",
		`true\;`,
		`display-message nested\;`,
		`already\\;`,
		"middle;value",
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("literalCmd() arguments = %#v, want %#v", requests, want)
	}
}

func TestRawCmdPreservesOuterCommandSeparators(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	_, err := server.Cmd(context.Background(), "display-message", "one", ";")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	want := []string{"display-message", "one", ";"}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("Cmd() arguments = %#v, want raw %#v", requests, want)
	}
}

func TestRawCmdDoesNotValidateSubcommandArguments(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	_, err := server.Cmd(context.Background(), "display-message", "raw\x00value")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	want := []string{"display-message", "raw\x00value"}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("Cmd() arguments = %#v, want unchanged %#v", requests, want)
	}
}

func TestLiteralCmdRejectsNULWithoutRetainingArguments(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	secret := "credential\x00material"
	_, err := serverWithRunner(runner).literalCmd(
		context.Background(),
		"set-environment",
		"--",
		"TOKEN",
		secret,
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("literalCmd() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	var requestError *ServerCommandRequestError
	if !errors.As(err, &requestError) || requestError.Value != "[redacted]" {
		t.Fatalf("literalCmd() error = %#v, want redacted request error", err)
	}
	if strings.Contains(err.Error(), "credential") || strings.Contains(err.Error(), "material") {
		t.Fatalf("literalCmd() error retained secret argument: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
	}
}

func TestConnectionArgumentsAreLiteralWhileRawSubcommandStaysRaw(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	server.state.options.ConfigFile = "config;"
	server.state.options.SocketName = "socket;"
	_, err := server.Cmd(context.Background(), "display-message", "raw;")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	want := []string{"-fconfig;", "-Lsocket;", "display-message", "raw;"}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("Cmd() arguments = %#v, want %#v", requests, want)
	}
}

func TestConnectionArgumentsRejectNULWithoutRetainingValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		field string
		set   func(*ServerOptions)
	}{
		{name: "config", field: "ConfigFile", set: func(options *ServerOptions) {
			options.ConfigFile = "secret\x00config"
		}},
		{name: "socket name", field: "SocketName", set: func(options *ServerOptions) {
			options.SocketName = "secret\x00name"
		}},
		{name: "socket path", field: "SocketPath", set: func(options *ServerOptions) {
			options.SocketPath = "secret\x00path"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server := serverWithRunner(runner)
			test.set(&server.state.options)
			_, err := server.Cmd(context.Background(), "list-sessions")
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("Cmd() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			var requestError *ServerCommandRequestError
			if !errors.As(err, &requestError) || requestError.Field != test.field ||
				requestError.Value != "[redacted]" {
				t.Fatalf("Cmd() error = %#v, want redacted %s error", err, test.field)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("Cmd() error retained connection value: %v", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestNewServerCopiesEnvironment(t *testing.T) {
	t.Parallel()

	environment := []string{"TMUX_GO_TEST=original"}
	server := NewServer(ServerOptions{Binary: os.Args[0], ProcessEnvironment: environment})
	environment[0] = "TMUX_GO_TEST=mutated"

	result, err := server.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"environment",
	)
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if want := []string{"original"}; !slices.Equal(result.Stdout, want) {
		t.Fatalf("Stdout = %#v, want %#v", result.Stdout, want)
	}
}

func TestServerProcessEnvironmentDistinguishesNilFromEmpty(t *testing.T) {
	t.Setenv("TMUX_GO_TEST", "inherited")

	inherited := NewServer(ServerOptions{Binary: os.Args[0]})
	result, err := inherited.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"environment",
	)
	if err != nil {
		t.Fatalf("inherited Cmd() error = %v", err)
	}
	if want := []string{"inherited"}; !slices.Equal(result.Stdout, want) {
		t.Fatalf("inherited Stdout = %#v, want %#v", result.Stdout, want)
	}

	empty := NewServer(ServerOptions{Binary: os.Args[0], ProcessEnvironment: []string{}})
	result, err = empty.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"environment",
	)
	if err != nil {
		t.Fatalf("empty Cmd() error = %v", err)
	}
	if len(result.Stdout) != 0 {
		t.Fatalf("empty Stdout = %#v, want no inherited value", result.Stdout)
	}
}

func TestServerCommandHelperDoesNotReenterTmuxSuite(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Binary: os.Args[0]})
	result, err := server.Cmd(
		context.Background(),
		"-test.run=^TestServerCommandHelperProcess$",
		"--",
		"suite-environment",
	)
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	want := []string{os.Getenv("TMPDIR"), os.Getenv("GOTMPDIR")}
	if !slices.Equal(result.Stdout, want) {
		t.Fatalf("helper suite environment = %#v, want inherited %#v", result.Stdout, want)
	}
}

// libtmux:parity libtmux.common.raise_if_stderr
// libtmux:parity libtmux.common.raise_if_stderr#parameter-branch:proc:da0890ac4ef0
// libtmux:parity libtmux.exc.LibTmuxException
// libtmux:parity libtmux.exc.LibTmuxException.__init__
// libtmux:parity libtmux.exc.LibTmuxException.__str__
func TestCommandResultsAndErrorsOwnTheirSlices(t *testing.T) {
	t.Parallel()

	shared := tmuxcmd.Result{
		Command:   []string{"tmux", "display-message"},
		Stdout:    []string{"output"},
		RawStdout: []byte("output\n"),
		Stderr:    []string{"warning"},
		ExitCode:  0,
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: shared},
		{result: shared},
	}}
	server := serverWithRunner(runner)
	first, err := server.Cmd(context.Background(), "display-message")
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.Cmd(context.Background(), "display-message")
	if err != nil {
		t.Fatal(err)
	}
	first.Command[0] = "mutated"
	first.Stdout[0] = "mutated"
	first.RawStdout[0] = 'X'
	first.Stderr[0] = "mutated"
	shared.Command[0] = "source-mutated"
	shared.Stdout[0] = "source-mutated"
	shared.RawStdout[0] = 'Y'
	shared.Stderr[0] = "source-mutated"
	if !slices.Equal(second.Command, []string{"tmux", "display-message"}) ||
		!slices.Equal(second.Stdout, []string{"output"}) ||
		!slices.Equal(second.RawStdout, []byte("output\n")) ||
		!slices.Equal(second.Stderr, []string{"warning"}) {
		t.Fatalf("second result aliases first or runner storage: %#v", second)
	}

	source := CommandResult{
		Command:   []string{"tmux", "list-sessions"},
		Stdout:    []string{"partial"},
		RawStdout: []byte("partial\n"),
		Stderr:    []string{"no server running"},
		ExitCode:  1,
	}
	commandError := newCommandError("list-sessions", source)
	source.Command[0] = "mutated"
	source.Stdout[0] = "mutated"
	source.RawStdout[0] = 'X'
	source.Stderr[0] = "mutated"
	source.ExitCode = 0
	if !slices.Equal(commandError.Result.Command, []string{"tmux", "list-sessions"}) ||
		!slices.Equal(commandError.Result.Stdout, []string{"partial"}) ||
		!slices.Equal(commandError.Result.RawStdout, []byte("partial\n")) ||
		!slices.Equal(commandError.Result.Stderr, []string{"no server running"}) ||
		commandError.Result.ExitCode != 1 {
		t.Fatalf("CommandError result = %#v, want independent completed result", commandError.Result)
	}
	if !strings.Contains(commandError.Error(), "no server running") {
		t.Fatalf("CommandError.Error() = %q, want tmux diagnostic", commandError.Error())
	}
}

func TestRedactedCommandErrorOmitsCompletedResult(t *testing.T) {
	t.Parallel()

	const secret = "command-error-secret"
	err := newRedactedCommandError("set-environment", CommandResult{
		Command:   []string{"tmux", "set-environment", "TOKEN", secret},
		Stdout:    []string{"stdout " + secret},
		RawStdout: []byte("stdout " + secret),
		Stderr:    []string{"stderr " + secret},
		ExitCode:  7,
	})
	if err.Result.ExitCode != 7 || err.Result.Command != nil ||
		err.Result.Stdout != nil || err.Result.RawStdout != nil || err.Result.Stderr != nil {
		t.Fatalf("CommandError result = %#v, want exit-code-only result", err.Result)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("CommandError disclosed completed command details: %v", err)
	}
}

func assertExitOnlyCommandErrorRedacts(
	t *testing.T,
	err error,
	subcommand string,
	exitCode int,
	secret string,
) {
	t.Helper()

	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != subcommand {
		t.Fatalf("operation error = %#v, want %s CommandError", err, subcommand)
	}
	if commandError.Result.ExitCode != exitCode || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.RawStdout != nil ||
		commandError.Result.Stderr != nil {
		t.Fatalf("CommandError result = %#v, want exit-code-only result", commandError.Result)
	}
	assertErrorGraphRedacts(t, err, secret)
}

func TestServerCommandHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator == -1 {
		return
	}
	if separator+1 >= len(os.Args) {
		t.Fatal("helper mode is missing")
	}

	switch os.Args[separator+1] {
	case "failure":
		_, _ = fmt.Fprintln(os.Stdout, "output")
		_, _ = fmt.Fprintln(os.Stderr, "failure")
		os.Exit(9)
	case "raw-output":
		_, _ = os.Stdout.Write([]byte{0xff, 'A', '\n', '\n'})
		os.Exit(0)
	case "environment":
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("TMUX_GO_TEST"))
		os.Exit(0)
	case "echo":
		if separator+2 >= len(os.Args) {
			t.Fatal("echo helper value is missing")
		}
		_, _ = fmt.Fprintln(os.Stdout, os.Args[separator+2])
		os.Exit(0)
	case "suite-environment":
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("TMPDIR"))
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("GOTMPDIR"))
		os.Exit(0)
	case "block":
		select {}
	default:
		t.Fatalf("unknown helper mode %q", os.Args[separator+1])
	}
}

func TestCommandTargetNotFoundMatchesTmuxResolutionFailures(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		stderr []string
		want   bool
	}{
		{
			name:   "session with target",
			stderr: []string{"can't find session: $99"},
			want:   true,
		},
		{
			name:   "window with target",
			stderr: []string{"can't find window: @99"},
			want:   true,
		},
		{
			name:   "pane with target",
			stderr: []string{"can't find pane: %99"},
			want:   true,
		},
		{
			name:   "client with target",
			stderr: []string{"can't find client: /dev/pts/9"},
			want:   true,
		},
		{
			name:   "object without a target",
			stderr: []string{"can't find pane"},
			want:   true,
		},
		{
			name:   "missing terminfo database is not a target failure",
			stderr: []string{"can't find terminfo database"},
			want:   false,
		},
		{
			name:   "exhausted session list is not a target failure",
			stderr: []string{"can't find next session"},
			want:   false,
		},
		{
			name:   "unrelated failure",
			stderr: []string{"no server running"},
			want:   false,
		},
		{
			name:   "no output",
			stderr: nil,
			want:   false,
		},
	} {
		if got := commandTargetNotFound(testCase.stderr); got != testCase.want {
			t.Errorf("%s: commandTargetNotFound(%q) = %t, want %t",
				testCase.name, testCase.stderr, got, testCase.want)
		}
	}
}

func TestServerUnreachableDiagnosisSurvivesRedaction(t *testing.T) {
	t.Parallel()
	unreachable := CommandResult{
		ExitCode: 1,
		Stderr:   []string{"error connecting to /run/tmux/x.sock (No such file or directory)"},
	}
	err := newRedactedCommandError("show-environment", unreachable)
	if !strings.Contains(err.Error(), "error connecting to") {
		t.Errorf("redacted error for an unreachable server = %q, want tmux's diagnosis", err)
	}

	noDaemon := CommandResult{ExitCode: 1, Stderr: []string{"no server running on /run/tmux/x.sock"}}
	if err := newRedactedCommandError("show-buffer", noDaemon); !strings.Contains(
		err.Error(), "no server running on",
	) {
		t.Errorf("redacted error for a missing daemon = %q, want tmux's diagnosis", err)
	}

	// tmux exits 0 after failing to create a socket, so without its message
	// this failure renders as a command that succeeded.
	noSocket := CommandResult{
		ExitCode: 0,
		Stderr:   []string{"error creating /run/tmux/x.sock (No such file or directory)"},
	}
	if err := newRedactedCommandError("new-session", noSocket); !strings.Contains(
		err.Error(), "error creating",
	) {
		t.Errorf("redacted error for an uncreatable socket = %q, want tmux's diagnosis", err)
	}
}

func TestRedactionStillWithholdsValueBearingOutput(t *testing.T) {
	t.Parallel()
	// A failure that reached tmux may quote the caller's value back, so the
	// carve-out above must not apply to it.
	secretBearing := CommandResult{
		ExitCode: 1,
		Stderr:   []string{"value too long: s3cr3t-passphrase"},
	}
	err := newRedactedCommandError("set-environment", secretBearing)
	if strings.Contains(err.Error(), "s3cr3t-passphrase") {
		t.Fatalf("redacted error disclosed the value: %q", err)
	}
	if !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("redacted error = %q, want the exit code", err)
	}
}

func TestServerUnreachableMatchesOnlyPreConnectionFailures(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		"no server running on /run/tmux/x.sock":                        true,
		"error connecting to /run/tmux/x.sock (Permission denied)":     true,
		"error creating /run/tmux/x.sock (No such file or directory)":  true,
		"can't find session: $9":                                       false,
		"invalid option: not-a-real-option":                            false,
		"the words no server running on appear mid-sentence elsewhere": false,
	} {
		if got := commandServerUnreachable([]string{line}); got != want {
			t.Errorf("commandServerUnreachable(%q) = %t, want %t", line, got, want)
		}
	}
}

// TestAFixedRefusalIsDisclosed covers a refusal tmux writes as a literal.
// Redaction exists for values a caller supplied and a message with nothing
// interpolated has none, so withholding it leaves an exit code where an
// actionable reason would fit.
func TestAFixedRefusalIsDisclosed(t *testing.T) {
	t.Parallel()
	noRoom := CommandResult{
		ExitCode: 1,
		Stderr:   []string{"size or position no space for a new pane"},
	}
	if err := newRedactedCommandError("split-window", noRoom); !strings.Contains(
		err.Error(), "no space for a new pane",
	) {
		t.Errorf("redacted split error = %q, want tmux's reason", err)
	}

	// A message that could carry a caller's value stays withheld.
	valueBearing := CommandResult{ExitCode: 1, Stderr: []string{"bad value: s3cr3t"}}
	if err := newRedactedCommandError("split-window", valueBearing); strings.Contains(
		err.Error(), "s3cr3t",
	) {
		t.Errorf("redacted error disclosed a value: %q", err)
	}
}
