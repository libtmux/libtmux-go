package tmux

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestGlobalScopeFactoriesReturnConcreteCapturedHandles(t *testing.T) {
	t.Parallel()

	sessionFactory := Server.GlobalSessionScope
	windowFactory := Server.GlobalWindowScope
	server := Server{state: &serverState{shared: &serverShared{}}}

	sessionScope := sessionFactory(server)
	if sessionScope.server != server {
		t.Fatalf("GlobalSessionScope() captured %#v, want %#v", sessionScope.server, server)
	}
	windowScope := windowFactory(server)
	if windowScope.server != server {
		t.Fatalf("GlobalWindowScope() captured %#v, want %#v", windowScope.server, server)
	}

	var zeroSession GlobalSessionScope
	if zeroSession.server.connectionState() != defaultServerState {
		t.Fatal("zero GlobalSessionScope does not use the zero Server connection")
	}
	var zeroWindow GlobalWindowScope
	if zeroWindow.server.connectionState() != defaultServerState {
		t.Fatal("zero GlobalWindowScope does not use the zero Server connection")
	}
}

// libtmux:parity libtmux.server.Server.default_option_scope
// libtmux:parity libtmux.session.Session.default_option_scope
// libtmux:parity libtmux.constants.DEFAULT_OPTION_SCOPE
// libtmux:parity libtmux.constants.OPTION_SCOPE_FLAG_MAP
// libtmux:parity libtmux.constants.OptionScope
// libtmux:parity libtmux.constants.OptionScope.Pane
// libtmux:parity libtmux.constants.OptionScope.Server
// libtmux:parity libtmux.constants.OptionScope.Session
// libtmux:parity libtmux.constants.OptionScope.Window
// libtmux:parity libtmux.pane.Pane.default_option_scope
// libtmux:parity libtmux.window.Window.default_option_scope
// libtmux:parity libtmux.window.Window.set_window_option
// libtmux:parity libtmux.window.Window.set_window_option#warning:595c4a498323
// libtmux:parity libtmux.window.Window.show_window_options
// libtmux:parity libtmux.window.Window.show_window_options#warning:33caaa4656b3
// libtmux:parity libtmux.options.OptionsMixin
// libtmux:parity libtmux.options.OptionsMixin.__init__
// libtmux:parity libtmux.options.OptionsMixin.__init__#parameter-branch:default_option_scope:2375a8b9e31a
// libtmux:parity libtmux.options.OptionsMixin.default_option_scope
// libtmux:parity libtmux.options.OptionsMixin.set_option
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:_format:b79419c02132
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:_format:c1a152a619ca
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:append:945ab72cb247
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:append:e358cfe094c6
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:ignore_errors:748118c5cba0
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:ignore_errors:e47c0b92f1e1
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:option,value:d3faaf781f06
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:prevent_overwrite:34c16bf03162
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:prevent_overwrite:b258594c6283
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:scope:2855c0da8f8d
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:scope:32fa398cc6e4
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:suppress_warnings:2a9b18081f99
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:suppress_warnings:780df2bfdbf2
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:value:3014ecf52618
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:value:7ae1ab1ce1f8
// libtmux:parity libtmux.options.OptionsMixin.show_option
// libtmux:parity libtmux.options.OptionsMixin.show_options
// libtmux:parity libtmux.options.OptionsMixin.unset_option
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:ignore_errors:748118c5cba0
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:ignore_errors:e47c0b92f1e1
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:option:d3faaf781f06
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:scope:2855c0da8f8d
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:scope:32fa398cc6e4
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:unset_panes:96c894e2433a
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:unset_panes:de1e3fe8e8ee
func TestOptionOperationsBuildExactScopeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server, Session, Window, Pane) error
	}{
		{
			name: "server options",
			want: []string{"show-options", "-s", "-A"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, err := server.Options(context.Background())
				return err
			},
		},
		{
			name: "session options",
			want: []string{"show-options", "-t", "$7", "-A"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				_, err := session.Options(context.Background())
				return err
			},
		},
		{
			name: "window options",
			want: []string{"show-options", "-t", "$7:3", "-w", "-A"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				_, err := window.Options(context.Background())
				return err
			},
		},
		{
			name: "pane options",
			want: []string{"show-options", "-t", "$7:3.%9", "-p", "-A"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				_, err := pane.Options(context.Background())
				return err
			},
		},
		{
			name: "global session options",
			want: []string{"show-options", "-g", "-A"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, err := server.GlobalSessionScope().Options(context.Background())
				return err
			},
		},
		{
			name: "global window options",
			want: []string{"show-options", "-g", "-w", "-A"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, err := server.GlobalWindowScope().Options(context.Background())
				return err
			},
		},
		{
			name: "server raw option",
			want: []string{"show-options", "-s", "-q", "-v", "--", "@custom"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, _, err := server.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "session raw option",
			want: []string{"show-options", "-t", "$7", "-q", "-v", "--", "@custom"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				_, _, err := session.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "window raw option",
			want: []string{"show-options", "-t", "$7:3", "-w", "-q", "-v", "--", "@custom"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				_, _, err := window.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "pane raw option",
			want: []string{"show-options", "-t", "$7:3.%9", "-p", "-q", "-v", "--", "@custom"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				_, _, err := pane.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "server set option",
			want: []string{"set-option", "-s", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.SetOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "session set option",
			want: []string{"set-option", "-t", "$7", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.SetOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "window set option",
			want: []string{"set-option", "-t", "$7:3", "-w", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.SetOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "pane set option",
			want: []string{"set-option", "-t", "$7:3.%9", "-p", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.SetOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "server append option",
			want: []string{"set-option", "-s", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.AppendOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "session append option",
			want: []string{"set-option", "-t", "$7", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.AppendOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "window append option",
			want: []string{"set-option", "-t", "$7:3", "-w", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.AppendOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "pane append option",
			want: []string{"set-option", "-t", "$7:3.%9", "-p", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.AppendOption(context.Background(), "@custom", "value", allSetOptionOptions())
			},
		},
		{
			name: "server unset option",
			want: []string{"set-option", "-s", "-u", "-q", "--", "@custom"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{Quiet: true})
			},
		},
		{
			name: "session unset option",
			want: []string{"set-option", "-t", "$7", "-u", "-q", "--", "@custom"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{Quiet: true})
			},
		},
		{
			name: "window unset panes",
			want: []string{"set-option", "-t", "$7:3", "-w", "-U", "-q", "--", "window-style"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.UnsetOption(
					context.Background(),
					"window-style",
					UnsetOptionOptions{UnsetPanes: true, Quiet: true},
				)
			},
		},
		{
			name: "pane unset option",
			want: []string{"set-option", "-t", "$7:3.%9", "-p", "-u", "-q", "--", "@custom"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{Quiet: true})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{RawStdout: []byte("value\n"), ExitCode: 0},
			}}}
			server, session, window, pane := optionTestObjects(runner)
			if err := test.run(server, session, window, pane); err != nil {
				t.Fatalf("option operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("option arguments = %#v, want %#v", requests, test.want)
			}
		})
	}
}

// libtmux:parity libtmux._internal.dataclasses.SkipDefaultFieldsReprMixin
// libtmux:parity libtmux._internal.dataclasses.SkipDefaultFieldsReprMixin.__repr__
func TestOptionValuesPreserveTerminalSeparators(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{},
	}}}
	err := serverWithRunner(runner).SetOption(
		context.Background(),
		"@literal;",
		"value;",
		SetOptionOptions{},
	)
	if err != nil {
		t.Fatalf("SetOption() error = %v", err)
	}
	requests := runner.recordedRequests()
	want := []string{"set-option", "-s", "--", `@literal\;`, `value\;`}
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("SetOption() arguments = %#v, want %#v", requests, want)
	}
}

func TestOptionModelOperationsValidateStableTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Session, Window, Pane) error
	}{
		{name: "session options", run: func(session Session, _ Window, _ Pane) error {
			_, err := session.Options(context.Background())
			return err
		}},
		{name: "session raw", run: func(session Session, _ Window, _ Pane) error {
			_, _, err := session.RawOption(context.Background(), "@custom")
			return err
		}},
		{name: "session set", run: func(session Session, _ Window, _ Pane) error {
			return session.SetOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "session append", run: func(session Session, _ Window, _ Pane) error {
			return session.AppendOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "session unset", run: func(session Session, _ Window, _ Pane) error {
			return session.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{})
		}},
		{name: "window options", run: func(_ Session, window Window, _ Pane) error {
			_, err := window.Options(context.Background())
			return err
		}},
		{name: "window raw", run: func(_ Session, window Window, _ Pane) error {
			_, _, err := window.RawOption(context.Background(), "@custom")
			return err
		}},
		{name: "window set", run: func(_ Session, window Window, _ Pane) error {
			return window.SetOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "window append", run: func(_ Session, window Window, _ Pane) error {
			return window.AppendOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "window unset", run: func(_ Session, window Window, _ Pane) error {
			return window.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{})
		}},
		{name: "pane options", run: func(_ Session, _ Window, pane Pane) error {
			_, err := pane.Options(context.Background())
			return err
		}},
		{name: "pane raw", run: func(_ Session, _ Window, pane Pane) error {
			_, _, err := pane.RawOption(context.Background(), "@custom")
			return err
		}},
		{name: "pane set", run: func(_ Session, _ Window, pane Pane) error {
			return pane.SetOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "pane append", run: func(_ Session, _ Window, pane Pane) error {
			return pane.AppendOption(context.Background(), "@custom", "value", SetOptionOptions{})
		}},
		{name: "pane unset", run: func(_ Session, _ Window, pane Pane) error {
			return pane.UnsetOption(context.Background(), "@custom", UnsetOptionOptions{})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server := serverWithRunner(runner)
			err := test.run(
				Session{server: server},
				Window{server: server, windowID: WindowID("name")},
				Pane{server: server, paneID: PaneID("%bad")},
			)
			if !errors.Is(err, ErrMissingTarget) && !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("option operation error = %v, want target validation", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("invalid target executed %d commands", runner.callCount())
			}
		})
	}
}

func TestUnsetPanesRequiresWindowReceiverBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, run := range []func(Server, Session, Pane) error{
		func(server Server, _ Session, _ Pane) error {
			return server.UnsetOption(context.Background(), "window-style", UnsetOptionOptions{UnsetPanes: true})
		},
		func(_ Server, session Session, _ Pane) error {
			return session.UnsetOption(context.Background(), "window-style", UnsetOptionOptions{UnsetPanes: true})
		},
		func(_ Server, _ Session, pane Pane) error {
			return pane.UnsetOption(context.Background(), "window-style", UnsetOptionOptions{UnsetPanes: true})
		},
	} {
		runner := &versionQueueRunner{}
		server, session, _, pane := optionTestObjects(runner)
		err := run(server, session, pane)
		if !errors.Is(err, ErrInvalidOption) {
			t.Fatalf("UnsetOption(UnsetPanes) error = %v, want ErrInvalidOption", err)
		}
		if runner.callCount() != 0 {
			t.Fatalf("invalid UnsetPanes executed %d commands", runner.callCount())
		}
	}
}

func TestOptionMutationsRejectKnownNamesOutsideReceiverScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server, Session, Window, Pane) error
	}{
		{name: "server set", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.SetOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "server append", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.AppendOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "server unset", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.UnsetOption(context.Background(), "status", UnsetOptionOptions{})
		}},
		{name: "server alias", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.SetOption(context.Background(), "cursor-color", "red", SetOptionOptions{})
		}},
		{name: "session set", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.SetOption(context.Background(), "backspace", "C-h", SetOptionOptions{})
		}},
		{name: "session append", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.AppendOption(context.Background(), "backspace", "C-h", SetOptionOptions{})
		}},
		{name: "session unset", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.UnsetOption(context.Background(), "backspace", UnsetOptionOptions{})
		}},
		{name: "window set", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "window append", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.AppendOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "window unset", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.UnsetOption(context.Background(), "status", UnsetOptionOptions{})
		}},
		{name: "pane set", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.SetOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "pane append", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.AppendOption(context.Background(), "status", "on", SetOptionOptions{})
		}},
		{name: "pane unset", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.UnsetOption(context.Background(), "status", UnsetOptionOptions{})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server, session, window, pane := optionTestObjects(runner)
			err := test.run(server, session, window, pane)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("option mutation error = %v, want ErrInvalidOption", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("wrong-scope option mutation executed %d commands", runner.callCount())
			}
		})
	}
}

