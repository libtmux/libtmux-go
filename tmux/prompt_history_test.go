package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.clear_prompt_history
// libtmux:parity libtmux.server.Server.clear_prompt_history#parameter-branch:prompt_type:83546fba116e
// libtmux:parity libtmux.server.Server.clear_prompt_history#version-branch:tmux-version:d9801479e597
// libtmux:parity libtmux.server.Server.show_prompt_history
// libtmux:parity libtmux.server.Server.show_prompt_history#parameter-branch:prompt_type:83546fba116e
// libtmux:parity libtmux.server.Server.show_prompt_history#version-branch:tmux-version:d9801479e597
func TestPromptHistoryBuildsExactArgumentsForClosedTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		promptType PromptType
		argument   string
	}{
		{name: "all", promptType: PromptTypeAll},
		{name: "command", promptType: PromptTypeCommand, argument: "command"},
		{name: "search", promptType: PromptTypeSearch, argument: "search"},
		{name: "target", promptType: PromptTypeTarget, argument: "target"},
		{name: "window target", promptType: PromptTypeWindowTarget, argument: "window-target"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, operation := range []string{"show", "clear"} {
				t.Run(operation, func(t *testing.T) {
					t.Parallel()

					responses := []historyResponse{{result: tmuxcmd.Result{
						Stdout: []string{"tmux 3.3"},
					}}}
					if operation == "show" {
						responses = append(responses, historyResponse{result: tmuxcmd.Result{
							Stdout: []string{"History for command:"},
						}})
					} else {
						responses = append(responses, historyResponse{result: tmuxcmd.Result{}})
					}
					runner := &historyQueueRunner{responses: responses}
					server := historyServerWithRunner(runner)
					request := PromptHistoryRequest{Type: test.promptType}
					if operation == "show" {
						output, err := server.ShowPromptHistory(context.Background(), request)
						if err != nil || !slices.Equal(output, []string{"History for command:"}) {
							t.Fatalf("ShowPromptHistory() = (%#v, %v), want history", output, err)
						}
					} else if err := server.ClearPromptHistory(context.Background(), request); err != nil {
						t.Fatalf("ClearPromptHistory() error = %v", err)
					}

					requests := runner.recordedRequests()
					if len(requests) != 2 {
						t.Fatalf("runner requests = %#v, want version and %s", requests, operation)
					}
					assertHistoryArguments(t, requests[0], []string{"-V"})
					arguments := []string{operation + "-prompt-history"}
					if test.argument != "" {
						arguments = append(arguments, "-T", test.argument)
					}
					assertHistoryArguments(t, requests[1], arguments)
				})
			}
		})
	}
}

func TestPromptHistoryRejectsOpenEndedTypesBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	for _, value := range []PromptType{5, 255} {
		t.Run(strconv.FormatUint(uint64(value), 10), func(t *testing.T) {
			t.Parallel()

			for _, operation := range []struct {
				name       string
				subcommand string
				run        func(Server, PromptHistoryRequest) error
			}{
				{
					name:       "show",
					subcommand: "show-prompt-history",
					run: func(server Server, request PromptHistoryRequest) error {
						_, err := server.ShowPromptHistory(context.Background(), request)
						return err
					},
				},
				{
					name:       "clear",
					subcommand: "clear-prompt-history",
					run: func(server Server, request PromptHistoryRequest) error {
						return server.ClearPromptHistory(context.Background(), request)
					},
				},
			} {
				t.Run(operation.name, func(t *testing.T) {
					t.Parallel()

					runner := &historyQueueRunner{}
					err := operation.run(
						historyServerWithRunner(runner),
						PromptHistoryRequest{Type: value},
					)
					if !errors.Is(err, ErrInvalidServerCommandRequest) {
						t.Fatalf("prompt history error = %v, want ErrInvalidServerCommandRequest", err)
					}
					var requestError *ServerCommandRequestError
					if !errors.As(err, &requestError) ||
						requestError.Subcommand != operation.subcommand ||
						requestError.Field != "Type" ||
						requestError.Value != strconv.FormatUint(uint64(value), 10) {
						t.Fatalf("prompt history error = %#v, want concrete Type error", err)
					}
					if runner.callCount() != 0 {
						t.Fatalf("runner calls = %d, want validation before version probe", runner.callCount())
					}
				})
			}
		})
	}
}

