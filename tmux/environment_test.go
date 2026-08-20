package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.common.EnvironmentMixin
// libtmux:parity libtmux.common.EnvironmentMixin.__init__
// libtmux:parity libtmux.common.EnvironmentMixin.cmd
// libtmux:parity libtmux.common.EnvironmentMixin.getenv
// libtmux:parity libtmux.common.EnvironmentMixin.remove_environment
// libtmux:parity libtmux.common.EnvironmentMixin.remove_environment#parameter-branch:name:85085d7ed332
// libtmux:parity libtmux.common.EnvironmentMixin.remove_environment#parameter-branch:name:a43f11265825
// libtmux:parity libtmux.common.EnvironmentMixin.set_environment
// libtmux:parity libtmux.common.EnvironmentMixin.set_environment#parameter-branch:expand_format:3634369442f2
// libtmux:parity libtmux.common.EnvironmentMixin.set_environment#parameter-branch:hidden:1ee18a21f74c
// libtmux:parity libtmux.common.EnvironmentMixin.set_environment#parameter-branch:name,value:85085d7ed332
// libtmux:parity libtmux.common.EnvironmentMixin.set_environment#parameter-branch:name,value:a43f11265825
// libtmux:parity libtmux.common.EnvironmentMixin.show_environment
// libtmux:parity libtmux.common.EnvironmentMixin.unset_environment
// libtmux:parity libtmux.common.EnvironmentMixin.unset_environment#parameter-branch:name:85085d7ed332
// libtmux:parity libtmux.common.EnvironmentMixin.unset_environment#parameter-branch:name:a43f11265825
func TestEnvironmentOperationsBuildScopeSpecificArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server, Session) error
	}{
		{
			name: "set global",
			want: []string{"set-environment", "-g", "-F", "-h", "--", "NAME", "value"},
			run: func(server Server, _ Session) error {
				return server.SetEnvironment(context.Background(), "NAME", "value", SetEnvironmentOptions{
					ExpandFormat: true,
					Hidden:       true,
				})
			},
		},
		{
			name: "set session",
			want: []string{"set-environment", "-t", "$7", "-F", "-h", "--", "NAME", "value"},
			run: func(_ Server, session Session) error {
				return session.SetEnvironment(context.Background(), "NAME", "value", SetEnvironmentOptions{
					ExpandFormat: true,
					Hidden:       true,
				})
			},
		},
		{
			name: "unset global",
			want: []string{"set-environment", "-g", "-u", "--", "NAME"},
			run: func(server Server, _ Session) error {
				return server.UnsetEnvironment(context.Background(), "NAME")
			},
		},
		{
			name: "unset session",
			want: []string{"set-environment", "-t", "$7", "-u", "--", "NAME"},
			run: func(_ Server, session Session) error {
				return session.UnsetEnvironment(context.Background(), "NAME")
			},
		},
		{
			name: "remove global",
			want: []string{"set-environment", "-g", "-r", "--", "NAME"},
			run: func(server Server, _ Session) error {
				return server.RemoveEnvironment(context.Background(), "NAME")
			},
		},
		{
			name: "remove session",
			want: []string{"set-environment", "-t", "$7", "-r", "--", "NAME"},
			run: func(_ Server, session Session) error {
				return session.RemoveEnvironment(context.Background(), "NAME")
			},
		},
		{
			name: "show global",
			want: []string{"show-environment", "-g"},
			run: func(server Server, _ Session) error {
				_, err := server.ShowEnvironment(context.Background())
				return err
			},
		},
		{
			name: "show session",
			want: []string{"show-environment", "-t", "$7"},
			run: func(_ Server, session Session) error {
				_, err := session.ShowEnvironment(context.Background())
				return err
			},
		},
		{
			name: "get global",
			want: []string{"show-environment", "-g", "--", "NAME"},
			run: func(server Server, _ Session) error {
				_, _, err := server.GetEnvironment(context.Background(), "NAME")
				return err
			},
		},
		{
			name: "get session",
			want: []string{"show-environment", "-t", "$7", "--", "NAME"},
			run: func(_ Server, session Session) error {
				_, _, err := session.GetEnvironment(context.Background(), "NAME")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{"NAME=value"}, ExitCode: 0},
			}}}
			server := serverWithRunner(runner)
			session := Session{server: server, sessionID: SessionID("$7")}
			if err := test.run(server, session); err != nil {
				t.Fatalf("environment operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("environment arguments = %#v, want %#v", requests, test.want)
			}
		})
	}
}