func TestOptionMutationPreflightQueriesVersionOnlyWhenScopeRequiresIt(t *testing.T) {
	t.Parallel()

	t.Run("stable scope does not query", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
		server := serverWithRunner(runner)
		if err := server.SetOption(context.Background(), "backspace", "C-h", SetOptionOptions{}); err != nil {
			t.Fatalf("SetOption() error = %v", err)
		}
		requests := runner.recordedRequests()
		want := []string{"set-option", "-s", "--", "backspace", "C-h"}
		if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
			t.Fatalf("SetOption() requests = %#v, want %#v", requests, want)
		}
	})

	t.Run("supported versioned scope uses cache", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		_, _, _, pane := optionTestObjects(runner)
		if err := pane.SetOption(context.Background(), "pane-active-border-style", "fg=red", SetOptionOptions{}); err != nil {
			t.Fatalf("SetOption() error = %v", err)
		}
		if err := pane.AppendOption(context.Background(), "pane-active-border-style", ",bold", SetOptionOptions{}); err != nil {
			t.Fatalf("AppendOption() error = %v", err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"-V"},
			{"set-option", "-t", "$7:3.%9", "-p", "--", "pane-active-border-style", "fg=red"},
			{"set-option", "-t", "$7:3.%9", "-p", "-a", "--", "pane-active-border-style", ",bold"},
		}
		if len(requests) != len(want) {
			t.Fatalf("versioned option requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("versioned option request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})

	for _, test := range []struct {
		name    string
		version string
		run     func(Server, Pane) error
	}{
		{
			name:    "pane scope before introduction",
			version: "3.6",
			run: func(_ Server, pane Pane) error {
				return pane.SetOption(context.Background(), "pane-active-border-style", "fg=red", SetOptionOptions{})
			},
		},
		{
			name:    "server option before introduction",
			version: "3.5",
			run: func(server Server, _ Pane) error {
				return server.UnsetOption(context.Background(), "codepoint-widths", UnsetOptionOptions{})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}, ExitCode: 0},
			}}}
			server, _, _, pane := optionTestObjects(runner)
			err := test.run(server, pane)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("versioned option mutation error = %v, want ErrInvalidOption", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, []string{"-V"}) {
				t.Fatalf("unsupported versioned option requests = %#v, want version probe only", requests)
			}
		})
	}

	t.Run("unknown names pass through without a query", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		_, session, _, _ := optionTestObjects(runner)
		if err := session.SetOption(context.Background(), "future-option", "value", SetOptionOptions{}); err != nil {
			t.Fatalf("SetOption(unknown) error = %v", err)
		}
		if err := session.UnsetOption(context.Background(), "future-option", UnsetOptionOptions{}); err != nil {
			t.Fatalf("UnsetOption(unknown) error = %v", err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"set-option", "-t", "$7", "--", "future-option", "value"},
			{"set-option", "-t", "$7", "-u", "--", "future-option"},
		}
		if len(requests) != len(want) {
			t.Fatalf("unknown option requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("unknown option request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})
}

