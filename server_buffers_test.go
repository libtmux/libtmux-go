package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.delete_buffer
// libtmux:parity libtmux.server.Server.delete_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.list_buffers
// libtmux:parity libtmux.server.Server.list_buffers#parameter-branch:filter:dad5b2f428ff
// libtmux:parity libtmux.server.Server.list_buffers#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.server.Server.load_buffer
// libtmux:parity libtmux.server.Server.load_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.save_buffer
// libtmux:parity libtmux.server.Server.save_buffer#parameter-branch:append:03665a1a84bd
// libtmux:parity libtmux.server.Server.save_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.set_buffer
// libtmux:parity libtmux.server.Server.set_buffer#parameter-branch:append:03665a1a84bd
// libtmux:parity libtmux.server.Server.set_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.show_buffer
// libtmux:parity libtmux.server.Server.show_buffer#parameter-branch:buffer_name:5c7057988ea3
func TestServerBufferCommandsBuildExactArguments(t *testing.T) {
	t.Parallel()

	name := "named"
	format := "#{buffer_name}:#{buffer_size}"
	filter := TmuxFilter("#{m:named,#{buffer_name}}")
	tests := []struct {
		name      string
		operation func(Server) error
		response  tmuxcmd.Result
		want      []string
	}{
		{
			name: "set",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{
					Data: "-literal", Name: &name, Append: true,
				})
			},
			want: []string{"set-buffer", "-a", "-b", "named", "--", "-literal"},
		},
		{
			name: "show",
			operation: func(server Server) error {
				_, err := server.ShowBuffer(context.Background(), &name)
				return err
			},
			response: tmuxcmd.Result{Stdout: []string{"one", "two"}},
			want:     []string{"show-buffer", "-b", "named"},
		},
		{
			name: "delete",
			operation: func(server Server) error {
				return server.DeleteBuffer(context.Background(), &name)
			},
			want: []string{"delete-buffer", "-b", "named"},
		},
		{
			name: "save",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{
					Path: "relative", Name: &name, Append: true,
				})
			},
			want: []string{"save-buffer", "-a", "-b", "named", "--", "relative"},
		},
		{
			name: "load",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{
					Path: "relative", Name: &name,
				})
			},
			want: []string{"load-buffer", "-b", "named", "--", "relative"},
		},
		{
			name: "list",
			operation: func(server Server) error {
				_, err := server.ListBuffers(context.Background(), ListBuffersRequest{
					Format: &format, Filter: &filter,
				})
				return err
			},
			response: tmuxcmd.Result{Stdout: []string{"named:7"}},
			want: []string{
				"list-buffers", "-F", "#{buffer_name}:#{buffer_size}",
				"-f", "#{m:named,#{buffer_name}}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: test.response}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one", requests)
			}
			assertRequestArguments(t, requests[0], test.want)
		})
	}
}

func TestServerBufferCommandsEscapeTerminalSemicolonArguments(t *testing.T) {
	t.Parallel()

	name := "named;"
	format := "#{buffer_name};"
	filter := TmuxFilter("#{m:named,#{buffer_name}};")
	tests := []struct {
		name      string
		operation func(Server) error
		want      []string
	}{
		{
			name: "set name and data",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{
					Data: "value;", Name: &name,
				})
			},
			want: []string{"set-buffer", "-b", `named\;`, "--", `value\;`},
		},
		{
			name: "set preserves an existing backslash",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{Data: `value\;`})
			},
			want: []string{"set-buffer", "--", `value\\;`},
		},
		{
			name: "set bare separator",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{Data: ";"})
			},
			want: []string{"set-buffer", "--", `\;`},
		},
		{
			name: "set escapes only final separator",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{Data: "value;;"})
			},
			want: []string{"set-buffer", "--", `value;\;`},
		},
		{
			name: "show name",
			operation: func(server Server) error {
				_, err := server.ShowBuffer(context.Background(), &name)
				return err
			},
			want: []string{"show-buffer", "-b", `named\;`},
		},
		{
			name: "delete name",
			operation: func(server Server) error {
				return server.DeleteBuffer(context.Background(), &name)
			},
			want: []string{"delete-buffer", "-b", `named\;`},
		},
		{
			name: "save name and path",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{
					Path: "saved;", Name: &name,
				})
			},
			want: []string{"save-buffer", "-b", `named\;`, "--", `saved\;`},
		},
		{
			name: "load name and path",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{
					Path: "loaded;", Name: &name,
				})
			},
			want: []string{"load-buffer", "-b", `named\;`, "--", `loaded\;`},
		},
		{
			name: "list format and filter",
			operation: func(server Server) error {
				_, err := server.ListBuffers(context.Background(), ListBuffersRequest{
					Format: &format, Filter: &filter,
				})
				return err
			},
			want: []string{
				"list-buffers", "-F", `#{buffer_name}\;`,
				"-f", `#{m:named,#{buffer_name}}\;`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			assertServerBufferArguments(t, runner, test.want)
		})
	}
}

