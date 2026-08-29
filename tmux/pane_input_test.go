package tmux

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestSendKeysBuildsExactArgumentsAndSeparateEnter(t *testing.T) {
	t.Parallel()

	command := "printf ok"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
		context.Background(),
		SendKeysRequest{Command: &command},
	)
	if err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want send and enter", requests)
	}
	assertRequestArguments(t, requests[0], []string{"send-keys", "-t", "$5:0.%7", "--", "printf ok"})
	assertRequestArguments(t, requests[1], []string{"send-keys", "-t", "$5:0.%7", "--", "Enter"})
}

func TestSendKeysPreservesEmptyCommandAsAnOperand(t *testing.T) {
	t.Parallel()

	command := ""
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
		context.Background(),
		SendKeysRequest{Command: &command},
	)
	if err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{"send-keys", "-t", "$5:0.%7", "--", ""})
	assertRequestArguments(t, requests[1], []string{"send-keys", "-t", "$5:0.%7", "--", "Enter"})
}

func TestSendKeysTerminatesOptionsBeforeOperands(t *testing.T) {
	t.Parallel()

	command := "-N"
	for _, test := range []struct {
		name string
		req  SendKeysRequest
		want []string
	}{
		{
			name: "normal",
			req:  SendKeysRequest{Command: &command, Literal: true, SkipEnter: true},
			want: []string{"send-keys", "-t", "$5:0.%7", "-l", "--", "-N"},
		},
		{
			name: "copy mode",
			req:  SendKeysRequest{CopyModeCommand: &command},
			want: []string{"send-keys", "-t", "$5:0.%7", "-X", "--", "-N"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
				context.Background(), test.req,
			)
			if err != nil {
				t.Fatalf("SendKeys() error = %v", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

// libtmux:parity libtmux.pane.Pane.send_keys
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:cmd:a9656802b58e
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:copy_mode_cmd,enter:ee0d792d6bbf
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:copy_mode_cmd:9f129ac517ed
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:expand_formats:0d7cebbeb84e
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:hex_keys:4cb2f6b6e23a
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:key_name:7147ea786c39
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:literal:399069e0cc76
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:repeat,reset:07cb45e98f89
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:repeat:9bfe1e965020
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:reset:503246936ad1
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:suppress_history:b5886e41a787
// libtmux:parity libtmux.pane.Pane.send_keys#parameter-branch:target_client:9bd26a6f1edf
func TestSendKeysBuildsFlagsInPythonOrder(t *testing.T) {
	t.Parallel()

	command := "41"
	client := ClientName("/dev/pts/7")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.4"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
		context.Background(),
		SendKeysRequest{
			Command:         &command,
			SkipEnter:       true,
			Reset:           true,
			ExpandFormats:   true,
			HexKeys:         true,
			KeyName:         true,
			Literal:         true,
			Repeat:          3,
			TargetClient:    client,
			SuppressHistory: true,
		},
	)
	if err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and send", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"send-keys", "-t", "$5:0.%7", "-R", "-F", "-H", "-K", "-l", "-N", "3",
		"-c", "/dev/pts/7", "--", " 41",
	})
}

func TestSendKeysCopyModeOverridesCommandAndEnter(t *testing.T) {
	t.Parallel()

	command := "ignored"
	copyCommand := "cursor-down;"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
		context.Background(),
		SendKeysRequest{Command: &command, CopyModeCommand: &copyCommand},
	)
	if err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one copy-mode command", requests)
	}
	assertRequestArguments(
		t,
		requests[0],
		[]string{"send-keys", "-t", "$5:0.%7", "-X", "--", `cursor-down\;`},
	)
}

func TestSendKeysFlagOnlyDoesNotEnter(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		request  SendKeysRequest
		wantArgs []string
	}{
		{
			name:     "reset with zero repeat",
			request:  SendKeysRequest{Reset: true, Repeat: 0},
			wantArgs: []string{"send-keys", "-t", "$5:0.%7", "-R"},
		},
		{
			name:     "repeat",
			request:  SendKeysRequest{Repeat: 2},
			wantArgs: []string{"send-keys", "-t", "$5:0.%7", "-N", "2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
				context.Background(), test.request,
			)
			if err != nil {
				t.Fatalf("SendKeys() error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one flag-only send", requests)
			}
			assertRequestArguments(t, requests[0], test.wantArgs)
		})
	}
}