// libtmux:parity libtmux.common.WindowOptionDict
// libtmux:parity libtmux.options.ConvertedValue
// libtmux:parity libtmux.options.ConvertedValues
// libtmux:parity libtmux.options.ExplodedComplexUntypedOptionsDict
// libtmux:parity libtmux.options.ExplodedUntypedOptionsDict
// libtmux:parity libtmux.options.OptionDict
// libtmux:parity libtmux.options.OptionsMixin.show_option
// libtmux:parity libtmux.options.OptionsMixin.show_options
// libtmux:parity libtmux.options.UntypedOptionsDict
// libtmux:parity libtmux.options.convert_value
// libtmux:parity libtmux.options.convert_value#parameter-branch:value:08fec6ce19e7
// libtmux:parity libtmux.options.convert_value#parameter-branch:value:280f36395c72
// libtmux:parity libtmux.options.convert_value#parameter-branch:value:7c8d96098f59
// libtmux:parity libtmux.options.convert_value#parameter-branch:value:7e40614009da
// libtmux:parity libtmux.options.convert_values
// libtmux:parity libtmux.options.convert_values#parameter-branch:value:325eb7d0cc70
// libtmux:parity libtmux.options.convert_values#parameter-branch:value:98507a1f7edd
// libtmux:parity libtmux.options.convert_values#parameter-branch:value:c0c67f3cf246
// libtmux:parity libtmux.options.explode_arrays
// libtmux:parity libtmux.options.explode_arrays#parameter-branch:force_array:6427948e563a
// libtmux:parity libtmux.options.explode_arrays#parameter-branch:force_array:a94938fdd506
// libtmux:parity libtmux.options.parse_options_to_dict
func TestOptionsDecodeExactTypedPresenceOriginAndSparseHoles(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"@ignored custom",
		"activity-action* none",
		"base-index 3",
		"mouse off",
		"status-left ''",
		`update-environment[2]* ''`,
		`update-environment[7]* "A B"`,
		"",
	}, "\n")
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{RawStdout: []byte(raw), ExitCode: 0},
	}}}
	_, session, _, _ := optionTestObjects(runner)
	values, err := session.Options(context.Background())
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}

	if got, ok := values.ActivityAction().Get(); !ok || got != "none" {
		t.Fatalf("ActivityAction().Get() = (%q, %t), want (none, true)", got, ok)
	}
	if origin, ok := values.ActivityAction().Origin(); !ok || origin != OptionOriginInherited {
		t.Fatalf("ActivityAction().Origin() = (%v, %t), want inherited", origin, ok)
	}
	if got, ok := values.BaseIndex().Get(); !ok || got != 3 {
		t.Fatalf("BaseIndex().Get() = (%d, %t), want (3, true)", got, ok)
	}
	if got, ok := values.Mouse().Get(); !ok || got {
		t.Fatalf("Mouse().Get() = (%t, %t), want (false, true)", got, ok)
	}
	if got, ok := values.StatusLeft().Get(); !ok || got != "" {
		t.Fatalf("StatusLeft().Get() = (%q, %t), want present empty", got, ok)
	}
	if origin, ok := values.StatusLeft().Origin(); !ok || origin != OptionOriginLocal {
		t.Fatalf("StatusLeft().Origin() = (%v, %t), want local", origin, ok)
	}
	if _, ok := values.StatusRight().Get(); ok {
		t.Fatal("StatusRight().Get() reported an absent record present")
	}

	array, ok := values.UpdateEnvironment().Get()
	if !ok {
		t.Fatal("UpdateEnvironment().Get() reported absent")
	}
	if got, want := array.Indices(), []int{2, 7}; !slices.Equal(got, want) {
		t.Fatalf("UpdateEnvironment().Indices() = %#v, want %#v", got, want)
	}
	if got, ok := array.Get(2); !ok || got != "" {
		t.Fatalf("UpdateEnvironment()[2] = (%q, %t), want present empty", got, ok)
	}
	if got, ok := array.Get(7); !ok || got != "A B" {
		t.Fatalf("UpdateEnvironment()[7] = (%q, %t), want (A B, true)", got, ok)
	}
	if _, ok := array.Get(3); ok {
		t.Fatal("UpdateEnvironment()[3] filled a sparse hole")
	}
	if origin, ok := values.UpdateEnvironment().Origin(); !ok || origin != OptionOriginInherited {
		t.Fatalf("UpdateEnvironment().Origin() = (%v, %t), want inherited", origin, ok)
	}

	entries := array.Entries()
	entries[0].Value = "mutated"
	again, _ := values.UpdateEnvironment().Get()
	if got, _ := again.Get(2); got != "" {
		t.Fatalf("typed option array aliases caller slice: %q", got)
	}
}

func TestOptionsDecodePresentEmptyArray(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{RawStdout: []byte("update-environment*\n"), ExitCode: 0},
	}}}
	_, session, _, _ := optionTestObjects(runner)
	values, err := session.Options(context.Background())
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	array, ok := values.UpdateEnvironment().Get()
	if !ok || array.Len() != 0 {
		t.Fatalf("UpdateEnvironment().Get() = (%#v, %t), want present empty array", array, ok)
	}
	if origin, ok := values.UpdateEnvironment().Origin(); !ok || origin != OptionOriginInherited {
		t.Fatalf("UpdateEnvironment().Origin() = (%v, %t), want inherited", origin, ok)
	}
}

// libtmux:parity libtmux._internal.constants.Options.__init__
// libtmux:parity libtmux._internal.constants.PaneOptions.__init__
// libtmux:parity libtmux._internal.constants.ServerOptions.__init__
// libtmux:parity libtmux._internal.constants.SessionOptions.__init__
// libtmux:parity libtmux._internal.constants.WindowOptions.__init__
func TestOptionsAcceptTmuxTableOrderAndCanonicalizeGeneratedConstruction(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{
			RawStdout: []byte("command-alias[0] copy-mode=copy-mode -e\ncodepoint-widths\n"),
			ExitCode:  0,
		}},
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
	}}
	values, err := serverWithRunner(runner).Options(context.Background())
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	aliases, ok := values.CommandAlias().Get()
	if !ok {
		t.Fatal("CommandAlias().Get() reported absent")
	}
	if got, ok := aliases.Get(0); !ok || got != "copy-mode=copy-mode -e" {
		t.Fatalf("CommandAlias()[0] = (%q, %t)", got, ok)
	}
	widths, ok := values.CodepointWidths().Get()
	if !ok || widths.Len() != 0 {
		t.Fatalf("CodepointWidths().Get() = (%#v, %t), want present empty", widths, ok)
	}
}

func TestOptionsRejectFutureGeneratedRecordInjectedByCustomName(t *testing.T) {
	t.Parallel()

	const secret = "future-value-must-not-be-reported"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{
			RawStdout: []byte("@attacker harmless\ncodepoint-widths[0] " + secret + "\n"),
			ExitCode:  0,
		}},
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.5"}, ExitCode: 0}},
	}}
	_, err := serverWithRunner(runner).Options(context.Background())
	if !errors.Is(err, ErrMalformedOptionOutput) {
		t.Fatalf("Options() error = %v, want ErrMalformedOptionOutput", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Options() error disclosed injected value: %v", err)
	}
	requests := runner.recordedRequests()
	want := [][]string{{"show-options", "-s", "-A"}, {"-V"}}
	if len(requests) != len(want) {
		t.Fatalf("Options() requests = %#v, want %#v", requests, want)
	}
	for index := range want {
		if !slices.Equal(requests[index].Arguments, want[index]) {
			t.Fatalf("Options() request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
		}
	}
}

// TestOptionsVersionBoundaryFailuresAreReportedWithoutDisclosure covers the
// version probe an option decode depends on. Every way it can fail is reported,
// because a zero option value is indistinguishable from one tmux set, and none
// of them may quote the option value or the version output back: both are
// caller data that an error is not a place for.
func TestOptionsVersionBoundaryFailuresAreReportedWithoutDisclosure(t *testing.T) {
	t.Parallel()

	const (
		optionSecret  = "option-value-must-not-be-reported"
		versionSecret = "version-output-must-not-be-reported"
	)
	transport := errors.New("version transport failed")
	tests := []struct {
		name          string
		response      versionResponse
		wantError     error
		wantMalformed bool
	}{
		{
			name:      "transport failure",
			response:  versionResponse{err: transport},
			wantError: transport,
		},
		{
			name: "completed failure",
			response: versionResponse{result: tmuxcmd.Result{
				Stderr:   []string{"version command failed"},
				ExitCode: 1,
			}},
			wantError: ErrVersionQuery,
		},
		{
			name:          "invalid version token",
			response:      versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux " + versionSecret}}},
			wantError:     ErrVersionQuery,
			wantMalformed: true,
		},
		{
			name:          "malformed successful output",
			response:      versionResponse{result: tmuxcmd.Result{Stdout: []string{versionSecret}}},
			wantError:     ErrVersionQuery,
			wantMalformed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{
					RawStdout: []byte("@attacker harmless\ncodepoint-widths[0] " + optionSecret + "\n"),
					ExitCode:  0,
				}},
				test.response,
			}}
			values, err := serverWithRunner(runner).Options(context.Background())
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Options() error = %v, want %v", err, test.wantError)
			}
			if _, ok := values.CodepointWidths().Get(); ok {
				t.Fatal("Options() returned a decoded value beside an error")
			}
			if test.wantMalformed && !errors.Is(err, ErrMalformedOptionOutput) {
				t.Fatalf("Options() error = %v, want ErrMalformedOptionOutput", err)
			}
			if strings.Contains(err.Error(), optionSecret) || strings.Contains(err.Error(), versionSecret) {
				t.Fatalf("Options() error disclosed command output: %v", err)
			}
		})
	}
}

