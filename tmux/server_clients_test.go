package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.server_access
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:allow:f3969308d5bd
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:deny:2eb790bb3e9a
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:list_access:f6e9907c043a
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:list_access:f6e9907c043a:2
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:read_only,write:14227e701e2d
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:read_only:e91b42a77c5e
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:write:fe54a3f721d4
// libtmux:parity libtmux.server.Server.server_access#version-branch:tmux-version:d9801479e597
func TestServerAccessBuildsValidTmuxArguments(t *testing.T) {
	t.Parallel()

	alice := "alice"
	bob := "bob"
	carol := "carol"
	dash := "-l"
	tests := []struct {
		name    string
		request ServerAccessRequest
		want    []string
	}{
		{
			name:    "allow read only",
			request: ServerAccessRequest{Allow: &alice, ReadOnly: true},
			want:    []string{"server-access", "-a", "-r", "--", "alice"},
		},
		{
			name:    "deny",
			request: ServerAccessRequest{Deny: &bob},
			want:    []string{"server-access", "-d", "--", "bob"},
		},
		{
			name:    "allow write",
			request: ServerAccessRequest{Allow: &carol, Write: true},
			want:    []string{"server-access", "-a", "-w", "--", "carol"},
		},
		{
			name:    "leading dash user",
			request: ServerAccessRequest{Allow: &dash},
			want:    []string{"server-access", "-a", "--", "-l"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
				{result: tmuxcmd.Result{Stdout: []string{"ignored mutation output"}}},
			}}
			output, err := serverWithRunner(runner).ServerAccess(
				context.Background(),
				test.request,
			)
			if err != nil || output != nil {
				t.Fatalf("ServerAccess() = (%#v, %v), want (nil, nil)", output, err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("runner requests = %#v, want version and server-access", requests)
			}
			assertRequestArguments(t, requests[0], []string{"-V"})
			assertRequestArguments(t, requests[1], test.want)
		})
	}
}

func TestServerAccessPlacesEveryFlagBeforeTheUser(t *testing.T) {
	t.Parallel()

	alice := "alice"
	bob := "bob"
	tests := []struct {
		name       string
		request    ServerAccessRequest
		result     tmuxcmd.Result
		wantArgs   []string
		wantOutput []string
	}{
		{
			name:     "deny before mode",
			request:  ServerAccessRequest{Deny: &bob, ReadOnly: true},
			wantArgs: []string{"server-access", "-d", "-r", "--", "bob"},
		},
		{
			name:       "allow before list and mode",
			request:    ServerAccessRequest{Allow: &alice, List: true, Write: true},
			result:     tmuxcmd.Result{Stdout: []string{"owner (W)"}},
			wantArgs:   []string{"server-access", "-a", "-l", "-w", "--", "alice"},
			wantOutput: []string{"owner (W)"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
				{result: test.result},
			}}
			output, err := serverWithRunner(runner).ServerAccess(
				context.Background(),
				test.request,
			)
			if err != nil || !slices.Equal(output, test.wantOutput) {
				t.Fatalf(
					"ServerAccess() = (%#v, %v), want (%#v, nil)",
					output,
					err,
					test.wantOutput,
				)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("runner requests = %#v, want version and server-access", requests)
			}
			assertRequestArguments(t, requests[0], []string{"-V"})
			assertRequestArguments(t, requests[1], test.wantArgs)
		})
	}
}

func TestServerAccessListOwnsOutput(t *testing.T) {
	t.Parallel()

	source := []string{"alice (R)", "bob (W)"}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{Stdout: source}},
	}}
	output, err := serverWithRunner(runner).ServerAccess(
		context.Background(),
		ServerAccessRequest{List: true},
	)
	if err != nil || !slices.Equal(output, []string{"alice (R)", "bob (W)"}) {
		t.Fatalf("ServerAccess(list) = (%#v, %v)", output, err)
	}
	output[0] = "mutated"
	if source[0] != "alice (R)" {
		t.Fatalf("ServerAccess(list) aliases runner output: %#v", source)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and server-access", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{"server-access", "-l"})
}

func TestServerAccessValidatesBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	empty := ""
	alice := "alice"
	bob := "bob"
	tests := []struct {
		name    string
		request ServerAccessRequest
	}{
		{name: "missing operation"},
		{name: "empty allow", request: ServerAccessRequest{Allow: &empty}},
		{name: "empty deny", request: ServerAccessRequest{Deny: &empty}},
		{
			name:    "allow and deny",
			request: ServerAccessRequest{Allow: &alice, Deny: &bob},
		},
		{
			name:    "read only and write",
			request: ServerAccessRequest{Allow: &alice, ReadOnly: true, Write: true},
		},
		{name: "read only without user", request: ServerAccessRequest{ReadOnly: true}},
		{name: "write without user", request: ServerAccessRequest{Write: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			_, err := serverWithRunner(runner).ServerAccess(
				context.Background(),
				test.request,
			)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("ServerAccess() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want validation before version probe", calls)
			}
		})
	}
}

func TestClientRequestsRejectNULBeforeExecutionOrVersionProbe(t *testing.T) {
	t.Parallel()

	secret := "secret\x00value"
	target := ClientName(secret)
	for _, test := range []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "server access user",
			run: func(server Server) error {
				_, err := server.ServerAccess(
					context.Background(),
					ServerAccessRequest{Allow: &secret},
				)
				return err
			},
		},
		{
			name: "refresh target",
			run: func(server Server) error {
				return server.RefreshClient(context.Background(), RefreshClientRequest{
					TargetClient:     target,
					RequestClipboard: true,
				})
			},
		},
		{
			name: "detach shell command",
			run: func(server Server) error {
				return server.DetachClient(context.Background(), DetachClientRequest{
					ShellCommand: &secret,
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("client request error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "value") {
				t.Fatalf("client request error retained argument: %v", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before version probe", runner.callCount())
			}
		})
	}
}

func TestServerAccessRequiresTmux33(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}},
	}}}
	_, err := serverWithRunner(runner).ServerAccess(
		context.Background(),
		ServerAccessRequest{List: true},
	)
	var tooLow *VersionTooLowError
	if !errors.As(err, &tooLow) || tooLow.Current.String() != "3.2a" ||
		tooLow.Minimum.String() != "3.3" {
		t.Fatalf("ServerAccess() error = %#v, want current 3.2a minimum 3.3", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want only version probe", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
}

// TestServerAccessListReportsVersionProbeFailures proves the version probe a
// listing depends on reports its failure rather than answering with an empty
// list, which reads as a server granting nobody access.
func TestServerAccessListReportsVersionProbeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		response     versionResponse
		wantAnyError bool
		wantError    error
	}{
		{
			name:         "transport failure",
			response:     versionResponse{err: errors.New("version transport failed")},
			wantAnyError: true,
		},
		{
			name: "completed failure",
			response: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"version command failed"}, ExitCode: 1,
			}},
			wantError: ErrVersionQuery,
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
			output, err := serverWithRunner(runner).ServerAccess(
				context.Background(),
				ServerAccessRequest{List: true},
			)
			if output != nil {
				t.Fatalf("ServerAccess(list) = %#v, want no output beside an error", output)
			}
			switch {
			case test.wantAnyError:
				if err == nil {
					t.Fatal("ServerAccess(list) error = nil")
				}
			case test.wantError != nil:
				if !errors.Is(err, test.wantError) {
					t.Fatalf("ServerAccess(list) error = %v, want %v", err, test.wantError)
				}
			default:
				t.Fatal("test has no expectation")
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one version probe", requests)
			}
			assertRequestArguments(t, requests[0], []string{"-V"})
		})
	}
}

func TestRefreshClientRequestsClipboardOnceItIsSafe(t *testing.T) {
	t.Parallel()

	target := ClientName("/dev/pts/9")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.4"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := serverWithRunner(runner).RefreshClient(
		context.Background(),
		RefreshClientRequest{TargetClient: target, RequestClipboard: true},
	)
	if err != nil {
		t.Fatalf("RefreshClient() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and refresh-client", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"refresh-client", "-t", "/dev/pts/9", "-l",
	})
}