func TestSendKeysRejectsInvalidRequestsBeforeExecution(t *testing.T) {
	t.Parallel()

	nulCommand := "unsafe\x00command"
	nulCopyCommand := "unsafe\x00copy"
	nulClient := ClientName("unsafe\x00client")
	for _, test := range []struct {
		name    string
		request SendKeysRequest
	}{
		{name: "missing command and flag-only effect"},
		{name: "zero repeat", request: SendKeysRequest{}},
		{name: "negative repeat", request: SendKeysRequest{Repeat: -1}},
		{name: "NUL command", request: SendKeysRequest{Command: &nulCommand, KeyName: true}},
		{name: "NUL copy command", request: SendKeysRequest{CopyModeCommand: &nulCopyCommand, KeyName: true}},
		{name: "NUL client", request: SendKeysRequest{Reset: true, TargetClient: nulClient}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
				context.Background(), test.request,
			)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("SendKeys() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

// libtmux:parity libtmux.pane.Pane.send_keys#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.send_keys#version-branch:tmux-version:4ec38997c7f9:2
// libtmux:parity libtmux.pane.Pane.send_keys#warning:095c3f7fa507
// libtmux:parity libtmux.pane.Pane.send_keys#warning:d7989c95c9e0
func TestSendKeysVersionGatesClientAndKeyName(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version      string
		wantArgs     []string
		wantFeatures []string
	}{
		{
			version:      "3.3a",
			wantArgs:     []string{"send-keys", "-t", "$5:0.%7", "-R"},
			wantFeatures: []string{"key_name", "target_client"},
		},
		{
			version:  "3.4",
			wantArgs: []string{"send-keys", "-t", "$5:0.%7", "-R", "-K", "-c", "client-a"},
		},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			client := ClientName("client-a")
			warnings := make([]Warning, 0, 2)
			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			server := serverWithOptionsAndRunner(ServerOptions{
				Unsupported: DegradeUnsupported,
				WarningHandler: func(warning Warning) {
					warnings = append(warnings, warning)
				},
			}, runner)
			err := paneWithExactTestTarget(server).SendKeys(
				context.Background(),
				SendKeysRequest{Reset: true, KeyName: true, TargetClient: client},
			)
			if err != nil {
				t.Fatalf("SendKeys() error = %v", err)
			}
			requests := runner.recordedRequests()
			assertRequestArguments(t, requests[0], []string{"-V"})
			assertRequestArguments(t, requests[1], test.wantArgs)
			if len(warnings) != len(test.wantFeatures) {
				t.Fatalf("warnings = %#v, want features %#v", warnings, test.wantFeatures)
			}
			for index, warning := range warnings {
				if warning.Kind != WarningUnsupportedFeature ||
					warning.Subcommand != "send-keys" ||
					warning.Feature != test.wantFeatures[index] ||
					warning.CurrentVersion.String() != test.version ||
					warning.RequiredVersion.String() != "3.4" {
					t.Fatalf("warning %d = %#v", index, warning)
				}
			}
		})
	}
}

func TestSendKeysCapturesPointerInputsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	command := "original;"
	client := ClientName("client-original")
	runner := &paneInputBlockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
			context.Background(),
			SendKeysRequest{
				Command:      &command,
				SkipEnter:    true,
				Repeat:       2,
				TargetClient: client,
				KeyName:      true,
			},
		)
	}()

	<-runner.started
	command = "mutated"
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"send-keys", "-t", "$5:0.%7", "-K", "-N", "2", "-c", "client-original", "--", `original\;`,
	})
}

func TestSendKeysVersionFailureDoesNotWarnOrExecute(t *testing.T) {
	t.Parallel()

	warnings := 0
	runner := &versionQueueRunner{responses: []versionResponse{{err: context.Canceled}}}
	server := serverWithOptionsAndRunner(ServerOptions{
		WarningHandler: func(Warning) { warnings++ },
	}, runner)
	err := paneWithExactTestTarget(server).SendKeys(
		context.Background(), SendKeysRequest{Reset: true, KeyName: true},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendKeys() error = %v, want context canceled", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner calls = %d, want one version probe", calls)
	}
	if warnings != 0 {
		t.Fatalf("warnings = %d, want none before a known version", warnings)
	}
}