func TestOptionsRejectMalformedRecognizedRecordsWithoutDisclosingValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "duplicate scalar", raw: "status-left one\nstatus-left two\n"},
		{name: "noncontiguous scalar", raw: "status-left one\nstatus-right two\nstatus-left three\n"},
		{name: "newline spoof", raw: "@custom ignored\nstatus-left spoof\nstatus-right actual\nstatus-left canonical\n"},
		{name: "indexed scalar", raw: "status-left[0] secret-value\n"},
		{name: "scalar missing value", raw: "status-left\n"},
		{name: "duplicate array index", raw: "update-environment[2] one\nupdate-environment[2] two\n"},
		{name: "descending array index", raw: "update-environment[7] one\nupdate-environment[2] two\n"},
		{name: "bare and indexed array", raw: "update-environment ''\nupdate-environment[2] two\n"},
		{name: "indexed and bare array", raw: "update-environment[2] two\nupdate-environment ''\n"},
		{name: "mixed array origin", raw: "update-environment[2] one\nupdate-environment[7]* two\n"},
		{name: "nonempty bare array", raw: "update-environment secret-value\n"},
		{name: "invalid bool", raw: "mouse secret-value\n"},
		{name: "invalid number", raw: "base-index secret-value\n"},
		{name: "malformed escaped value", raw: "status-left 'secret-value\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{RawStdout: []byte(test.raw), ExitCode: 0},
			}}}
			_, session, _, _ := optionTestObjects(runner)
			_, err := session.Options(context.Background())
			if !errors.Is(err, ErrMalformedOptionOutput) {
				t.Fatalf("Options() error = %v, want ErrMalformedOptionOutput", err)
			}
			var decodeError *OptionDecodeError
			if !errors.As(err, &decodeError) || decodeError.Reason == "" {
				t.Fatalf("Options() error = %#v, want exported decode detail", err)
			}
			if strings.Contains(err.Error(), "secret-value") ||
				strings.Contains(err.Error(), "spoof") ||
				strings.Contains(err.Error(), "actual") {
				t.Fatalf("decode error disclosed option output: %v", err)
			}
		})
	}
}

func TestOptionBulkReadReportsFailures(t *testing.T) {
	t.Parallel()

	t.Run("completed failure discards partial output", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{
				RawStdout: []byte("status-left leaked-partial\n"),
				Stderr:    []string{"unknown option: private"},
				ExitCode:  1,
			},
		}}}
		values, err := serverWithRunner(runner).Options(context.Background())
		if !errors.Is(err, ErrUnknownOption) {
			t.Fatalf("Options() error = %v, want ErrUnknownOption", err)
		}
		if _, ok := values.Backspace().Get(); ok {
			t.Fatal("Options() parsed the partial output tmux wrote before failing")
		}
	})

	t.Run("non-context transport", func(t *testing.T) {
		t.Parallel()

		transport := errors.New("transport failed")
		runner := &versionQueueRunner{responses: []versionResponse{{err: transport}}}
		if _, err := serverWithRunner(runner).Options(context.Background()); !errors.Is(err, transport) {
			t.Fatalf("Options() transport error = %v, want transport", err)
		}
	})

	for _, contextError := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(contextError.Error(), func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{err: contextError}}}
			_, err := serverWithRunner(runner).Options(context.Background())
			if !errors.Is(err, contextError) {
				t.Fatalf("Options() context error = %v, want %v", err, contextError)
			}
		})
	}

	t.Run("decode is always loud", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{RawStdout: []byte("status-left one\nstatus-left two\n"), ExitCode: 0},
		}}}
		_, err := serverWithRunner(runner).GlobalSessionScope().Options(context.Background())
		if !errors.Is(err, ErrMalformedOptionOutput) {
			t.Fatalf("lenient decode error = %v, want ErrMalformedOptionOutput", err)
		}
	})
}

func TestGlobalOptionScopesReportBulkReadFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "session",
			run: func(server Server) error {
				_, err := server.GlobalSessionScope().Options(context.Background())
				return err
			},
		},
		{
			name: "window",
			run: func(server Server) error {
				_, err := server.GlobalWindowScope().Options(context.Background())
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{
					Stderr: []string{"invalid option: private"}, ExitCode: 1,
				},
			}}}
			if err := test.run(serverWithRunner(runner)); !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("Options() error = %v, want ErrInvalidOption", err)
			}
		})
	}
}

// libtmux:parity libtmux.window.Window.show_window_option
// libtmux:parity libtmux.window.Window.show_window_option#warning:a89297922e19
func TestRawOptionPreservesPresenceAndExactTerminalLineFeedSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response tmuxcmd.Result
		want     string
		wantOK   bool
		wantErr  error
	}{
		{
			name:     "quiet miss even nonzero",
			response: tmuxcmd.Result{ExitCode: 1},
		},
		{
			name:     "present empty scalar",
			response: tmuxcmd.Result{RawStdout: []byte("\n"), ExitCode: 0},
			wantOK:   true,
		},
		{
			name:     "remove exactly one line feed",
			response: tmuxcmd.Result{RawStdout: []byte("line one\nline two\n\n"), ExitCode: 0},
			want:     "line one\nline two\n",
			wantOK:   true,
		},
		{
			name:     "stderr at zero exit",
			response: tmuxcmd.Result{Stderr: []string{"invalid option: @custom"}, ExitCode: 0},
			wantErr:  ErrInvalidOption,
		},
		{
			name: "nonzero with output",
			response: tmuxcmd.Result{
				RawStdout: []byte("partial\n"),
				Stderr:    []string{"ambiguous option: @custom"},
				ExitCode:  1,
			},
			wantErr: ErrAmbiguousOption,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: test.response}}}
			got, ok, err := serverWithRunner(runner).RawOption(context.Background(), "@custom")
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("RawOption() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want || ok != test.wantOK {
				t.Fatalf("RawOption() = (%q, %t, %v), want (%q, %t, nil)", got, ok, err, test.want, test.wantOK)
			}
		})
	}
}

func TestRawIndexedOptionRequeriesEscapedBaseListingForEmptyOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		listing string
		wantOK  bool
	}{
		{name: "present empty", listing: "update-environment[3] ''\n", wantOK: true},
		{name: "missing hole", listing: "update-environment[7] value\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{RawStdout: []byte("\n"), ExitCode: 0}},
				{result: tmuxcmd.Result{RawStdout: []byte(test.listing), ExitCode: 0}},
			}}
			server := serverWithRunner(runner)
			got, ok, err := server.RawOption(context.Background(), "update-environment[3]")
			if err != nil || got != "" || ok != test.wantOK {
				t.Fatalf("RawOption(index) = (%q, %t, %v), want (empty, %t, nil)", got, ok, err, test.wantOK)
			}
			requests := runner.recordedRequests()
			want := [][]string{
				{"show-options", "-s", "-q", "-v", "--", "update-environment[3]"},
				{"show-options", "-s", "-q", "--", "update-environment"},
			}
			if len(requests) != len(want) {
				t.Fatalf("RawOption(index) requests = %#v, want %#v", requests, want)
			}
			for index := range want {
				if !slices.Equal(requests[index].Arguments, want[index]) {
					t.Fatalf("RawOption(index) request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
				}
			}
		})
	}
}

func TestIndexedOptionPresenceRejectsUnexpectedAndNonemptyBareRecords(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "unexpected base", raw: "status-left harmless\n"},
		{name: "nonempty bare value", raw: "update-environment secret-value\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := indexedOptionPresent([]byte(test.raw), "update-environment", 3)
			if !errors.Is(err, ErrMalformedOptionOutput) {
				t.Fatalf("indexedOptionPresent() error = %v, want ErrMalformedOptionOutput", err)
			}
			if strings.Contains(err.Error(), "secret-value") ||
				strings.Contains(err.Error(), "status-left") ||
				strings.Contains(err.Error(), "harmless") {
				t.Fatalf("indexedOptionPresent() error disclosed output: %v", err)
			}
		})
	}

	for _, raw := range []string{"update-environment\n", "update-environment \n"} {
		present, err := indexedOptionPresent([]byte(raw), "update-environment", 3)
		if err != nil || present {
			t.Fatalf("indexedOptionPresent(%q) = (%t, %v), want absent", raw, present, err)
		}
	}
}

