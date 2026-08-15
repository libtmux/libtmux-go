package tmux

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.bind_key
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:note:f3fa07adb3fd
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:repeat:f439cc0551df
// libtmux:parity libtmux.server.Server.list_keys
// libtmux:parity libtmux.server.Server.list_keys#parameter-branch:format_:b99004c71fa4
// libtmux:parity libtmux.server.Server.list_keys#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.list_keys#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.list_keys#warning:afabcd354447
// libtmux:parity libtmux.server.Server.unbind_key
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:all_keys:74326c3756f4
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:key:c85749129f8e
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:quiet:8573bc8befe4
func TestServerKeyCommandsBuildExactArguments(t *testing.T) {
	t.Parallel()

	keyTable := "root"
	note := "phase 6 binding"
	tests := []struct {
		name      string
		operation func(Server) error
		want      []string
	}{
		{
			name: "bind",
			operation: func(server Server) error {
				return server.BindKey(context.Background(), BindKeyRequest{
					Key:      "F12",
					Command:  "display-message -p bound",
					KeyTable: &keyTable,
					Note:     &note,
					Repeat:   true,
				})
			},
			want: []string{
				"bind-key", "-r", "-N", "phase 6 binding", "-T", "root",
				"F12", "display-message -p bound",
			},
		},
		{
			name: "bind empty no-op command",
			operation: func(server Server) error {
				return server.BindKey(context.Background(), BindKeyRequest{
					Key: "F11",
				})
			},
			want: []string{"bind-key", "F11", ""},
		},
		{
			name: "unbind one",
			operation: func(server Server) error {
				key := "F12"
				return server.UnbindKey(context.Background(), UnbindKeyRequest{
					Key:      &key,
					KeyTable: &keyTable,
					Quiet:    true,
				})
			},
			want: []string{"unbind-key", "-q", "-T", "root", "F12"},
		},
		{
			name: "unbind all",
			operation: func(server Server) error {
				return server.UnbindKey(context.Background(), UnbindKeyRequest{
					AllKeys:  true,
					KeyTable: &keyTable,
					Quiet:    true,
				})
			},
			want: []string{"unbind-key", "-a", "-q", "-T", "root"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			assertServerKeysArguments(t, runner, test.want)
		})
	}
}

// libtmux:parity libtmux.server.Server.list_clients
// libtmux:parity libtmux.server.Server.list_commands
// libtmux:parity libtmux.server.Server.list_commands#parameter-branch:command_name:a5ecee67e253
// libtmux:parity libtmux.server.Server.show_messages
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:jobs:674b7b889698
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:terminals:d731ae9a0080
func TestServerListingsBuildExactArgumentsAndOwnOutput(t *testing.T) {
	t.Parallel()

	keyTable := "root"
	commandName := "send-keys"
	targetClient := ClientName("/dev/pts/9")
	tests := []struct {
		name      string
		operation func(Server) ([]string, error)
		want      []string
	}{
		{
			name: "list keys",
			operation: func(server Server) ([]string, error) {
				return server.ListKeys(context.Background(), ListKeysRequest{KeyTable: &keyTable})
			},
			want: []string{"list-keys", "-T", "root"},
		},
		{
			name: "list commands",
			operation: func(server Server) ([]string, error) {
				return server.ListCommands(
					context.Background(),
					ListCommandsRequest{CommandName: &commandName},
				)
			},
			want: []string{"list-commands", "send-keys"},
		},
		{
			name: "list clients",
			operation: func(server Server) ([]string, error) {
				return server.ListClients(context.Background())
			},
			want: []string{"list-clients"},
		},
		{
			name: "show messages",
			operation: func(server Server) ([]string, error) {
				return server.ShowMessages(context.Background(), ShowMessagesRequest{
					TargetClient: targetClient,
					Terminals:    true,
					Jobs:         true,
				})
			},
			want: []string{"show-messages", "-T", "-J", "-t", "/dev/pts/9"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := []string{"first", "second"}
			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: source},
			}}}
			output, err := test.operation(serverWithRunner(runner))
			if err != nil {
				t.Fatalf("operation error = %v", err)
			}
			if !slices.Equal(output, []string{"first", "second"}) {
				t.Fatalf("output = %#v, want owned source lines", output)
			}
			output[0] = "mutated"
			if source[0] != "first" {
				t.Fatalf("output aliases runner stdout: %#v", source)
			}
			assertServerKeysArguments(t, runner, test.want)
		})
	}
}

func TestListKeysUsesFormatAt37(t *testing.T) {
	t.Parallel()

	keyTable := "root"
	format := "#{key_string}:#{key_command}"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}},
		{result: tmuxcmd.Result{Stdout: []string{"F12:display-message bound"}}},
	}}
	server := serverWithRunner(runner)

	output, err := server.ListKeys(context.Background(), ListKeysRequest{
		KeyTable: &keyTable,
		Format:   &format,
	})
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if !slices.Equal(output, []string{"F12:display-message bound"}) {
		t.Fatalf("ListKeys() = %#v, want formatted line", output)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and list-keys", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"list-keys", "-T", "root", "-F", "#{key_string}:#{key_command}",
	})
}