func TestShowBufferJoinsPythonCompatibleLines(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stdout: []string{"one", "two", ""}, ExitCode: 0,
	}}}}
	got, err := serverWithRunner(runner).ShowBuffer(context.Background(), nil)
	if err != nil {
		t.Fatalf("ShowBuffer() error = %v", err)
	}
	if got != "one\ntwo\n" {
		t.Fatalf("ShowBuffer() = %q, want joined lines", got)
	}
	assertServerBufferArguments(t, runner, []string{"show-buffer"})
}

func TestShowBufferBytesReturnsExactOwnedOutput(t *testing.T) {
	t.Parallel()

	source := []byte{0xff, 'A', '\n', '\n'}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stdout:    []string{`\xffA`},
		RawStdout: source,
		ExitCode:  0,
	}}}}
	got, err := serverWithRunner(runner).ShowBufferBytes(context.Background(), nil)
	if err != nil {
		t.Fatalf("ShowBufferBytes() error = %v", err)
	}
	want := []byte{0xff, 'A', '\n', '\n'}
	if !slices.Equal(got, want) {
		t.Fatalf("ShowBufferBytes() = %q, want %q", got, want)
	}
	got[0] = 'X'
	if source[0] != 0xff {
		t.Fatalf("ShowBufferBytes() aliases runner storage: %q", source)
	}
	assertServerBufferArguments(t, runner, []string{"show-buffer"})
}

func TestShowBufferBytesFailureDoesNotReturnData(t *testing.T) {
	t.Parallel()

	const secret = "clipboard-secret"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		RawStdout: []byte(secret),
		Stderr:    []string{"failed: " + secret},
		ExitCode:  1,
	}}}}
	output, err := serverWithRunner(runner).ShowBufferBytes(context.Background(), nil)
	if output != nil {
		t.Fatalf("ShowBufferBytes() output = %q, want nil on failure", output)
	}
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("ShowBufferBytes() error = %v, want *CommandError", err)
	}
	if commandError.Result.ExitCode != 1 || commandError.Result.RawStdout != nil ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("ShowBufferBytes() retained secret: %#v", commandError)
	}
}

func TestServerBufferPathsExpandAndRejectStdinBeforeExecution(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tildeLexicalPath := home
	if !os.IsPathSeparator(home[len(home)-1]) {
		tildeLexicalPath += string(os.PathSeparator)
	}
	tildeLexicalPath += filepath.FromSlash("link/../saved")
	for _, test := range []struct {
		name      string
		operation func(Server) error
		want      []string
	}{
		{
			name: "save current user",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{Path: "~/saved"})
			},
			want: []string{"save-buffer", "--", filepath.Join(home, "saved")},
		},
		{
			name: "load current user",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{Path: "~"})
			},
			want: []string{"load-buffer", "--", home},
		},
		{
			name: "save current user preserves lexical components",
			operation: func(server Server) error {
				return server.SaveBuffer(
					context.Background(),
					SaveBufferRequest{Path: "~/link/../saved"},
				)
			},
			want: []string{"save-buffer", "--", tildeLexicalPath},
		},
		{
			name: "save preserves lexical components",
			operation: func(server Server) error {
				return server.SaveBuffer(
					context.Background(),
					SaveBufferRequest{Path: "link/../saved"},
				)
			},
			want: []string{"save-buffer", "--", "link/../saved"},
		},
		{
			name: "save preserves dash filename",
			operation: func(server Server) error {
				return server.SaveBuffer(
					context.Background(),
					SaveBufferRequest{Path: "./-"},
				)
			},
			want: []string{"save-buffer", "--", "." + string(os.PathSeparator) + "-"},
		},
		{
			name: "load preserves lexical components",
			operation: func(server Server) error {
				return server.LoadBuffer(
					context.Background(),
					LoadBufferRequest{Path: "link/../loaded"},
				)
			},
			want: []string{"load-buffer", "--", "link/../loaded"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			assertServerBufferArguments(t, runner, test.want)
		})
	}

	for _, test := range []struct {
		name      string
		operation func(Server) error
	}{
		{
			name: "save stdin path",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{Path: "-"})
			},
		},
		{
			name: "empty save path",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{})
			},
		},
		{
			name: "load stdin path",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{Path: "-"})
			},
		},
		{
			name: "named user path",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{Path: "~other/value"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.operation(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want zero", runner.callCount())
			}
		})
	}
}