// libtmux:parity libtmux.exc.AmbiguousOption
// libtmux:parity libtmux.exc.InvalidOption
// libtmux:parity libtmux.exc.OptionError
// libtmux:parity libtmux.exc.UnknownOption
// libtmux:parity libtmux.options.handle_option_error
// libtmux:parity libtmux.options.handle_option_error#parameter-branch:error:1136d96388c5
// libtmux:parity libtmux.options.handle_option_error#parameter-branch:error:2fbb42d5a549
// libtmux:parity libtmux.options.handle_option_error#parameter-branch:error:b3e270235515
func TestOptionErrorsClassifyBeforeRedactingOwnedResults(t *testing.T) {
	t.Parallel()

	if !errors.Is(ErrUnknownOption, ErrOption) ||
		!errors.Is(ErrInvalidOption, ErrOption) ||
		!errors.Is(ErrAmbiguousOption, ErrOption) {
		t.Fatal("specific option sentinels must wrap ErrOption")
	}

	tests := []struct {
		name   string
		stderr []string
		want   error
	}{
		{name: "unknown before other words", stderr: []string{"ambiguous invalid option unknown option"}, want: ErrUnknownOption},
		{name: "invalid", stderr: []string{"invalid option: status"}, want: ErrInvalidOption},
		{name: "ambiguous", stderr: []string{"ambiguous option: stat"}, want: ErrAmbiguousOption},
		{name: "only first line", stderr: []string{"generic failure", "unknown option: hidden"}, want: ErrOption},
		{name: "case sensitive", stderr: []string{"Unknown option: status"}, want: ErrOption},
		{name: "no stderr", want: ErrOption},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stderr := slices.Clone(test.stderr)
			if len(stderr) != 0 {
				stderr[0] += ": secret-stderr"
			}
			source := tmuxcmd.Result{
				Command:  []string{"tmux", "set-option", "backspace", "secret-command"},
				Stdout:   []string{"secret-stdout"},
				Stderr:   stderr,
				ExitCode: 1,
			}
			runner := &versionQueueRunner{responses: []versionResponse{{result: source}}}
			err := serverWithRunner(runner).SetOption(
				context.Background(),
				"backspace",
				"secret-value",
				SetOptionOptions{},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("SetOption() error = %v, want %v", err, test.want)
			}
			var optionError *OptionError
			if !errors.As(err, &optionError) {
				t.Fatalf("SetOption() error type = %T, want *OptionError", err)
			}
			if optionError.Subcommand != "set-option" || optionError.Name != "backspace" || optionError.Result.ExitCode != 1 {
				t.Fatalf("OptionError = %#v, want operation metadata", optionError)
			}
			if optionError.Result.Command != nil ||
				optionError.Result.Stdout != nil ||
				optionError.Result.Stderr != nil {
				t.Fatalf("OptionError retained command output: %#v", optionError.Result)
			}
			optionError.Result.Stderr = []string{"secret-injected-stderr"}
			for _, secret := range []string{
				"secret-value",
				"secret-command",
				"secret-stdout",
				"secret-stderr",
				"secret-injected-stderr",
			} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("OptionError disclosed %q: %v", secret, err)
				}
			}
		})
	}
}

// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:ignore_errors:748118c5cba0
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:ignore_errors:e47c0b92f1e1
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:suppress_warnings:2a9b18081f99
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:suppress_warnings:780df2bfdbf2
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:ignore_errors:748118c5cba0
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:ignore_errors:e47c0b92f1e1
func TestOptionMutationsTreatStderrAtZeroExitAsFailureAndQuietMissAsSuccess(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stderr: []string{"invalid option: @custom"}, ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 1}},
	}}
	server := serverWithRunner(runner)
	if err := server.SetOption(context.Background(), "@custom", "value", SetOptionOptions{}); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("SetOption(stderr, exit 0) error = %v, want ErrInvalidOption", err)
	}
	if err := server.UnsetOption(context.Background(), "@missing", UnsetOptionOptions{Quiet: true}); err != nil {
		t.Fatalf("quiet UnsetOption(missing) error = %v", err)
	}
}

func allSetOptionOptions() SetOptionOptions {
	return SetOptionOptions{ExpandFormat: true, PreventOverwrite: true, Quiet: true}
}

func TestTypedScalarOptionSettersEncodeEveryValueKindAndScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server, Session, Window, Pane) error
	}{
		{name: "server number", want: []string{"set-option", "-s", "--", "buffer-limit", "-12"}, run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.SetBufferLimit(context.Background(), -12)
		}},
		{name: "session bool", want: []string{"set-option", "-t", "$7", "--", "mouse", "on"}, run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.SetMouse(context.Background(), true)
		}},
		{name: "window string", want: []string{"set-option", "-t", "$7:3", "-w", "--", "pane-border-format", `#{pane_id}\;`}, run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetPaneBorderFormat(context.Background(), "#{pane_id};")
		}},
		{name: "pane bool", want: []string{"set-option", "-t", "$7:3.%9", "-p", "--", "synchronize-panes", "off"}, run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.SetSynchronizePanes(context.Background(), false)
		}},
		{name: "global session string", want: []string{"set-option", "-g", "--", "status-left", `private\;`}, run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalSessionScope().SetStatusLeft(context.Background(), "private;")
		}},
		{name: "global window choice", want: []string{"set-option", "-g", "-w", "--", "pane-border-status", "top"}, run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalWindowScope().SetPaneBorderStatus(context.Background(), PaneBorderStatusTop)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			server, session, window, pane := optionTestObjects(runner)
			if err := test.run(server, session, window, pane); err != nil {
				t.Fatalf("typed setter error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("typed setter requests = %#v, want %#v", requests, test.want)
			}
		})
	}
}

// libtmux:parity libtmux.options.OptionsMixin.set_option
func TestTypedArrayOptionSettersReplaceWithExactScopeArguments(t *testing.T) {
	t.Parallel()

	values := SparseArray[string]{entries: []SparseEntry[string]{
		{Index: 4, Value: "trailing;"},
		{Index: 0, Value: ""},
	}}
	tests := []struct {
		name         string
		versionProbe bool
		want         [][]string
		run          func(Server, Session, Window, Pane) (SetArrayResult, error)
	}{
		{
			name: "server",
			want: [][]string{
				{"set-option", "-s", "--", "command-alias", ""},
				{"set-option", "-s", "--", "command-alias[0]", ""},
				{"set-option", "-s", "--", "command-alias[4]", `trailing\;`},
			},
			run: func(server Server, _ Session, _ Window, _ Pane) (SetArrayResult, error) {
				return server.SetCommandAlias(context.Background(), values)
			},
		},
		{
			name: "global session",
			want: [][]string{
				{"set-option", "-g", "--", "status-format", ""},
				{"set-option", "-g", "--", "status-format[0]", ""},
				{"set-option", "-g", "--", "status-format[4]", `trailing\;`},
			},
			run: func(server Server, _ Session, _ Window, _ Pane) (SetArrayResult, error) {
				return server.GlobalSessionScope().SetStatusFormat(context.Background(), values)
			},
		},
		{
			name: "session",
			want: [][]string{
				{"set-option", "-t", "$7", "--", "update-environment", ""},
				{"set-option", "-t", "$7", "--", "update-environment[0]", ""},
				{"set-option", "-t", "$7", "--", "update-environment[4]", `trailing\;`},
			},
			run: func(_ Server, session Session, _ Window, _ Pane) (SetArrayResult, error) {
				return session.SetUpdateEnvironment(context.Background(), values)
			},
		},
		{
			name:         "global window",
			versionProbe: true,
			want: [][]string{
				{"-V"},
				{"set-option", "-g", "-w", "--", "pane-colours", ""},
				{"set-option", "-g", "-w", "--", "pane-colours[0]", ""},
				{"set-option", "-g", "-w", "--", "pane-colours[4]", `trailing\;`},
			},
			run: func(server Server, _ Session, _ Window, _ Pane) (SetArrayResult, error) {
				return server.GlobalWindowScope().SetPaneColours(context.Background(), values)
			},
		},
		{
			name:         "window",
			versionProbe: true,
			want: [][]string{
				{"-V"},
				{"set-option", "-t", "$7:3", "-w", "--", "pane-colours", ""},
				{"set-option", "-t", "$7:3", "-w", "--", "pane-colours[0]", ""},
				{"set-option", "-t", "$7:3", "-w", "--", "pane-colours[4]", `trailing\;`},
			},
			run: func(_ Server, _ Session, window Window, _ Pane) (SetArrayResult, error) {
				return window.SetPaneColours(context.Background(), values)
			},
		},
		{
			name:         "pane",
			versionProbe: true,
			want: [][]string{
				{"-V"},
				{"set-option", "-t", "$7:3.%9", "-p", "--", "pane-colours", ""},
				{"set-option", "-t", "$7:3.%9", "-p", "--", "pane-colours[0]", ""},
				{"set-option", "-t", "$7:3.%9", "-p", "--", "pane-colours[4]", `trailing\;`},
			},
			run: func(_ Server, _ Session, _ Window, pane Pane) (SetArrayResult, error) {
				return pane.SetPaneColours(context.Background(), values)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			responses := make([]versionResponse, len(test.want))
			if test.versionProbe {
				responses[0].result.Stdout = []string{"tmux 3.7b"}
			}
			runner := &versionQueueRunner{responses: responses}
			server, session, window, pane := optionTestObjects(runner)
			result, err := test.run(server, session, window, pane)
			if err != nil {
				t.Fatalf("typed array setter error = %v", err)
			}
			if !result.Replaced || !slices.Equal(result.AppliedIndices, []int{0, 4}) {
				t.Fatalf("typed array setter result = %#v, want replaced indices 0 and 4", result)
			}
			requests := runner.recordedRequests()
			if len(requests) != len(test.want) {
				t.Fatalf("typed array setter requests = %#v, want %#v", requests, test.want)
			}
			for index := range requests {
				if !slices.Equal(requests[index].Arguments, test.want[index]) {
					t.Errorf("request %d arguments = %#v, want %#v", index, requests[index].Arguments, test.want[index])
				}
			}
		})
	}
}