func TestListKeysWarnsAndOmitsFormatBefore37(t *testing.T) {
	t.Parallel()

	keyTable := "root"
	format := "#{key_string}"
	var warnings []Warning
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{Stdout: []string{"bind-key -T root F12"}}},
	}}
	server := serverWithRunner(runner)
	server.connectionState().options.WarningHandler = func(warning Warning) {
		warnings = append(warnings, warning)
	}

	output, err := server.ListKeys(context.Background(), ListKeysRequest{
		KeyTable: &keyTable,
		Format:   &format,
	})
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if !slices.Equal(output, []string{"bind-key -T root F12"}) {
		t.Fatalf("ListKeys() = %#v, want legacy output", output)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and reduced list-keys", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{"list-keys", "-T", "root"})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one", warnings)
	}
	warning := warnings[0]
	if warning.Kind != WarningUnsupportedFeature ||
		warning.Subcommand != "list-keys" ||
		warning.Feature != "format" ||
		warning.CurrentVersion.String() != "3.6" ||
		warning.RequiredVersion.String() != "3.7" {
		t.Fatalf("warning = %#v, want list-keys format minimum 3.7", warning)
	}
}

func TestListKeysCapturesPointerValuesBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newListKeysGateRunner()
	server := serverWithRunner(runner)
	keyTable := "before-table"
	format := "before-format"
	response := make(chan error, 1)
	go func() {
		_, err := server.ListKeys(context.Background(), ListKeysRequest{
			KeyTable: &keyTable,
			Format:   &format,
		})
		response <- err
	}()

	<-runner.versionStarted
	keyTable = "after-table"
	format = "after-format"
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and list-keys", requests)
	}
	assertRequestArguments(t, requests[1], []string{
		"list-keys", "-T", "before-table", "-F", "before-format",
	})
}

func TestListKeysFormatVersionProbeFollowsListPolicy(t *testing.T) {
	t.Parallel()

	format := "#{key_string}"
	tests := []struct {
		name         string
		response     versionResponse
		strict       bool
		wantEmpty    bool
		wantAnyError bool
		wantError    error
	}{
		{
			name:      "lenient transport failure",
			response:  versionResponse{err: errors.New("version transport failed")},
			wantEmpty: true,
		},
		{
			name: "lenient completed failure",
			response: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"version command failed"}, ExitCode: 1,
			}},
			wantEmpty: true,
		},
		{
			name:         "strict transport failure",
			response:     versionResponse{err: errors.New("version transport failed")},
			strict:       true,
			wantAnyError: true,
		},
		{
			name:      "context failure",
			response:  versionResponse{err: context.DeadlineExceeded},
			wantError: context.DeadlineExceeded,
		},
		{
			name: "malformed successful output",
			response: versionResponse{result: tmuxcmd.Result{
				Stdout: []string{"not a tmux version"},
			}},
			wantError: ErrVersionQuery,
		},
		{
			name: "invalid version token",
			response: versionResponse{result: tmuxcmd.Result{
				Stdout: []string{"tmux invalid!"},
			}},
			wantError: ErrVersionQuery,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{test.response}}
			server := serverWithRunner(runner)
			if test.strict {
				server = server.WithStrictErrors()
			}
			output, err := server.ListKeys(
				context.Background(),
				ListKeysRequest{Format: &format},
			)
			if test.wantEmpty {
				if err != nil || output == nil || len(output) != 0 {
					t.Fatalf("ListKeys() = (%#v, %v), want lenient nonnil empty", output, err)
				}
			} else if test.wantAnyError {
				if err == nil {
					t.Fatal("ListKeys() error = nil, want strict transport error")
				}
			} else if !errors.Is(err, test.wantError) {
				t.Fatalf("ListKeys() error = %v, want %v", err, test.wantError)
			}
			if runner.callCount() != 1 {
				t.Fatalf("runner calls = %d, want only version probe", runner.callCount())
			}
		})
	}
}

func TestServerKeyRequestsValidateBeforeExecution(t *testing.T) {
	t.Parallel()

	key := "F12"
	empty := ""
	tests := []struct {
		name      string
		operation func(Server) error
		wantField string
	}{
		{
			name: "bind missing key",
			operation: func(server Server) error {
				return server.BindKey(context.Background(), BindKeyRequest{Command: "display-message ok"})
			},
			wantField: "Key",
		},
		{
			name: "unbind missing selection",
			operation: func(server Server) error {
				return server.UnbindKey(context.Background(), UnbindKeyRequest{})
			},
			wantField: "Key",
		},
		{
			name: "unbind key and all",
			operation: func(server Server) error {
				return server.UnbindKey(context.Background(), UnbindKeyRequest{
					Key:     &key,
					AllKeys: true,
				})
			},
			wantField: "Key",
		},
		{
			name: "unbind empty key",
			operation: func(server Server) error {
				return server.UnbindKey(context.Background(), UnbindKeyRequest{Key: &empty})
			},
			wantField: "Key",
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
			var requestError *ServerCommandRequestError
			if !errors.As(err, &requestError) || requestError.Field != test.wantField {
				t.Fatalf("operation error = %#v, want field %s", err, test.wantField)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
			}
		})
	}
}