func TestSendKeysIgnoresCompletedFailuresButSurfacesTransportErrors(t *testing.T) {
	t.Parallel()

	t.Run("completed failures", func(t *testing.T) {
		t.Parallel()

		command := "false"
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stderr: []string{"send failed"}, ExitCode: 1}},
			{result: tmuxcmd.Result{Stderr: []string{"enter failed"}, ExitCode: 1}},
		}}
		err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
			context.Background(), SendKeysRequest{Command: &command},
		)
		if err != nil {
			t.Fatalf("SendKeys() error = %v, want nil", err)
		}
		if calls := runner.callCount(); calls != 2 {
			t.Fatalf("runner calls = %d, want 2", calls)
		}
	})

	t.Run("transport failure stops before enter", func(t *testing.T) {
		t.Parallel()

		command := "echo"
		runner := &versionQueueRunner{responses: []versionResponse{{err: context.Canceled}}}
		err := paneWithExactTestTarget(serverWithRunner(runner)).SendKeys(
			context.Background(), SendKeysRequest{Command: &command},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SendKeys() error = %v, want context canceled", err)
		}
		if calls := runner.callCount(); calls != 1 {
			t.Fatalf("runner calls = %d, want 1", calls)
		}
	})
}

// libtmux:parity libtmux.pane.Pane.clear
// libtmux:parity libtmux.pane.Pane.enter
func TestEnterAndClearUseSendKeysRawSemantics(t *testing.T) {
	t.Parallel()

	t.Run("enter", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
			Stderr: []string{"ignored"}, ExitCode: 1,
		}}}}
		err := paneWithExactTestTarget(serverWithRunner(runner)).Enter(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("Enter() error = %v", err)
		}
		assertRequestArguments(
			t, runner.recordedRequests()[0], []string{"send-keys", "-t", "$5:0.%7", "--", "Enter"},
		)
	})

	t.Run("clear", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stderr: []string{"ignored"}, ExitCode: 1}},
			{result: tmuxcmd.Result{Stderr: []string{"ignored"}, ExitCode: 1}},
		}}
		err := paneWithExactTestTarget(serverWithRunner(runner)).Clear(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("Clear() error = %v", err)
		}
		requests := runner.recordedRequests()
		assertRequestArguments(
			t, requests[0], []string{"send-keys", "-t", "$5:0.%7", "--", "reset"},
		)
		assertRequestArguments(
			t, requests[1], []string{"send-keys", "-t", "$5:0.%7", "--", "Enter"},
		)
	})
}

// libtmux:parity libtmux.pane.Pane.send_prefix
// libtmux:parity libtmux.pane.Pane.send_prefix#parameter-branch:secondary:b3326a61afb3
func TestSendPrefixBuildsExactArgumentsAndRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		key      PrefixKey
		wantArgs []string
	}{
		{name: "primary", key: PrefixPrimary, wantArgs: []string{"send-prefix", "-t", "$5:0.%7"}},
		{name: "secondary", key: PrefixSecondary, wantArgs: []string{"send-prefix", "-t", "$5:0.%7", "-2"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			err := paneWithExactTestTarget(serverWithRunner(runner)).SendPrefix(
				context.Background(), test.key,
			)
			if err != nil {
				t.Fatalf("SendPrefix() error = %v", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}

	runner := &versionQueueRunner{}
	err := paneWithExactTestTarget(serverWithRunner(runner)).SendPrefix(
		context.Background(), PrefixKey(99),
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("SendPrefix() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want 0", calls)
	}
}

func TestSendPrefixAndClearHistoryTreatCompletedStderrAsCommandError(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		run  func(Pane) error
	}{
		{
			name: "send prefix",
			run: func(pane Pane) error {
				return pane.SendPrefix(context.Background(), PrefixPrimary)
			},
		},
		{
			name: "clear history",
			run: func(pane Pane) error {
				return pane.ClearHistory(context.Background(), ClearHistoryRequest{})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"tmux failed"}, ExitCode: 1,
			}}}}
			err := test.run(paneWithExactTestTarget(serverWithRunner(runner)))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("%s error = %v, want ErrCommand", test.name, err)
			}
		})
	}
}

func TestDocumentedPaneInputFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "pane-input-secret"
	tests := []struct {
		name       string
		subcommand string
		invoke     func(Pane) error
	}{
		{name: "send prefix", subcommand: "send-prefix", invoke: func(pane Pane) error {
			return pane.SendPrefix(context.Background(), PrefixPrimary)
		}},
		{name: "clear history", subcommand: "clear-history", invoke: func(pane Pane) error {
			return pane.ClearHistory(context.Background(), ClearHistoryRequest{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Command:  []string{"tmux", test.subcommand, secret},
				Stdout:   []string{"stdout " + secret},
				Stderr:   []string{"stderr " + secret},
				ExitCode: 7,
			}}}}
			err := test.invoke(paneWithExactTestTarget(serverWithRunner(runner)))
			assertExitOnlyCommandErrorRedacts(t, err, test.subcommand, 7, secret)
		})
	}
}