func TestTypedArrayOptionSetterReplacesWithExplicitEmptyArray(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{}}}
	result, err := serverWithRunner(runner).SetCommandAlias(
		context.Background(), SparseArray[string]{},
	)
	if err != nil {
		t.Fatalf("SetCommandAlias(empty) error = %v", err)
	}
	if !result.Replaced || result.AppliedIndices == nil || len(result.AppliedIndices) != 0 {
		t.Fatalf("SetCommandAlias(empty) result = %#v, want replaced with non-nil empty indices", result)
	}
	want := []string{"set-option", "-s", "--", "command-alias", ""}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("SetCommandAlias(empty) requests = %#v, want %#v", requests, want)
	}
}

func TestTypedArrayOptionSetterPrevalidatesEveryEntryBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values SparseArray[string]
		want   error
		run    func(Server, SparseArray[string]) (SetArrayResult, error)
	}{
		{
			name: "later NUL",
			values: SparseArray[string]{entries: []SparseEntry[string]{
				{Index: 0, Value: "safe"},
				{Index: 2, Value: "private\x00value"},
			}},
			want: ErrInvalidServerCommandRequest,
			run: func(server Server, values SparseArray[string]) (SetArrayResult, error) {
				return server.SetCodepointWidths(context.Background(), values)
			},
		},
		{
			name: "index above signed 32-bit",
			values: SparseArray[string]{entries: []SparseEntry[string]{
				{Index: int(int64(math.MaxInt32) + 1), Value: "safe"},
			}},
			want: ErrInvalidSparseIndex,
			run: func(server Server, values SparseArray[string]) (SetArrayResult, error) {
				return server.SetCommandAlias(context.Background(), values)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{}
			result, err := test.run(serverWithRunner(runner), test.values)
			if !errors.Is(err, test.want) {
				t.Fatalf("SetCommandAlias() error = %v, want %v", err, test.want)
			}
			if result.Replaced || result.AppliedIndices != nil {
				t.Fatalf("SetCommandAlias() result = %#v, want zero result", result)
			}
			if runner.callCount() != 0 {
				t.Fatalf("SetCommandAlias() calls = %d, want 0", runner.callCount())
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("SetCommandAlias() error disclosed value: %v", err)
			}
		})
	}
}

func TestTypedArrayOptionSetterRejectsUnsupportedVersionBeforeMutation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stdout: []string{"tmux 3.5"},
	}}}}
	result, err := serverWithRunner(runner).SetCodepointWidths(
		context.Background(), SparseArray[string]{},
	)
	if !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("SetCodepointWidths(tmux 3.5) error = %v, want ErrInvalidOption", err)
	}
	if result.Replaced || result.AppliedIndices != nil {
		t.Fatalf("SetCodepointWidths(tmux 3.5) result = %#v, want zero result", result)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, []string{"-V"}) {
		t.Fatalf("SetCodepointWidths(tmux 3.5) requests = %#v, want one version probe", requests)
	}
}

func TestTypedArrayOptionSetterReportsCompletedPartialProgress(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "first-private-value"},
		SparseEntry[string]{Index: 2, Value: "second-private-value"},
		SparseEntry[string]{Index: 4, Value: "third-private-value"},
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		responses     []versionResponse
		wantReplaced  bool
		wantApplied   []int
		wantCalls     int
		wantErrorName string
	}{
		{
			name: "base exit",
			responses: []versionResponse{{result: tmuxcmd.Result{
				ExitCode: 1, Stderr: []string{"private base diagnostic"},
			}}},
			wantApplied:   []int{},
			wantCalls:     1,
			wantErrorName: "command-alias",
		},
		{
			name: "base stderr at zero",
			responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"private base diagnostic"},
			}}},
			wantApplied:   []int{},
			wantCalls:     1,
			wantErrorName: "command-alias",
		},
		{
			name: "indexed exit",
			responses: []versionResponse{
				{},
				{},
				{result: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"private indexed diagnostic"}}},
			},
			wantReplaced:  true,
			wantApplied:   []int{0},
			wantCalls:     3,
			wantErrorName: "command-alias[2]",
		},
		{
			name: "indexed stderr at zero",
			responses: []versionResponse{
				{},
				{},
				{result: tmuxcmd.Result{Stderr: []string{"private indexed diagnostic"}}},
			},
			wantReplaced:  true,
			wantApplied:   []int{0},
			wantCalls:     3,
			wantErrorName: "command-alias[2]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: test.responses}
			result, err := serverWithRunner(runner).SetCommandAlias(context.Background(), values)
			if !errors.Is(err, ErrOption) {
				t.Fatalf("SetCommandAlias() error = %v, want ErrOption", err)
			}
			if result.Replaced != test.wantReplaced || !slices.Equal(result.AppliedIndices, test.wantApplied) {
				t.Fatalf("SetCommandAlias() result = %#v, want replaced=%t applied=%v", result, test.wantReplaced, test.wantApplied)
			}
			if result.AppliedIndices == nil {
				t.Fatal("SetCommandAlias() returned nil attempted-operation indices")
			}
			if runner.callCount() != test.wantCalls {
				t.Fatalf("SetCommandAlias() calls = %d, want %d without rollback", runner.callCount(), test.wantCalls)
			}
			var optionError *OptionError
			if !errors.As(err, &optionError) || optionError.Name != test.wantErrorName ||
				optionError.Result.Command != nil || optionError.Result.Stdout != nil ||
				optionError.Result.Stderr != nil {
				t.Fatalf("SetCommandAlias() error = %#v, want redacted attempted option error", err)
			}
			for _, secret := range []string{"first-private-value", "second-private-value", "private base", "private indexed"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("SetCommandAlias() error disclosed %q: %v", secret, err)
				}
			}
		})
	}
}

func TestTypedArrayOptionSetterReportsOnlyConfirmedProgressOnTransportFailure(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "first"},
		SparseEntry[string]{Index: 2, Value: "second"},
	)
	if err != nil {
		t.Fatal(err)
	}
	transportError := errors.New("transport failed")
	tests := []struct {
		name         string
		responses    []versionResponse
		want         error
		wantReplaced bool
		wantApplied  []int
	}{
		{name: "base transport", responses: []versionResponse{{err: transportError}}, want: transportError, wantApplied: []int{}},
		{name: "base context", responses: []versionResponse{{err: context.Canceled}}, want: context.Canceled, wantApplied: []int{}},
		{
			name:      "later transport",
			responses: []versionResponse{{}, {}, {err: transportError}},
			want:      transportError, wantReplaced: true, wantApplied: []int{0},
		},
		{
			name:      "later context",
			responses: []versionResponse{{}, {}, {err: context.DeadlineExceeded}},
			want:      context.DeadlineExceeded, wantReplaced: true, wantApplied: []int{0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: test.responses}
			result, err := serverWithRunner(runner).SetCommandAlias(context.Background(), values)
			if !errors.Is(err, test.want) {
				t.Fatalf("SetCommandAlias() error = %v, want %v", err, test.want)
			}
			if result.Replaced != test.wantReplaced || !slices.Equal(result.AppliedIndices, test.wantApplied) {
				t.Fatalf("SetCommandAlias() result = %#v, want replaced=%t applied=%v", result, test.wantReplaced, test.wantApplied)
			}
			if result.AppliedIndices == nil {
				t.Fatal("SetCommandAlias() returned nil attempted-operation indices")
			}
			if runner.callCount() != len(test.responses) {
				t.Fatalf("SetCommandAlias() calls = %d, want %d without rollback", runner.callCount(), len(test.responses))
			}
		})
	}
}