// libtmux:parity libtmux.server.Server.refresh_client
// libtmux:parity libtmux.server.Server.refresh_client#parameter-branch:request_clipboard:f7531c059ac6
// libtmux:parity libtmux.server.Server.refresh_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.refresh_client#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.refresh_client#warning:1fa7f6b92788
// libtmux:parity libtmux.server.Server.switch_client
// libtmux:parity libtmux.session.Session.switch_client
func TestRefreshClientWarnsAndOmitsClipboardBeforeItIsSafe(t *testing.T) {
	t.Parallel()

	target := ClientName("/dev/pts/9")
	var warnings []Warning
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := degradingServerWithRunner(runner)
	server.connectionState().options.WarningHandler = func(warning Warning) {
		warnings = append(warnings, warning)
	}
	err := server.RefreshClient(
		context.Background(),
		RefreshClientRequest{TargetClient: target, RequestClipboard: true},
	)
	if err != nil {
		t.Fatalf("RefreshClient() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and refresh-client", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"refresh-client", "-t", "/dev/pts/9",
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one", warnings)
	}
	warning := warnings[0]
	if warning.Kind != WarningUnsupportedFeature ||
		warning.Subcommand != "refresh-client" ||
		warning.Feature != "request_clipboard" ||
		warning.CurrentVersion.String() != "3.3a" ||
		warning.RequiredVersion.String() != "3.4" {
		t.Fatalf("warning = %#v, want refresh-client clipboard minimum 3.4", warning)
	}
}

func TestRefreshClientClipboardVersionFailuresAreMutationErrors(t *testing.T) {
	t.Parallel()

	transportFailure := errors.New("version transport failed")
	tests := []struct {
		name      string
		response  versionResponse
		wantError error
	}{
		{
			name:      "transport failure",
			response:  versionResponse{err: transportFailure},
			wantError: transportFailure,
		},
		{
			name: "completed failure",
			response: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"version command failed"}, ExitCode: 1,
			}},
			wantError: ErrVersionQuery,
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{test.response}}
			err := serverWithRunner(runner).RefreshClient(
				context.Background(),
				RefreshClientRequest{RequestClipboard: true},
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("RefreshClient() error = %v, want %v", err, test.wantError)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one version probe", requests)
			}
			assertRequestArguments(t, requests[0], []string{"-V"})
		})
	}
}

func TestServerAccessCapturesUserBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newClientVersionGateRunner()
	server := serverWithRunner(runner)
	allow := "before-user"
	response := make(chan error, 1)
	go func() {
		_, err := server.ServerAccess(context.Background(), ServerAccessRequest{Allow: &allow})
		response <- err
	}()

	<-runner.versionStarted
	allow = "after-user"
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("ServerAccess() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and server-access", requests)
	}
	assertRequestArguments(t, requests[1], []string{
		"server-access", "-a", "--", "before-user",
	})
}

func TestServerAccessListReportsCommandFailure(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{
			Stdout:   []string{"partial"},
			Stderr:   []string{"failure"},
			ExitCode: 1,
		}},
	}}
	output, err := serverWithRunner(runner).ServerAccess(
		context.Background(),
		ServerAccessRequest{List: true},
	)
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "server-access" {
		t.Fatalf("ServerAccess(list) error = %#v, want a server-access CommandError", err)
	}
	if output != nil {
		t.Fatalf("ServerAccess(list) = %#v, want no entries beside an error", output)
	}
}

func TestClientAdministrationMethodsAreStderrStrict(t *testing.T) {
	t.Parallel()

	for _, test := range clientPointOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := test.prefixResponses()
			responses = append(responses, versionResponse{result: tmuxcmd.Result{
				Stdout:   []string{"partial"},
				Stderr:   []string{"failure"},
				ExitCode: 0,
			}})
			runner := &versionQueueRunner{responses: responses}
			err := test.operation(serverWithRunner(runner))
			var commandError *CommandError
			if !errors.As(err, &commandError) || commandError.Subcommand != test.subcommand {
				t.Fatalf("operation error = %#v, want %s CommandError", err, test.subcommand)
			}
		})
	}
}

func TestClientAdministrationMethodsIgnoreExitOnlyFailures(t *testing.T) {
	t.Parallel()

	for _, test := range clientPointOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := test.prefixResponses()
			responses = append(responses, versionResponse{result: tmuxcmd.Result{
				Stdout:   []string{"completed output"},
				ExitCode: 7,
			}})
			runner := &versionQueueRunner{responses: responses}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v, want nil for empty stderr", err)
			}
		})
	}
}

func TestClientAdministrationMethodsSurfaceTransportErrors(t *testing.T) {
	t.Parallel()

	for _, test := range clientPointOperations() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := test.prefixResponses()
			responses = append(responses, versionResponse{err: context.Canceled})
			runner := &versionQueueRunner{responses: responses}
			if err := test.operation(serverWithRunner(runner)); !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}