func TestPromptHistoryRequiresTmux33BeforeCommand(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "show",
			run: func(server Server) error {
				_, err := server.ShowPromptHistory(context.Background(), PromptHistoryRequest{})
				return err
			},
		},
		{
			name: "clear",
			run: func(server Server) error {
				return server.ClearPromptHistory(context.Background(), PromptHistoryRequest{})
			},
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()

			runner := &historyQueueRunner{responses: []historyResponse{{result: tmuxcmd.Result{
				Stdout: []string{"tmux 3.2a"},
			}}}}
			err := operation.run(historyServerWithRunner(runner))
			var tooLow *VersionTooLowError
			if !errors.As(err, &tooLow) || tooLow.Current.String() != "3.2a" ||
				tooLow.Minimum.String() != "3.3" {
				t.Fatalf("prompt history error = %#v, want current 3.2a minimum 3.3", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want only version probe", requests)
			}
			assertHistoryArguments(t, requests[0], []string{"-V"})
		})
	}
}

// TestShowPromptHistoryReportsVersionProbeFailures proves that a version probe
// the listing depends on reports its failure rather than answering with an
// empty history, which would read as a server holding no prompt history.
func TestShowPromptHistoryReportsVersionProbeFailures(t *testing.T) {
	t.Parallel()

	transport := errors.New("version transport failed")
	tests := []struct {
		name         string
		response     historyResponse
		wantAnyError bool
		wantError    error
	}{
		{
			name:         "transport failure",
			response:     historyResponse{err: transport},
			wantAnyError: true,
		},
		{
			name: "completed failure",
			response: historyResponse{result: tmuxcmd.Result{
				Stderr: []string{"version command failed"}, ExitCode: 1,
			}},
			wantError: ErrVersionQuery,
		},
		{
			name:      "context failure",
			response:  historyResponse{err: context.DeadlineExceeded},
			wantError: context.DeadlineExceeded,
		},
		{
			name: "malformed successful output",
			response: historyResponse{result: tmuxcmd.Result{
				Stdout: []string{"not a tmux version"},
			}},
			wantError: ErrVersionQuery,
		},
		{
			name: "invalid version token",
			response: historyResponse{result: tmuxcmd.Result{
				Stdout: []string{"tmux invalid!"},
			}},
			wantError: ErrVersionQuery,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &historyQueueRunner{responses: []historyResponse{test.response}}
			server := historyServerWithRunner(runner)
			output, err := server.ShowPromptHistory(
				context.Background(), PromptHistoryRequest{},
			)
			if output != nil {
				t.Fatalf("ShowPromptHistory() = %#v, want no history beside an error", output)
			}
			if test.wantAnyError {
				if err == nil {
					t.Fatal("ShowPromptHistory() error = nil, want transport error")
				}
			} else if !errors.Is(err, test.wantError) {
				t.Fatalf("ShowPromptHistory() error = %v, want %v", err, test.wantError)
			}
			if runner.callCount() != 1 {
				t.Fatalf("runner calls = %d, want only version probe", runner.callCount())
			}
		})
	}
}