func TestTypedArrayOptionSetterOwnsResultsAndFreezesInput(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 1, Value: "original"})
	if err != nil {
		t.Fatal(err)
	}
	mutationRunner := &mutatingSparseOptionRunner{values: &values}
	result, err := serverWithRunner(mutationRunner).SetCommandAlias(context.Background(), values)
	if err != nil {
		t.Fatalf("SetCommandAlias() error = %v", err)
	}
	requests := mutationRunner.recordedRequests()
	wantIndexed := []string{"set-option", "-s", "--", "command-alias[1]", "original"}
	if len(requests) != 2 || !slices.Equal(requests[1].Arguments, wantIndexed) {
		t.Fatalf("SetCommandAlias() requests = %#v, want frozen indexed write %#v", requests, wantIndexed)
	}
	if !slices.Equal(result.AppliedIndices, []int{1}) {
		t.Fatalf("SetCommandAlias() result = %#v, want original index", result)
	}

	versionValues, err := NewSparseArray(SparseEntry[string]{Index: 1, Value: "version-original"})
	if err != nil {
		t.Fatal(err)
	}
	versionMutationRunner := &mutatingSparseOptionRunner{values: &versionValues}
	result, err = serverWithRunner(versionMutationRunner).SetCodepointWidths(
		context.Background(), versionValues,
	)
	if err != nil {
		t.Fatalf("SetCodepointWidths() error = %v", err)
	}
	requests = versionMutationRunner.recordedRequests()
	wantVersionIndexed := []string{"set-option", "-s", "--", "codepoint-widths[1]", "version-original"}
	if len(requests) != 3 || !slices.Equal(requests[2].Arguments, wantVersionIndexed) ||
		!result.Replaced || !slices.Equal(result.AppliedIndices, []int{1}) {
		t.Fatalf("SetCodepointWidths() = (%#v, %#v), want frozen indexed write %#v", result, requests, wantVersionIndexed)
	}

	freshValues, err := NewSparseArray(SparseEntry[string]{Index: 1, Value: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{{}, {}, {}, {}}}
	server := serverWithRunner(runner)
	first, err := server.SetCommandAlias(context.Background(), freshValues)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.SetCommandAlias(context.Background(), freshValues)
	if err != nil {
		t.Fatal(err)
	}
	first.AppliedIndices[0] = 99
	if !slices.Equal(second.AppliedIndices, []int{1}) {
		t.Fatalf("second result aliases first result: %#v", second)
	}
}

func TestTypedArrayOptionSetterRejectsTargetAndScopeBeforeMutation(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 0, Value: "value"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("target", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{}
		result, err := (Session{
			server: serverWithRunner(runner), sessionID: SessionID("$7\x00private"),
		}).SetUpdateEnvironment(
			context.Background(), values,
		)
		if !errors.Is(err, ErrInvalidServerCommandRequest) || result.Replaced || result.AppliedIndices != nil {
			t.Fatalf("SetUpdateEnvironment(invalid target) = (%#v, %v), want zero redacted request error", result, err)
		}
		if runner.callCount() != 0 {
			t.Fatalf("SetUpdateEnvironment(invalid target) calls = %d, want 0", runner.callCount())
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatalf("SetUpdateEnvironment(invalid target) disclosed target: %v", err)
		}
	})
	t.Run("scope", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{}
		result, err := setTypedOptionArray(
			context.Background(), serverWithRunner(runner), []string{"-s"},
			generatedOptionScopeServer, "pane-colours", values,
		)
		if !errors.Is(err, ErrInvalidOption) || result.Replaced || result.AppliedIndices != nil {
			t.Fatalf("setTypedOptionArray(invalid scope) = (%#v, %v), want zero OptionError", result, err)
		}
		if runner.callCount() != 0 {
			t.Fatalf("setTypedOptionArray(invalid scope) calls = %d, want 0", runner.callCount())
		}
	})
}

type mutatingSparseOptionRunner struct {
	values   *SparseArray[string]
	requests []tmuxcmd.Request
}

func (r *mutatingSparseOptionRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.requests = append(r.requests, request)
	if len(r.requests) == 1 {
		r.values.entries[0] = SparseEntry[string]{Index: 5, Value: "mutated"}
	}
	if slices.Equal(request.Arguments, []string{"-V"}) {
		return tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}, nil
	}
	return tmuxcmd.Result{}, nil
}

func (r *mutatingSparseOptionRunner) recordedRequests() []tmuxcmd.Request {
	return slices.Clone(r.requests)
}

func TestTypedChoiceSetterRejectsValuesWithoutDisclosureOrExecution(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value ExtendedKeys
	}{
		{name: "zero"},
		{name: "unknown", value: ExtendedKeys("private-future-choice")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{}
			err := serverWithRunner(runner).SetExtendedKeys(context.Background(), test.value)
			if !errors.Is(err, ErrInvalidOptionValue) || !errors.Is(err, ErrOption) {
				t.Fatalf("SetExtendedKeys() error = %v, want ErrInvalidOptionValue and ErrOption", err)
			}
			if errors.Is(err, ErrInvalidOption) {
				t.Fatalf("SetExtendedKeys() error = %v, unexpectedly matches ErrInvalidOption", err)
			}
			var valueError *OptionValueError
			if !errors.As(err, &valueError) || valueError.Name != "extended-keys" {
				t.Fatalf("SetExtendedKeys() error = %#v, want safe option metadata", err)
			}
			if strings.Contains(err.Error(), "private-future-choice") || runner.callCount() != 0 {
				t.Fatalf("invalid choice disclosed data or executed: error=%v calls=%d", err, runner.callCount())
			}
		})
	}
}

func TestTypedChoiceSetterClassifiesNULBeforeUnionValidation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	err := serverWithRunner(runner).SetExtendedKeys(
		context.Background(), ExtendedKeys("private\x00value"),
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) || errors.Is(err, ErrInvalidOptionValue) {
		t.Fatalf("SetExtendedKeys(NUL) error = %v, want only ErrInvalidServerCommandRequest", err)
	}
	var requestError *ServerCommandRequestError
	if !errors.As(err, &requestError) || strings.Contains(err.Error(), "private") {
		t.Fatalf("SetExtendedKeys(NUL) error retained the value: %#v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("SetExtendedKeys(NUL) calls = %d, want 0", runner.callCount())
	}
}

func TestChoiceReadsPreserveUnknownFutureValues(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		RawStdout: []byte("extended-keys private-future-choice\n"),
	}}}}
	values, err := serverWithRunner(runner).Options(context.Background())
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	value, ok := values.ExtendedKeys().Get()
	if !ok || value.String() != "private-future-choice" || value.Valid() {
		t.Fatalf("ExtendedKeys() = (%q, %t, valid=%t), want preserved invalid future value", value, ok, value.Valid())
	}
}

func TestRawOptionSetterAcceptsFutureChoiceValue(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	err := serverWithRunner(runner).SetOption(
		context.Background(), "extended-keys", "private-future-choice", SetOptionOptions{},
	)
	if err != nil {
		t.Fatalf("SetOption(future choice) error = %v", err)
	}
	requests := runner.recordedRequests()
	want := []string{"set-option", "-s", "--", "extended-keys", "private-future-choice"}
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("SetOption(future choice) requests = %#v, want %#v", requests, want)
	}
}

func TestVersionVaryingChoiceSettersUseTheActiveDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		before    string
		boundary  string
		operation func(Server, Session, Window, Pane) error
	}{
		{name: "allow passthrough 3.4", before: "3.3", boundary: "3.4", operation: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetAllowPassthrough(context.Background(), AllowPassthroughAll)
		}},
		{name: "destroy unattached 3.4", before: "3.3", boundary: "3.4", operation: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.SetDestroyUnattached(context.Background(), DestroyUnattachedKeepLast)
		}},
		{name: "detach on destroy 3.4", before: "3.3", boundary: "3.4", operation: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.SetDetachOnDestroy(context.Background(), DetachOnDestroyPrevious)
		}},
		{name: "clock mode style 3.6", before: "3.5", boundary: "3.6", operation: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetClockModeStyle(context.Background(), ClockModeStyle12WithSeconds)
		}},
		{name: "pane border lines 3.6", before: "3.5", boundary: "3.6", operation: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetPaneBorderLines(context.Background(), PaneBorderLinesSpaces)
		}},
		{name: "remain on exit 3.7", before: "3.6", boundary: "3.7", operation: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.SetRemainOnExit(context.Background(), RemainOnExitKey)
		}},
	}

	for _, test := range tests {
		t.Run(test.name+" rejects before", func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.before}}}}}
			server, session, window, pane := optionTestObjects(runner)
			err := test.operation(server, session, window, pane)
			if !errors.Is(err, ErrInvalidOptionValue) {
				t.Fatalf("typed setter at %s error = %v, want ErrInvalidOptionValue", test.before, err)
			}
			if runner.callCount() != 1 {
				t.Fatalf("typed setter at %s calls = %d, want one version probe", test.before, runner.callCount())
			}
		})
		t.Run(test.name+" accepts at boundary", func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.boundary}}},
				{result: tmuxcmd.Result{}},
			}}
			server, session, window, pane := optionTestObjects(runner)
			if err := test.operation(server, session, window, pane); err != nil {
				t.Fatalf("typed setter at %s error = %v", test.boundary, err)
			}
			if runner.callCount() != 2 {
				t.Fatalf("typed setter at %s calls = %d, want version and mutation", test.boundary, runner.callCount())
			}
		})
	}
}