// libtmux:parity libtmux.server.Server.detach_all_clients
// libtmux:parity libtmux.server.Server.detach_all_clients#parameter-branch:keep_client:169ea5ceb8fe
// libtmux:parity libtmux.server.Server.detach_all_clients#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.server.Server.detach_client
// libtmux:parity libtmux.server.Server.detach_client#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.server.Server.detach_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.lock_client
// libtmux:parity libtmux.server.Server.lock_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.lock_server
// libtmux:parity libtmux.server.Server.suspend_client
// libtmux:parity libtmux.server.Server.suspend_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.session.Session.detach_client
// libtmux:parity libtmux.session.Session.detach_client#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.session.Session.lock_session
func TestClientAdministrationBuildsExactPythonArguments(t *testing.T) {
	t.Parallel()

	targetClient := ClientName("/dev/pts/9;printf not-a-shell")
	keepClient := ClientName("/dev/pts/10")
	shellCommand := "printf 'detached client'"
	tests := []struct {
		name      string
		operation func(Server) error
		want      []string
	}{
		{
			name: "lock server",
			operation: func(server Server) error {
				return server.LockServer(context.Background())
			},
			want: []string{"lock-server"},
		},
		{
			name: "refresh targeted client",
			operation: func(server Server) error {
				return server.RefreshClient(context.Background(), RefreshClientRequest{
					TargetClient: targetClient,
				})
			},
			want: []string{"refresh-client", "-t", "/dev/pts/9;printf not-a-shell"},
		},
		{
			name: "lock targeted client",
			operation: func(server Server) error {
				return server.LockClient(context.Background(), &targetClient)
			},
			want: []string{"lock-client", "-t", "/dev/pts/9;printf not-a-shell"},
		},
		{
			name: "suspend targeted client",
			operation: func(server Server) error {
				return server.SuspendClient(context.Background(), &targetClient)
			},
			want: []string{"suspend-client", "-t", "/dev/pts/9;printf not-a-shell"},
		},
		{
			name: "detach targeted client",
			operation: func(server Server) error {
				return server.DetachClient(context.Background(), DetachClientRequest{
					TargetClient: targetClient,
					ShellCommand: &shellCommand,
				})
			},
			want: []string{
				"detach-client", "-E", "printf 'detached client'",
				"-t", "/dev/pts/9;printf not-a-shell",
			},
		},
		{
			name: "detach all clients",
			operation: func(server Server) error {
				return server.DetachAllClients(context.Background(), DetachAllClientsRequest{
					KeepClient:   keepClient,
					ShellCommand: &shellCommand,
				})
			},
			want: []string{
				"detach-client", "-a", "-E", "printf 'detached client'",
				"-t", "/dev/pts/10",
			},
		},
		{
			name: "lock session",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).Lock(context.Background())
			},
			want: []string{"lock-session", "-t", "$7"},
		},
		{
			name: "detach session clients",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).DetachClients(
					context.Background(),
					&shellCommand,
				)
			},
			want: []string{
				"detach-client", "-E", "printf 'detached client'", "-s", "$7",
			},
		},
		{
			name: "switch server client",
			operation: func(server Server) error {
				return server.SwitchClient(context.Background(), "work $(not-a-shell)")
			},
			want: []string{"switch-client", "-t", "work $(not-a-shell)"},
		},
		{
			name: "switch session client",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).SwitchClient(
					context.Background(),
				)
			},
			want: []string{"switch-client", "-t", "$7"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			assertClientArguments(t, runner, test.want)
		})
	}
}

func TestClientAdministrationOmitsOptionalArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) error
		want      []string
	}{
		{
			name: "refresh client",
			operation: func(server Server) error {
				return server.RefreshClient(context.Background(), RefreshClientRequest{})
			},
			want: []string{"refresh-client"},
		},
		{
			name: "lock client",
			operation: func(server Server) error {
				return server.LockClient(context.Background(), nil)
			},
			want: []string{"lock-client"},
		},
		{
			name: "suspend client",
			operation: func(server Server) error {
				return server.SuspendClient(context.Background(), nil)
			},
			want: []string{"suspend-client"},
		},
		{
			name: "detach client",
			operation: func(server Server) error {
				return server.DetachClient(context.Background(), DetachClientRequest{})
			},
			want: []string{"detach-client"},
		},
		{
			name: "detach all clients",
			operation: func(server Server) error {
				return server.DetachAllClients(context.Background(), DetachAllClientsRequest{})
			},
			want: []string{"detach-client", "-a"},
		},
		{
			name: "detach session clients",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).DetachClients(
					context.Background(),
					nil,
				)
			},
			want: []string{"detach-client", "-s", "$7"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			if err := test.operation(serverWithRunner(runner)); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			assertClientArguments(t, runner, test.want)
		})
	}
}

func TestClientAdministrationValidatesTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	emptyClient := ClientName("")
	tests := []struct {
		name      string
		operation func(Server) error
		want      error
	}{
		{
			name: "lock empty client",
			operation: func(server Server) error {
				return server.LockClient(context.Background(), &emptyClient)
			},
			want: ErrMissingTarget,
		},
		{
			name: "suspend empty client",
			operation: func(server Server) error {
				return server.SuspendClient(context.Background(), &emptyClient)
			},
			want: ErrMissingTarget,
		},
		{
			name: "lock empty session",
			operation: func(server Server) error {
				return (Session{server: server}).Lock(context.Background())
			},
			want: ErrMissingTarget,
		},
		{
			name: "detach empty session",
			operation: func(server Server) error {
				return (Session{server: server}).DetachClients(context.Background(), nil)
			},
			want: ErrMissingTarget,
		},
		{
			name: "switch empty session",
			operation: func(server Server) error {
				return (Session{server: server}).SwitchClient(context.Background())
			},
			want: ErrMissingTarget,
		},
		{
			name: "server switch empty session",
			operation: func(server Server) error {
				return server.SwitchClient(context.Background(), "")
			},
			want: ErrInvalidRequest,
		},
		{
			name: "server switch session with period",
			operation: func(server Server) error {
				return server.SwitchClient(context.Background(), "bad.name")
			},
			want: ErrInvalidRequest,
		},
		{
			name: "server switch session with colon",
			operation: func(server Server) error {
				return server.SwitchClient(context.Background(), "bad:name")
			},
			want: ErrInvalidRequest,
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
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", calls)
			}
		})
	}
}

func assertClientArguments(t *testing.T, runner *versionQueueRunner, want []string) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one", requests)
	}
	assertRequestArguments(t, requests[0], want)
}

type clientPointOperation struct {
	name         string
	subcommand   string
	versionProbe bool
	operation    func(Server) error
}

func (o clientPointOperation) prefixResponses() []versionResponse {
	if !o.versionProbe {
		return nil
	}
	return []versionResponse{{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}}}
}

func clientPointOperations() []clientPointOperation {
	allow := "alice"
	return []clientPointOperation{
		{
			name:         "server access",
			subcommand:   "server-access",
			versionProbe: true,
			operation: func(server Server) error {
				_, err := server.ServerAccess(
					context.Background(),
					ServerAccessRequest{Allow: &allow},
				)
				return err
			},
		},
		{
			name:       "lock server",
			subcommand: "lock-server",
			operation: func(server Server) error {
				return server.LockServer(context.Background())
			},
		},
		{
			name:       "refresh client",
			subcommand: "refresh-client",
			operation: func(server Server) error {
				return server.RefreshClient(context.Background(), RefreshClientRequest{})
			},
		},
		{
			name:       "lock client",
			subcommand: "lock-client",
			operation: func(server Server) error {
				return server.LockClient(context.Background(), nil)
			},
		},
		{
			name:       "suspend client",
			subcommand: "suspend-client",
			operation: func(server Server) error {
				return server.SuspendClient(context.Background(), nil)
			},
		},
		{
			name:       "detach client",
			subcommand: "detach-client",
			operation: func(server Server) error {
				return server.DetachClient(context.Background(), DetachClientRequest{})
			},
		},
		{
			name:       "detach all clients",
			subcommand: "detach-client",
			operation: func(server Server) error {
				return server.DetachAllClients(
					context.Background(),
					DetachAllClientsRequest{},
				)
			},
		},
		{
			name:       "switch server client",
			subcommand: "switch-client",
			operation: func(server Server) error {
				return server.SwitchClient(context.Background(), "work")
			},
		},
		{
			name:       "lock session",
			subcommand: "lock-session",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).Lock(context.Background())
			},
		},
		{
			name:       "detach session clients",
			subcommand: "detach-client",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).DetachClients(
					context.Background(),
					nil,
				)
			},
		},
		{
			name:       "switch session client",
			subcommand: "switch-client",
			operation: func(server Server) error {
				return (Session{server: server, sessionID: "$7"}).SwitchClient(
					context.Background(),
				)
			},
		},
	}
}

type clientVersionGateRunner struct {
	versionStarted chan struct{}
	releaseVersion chan struct{}

	mu       sync.Mutex
	requests []tmuxcmd.Request
}

func newClientVersionGateRunner() *clientVersionGateRunner {
	return &clientVersionGateRunner{
		versionStarted: make(chan struct{}),
		releaseVersion: make(chan struct{}),
	}
}

func (r *clientVersionGateRunner) Run(
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

func (r *clientVersionGateRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}