func TestServerBufferPointOperationsUseStderrOnlyFailurePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		operation func(Server) error
	}{
		{
			name: "set",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{Data: "value"})
			},
		},
		{
			name: "show",
			operation: func(server Server) error {
				_, err := server.ShowBuffer(context.Background(), nil)
				return err
			},
		},
		{
			name: "delete",
			operation: func(server Server) error {
				return server.DeleteBuffer(context.Background(), nil)
			},
		},
		{
			name: "save",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{Path: "value"})
			},
		},
		{
			name: "load",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{Path: "value"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stderrRunner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"buffer failure"}, ExitCode: 0,
			}}}}
			err := test.operation(serverWithRunner(stderrRunner))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("stderr operation error = %v, want ErrCommand", err)
			}

			exitRunner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				ExitCode: 7,
			}}}}
			if err := test.operation(serverWithRunner(exitRunner)); err != nil {
				t.Fatalf("exit-only operation error = %v, want nil", err)
			}
		})
	}
}

func TestSetBufferFailureDoesNotRetainData(t *testing.T) {
	t.Parallel()

	const secret = "clipboard-secret"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:  []string{"tmux", "set-buffer", "--", secret},
		Stdout:   []string{secret},
		Stderr:   []string{"failed: " + secret},
		ExitCode: 1,
	}}}}
	err := serverWithRunner(runner).SetBuffer(
		context.Background(),
		SetBufferRequest{Data: secret},
	)
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("SetBuffer() error = %v, want *CommandError", err)
	}
	if commandError.Result.ExitCode != 1 ||
		commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil ||
		commandError.Result.Stderr != nil ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("SetBuffer() retained secret: %#v", commandError)
	}
}

func TestShowBufferFailureDoesNotRetainData(t *testing.T) {
	t.Parallel()

	const secret = "clipboard-secret"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:   []string{"tmux", "show-buffer"},
		Stdout:    []string{secret},
		RawStdout: []byte(secret),
		Stderr:    []string{"failed: " + secret},
		ExitCode:  1,
	}}}}
	_, err := serverWithRunner(runner).ShowBuffer(context.Background(), nil)
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("ShowBuffer() error = %v, want *CommandError", err)
	}
	if commandError.Result.ExitCode != 1 ||
		commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil ||
		commandError.Result.RawStdout != nil ||
		commandError.Result.Stderr != nil ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("ShowBuffer() retained secret: %#v", commandError)
	}
}

func TestListBuffersFailureDoesNotRetainData(t *testing.T) {
	t.Parallel()

	const secret = "clipboard-secret"
	format := "#{buffer_sample}"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:  []string{"tmux", "list-buffers", "-F", format},
		Stdout:   []string{secret},
		Stderr:   []string{"failed: " + secret},
		ExitCode: 1,
	}}}}
	_, err := serverWithRunner(runner).WithStrictErrors().ListBuffers(
		context.Background(),
		ListBuffersRequest{Format: &format},
	)
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("ListBuffers() error = %v, want *CommandError", err)
	}
	if commandError.Result.ExitCode != 1 ||
		commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil ||
		commandError.Result.Stderr != nil ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("ListBuffers() retained secret: %#v", commandError)
	}
}