// libtmux:parity libtmux.pane.Pane.clear_history
// libtmux:parity libtmux.pane.Pane.clear_history#parameter-branch:reset_hyperlinks:d6bb66101896
// libtmux:parity libtmux.pane.Pane.clear_history#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.clear_history#warning:ce7f518adef8
func TestClearHistoryVersionGatesHyperlinkReset(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version     string
		wantArgs    []string
		wantWarning bool
	}{
		{version: "3.3a", wantArgs: []string{"clear-history", "-t", "$5:0.%7"}, wantWarning: true},
		{version: "3.4", wantArgs: []string{"clear-history", "-t", "$5:0.%7", "-H"}},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			warnings := make([]Warning, 0, 1)
			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			server := serverWithOptionsAndRunner(ServerOptions{
				Unsupported: DegradeUnsupported,
				WarningHandler: func(warning Warning) {
					warnings = append(warnings, warning)
				},
			}, runner)
			err := paneWithExactTestTarget(server).ClearHistory(
				context.Background(), ClearHistoryRequest{ResetHyperlinks: true},
			)
			if err != nil {
				t.Fatalf("ClearHistory() error = %v", err)
			}
			requests := runner.recordedRequests()
			assertRequestArguments(t, requests[0], []string{"-V"})
			assertRequestArguments(t, requests[1], test.wantArgs)
			if (len(warnings) != 0) != test.wantWarning {
				t.Fatalf("warnings = %#v, want warning %t", warnings, test.wantWarning)
			}
			if test.wantWarning {
				warning := warnings[0]
				if warning.Kind != WarningUnsupportedFeature ||
					warning.Subcommand != "clear-history" ||
					warning.Feature != "reset_hyperlinks" ||
					warning.CurrentVersion.String() != test.version ||
					warning.RequiredVersion.String() != "3.4" {
					t.Fatalf("warning = %#v", warning)
				}
			}
		})
	}
}

// libtmux:parity libtmux.pane.Pane.reset
func TestResetUsesOneTrustedTwoTargetCommandList(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"ignored"}, ExitCode: 1,
	}}}}
	err := paneWithExactTestTarget(serverWithRunner(runner)).Reset(
		context.Background(),
	)
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one command list", requests)
	}
	assertRequestArguments(t, requests[0], []string{
		"send-keys", "-t", "$5:0.%7", "-R", ";",
		"clear-history", "-t", "$5:0.%7",
	})
}

func TestPaneInputMethodsRejectInvalidTargetBeforeExecution(t *testing.T) {
	t.Parallel()

	command := "echo"
	for _, run := range []func(Pane) error{
		func(pane Pane) error {
			return pane.SendKeys(context.Background(), SendKeysRequest{Command: &command})
		},
		func(pane Pane) error { return pane.Enter(context.Background()) },
		func(pane Pane) error { return pane.SendPrefix(context.Background(), PrefixPrimary) },
		func(pane Pane) error {
			return pane.ClearHistory(context.Background(), ClearHistoryRequest{})
		},
		func(pane Pane) error { return pane.Clear(context.Background()) },
		func(pane Pane) error { return pane.Reset(context.Background()) },
	} {
		runner := &versionQueueRunner{}
		err := run(Pane{
			server: serverWithRunner(runner), sessionID: "$1", windowID: "@2",
			paneID: "pane-name",
		})
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("input method error = %v, want ErrInvalidTarget", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	}
}

type paneInputBlockingRunner struct {
	mu       sync.Mutex
	started  chan struct{}
	release  chan struct{}
	requests []tmuxcmd.Request
}

func (r *paneInputBlockingRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	request.Arguments = slices.Clone(request.Arguments)
	r.requests = append(r.requests, request)
	call := len(r.requests)
	r.mu.Unlock()
	if call == 1 {
		close(r.started)
		<-r.release
		return tmuxcmd.Result{Stdout: []string{"tmux 3.4"}}, nil
	}
	return tmuxcmd.Result{}, nil
}

func (r *paneInputBlockingRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}