func TestFixedChoiceAvoidsProbeAndLaterOptionUsesCachedProbe(t *testing.T) {
	t.Parallel()

	fixedRunner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	_, _, window, _ := optionTestObjects(fixedRunner)
	if err := window.SetPaneBorderStatus(context.Background(), PaneBorderStatusBottom); err != nil {
		t.Fatalf("SetPaneBorderStatus() error = %v", err)
	}
	if fixedRunner.callCount() != 1 || slices.Equal(fixedRunner.recordedRequests()[0].Arguments, []string{"-V"}) {
		t.Fatalf("fixed choice requests = %#v, want mutation without version probe", fixedRunner.recordedRequests())
	}

	laterRunner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithRunner(laterRunner)
	for range 2 {
		if err := server.SetGetClipboard(context.Background(), GetClipboardBoth); err != nil {
			t.Fatalf("SetGetClipboard() error = %v", err)
		}
	}
	if laterRunner.callCount() != 3 {
		t.Fatalf("two SetGetClipboard() calls = %d runner calls, want one cached probe and two mutations", laterRunner.callCount())
	}
}

func optionTestObjects(runner commandRunner) (Server, Session, Window, Pane) {
	server := serverWithRunner(runner)
	return server,
		Session{server: server, sessionID: SessionID("$7")},
		Window{server: server, sessionID: SessionID("$7"), windowID: WindowID("@8"), windowIndex: 3},
		Pane{
			server: server, sessionID: SessionID("$7"), windowID: WindowID("@8"),
			windowIndex: 3, paneID: PaneID("%9"),
		}
}

// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:g,global_:bcf3b5284452
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:g,global_:fabe8cef3aca
// libtmux:parity libtmux.options.OptionsMixin.set_option#parameter-branch:g:aab8d0b6d44c
// libtmux:parity libtmux.options.OptionsMixin.set_option#warning:abb8cfc75660
// libtmux:parity libtmux.options.OptionsMixin.show_option
// libtmux:parity libtmux.options.OptionsMixin.show_option#parameter-branch:g:03e14ffa90c7
// libtmux:parity libtmux.options.OptionsMixin.show_option#warning:abb8cfc75660
// libtmux:parity libtmux.options.OptionsMixin.show_options
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:global_:bcf3b5284452
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:global_:fabe8cef3aca
func TestGlobalOptionOperationsBuildExactScopeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(GlobalSessionScope, GlobalWindowScope) error
	}{
		{
			name: "global session options",
			want: []string{"show-options", "-g", "-A"},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				_, err := scope.Options(context.Background())
				return err
			},
		},
		{
			name: "global window options",
			want: []string{"show-options", "-g", "-w", "-A"},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				_, err := scope.Options(context.Background())
				return err
			},
		},
		{
			name: "raw global session option",
			want: []string{"show-options", "-g", "-q", "-v", "--", "@custom"},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				_, _, err := scope.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "raw global window option",
			want: []string{"show-options", "-g", "-w", "-q", "-v", "--", "@custom"},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				_, _, err := scope.RawOption(context.Background(), "@custom")
				return err
			},
		},
		{
			name: "set global session option",
			want: []string{"set-option", "-g", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.SetOption(
					context.Background(), "@custom", "value", allSetOptionOptions(),
				)
			},
		},
		{
			name: "append global session option",
			want: []string{"set-option", "-g", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.AppendOption(
					context.Background(), "@custom", "value", allSetOptionOptions(),
				)
			},
		},
		{
			name: "unset global session option",
			want: []string{"set-option", "-g", "-u", "-q", "--", "@custom"},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.UnsetOption(
					context.Background(), "@custom", UnsetOptionOptions{Quiet: true},
				)
			},
		},
		{
			name: "set global window option",
			want: []string{"set-option", "-g", "-w", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.SetOption(
					context.Background(), "@custom", "value", allSetOptionOptions(),
				)
			},
		},
		{
			name: "append global window option",
			want: []string{"set-option", "-g", "-w", "-a", "-F", "-o", "-q", "--", "@custom", "value"},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.AppendOption(
					context.Background(), "@custom", "value", allSetOptionOptions(),
				)
			},
		},
		{
			name: "unset global window option",
			want: []string{"set-option", "-g", "-w", "-u", "-q", "--", "@custom"},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.UnsetOption(
					context.Background(), "@custom", UnsetOptionOptions{Quiet: true},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
			server := serverWithRunner(runner)
			if err := test.run(server.GlobalSessionScope(), server.GlobalWindowScope()); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("requests = %#v, want %#v", requests, test.want)
			}
		})
	}
}

// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:unset_panes:96c894e2433a
// libtmux:parity libtmux.options.OptionsMixin.unset_option#parameter-branch:unset_panes:de1e3fe8e8ee
func TestGlobalOptionScopesRejectPaneCascadeBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "session",
			run: func(server Server) error {
				return server.GlobalSessionScope().UnsetOption(
					context.Background(), "status-left", UnsetOptionOptions{UnsetPanes: true},
				)
			},
		},
		{
			name: "window",
			run: func(server Server) error {
				return server.GlobalWindowScope().UnsetOption(
					context.Background(), "window-style", UnsetOptionOptions{UnsetPanes: true},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("UnsetOption(UnsetPanes) error = %v, want ErrInvalidOption", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("UnsetOption(UnsetPanes) calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestGlobalOptionMutationsPreflightGeneratedScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "session option through global window scope",
			run: func(server Server) error {
				return server.GlobalWindowScope().SetOption(
					context.Background(), "status-left", "value", SetOptionOptions{},
				)
			},
		},
		{
			name: "window option through global session scope",
			run: func(server Server) error {
				return server.GlobalSessionScope().SetOption(
					context.Background(), "window-style", "value", SetOptionOptions{},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("operation error = %v, want ErrInvalidOption", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("operation calls = %d, want 0", runner.callCount())
			}
		})
	}
}

// TestOptionErrorsDistinguishAMissingTarget covers the failure an unknown
// option name is most easily confused with. Both exit 1 and neither renders
// tmux's message, so without a sentinel the two are the same error.
func TestOptionErrorsDistinguishAMissingTarget(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		stderr []string
		want   error
		reject error
	}{
		{
			name:   "missing window",
			stderr: []string{"no such window: @1"},
			want:   ErrOptionTarget,
			reject: ErrUnknownOption,
		},
		{
			name:   "missing pane",
			stderr: []string{"no such pane: %99"},
			want:   ErrOptionTarget,
			reject: ErrUnknownOption,
		},
		{
			name:   "unknown option",
			stderr: []string{"unknown option: no-such-option"},
			want:   ErrUnknownOption,
			reject: ErrOptionTarget,
		},
		{
			// cmd_find_target's wording is a different failure from the option
			// subsystem's own lookup, and is not what an option error carries.
			name:   "unrelated can't find",
			stderr: []string{"can't find terminfo database"},
			want:   ErrOption,
			reject: ErrOptionTarget,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := classifyOptionError(testCase.stderr)
			if !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
			if errors.Is(err, testCase.reject) {
				t.Errorf("got %v, which must not match %v", err, testCase.reject)
			}
			if !errors.Is(err, ErrOption) {
				t.Errorf("got %v, which must still match ErrOption", err)
			}
		})
	}
}

// TestARefusedOptionDoesNotReportAnExitStatus covers the message a caller sees
// when this package rejects an option before building a command. Reporting a
// status of -1 sends a reader looking for a tmux exit code that never existed.
func TestARefusedOptionDoesNotReportAnExitStatus(t *testing.T) {
	err := newLocalInvalidOptionError("set-option", "main-pane-height")
	message := err.Error()
	if strings.Contains(message, "-1") {
		t.Errorf("refusal reports an exit status: %q", message)
	}
	if !strings.Contains(message, "refused before tmux ran it") {
		t.Errorf("refusal does not say no command ran: %q", message)
	}
	if !strings.Contains(message, "main-pane-height") {
		t.Errorf("refusal does not name the option: %q", message)
	}
}