func TestEnvironmentArgumentsPreserveTerminalSeparators(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{},
	}}}
	err := serverWithRunner(runner).SetEnvironment(
		context.Background(),
		"NAME;",
		"value;",
		SetEnvironmentOptions{},
	)
	if err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	requests := runner.recordedRequests()
	want := []string{"set-environment", "-g", "--", `NAME\;`, `value\;`}
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("SetEnvironment() arguments = %#v, want %#v", requests, want)
	}
}

func TestSetEnvironmentCompletedFailureRedactsValue(t *testing.T) {
	t.Parallel()

	const secret = "environment-secret"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:  []string{"tmux", "set-environment", "--", "TOKEN", secret},
		Stdout:   []string{"stdout " + secret},
		Stderr:   []string{"stderr " + secret},
		ExitCode: 1,
	}}}}
	err := serverWithRunner(runner).SetEnvironment(
		context.Background(),
		"TOKEN",
		secret,
		SetEnvironmentOptions{},
	)
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("SetEnvironment() error = %#v, want *CommandError", err)
	}
	if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
		t.Fatalf("SetEnvironment() CommandError = %#v, want exit-code-only result", commandError)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("SetEnvironment() error disclosed value: %v", err)
	}
}

func TestEnvironmentReadFailuresRedactResults(t *testing.T) {
	t.Parallel()

	const secret = "environment-secret"
	tests := []struct {
		name string
		read func(Server) error
	}{
		{
			name: "show",
			read: func(server Server) error {
				_, err := server.ShowEnvironment(context.Background())
				return err
			},
		},
		{
			name: "get",
			read: func(server Server) error {
				_, _, err := server.GetEnvironment(
					context.Background(),
					"TOKEN",
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Command:  []string{"tmux", "show-environment", secret},
				Stdout:   []string{"TOKEN=" + secret},
				Stderr:   []string{"stderr " + secret},
				ExitCode: 1,
			}}}}
			err := test.read(serverWithRunner(runner))
			var commandError *CommandError
			if !errors.As(err, &commandError) {
				t.Fatalf("environment read error = %#v, want *CommandError", err)
			}
			if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
				commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
				t.Fatalf("environment read CommandError = %#v, want exit-code-only result", commandError)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("environment read error disclosed value: %v", err)
			}
		})
	}
}

func TestShowEnvironmentPreservesRawValueStates(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{
			Stdout:   []string{"EMPTY=", "EQUALS=one=two", "-REMOVED"},
			ExitCode: 0,
		},
	}}}
	got, err := serverWithRunner(runner).ShowEnvironment(context.Background())
	if err != nil {
		t.Fatalf("ShowEnvironment() error = %v", err)
	}
	want := map[string]EnvironmentValue{
		"EMPTY":   {Value: ""},
		"EQUALS":  {Value: "one=two"},
		"REMOVED": {Removed: true},
	}
	if !environmentMapsEqual(got, want) {
		t.Fatalf("ShowEnvironment() = %#v, want %#v", got, want)
	}

	got["EMPTY"] = EnvironmentValue{Value: "mutated"}
	if want["EMPTY"].Value != "" {
		t.Fatal("ShowEnvironment() map aliases expected state")
	}
}

// libtmux:parity libtmux.exc.VariableUnpackingError
// libtmux:parity libtmux.exc.VariableUnpackingError.__init__
func TestShowEnvironmentRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

	tests := []string{"TOP_SECRET", "-", "=secret-value"}
	for _, line := range tests {
		t.Run(line, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{line}, ExitCode: 0},
			}}}
			_, err := serverWithRunner(runner).ShowEnvironment(context.Background())
			if !errors.Is(err, ErrMalformedEnvironment) {
				t.Fatalf("ShowEnvironment() error = %v, want ErrMalformedEnvironment", err)
			}
			var decodeError *EnvironmentDecodeError
			if !errors.As(err, &decodeError) || decodeError.Record != 0 || decodeError.Reason == "" {
				t.Fatalf("ShowEnvironment() error = %#v, want record location and reason", err)
			}
			if strings.Contains(err.Error(), "TOP_SECRET") || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("ShowEnvironment() error disclosed environment output: %v", err)
			}
		})
	}
}

func TestEnvironmentNamesValidateBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "empty set",
			run: func(server Server) error {
				return server.SetEnvironment(context.Background(), "", "value", SetEnvironmentOptions{})
			},
		},
		{
			name: "equals unset",
			run: func(server Server) error {
				return server.UnsetEnvironment(context.Background(), "BAD=NAME")
			},
		},
		{
			name: "nul remove",
			run: func(server Server) error {
				return server.RemoveEnvironment(context.Background(), "BAD\x00NAME")
			},
		},
		{
			name: "newline name",
			run: func(server Server) error {
				return server.UnsetEnvironment(context.Background(), "BAD\nNAME")
			},
		},
		{
			name: "empty get",
			run: func(server Server) error {
				_, _, err := server.GetEnvironment(context.Background(), "")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidEnvironmentName) {
				t.Fatalf("environment operation error = %v, want ErrInvalidEnvironmentName", err)
			}
			if _, ok := errors.AsType[*EnvironmentNameError](err); !ok {
				t.Fatalf("environment operation error type = %T, want *EnvironmentNameError", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("invalid environment name executed %d commands", runner.callCount())
			}
		})
	}
}

func TestEnvironmentValuesValidateBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"secret\nvalue", "secret\rvalue", "secret\x00value"} {
		runner := &versionQueueRunner{}
		err := serverWithRunner(runner).SetEnvironment(
			context.Background(),
			"NAME",
			value,
			SetEnvironmentOptions{},
		)
		if !errors.Is(err, ErrInvalidEnvironmentValue) {
			t.Fatalf("SetEnvironment(%q) error = %v, want ErrInvalidEnvironmentValue", value, err)
		}
		if runner.callCount() != 0 {
			t.Fatalf("invalid environment value executed %d commands", runner.callCount())
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("environment value error disclosed value: %v", err)
		}
	}
}

func TestSessionEnvironmentRequiresStableTarget(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	_, err := (Session{server: server}).ShowEnvironment(context.Background())
	if !errors.Is(err, ErrMissingTarget) || runner.callCount() != 0 {
		t.Fatalf("zero Session.ShowEnvironment() = (%v, %d calls), want ErrMissingTarget without execution", err, runner.callCount())
	}
}

func TestEnvironmentReadErrorPolicy(t *testing.T) {
	t.Parallel()

	t.Run("show", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stderr: []string{"no server running"}, ExitCode: 1}},
		}}
		values, err := serverWithRunner(runner).ShowEnvironment(context.Background())
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("ShowEnvironment() error = %v, want ErrCommand", err)
		}
		if values != nil {
			t.Fatalf("ShowEnvironment() = %#v, want no values beside an error", values)
		}
	})

	t.Run("get missing", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stderr: []string{"unknown variable: NAME"}, ExitCode: 1}},
		}}
		// A name tmux does not hold is absence, which the ok result carries.
		value, ok, err := serverWithRunner(runner).GetEnvironment(context.Background(), "NAME")
		if err != nil || ok || value != (EnvironmentValue{}) {
			t.Fatalf("GetEnvironment(missing) = (%#v, %t, %v), want zero, false, nil", value, ok, err)
		}
	})

	t.Run("get command failure", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stderr: []string{"no server running"}, ExitCode: 1}},
		}}
		// A failure is not absence: only tmux naming the variable as unknown
		// answers false, so a server that could not be read stays an error.
		_, ok, err := serverWithRunner(runner).GetEnvironment(context.Background(), "NAME")
		if !errors.Is(err, ErrCommand) || ok {
			t.Fatalf("GetEnvironment() = (%t, %v), want ErrCommand", ok, err)
		}
	})

	t.Run("mutation", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stderr: []string{"no current session"}, ExitCode: 1},
		}}}
		err := serverWithRunner(runner).SetEnvironment(
			context.Background(),
			"NAME",
			"value",
			SetEnvironmentOptions{},
		)
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("SetEnvironment() error = %v, want ErrCommand", err)
		}
	})

	t.Run("mutation stderr with zero exit", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stderr: []string{"hook warning"}, ExitCode: 0},
		}}}
		err := serverWithRunner(runner).SetEnvironment(
			context.Background(),
			"NAME",
			"value",
			SetEnvironmentOptions{},
		)
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("SetEnvironment() error = %v, want ErrCommand", err)
		}
	})
}

func environmentMapsEqual(left, right map[string]EnvironmentValue) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if right[name] != value {
			return false
		}
	}
	return true
}