func TestServerBufferCommandsRejectNULBeforeExecution(t *testing.T) {
	t.Parallel()

	nulName := "name\x00suffix"
	nulFormat := "#{buffer_name}\x00"
	nulFilter := TmuxFilter("#{==:1,1}\x00")
	tests := []struct {
		name      string
		operation func(Server) error
		secret    string
	}{
		{
			name: "set data",
			operation: func(server Server) error {
				return server.SetBuffer(
					context.Background(),
					SetBufferRequest{Data: "clipboard-secret\x00suffix"},
				)
			},
			secret: "clipboard-secret",
		},
		{
			name: "set name",
			operation: func(server Server) error {
				return server.SetBuffer(context.Background(), SetBufferRequest{Name: &nulName})
			},
		},
		{
			name: "show name",
			operation: func(server Server) error {
				_, err := server.ShowBuffer(context.Background(), &nulName)
				return err
			},
		},
		{
			name: "delete name",
			operation: func(server Server) error {
				return server.DeleteBuffer(context.Background(), &nulName)
			},
		},
		{
			name: "save path",
			operation: func(server Server) error {
				return server.SaveBuffer(context.Background(), SaveBufferRequest{Path: "path\x00suffix"})
			},
		},
		{
			name: "load path",
			operation: func(server Server) error {
				return server.LoadBuffer(context.Background(), LoadBufferRequest{Path: "path\x00suffix"})
			},
		},
		{
			name: "list format",
			operation: func(server Server) error {
				_, err := server.ListBuffers(context.Background(), ListBuffersRequest{Format: &nulFormat})
				return err
			},
		},
		{
			name: "list filter",
			operation: func(server Server) error {
				_, err := server.ListBuffers(context.Background(), ListBuffersRequest{Filter: &nulFilter})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.operation(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatalf("operation error retained secret: %v", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestListBuffersNormalizesFailuresAndOwnsResults(t *testing.T) {
	t.Parallel()

	source := []string{"one", "two"}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: source, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{"partial"}, Stderr: []string{"failed"}, ExitCode: 1}},
		{result: tmuxcmd.Result{Stdout: []string{"partial"}, Stderr: []string{"failed"}, ExitCode: 1}},
		{err: errors.New("transport")},
		{err: context.DeadlineExceeded},
	}}
	server := serverWithRunner(runner)

	got, err := server.ListBuffers(context.Background(), ListBuffersRequest{})
	if err != nil || !slices.Equal(got, source) {
		t.Fatalf("ListBuffers() = (%#v, %v), want source", got, err)
	}
	source[0] = "mutated"
	if got[0] != "one" {
		t.Fatalf("ListBuffers() aliases runner output: %#v", got)
	}

	got, err = server.ListBuffers(context.Background(), ListBuffersRequest{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("lenient completed failure = (%#v, %v), want nonnil empty", got, err)
	}
	if _, err := server.WithStrictErrors().ListBuffers(context.Background(), ListBuffersRequest{}); !errors.Is(err, ErrCommand) {
		t.Fatalf("strict completed failure error = %v, want ErrCommand", err)
	}
	got, err = server.ListBuffers(context.Background(), ListBuffersRequest{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("lenient transport failure = (%#v, %v), want nonnil empty", got, err)
	}
	if _, err := server.ListBuffers(context.Background(), ListBuffersRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context failure error = %v, want deadline", err)
	}
}

func TestServerListCommandsKeepLocalValidationErrorsLoud(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Colors: ColorMode(16)})
	got, err := server.ListBuffers(context.Background(), ListBuffersRequest{})
	if !errors.Is(err, ErrUnknownColor) {
		t.Fatalf("ListBuffers(invalid color) = (%#v, %v), want ErrUnknownColor", got, err)
	}
}

func assertServerBufferArguments(t *testing.T, runner *versionQueueRunner, want []string) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one", requests)
	}
	assertRequestArguments(t, requests[0], want)
}