func TestShowMessagesZeroTargetOmitsClientFlag(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	_, err := serverWithRunner(runner).ShowMessages(
		context.Background(),
		ShowMessagesRequest{},
	)
	if err != nil {
		t.Fatalf("ShowMessages() error = %v", err)
	}
	assertServerKeysArguments(t, runner, []string{"show-messages"})
}

func TestServerKeyMethodsAreStderrStrict(t *testing.T) {
	t.Parallel()

	key := "F12"
	tests := serverPointKeyOperations(key)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stdout:   []string{"partial"},
				Stderr:   []string{"failure"},
				ExitCode: 0,
			}}}}
			_, err := test.operation(serverWithRunner(runner))
			var commandError *CommandError
			if !errors.As(err, &commandError) || commandError.Subcommand != test.subcommand {
				t.Fatalf("operation error = %#v, want %s CommandError", err, test.subcommand)
			}
			if commandError.Result.ExitCode != 0 || commandError.Result.Command != nil ||
				commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
				t.Fatalf("CommandError result = %#v, want exit-code-only result", commandError.Result)
			}
		})
	}
}

func TestServerKeyMethodsKeepExitOnlyFailureAsData(t *testing.T) {
	t.Parallel()

	key := "F12"
	for _, test := range serverPointKeyOperations(key) {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stdout:   []string{"completed output"},
				ExitCode: 7,
			}}}}
			_, err := test.operation(serverWithRunner(runner))
			if err != nil {
				t.Fatalf("operation error = %v, want nil for empty stderr", err)
			}
		})
	}
}

func TestServerListingsFollowLenientAndStrictPolicies(t *testing.T) {
	t.Parallel()

	for _, test := range serverListingOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			failedResult := tmuxcmd.Result{
				Stdout:   []string{"partial"},
				Stderr:   []string{"failure"},
				ExitCode: 1,
			}
			lenientRunner := &versionQueueRunner{responses: []versionResponse{{
				result: failedResult,
			}}}
			output, err := test.operation(serverWithRunner(lenientRunner))
			if err != nil || output == nil || len(output) != 0 {
				t.Fatalf("lenient operation = (%#v, %v), want nonnil empty", output, err)
			}

			strictRunner := &versionQueueRunner{responses: []versionResponse{{
				result: failedResult,
			}}}
			_, err = test.operation(serverWithRunner(strictRunner).WithStrictErrors())
			var commandError *CommandError
			if !errors.As(err, &commandError) || commandError.Subcommand != test.subcommand {
				t.Fatalf("strict operation error = %#v, want %s CommandError", err, test.subcommand)
			}
		})
	}
}

type serverKeyOperation struct {
	name       string
	subcommand string
	operation  func(Server) ([]string, error)
}

func serverPointKeyOperations(key string) []serverKeyOperation {
	return []serverKeyOperation{
		{
			name:       "bind key",
			subcommand: "bind-key",
			operation: func(server Server) ([]string, error) {
				return nil, server.BindKey(context.Background(), BindKeyRequest{
					Key:     "F12",
					Command: "display-message ok",
				})
			},
		},
		{
			name:       "unbind key",
			subcommand: "unbind-key",
			operation: func(server Server) ([]string, error) {
				return nil, server.UnbindKey(
					context.Background(),
					UnbindKeyRequest{Key: &key},
				)
			},
		},
	}
}

func serverListingOperations() []serverKeyOperation {
	return []serverKeyOperation{
		{
			name:       "list keys",
			subcommand: "list-keys",
			operation: func(server Server) ([]string, error) {
				return server.ListKeys(context.Background(), ListKeysRequest{})
			},
		},
		{
			name:       "list commands",
			subcommand: "list-commands",
			operation: func(server Server) ([]string, error) {
				return server.ListCommands(context.Background(), ListCommandsRequest{})
			},
		},
		{
			name:       "list clients",
			subcommand: "list-clients",
			operation: func(server Server) ([]string, error) {
				return server.ListClients(context.Background())
			},
		},
		{
			name:       "show messages",
			subcommand: "show-messages",
			operation: func(server Server) ([]string, error) {
				return server.ShowMessages(context.Background(), ShowMessagesRequest{Jobs: true})
			},
		},
	}
}

func assertServerKeysArguments(t *testing.T, runner *versionQueueRunner, want []string) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one", requests)
	}
	assertRequestArguments(t, requests[0], want)
}

type listKeysGateRunner struct {
	versionStarted chan struct{}
	releaseVersion chan struct{}

	mu       sync.Mutex
	requests []tmuxcmd.Request
}

func newListKeysGateRunner() *listKeysGateRunner {
	return &listKeysGateRunner{
		versionStarted: make(chan struct{}),
		releaseVersion: make(chan struct{}),
	}
}

func (r *listKeysGateRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
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

func (r *listKeysGateRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}