func TestShowPromptHistoryCommandFollowsListPolicyAndOwnsOutput(t *testing.T) {
	t.Parallel()

	source := []string{"History for command:", "1: list-sessions"}
	transport := errors.New("show transport failed")
	tests := []struct {
		name         string
		response     historyResponse
		want         []string
		wantAnyError bool
		wantError    error
	}{
		{
			name:     "success",
			response: historyResponse{result: tmuxcmd.Result{Stdout: source}},
			want:     []string{"History for command:", "1: list-sessions"},
		},
		{
			name: "completed failure",
			response: historyResponse{result: tmuxcmd.Result{
				Stdout: []string{"partial"}, Stderr: []string{"failed"}, ExitCode: 1,
			}},
			wantError: ErrCommand,
		},
		{
			name:         "transport failure",
			response:     historyResponse{err: transport},
			wantAnyError: true,
		},
		{
			name:      "context failure",
			response:  historyResponse{err: context.Canceled},
			wantError: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &historyQueueRunner{responses: []historyResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
				test.response,
			}}
			server := historyServerWithRunner(runner)
			output, err := server.ShowPromptHistory(
				context.Background(), PromptHistoryRequest{},
			)
			switch {
			case test.wantAnyError:
				if err == nil {
					t.Fatal("ShowPromptHistory() error = nil, want transport error")
				}
			case test.wantError != nil:
				if !errors.Is(err, test.wantError) {
					t.Fatalf("ShowPromptHistory() error = %v, want %v", err, test.wantError)
				}
			default:
				if err != nil || !slices.Equal(output, test.want) {
					t.Fatalf("ShowPromptHistory() = (%#v, %v), want %#v", output, err, test.want)
				}
				output[0] = "mutated"
				if source[0] != "History for command:" {
					t.Fatalf("ShowPromptHistory() aliases runner stdout: %#v", source)
				}
			}
		})
	}
}

func TestClearPromptHistoryIsLoudAfterVersionGate(t *testing.T) {
	t.Parallel()

	transport := errors.New("clear transport failed")
	tests := []struct {
		name      string
		response  historyResponse
		wantError error
	}{
		{
			name: "completed stderr",
			response: historyResponse{result: tmuxcmd.Result{
				Stderr: []string{"invalid type"}, ExitCode: 1,
			}},
			wantError: ErrCommand,
		},
		{name: "transport", response: historyResponse{err: transport}, wantError: transport},
		{name: "context", response: historyResponse{err: context.Canceled}, wantError: context.Canceled},
		{name: "exit only remains Python completion data", response: historyResponse{result: tmuxcmd.Result{ExitCode: 7}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &historyQueueRunner{responses: []historyResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
				test.response,
			}}
			err := historyServerWithRunner(runner).ClearPromptHistory(
				context.Background(), PromptHistoryRequest{},
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("ClearPromptHistory() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestPromptHistoryOperationsShareOneSuccessfulVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &historyQueueRunner{responses: []historyResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{Stdout: []string{"History for command:"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := historyServerWithRunner(runner)
	if _, err := server.ShowPromptHistory(
		context.Background(), PromptHistoryRequest{Type: PromptTypeCommand},
	); err != nil {
		t.Fatalf("ShowPromptHistory() error = %v", err)
	}
	if err := server.ClearPromptHistory(
		context.Background(), PromptHistoryRequest{Type: PromptTypeCommand},
	); err != nil {
		t.Fatalf("ClearPromptHistory() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 3 {
		t.Fatalf("runner requests = %#v, want one version and two history calls", requests)
	}
	assertHistoryArguments(t, requests[0], []string{"-V"})
	assertHistoryArguments(t, requests[1], []string{"show-prompt-history", "-T", "command"})
	assertHistoryArguments(t, requests[2], []string{"clear-prompt-history", "-T", "command"})
}

type historyResponse struct {
	result tmuxcmd.Result
	err    error
}

type historyQueueRunner struct {
	mu        sync.Mutex
	responses []historyResponse
	requests  []tmuxcmd.Request
}

func (r *historyQueueRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		return tmuxcmd.Result{}, errors.New("unexpected prompt history runner call")
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response.result, response.err
}

func (r *historyQueueRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *historyQueueRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func historyServerWithRunner(runner commandRunner) Server {
	return Server{state: &serverState{runner: runner}}
}

func assertHistoryArguments(t *testing.T, request tmuxcmd.Request, want []string) {
	t.Helper()
	if !slices.Equal(request.Arguments, want) {
		t.Fatalf("runner arguments = %#v, want %#v", request.Arguments, want)
	}
}
