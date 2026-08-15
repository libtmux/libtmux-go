package tmux

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.default_hook_scope
// libtmux:parity libtmux.session.Session.default_hook_scope
// libtmux:parity libtmux.constants.HOOK_SCOPE_FLAG_MAP
// libtmux:parity libtmux.pane.Pane.default_hook_scope
// libtmux:parity libtmux.window.Window.default_hook_scope
// libtmux:parity libtmux.hooks.HooksMixin
// libtmux:parity libtmux.hooks.HooksMixin.__init__
// libtmux:parity libtmux.hooks.HooksMixin.default_hook_scope
// libtmux:parity libtmux.hooks.HooksMixin.hooks
// libtmux:parity libtmux.hooks.HooksMixin.run_hook
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:hook,scope:d3faaf781f06
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:scope:1b144cf5c43d
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.hooks.HooksMixin.set_hook
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:append:945ab72cb247
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:append:e358cfe094c6
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g,global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g,global_:fabe8cef3aca
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g:03e14ffa90c7
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:hook,scope,value:d3faaf781f06
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:run:374168ca78de
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:run:c822680ccb58
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:scope:1b144cf5c43d
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:unset:2bfd66177de3
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:unset:fcb96f7ba797
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#warning:abb8cfc75660
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks#parameter-branch:clear_existing:563b7a5e5f56
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks#parameter-branch:values:45898433e258
// libtmux:parity libtmux.hooks.HooksMixin.show_hook
// libtmux:parity libtmux.hooks.HooksMixin.show_hook#parameter-branch:global_,hook,scope:4704725be4dd
// libtmux:parity libtmux.hooks.HooksMixin.show_hook#parameter-branch:global_,hook,scope:52fe2fc27f5b
// libtmux:parity libtmux.hooks.HooksMixin.show_hook#parameter-branch:hook:f9e8cb29bf68
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:global_:746212d66b3a
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:1b144cf5c43d
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:global_:fabe8cef3aca
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:hook,scope:d3faaf781f06
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:scope:1b144cf5c43d
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:scope:b959adc22e97
// libtmux:parity libtmux.options.OptionsMixin.show_option
// libtmux:parity libtmux.options.OptionsMixin.show_option#parameter-branch:g:03e14ffa90c7
// libtmux:parity libtmux.options.OptionsMixin.show_options
func TestHookOperationsBuildExactScopeArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server, Session, Window, Pane) error
	}{
		{
			name: "server hooks",
			want: []string{"show-options", "-g", "-A", "-H"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, err := server.GlobalSessionScope().Hooks(context.Background())
				return err
			},
		},
		{
			name: "session hooks",
			want: []string{"show-options", "-t", "$7", "-A", "-H"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				_, err := session.Hooks(context.Background())
				return err
			},
		},
		{
			name: "window hooks",
			want: []string{"show-options", "-t", "$7:3", "-w", "-A", "-H"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				_, err := window.Hooks(context.Background())
				return err
			},
		},
		{
			name: "pane hooks",
			want: []string{"show-options", "-t", "$7:3.%9", "-p", "-A", "-H"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				_, err := pane.Hooks(context.Background())
				return err
			},
		},
		{
			name: "global window hooks",
			want: []string{"show-options", "-g", "-w", "-A", "-H"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, err := server.GlobalWindowScope().Hooks(context.Background())
				return err
			},
		},
		{
			name: "server raw hook",
			want: []string{"show-options", "-g", "-H", "-q", "-v", "--", "session-renamed[0]"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				_, _, err := server.GlobalSessionScope().RawHook(context.Background(), "session-renamed[0]")
				return err
			},
		},
		{
			name: "session raw hook",
			want: []string{"show-options", "-t", "$7", "-H", "-q", "-v", "--", "session-renamed[0]"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				_, _, err := session.RawHook(context.Background(), "session-renamed[0]")
				return err
			},
		},
		{
			name: "window raw hook",
			want: []string{"show-options", "-t", "$7:3", "-w", "-H", "-q", "-v", "--", "pane-died[0]"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				_, _, err := window.RawHook(context.Background(), "pane-died[0]")
				return err
			},
		},
		{
			name: "pane raw hook",
			want: []string{"show-options", "-t", "$7:3.%9", "-p", "-H", "-q", "-v", "--", "pane-died[0]"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				_, _, err := pane.RawHook(context.Background(), "pane-died[0]")
				return err
			},
		},
		{
			name: "server set hook",
			want: []string{"set-hook", "-g", "--", "session-renamed[0]", "display-message set"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.GlobalSessionScope().SetHook(context.Background(), "session-renamed[0]", "display-message set")
			},
		},
		{
			name: "session set hook",
			want: []string{"set-hook", "-t", "$7", "--", "session-renamed[0]", "display-message set"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.SetHook(context.Background(), "session-renamed[0]", "display-message set")
			},
		},
		{
			name: "window set hook",
			want: []string{"set-hook", "-t", "$7:3", "-w", "--", "pane-died[0]", "display-message set"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.SetHook(context.Background(), "pane-died[0]", "display-message set")
			},
		},
		{
			name: "pane set hook",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "--", "pane-died[0]", "display-message set"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.SetHook(context.Background(), "pane-died[0]", "display-message set")
			},
		},
		{
			name: "server append hook",
			want: []string{"set-hook", "-g", "-a", "--", "session-renamed", "display-message append"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.GlobalSessionScope().AppendHook(context.Background(), "session-renamed", "display-message append")
			},
		},
		{
			name: "session append hook",
			want: []string{"set-hook", "-t", "$7", "-a", "--", "session-renamed", "display-message append"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.AppendHook(context.Background(), "session-renamed", "display-message append")
			},
		},
		{
			name: "window append hook",
			want: []string{"set-hook", "-t", "$7:3", "-w", "-a", "--", "pane-died", "display-message append"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.AppendHook(context.Background(), "pane-died", "display-message append")
			},
		},
		{
			name: "pane append hook",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "-a", "--", "pane-died", "display-message append"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.AppendHook(context.Background(), "pane-died", "display-message append")
			},
		},
		{
			name: "server unset hook",
			want: []string{"set-hook", "-g", "-u", "--", "session-renamed"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.GlobalSessionScope().UnsetHook(context.Background(), "session-renamed")
			},
		},
		{
			name: "session unset hook",
			want: []string{"set-hook", "-t", "$7", "-u", "--", "session-renamed"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.UnsetHook(context.Background(), "session-renamed")
			},
		},
		{
			name: "window unset hook",
			want: []string{"set-hook", "-t", "$7:3", "-w", "-u", "--", "pane-died"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.UnsetHook(context.Background(), "pane-died")
			},
		},
		{
			name: "pane unset hook",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "-u", "--", "pane-died"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.UnsetHook(context.Background(), "pane-died")
			},
		},
		{
			name: "server run hook",
			want: []string{"set-hook", "-g", "-R", "--", "session-renamed[0]"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.GlobalSessionScope().RunHook(context.Background(), "session-renamed[0]")
			},
		},
		{
			name: "session run hook",
			want: []string{"set-hook", "-t", "$7", "-R", "--", "session-renamed[0]"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.RunHook(context.Background(), "session-renamed[0]")
			},
		},
		{
			name: "window run hook",
			want: []string{"set-hook", "-t", "$7:3", "-w", "-R", "--", "pane-died[0]"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.RunHook(context.Background(), "pane-died[0]")
			},
		},
		{
			name: "pane run hook",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "-R", "--", "pane-died[0]"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.RunHook(context.Background(), "pane-died[0]")
			},
		},
		{
			name: "server set hooks",
			want: []string{"set-hook", "-g", "--", "session-renamed[3]", "display-message bulk"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				values, err := NewSparseArray(SparseEntry[string]{Index: 3, Value: "display-message bulk"})
				if err != nil {
					return err
				}
				_, err = server.GlobalSessionScope().SetHooks(context.Background(), "session-renamed", values, SetHooksOptions{})
				return err
			},
		},
		{
			name: "session set hooks",
			want: []string{"set-hook", "-t", "$7", "--", "session-renamed[3]", "display-message bulk"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				values, err := NewSparseArray(SparseEntry[string]{Index: 3, Value: "display-message bulk"})
				if err != nil {
					return err
				}
				_, err = session.SetHooks(context.Background(), "session-renamed", values, SetHooksOptions{})
				return err
			},
		},
		{
			name: "window set hooks",
			want: []string{"set-hook", "-t", "$7:3", "-w", "--", "pane-died[3]", "display-message bulk"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				values, err := NewSparseArray(SparseEntry[string]{Index: 3, Value: "display-message bulk"})
				if err != nil {
					return err
				}
				_, err = window.SetHooks(context.Background(), "pane-died", values, SetHooksOptions{})
				return err
			},
		},
		{
			name: "pane set hooks",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "--", "pane-died[3]", "display-message bulk"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				values, err := NewSparseArray(SparseEntry[string]{Index: 3, Value: "display-message bulk"})
				if err != nil {
					return err
				}
				_, err = pane.SetHooks(context.Background(), "pane-died", values, SetHooksOptions{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{RawStdout: []byte("display-message value\n"), ExitCode: 0},
			}}}
			server, session, window, pane := optionTestObjects(runner)
			if err := test.run(server, session, window, pane); err != nil {
				t.Fatalf("hook operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("hook arguments = %#v, want %#v", requests, test.want)
			}
		})
	}
}

func TestHookModelOperationsValidateStableTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 0, Value: "display-message value"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func(Session, Window, Pane) error
	}{
		{name: "session hooks", run: func(session Session, _ Window, _ Pane) error {
			_, err := session.Hooks(context.Background())
			return err
		}},
		{name: "session raw", run: func(session Session, _ Window, _ Pane) error {
			_, _, err := session.RawHook(context.Background(), "session-renamed")
			return err
		}},
		{name: "session set", run: func(session Session, _ Window, _ Pane) error {
			return session.SetHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "session append", run: func(session Session, _ Window, _ Pane) error {
			return session.AppendHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "session unset", run: func(session Session, _ Window, _ Pane) error {
			return session.UnsetHook(context.Background(), "session-renamed")
		}},
		{name: "session run", run: func(session Session, _ Window, _ Pane) error {
			return session.RunHook(context.Background(), "session-renamed")
		}},
		{name: "session set hooks", run: func(session Session, _ Window, _ Pane) error {
			_, err := session.SetHooks(context.Background(), "session-renamed", values, SetHooksOptions{})
			return err
		}},
		{name: "window hooks", run: func(_ Session, window Window, _ Pane) error {
			_, err := window.Hooks(context.Background())
			return err
		}},
		{name: "window raw", run: func(_ Session, window Window, _ Pane) error {
			_, _, err := window.RawHook(context.Background(), "pane-died")
			return err
		}},
		{name: "window set", run: func(_ Session, window Window, _ Pane) error {
			return window.SetHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "window append", run: func(_ Session, window Window, _ Pane) error {
			return window.AppendHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "window unset", run: func(_ Session, window Window, _ Pane) error {
			return window.UnsetHook(context.Background(), "pane-died")
		}},
		{name: "window run", run: func(_ Session, window Window, _ Pane) error {
			return window.RunHook(context.Background(), "pane-died")
		}},
		{name: "window set hooks", run: func(_ Session, window Window, _ Pane) error {
			_, err := window.SetHooks(context.Background(), "pane-died", values, SetHooksOptions{})
			return err
		}},
		{name: "pane hooks", run: func(_ Session, _ Window, pane Pane) error {
			_, err := pane.Hooks(context.Background())
			return err
		}},
		{name: "pane raw", run: func(_ Session, _ Window, pane Pane) error {
			_, _, err := pane.RawHook(context.Background(), "pane-died")
			return err
		}},
		{name: "pane set", run: func(_ Session, _ Window, pane Pane) error {
			return pane.SetHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "pane append", run: func(_ Session, _ Window, pane Pane) error {
			return pane.AppendHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "pane unset", run: func(_ Session, _ Window, pane Pane) error {
			return pane.UnsetHook(context.Background(), "pane-died")
		}},
		{name: "pane run", run: func(_ Session, _ Window, pane Pane) error {
			return pane.RunHook(context.Background(), "pane-died")
		}},
		{name: "pane set hooks", run: func(_ Session, _ Window, pane Pane) error {
			_, err := pane.SetHooks(context.Background(), "pane-died", values, SetHooksOptions{})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server := serverWithRunner(runner)
			err := test.run(
				Session{server: server},
				Window{server: server, windowID: WindowID("@bad")},
				Pane{server: server, paneID: PaneID("pane")},
			)
			if !errors.Is(err, ErrMissingTarget) && !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("hook operation error = %v, want target validation", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("invalid target executed %d commands", runner.callCount())
			}
		})
	}
}

func TestHookMutationsRejectKnownNamesOutsideReceiverScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server, Session, Window, Pane) error
	}{
		{name: "server set", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalSessionScope().SetHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "server append", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalSessionScope().AppendHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "server unset", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalSessionScope().UnsetHook(context.Background(), "pane-died")
		}},
		{name: "server run", run: func(server Server, _ Session, _ Window, _ Pane) error {
			return server.GlobalSessionScope().RunHook(context.Background(), "pane-died")
		}},
		{name: "session set", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.SetHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "session append", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.AppendHook(context.Background(), "pane-died", "display-message value")
		}},
		{name: "session unset", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.UnsetHook(context.Background(), "pane-died")
		}},
		{name: "session run", run: func(_ Server, session Session, _ Window, _ Pane) error {
			return session.RunHook(context.Background(), "pane-died")
		}},
		{name: "window set", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.SetHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "window append", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.AppendHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "window unset", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.UnsetHook(context.Background(), "session-renamed")
		}},
		{name: "window run", run: func(_ Server, _ Session, window Window, _ Pane) error {
			return window.RunHook(context.Background(), "session-renamed")
		}},
		{name: "pane set", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.SetHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "pane append", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.AppendHook(context.Background(), "session-renamed", "display-message value")
		}},
		{name: "pane unset", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.UnsetHook(context.Background(), "session-renamed")
		}},
		{name: "pane run", run: func(_ Server, _ Session, _ Window, pane Pane) error {
			return pane.RunHook(context.Background(), "session-renamed")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server, session, window, pane := optionTestObjects(runner)
			err := test.run(server, session, window, pane)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("hook mutation error = %v, want ErrInvalidOption", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("wrong-scope hook mutation executed %d commands", runner.callCount())
			}
		})
	}
}

func TestSingleHookMutationsPreserveUnknownPassThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server, Session, Window, Pane) error
	}{
		{
			name: "server set unknown",
			want: []string{"set-hook", "-g", "--", "future-hook", "display-message value"},
			run: func(server Server, _ Session, _ Window, _ Pane) error {
				return server.GlobalSessionScope().SetHook(context.Background(), "future-hook", "display-message value")
			},
		},
		{
			name: "session append custom",
			want: []string{"set-hook", "-t", "$7", "-a", "--", "@custom-hook", "display-message value"},
			run: func(_ Server, session Session, _ Window, _ Pane) error {
				return session.AppendHook(context.Background(), "@custom-hook", "display-message value")
			},
		},
		{
			name: "window unset unknown",
			want: []string{"set-hook", "-t", "$7:3", "-w", "-u", "--", "future-hook"},
			run: func(_ Server, _ Session, window Window, _ Pane) error {
				return window.UnsetHook(context.Background(), "future-hook")
			},
		},
		{
			name: "pane run custom",
			want: []string{"set-hook", "-t", "$7:3.%9", "-p", "-R", "--", "@custom-hook"},
			run: func(_ Server, _ Session, _ Window, pane Pane) error {
				return pane.RunHook(context.Background(), "@custom-hook")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}}
			server, session, window, pane := optionTestObjects(runner)
			if err := test.run(server, session, window, pane); err != nil {
				t.Fatalf("unknown hook mutation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("unknown hook mutation requests = %#v, want %#v", requests, test.want)
			}
		})
	}
}

// libtmux:parity libtmux.hooks.HooksMixin.run_hook#version-branch:scope:034bf64d8814
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#version-branch:scope:034bf64d8814
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#version-branch:scope:034bf64d8814
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#version-branch:scope:034bf64d8814
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#warning:21a25a5f2e9b
func TestHookMutationPreflightUsesCachedVersionForChangingScopes(t *testing.T) {
	t.Parallel()

	t.Run("supported session scope", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		server := serverWithRunner(runner)
		if err := server.GlobalSessionScope().SetHook(context.Background(), "window-linked", "display-message set"); err != nil {
			t.Fatalf("SetHook() error = %v", err)
		}
		if err := server.GlobalSessionScope().AppendHook(context.Background(), "window-linked", "display-message append"); err != nil {
			t.Fatalf("AppendHook() error = %v", err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"-V"},
			{"set-hook", "-g", "--", "window-linked", "display-message set"},
			{"set-hook", "-g", "-a", "--", "window-linked", "display-message append"},
		}
		if len(requests) != len(want) {
			t.Fatalf("versioned hook requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("versioned hook request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})

	for _, test := range []struct {
		name    string
		version string
		run     func(Server, Session, Window) error
	}{
		{
			name:    "session scope before switch",
			version: "3.2a",
			run: func(server Server, _ Session, _ Window) error {
				return server.GlobalSessionScope().UnsetHook(context.Background(), "window-linked")
			},
		},
		{
			name:    "window scope after switch",
			version: "3.3",
			run: func(_ Server, _ Session, window Window) error {
				return window.RunHook(context.Background(), "window-linked")
			},
		},
		{
			name:    "hook before introduction",
			version: "3.5",
			run: func(_ Server, session Session, _ Window) error {
				return session.SetHook(context.Background(), "client-dark-theme", "display-message value")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}, ExitCode: 0},
			}}}
			server, session, window, _ := optionTestObjects(runner)
			err := test.run(server, session, window)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("versioned hook mutation error = %v, want ErrInvalidOption", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, []string{"-V"}) {
				t.Fatalf("unsupported versioned hook requests = %#v, want version probe only", requests)
			}
		})
	}
}

// libtmux:parity libtmux._internal.constants.Hooks.from_stdout
// libtmux:parity libtmux._internal.constants.Hooks.from_stdout#parameter-branch:value:8045c0e2d5f6
// libtmux:parity libtmux.hooks.HookDict
// libtmux:parity libtmux.hooks.HookValues
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:0c5ef270ac46
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:1b144cf5c43d
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:scope:b959adc22e97
func TestHooksDecodeBareEmptyIndexedValuesAndSparseHoles(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"@ignored custom",
		"after-bind-key*",
		"session-renamed[0] display-message zero",
		"session-renamed[5] ",
		"",
	}, "\n")
	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{RawStdout: []byte(raw), ExitCode: 0},
	}}}
	_, session, _, _ := optionTestObjects(runner)
	values, err := session.Hooks(context.Background())
	if err != nil {
		t.Fatalf("Hooks() error = %v", err)
	}

	empty, ok := values.AfterBindKey().Get()
	if !ok || empty.Len() != 0 {
		t.Fatalf("AfterBindKey().Get() = (%#v, %t), want present empty", empty, ok)
	}
	if origin, ok := values.AfterBindKey().Origin(); !ok || origin != OptionOriginInherited {
		t.Fatalf("AfterBindKey().Origin() = (%v, %t), want inherited", origin, ok)
	}

	hooks, ok := values.SessionRenamed().Get()
	if !ok {
		t.Fatal("SessionRenamed().Get() reported absent")
	}
	if got, want := hooks.Indices(), []int{0, 5}; !slices.Equal(got, want) {
		t.Fatalf("SessionRenamed().Indices() = %#v, want %#v", got, want)
	}
	if got, ok := hooks.Get(0); !ok || got != "display-message zero" {
		t.Fatalf("SessionRenamed()[0] = (%q, %t), want command", got, ok)
	}
	if got, ok := hooks.Get(5); !ok || got != "" {
		t.Fatalf("SessionRenamed()[5] = (%q, %t), want present empty", got, ok)
	}
	if _, ok := hooks.Get(1); ok {
		t.Fatal("SessionRenamed()[1] filled a sparse hole")
	}
	if _, ok := values.SessionCreated().Get(); ok {
		t.Fatal("SessionCreated().Get() reported absent record present")
	}
}

func TestHooksRejectMalformedRecognizedRecordsWithoutDisclosingCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "duplicate index", raw: "session-renamed[0] secret-command\nsession-renamed[0] other\n"},
		{name: "descending index", raw: "session-renamed[5] secret-command\nsession-renamed[0] other\n"},
		{name: "bare and indexed", raw: "session-renamed\nsession-renamed[0] secret-command\n"},
		{name: "indexed and bare", raw: "session-renamed[0] secret-command\nsession-renamed\n"},
		{name: "mixed origin", raw: "session-renamed[0] secret-command\nsession-renamed[5]* other\n"},
		{name: "noncontiguous array", raw: "session-renamed[0] secret-command\nafter-bind-key[0] other\nsession-renamed[5] later\n"},
		{name: "newline spoof", raw: "@custom ignored\nafter-bind-key[0] spoof\nsession-renamed[0] actual\nafter-bind-key[5] canonical\n"},
		{name: "indexed missing delimiter", raw: "session-renamed[0]\n"},
		{name: "nonempty bare", raw: "session-renamed secret-command\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{RawStdout: []byte(test.raw), ExitCode: 0},
			}}}
			_, session, _, _ := optionTestObjects(runner)
			_, err := session.Hooks(context.Background())
			if !errors.Is(err, ErrMalformedOptionOutput) {
				t.Fatalf("Hooks() error = %v, want ErrMalformedOptionOutput", err)
			}
			var decodeError *OptionDecodeError
			if !errors.As(err, &decodeError) || decodeError.Reason == "" {
				t.Fatalf("Hooks() error = %#v, want exported decode detail", err)
			}
			if strings.Contains(err.Error(), "secret-command") ||
				strings.Contains(err.Error(), "spoof") ||
				strings.Contains(err.Error(), "actual") {
				t.Fatalf("decode error disclosed hook output: %v", err)
			}
		})
	}
}

func TestHookBulkReadLeniencyStrictnessAndDecodeLoudness(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{
			RawStdout: []byte("session-renamed[0] partial\n"),
			Stderr:    []string{"invalid option: hook"},
			ExitCode:  0,
		}},
		{result: tmuxcmd.Result{
			RawStdout: []byte("session-renamed[0] partial\n"),
			Stderr:    []string{"invalid option: hook"},
			ExitCode:  0,
		}},
		{result: tmuxcmd.Result{
			RawStdout: []byte("session-renamed[0] one\nsession-renamed[0] two\n"),
			ExitCode:  0,
		}},
	}}
	server := serverWithRunner(runner)
	values, err := server.GlobalSessionScope().Hooks(context.Background())
	if err != nil {
		t.Fatalf("lenient Hooks() error = %v", err)
	}
	if _, ok := values.SessionRenamed().Get(); ok {
		t.Fatal("lenient Hooks() parsed partial failure output")
	}
	if _, err := server.WithStrictErrors().GlobalSessionScope().Hooks(context.Background()); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("strict Hooks() error = %v, want ErrInvalidOption", err)
	}
	if _, err := server.GlobalSessionScope().Hooks(context.Background()); !errors.Is(err, ErrMalformedOptionOutput) {
		t.Fatalf("lenient Hooks() decode error = %v, want ErrMalformedOptionOutput", err)
	}
}

func TestGlobalHookScopesCaptureBulkReadStrictness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(Server) (error, error)
	}{
		{
			name: "session",
			run: func(server Server) (error, error) {
				lenient := server.GlobalSessionScope()
				strict := server.WithStrictErrors().GlobalSessionScope()
				_, lenientErr := lenient.Hooks(context.Background())
				_, strictErr := strict.Hooks(context.Background())
				return lenientErr, strictErr
			},
		},
		{
			name: "window",
			run: func(server Server) (error, error) {
				lenient := server.GlobalWindowScope()
				strict := server.WithStrictErrors().GlobalWindowScope()
				_, lenientErr := lenient.Hooks(context.Background())
				_, strictErr := strict.Hooks(context.Background())
				return lenientErr, strictErr
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			failed := versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"invalid option: private"}, ExitCode: 1,
			}}
			runner := &versionQueueRunner{responses: []versionResponse{failed, failed}}
			lenientErr, strictErr := test.run(serverWithRunner(runner))
			if lenientErr != nil {
				t.Fatalf("lenient Hooks() error = %v", lenientErr)
			}
			if !errors.Is(strictErr, ErrInvalidOption) {
				t.Fatalf("strict Hooks() error = %v, want ErrInvalidOption", strictErr)
			}
		})
	}
}

func TestRawHookPreservesPassThroughAndTerminalLineFeedSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response tmuxcmd.Result
		want     string
		wantOK   bool
		wantErr  error
	}{
		{name: "quiet miss", response: tmuxcmd.Result{ExitCode: 1}},
		{name: "unindexed empty remains ambiguous", response: tmuxcmd.Result{RawStdout: []byte("\n"), ExitCode: 0}, wantOK: true},
		{name: "remove one LF", response: tmuxcmd.Result{RawStdout: []byte("display one\ndisplay two\n"), ExitCode: 0}, want: "display one\ndisplay two", wantOK: true},
		{name: "stderr zero", response: tmuxcmd.Result{Stderr: []string{"unknown option: custom"}, ExitCode: 0}, wantErr: ErrUnknownOption},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: test.response}}}
			got, ok, err := serverWithRunner(runner).GlobalSessionScope().RawHook(context.Background(), "custom-hook")
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("RawHook() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want || ok != test.wantOK {
				t.Fatalf("RawHook() = (%q, %t, %v), want (%q, %t, nil)", got, ok, err, test.want, test.wantOK)
			}
		})
	}
}

func TestRawIndexedHookVersionGateDisambiguatesLineFeedOnlyOutput(t *testing.T) {
	t.Parallel()

	t.Run("3.2a treats LF-only as absent", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{RawStdout: []byte("\n"), ExitCode: 0}},
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}, ExitCode: 0}},
		}}
		got, ok, err := serverWithRunner(runner).GlobalSessionScope().RawHook(context.Background(), "session-renamed[3]")
		if err != nil || got != "" || ok {
			t.Fatalf("RawHook(3.2a index) = (%q, %t, %v), want absent", got, ok, err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"show-options", "-g", "-H", "-q", "-v", "--", "session-renamed[3]"},
			{"-V"},
		}
		if len(requests) != len(want) {
			t.Fatalf("RawHook(3.2a) requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("RawHook(3.2a) request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})

	for _, test := range []struct {
		name    string
		listing string
		wantOK  bool
	}{
		{name: "present empty", listing: "session-renamed[3] \n", wantOK: true},
		{name: "missing hole", listing: "session-renamed[7] display-message value\n"},
	} {
		t.Run("3.3 "+test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{RawStdout: []byte("\n"), ExitCode: 0}},
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}, ExitCode: 0}},
				{result: tmuxcmd.Result{RawStdout: []byte(test.listing), ExitCode: 0}},
			}}
			got, ok, err := serverWithRunner(runner).GlobalSessionScope().RawHook(context.Background(), "session-renamed[3]")
			if err != nil || got != "" || ok != test.wantOK {
				t.Fatalf("RawHook(3.3 index) = (%q, %t, %v), want (empty, %t, nil)", got, ok, err, test.wantOK)
			}
			requests := runner.recordedRequests()
			want := [][]string{
				{"show-options", "-g", "-H", "-q", "-v", "--", "session-renamed[3]"},
				{"-V"},
				{"show-options", "-g", "-H", "-q", "--", "session-renamed"},
			}
			if len(requests) != len(want) {
				t.Fatalf("RawHook(3.3) requests = %#v, want %#v", requests, want)
			}
			for index := range want {
				if !slices.Equal(requests[index].Arguments, want[index]) {
					t.Fatalf("RawHook(3.3) request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
				}
			}
		})
	}
}

func TestSetHooksClearsThenAppliesSortedEntriesAndReportsProgress(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(
		SparseEntry[string]{Index: 5, Value: "display-message five"},
		SparseEntry[string]{Index: 0, Value: "display-message zero"},
		SparseEntry[string]{Index: 2, Value: "display-message two"},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
	}}
	_, session, _, _ := optionTestObjects(runner)
	result, err := session.SetHooks(
		context.Background(),
		"session-renamed",
		values,
		SetHooksOptions{ClearExisting: true},
	)
	if err != nil {
		t.Fatalf("SetHooks() error = %v", err)
	}
	if !result.Cleared || !slices.Equal(result.AppliedIndices, []int{0, 2, 5}) {
		t.Fatalf("SetHooks() result = %#v, want cleared indices [0 2 5]", result)
	}
	want := [][]string{
		{"set-hook", "-t", "$7", "-u", "--", "session-renamed"},
		{"set-hook", "-t", "$7", "--", "session-renamed[0]", "display-message zero"},
		{"set-hook", "-t", "$7", "--", "session-renamed[2]", "display-message two"},
		{"set-hook", "-t", "$7", "--", "session-renamed[5]", "display-message five"},
	}
	requests := runner.recordedRequests()
	if len(requests) != len(want) {
		t.Fatalf("SetHooks() requests = %#v, want %#v", requests, want)
	}
	for index := range want {
		if !slices.Equal(requests[index].Arguments, want[index]) {
			t.Fatalf("SetHooks() request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
		}
	}
}

func TestSetHooksStopsAtFirstFailureWithoutRollbackAndReturnsFreshProgress(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "display-message zero"},
		SparseEntry[string]{Index: 2, Value: "display-message two"},
		SparseEntry[string]{Index: 5, Value: "display-message five"},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"ambiguous option: session-renamed[2]"}, ExitCode: 1}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
	}}
	_, session, _, _ := optionTestObjects(runner)
	result, err := session.SetHooks(
		context.Background(),
		"session-renamed",
		values,
		SetHooksOptions{ClearExisting: true},
	)
	if !errors.Is(err, ErrAmbiguousOption) {
		t.Fatalf("first SetHooks() error = %v, want ErrAmbiguousOption", err)
	}
	if !result.Cleared || !slices.Equal(result.AppliedIndices, []int{0}) {
		t.Fatalf("first SetHooks() result = %#v, want cleared with index 0 only", result)
	}
	if runner.callCount() != 3 {
		t.Fatalf("first SetHooks() calls = %d, want stop after 3", runner.callCount())
	}

	result.AppliedIndices[0] = 99
	second, err := session.SetHooks(context.Background(), "session-renamed", values, SetHooksOptions{})
	if err != nil {
		t.Fatalf("second SetHooks() error = %v", err)
	}
	if !slices.Equal(second.AppliedIndices, []int{0, 2, 5}) {
		t.Fatalf("second SetHooks() result aliases first: %#v", second)
	}
}

func TestSetHooksValidatesUnindexedBaseBeforeSideEffects(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 0, Value: "display-message value"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "session-renamed[0]", "session-renamed\nother", "session renamed"} {
		runner := &versionQueueRunner{}
		server := serverWithRunner(runner)
		result, err := server.GlobalSessionScope().SetHooks(
			context.Background(),
			name,
			values,
			SetHooksOptions{ClearExisting: true},
		)
		if !errors.Is(err, ErrInvalidOption) {
			t.Fatalf("SetHooks(%q) error = %v, want ErrInvalidOption", name, err)
		}
		if result.Cleared || len(result.AppliedIndices) != 0 || runner.callCount() != 0 {
			t.Fatalf("SetHooks(%q) = (%#v, %d calls), want no side effects", name, result, runner.callCount())
		}
	}
}

func TestSetHooksRejectsOverflowingIndexBeforeSideEffects(t *testing.T) {
	t.Parallel()

	if strconv.IntSize < 64 {
		t.Skip("an index above MaxInt32 is not representable by int")
	}
	const secret = "private-hook-command"
	values, err := NewSparseArray(SparseEntry[string]{
		Index: int(int64(math.MaxInt32) + 1),
		Value: secret,
	})
	if err != nil {
		t.Fatalf("NewSparseArray() error = %v, want construction before tmux validation", err)
	}
	runner := &versionQueueRunner{}
	result, err := serverWithRunner(runner).GlobalSessionScope().SetHooks(
		context.Background(),
		"session-renamed",
		values,
		SetHooksOptions{ClearExisting: true},
	)
	if !errors.Is(err, ErrInvalidSparseIndex) {
		t.Fatalf("SetHooks() error = %v, want ErrInvalidSparseIndex", err)
	}
	if result.Cleared || result.AppliedIndices != nil {
		t.Fatalf("SetHooks() result = %#v, want zero progress", result)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("SetHooks() runner calls = %d, want 0", calls)
	}
	assertErrorGraphRedacts(t, err, secret)
}

func TestSetHooksRequiresGeneratedFamilyAndReceiverScopeBeforeSideEffects(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 0, Value: "display-message value"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func(Server, Session) (SetHooksResult, error)
	}{
		{
			name: "unknown family",
			run: func(server Server, _ Session) (SetHooksResult, error) {
				return server.GlobalSessionScope().SetHooks(context.Background(), "future-hook", values, SetHooksOptions{ClearExisting: true})
			},
		},
		{
			name: "custom family",
			run: func(server Server, _ Session) (SetHooksResult, error) {
				return server.GlobalSessionScope().SetHooks(context.Background(), "@custom-hook", values, SetHooksOptions{ClearExisting: true})
			},
		},
		{
			name: "wrong receiver scope",
			run: func(_ Server, session Session) (SetHooksResult, error) {
				return session.SetHooks(context.Background(), "pane-died", values, SetHooksOptions{ClearExisting: true})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			server, session, _, _ := optionTestObjects(runner)
			result, err := test.run(server, session)
			if !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("SetHooks() error = %v, want ErrInvalidOption", err)
			}
			if result.Cleared || len(result.AppliedIndices) != 0 || runner.callCount() != 0 {
				t.Fatalf("SetHooks() = (%#v, %d calls), want no side effects", result, runner.callCount())
			}
		})
	}
}

func TestSetHooksVersionPreflightCompletesBeforeSideEffects(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 2, Value: "display-message value"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unsupported scope version", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}, ExitCode: 0},
		}}}
		server := serverWithRunner(runner)
		result, err := server.GlobalSessionScope().SetHooks(
			context.Background(),
			"window-linked",
			values,
			SetHooksOptions{ClearExisting: true},
		)
		if !errors.Is(err, ErrInvalidOption) {
			t.Fatalf("SetHooks() error = %v, want ErrInvalidOption", err)
		}
		requests := runner.recordedRequests()
		if result.Cleared || len(result.AppliedIndices) != 0 ||
			len(requests) != 1 || !slices.Equal(requests[0].Arguments, []string{"-V"}) {
			t.Fatalf("SetHooks() = (%#v, %#v), want version probe without mutation", result, requests)
		}
	})

	t.Run("supported scope version", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		server := serverWithRunner(runner)
		result, err := server.GlobalSessionScope().SetHooks(
			context.Background(),
			"window-linked",
			values,
			SetHooksOptions{ClearExisting: true},
		)
		if err != nil {
			t.Fatalf("SetHooks() error = %v", err)
		}
		if !result.Cleared || !slices.Equal(result.AppliedIndices, []int{2}) {
			t.Fatalf("SetHooks() result = %#v, want cleared index 2", result)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"-V"},
			{"set-hook", "-g", "-u", "--", "window-linked"},
			{"set-hook", "-g", "--", "window-linked[2]", "display-message value"},
		}
		if len(requests) != len(want) {
			t.Fatalf("SetHooks() requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("SetHooks() request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})
}

func TestHookMutationTreatsStderrAtZeroExitAsFailure(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stderr: []string{"unknown option: session-renamed"}, ExitCode: 0},
	}}}
	err := serverWithRunner(runner).GlobalSessionScope().RunHook(context.Background(), "session-renamed")
	if !errors.Is(err, ErrUnknownOption) {
		t.Fatalf("RunHook(stderr, exit 0) error = %v, want ErrUnknownOption", err)
	}
}

// libtmux:parity libtmux.hooks.HooksMixin.run_hook#parameter-branch:global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g,global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g,global_:fabe8cef3aca
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#parameter-branch:g:03e14ffa90c7
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#warning:abb8cfc75660
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks#parameter-branch:clear_existing:563b7a5e5f56
// libtmux:parity libtmux.hooks.HooksMixin.set_hooks#parameter-branch:values:45898433e258
// libtmux:parity libtmux.hooks.HooksMixin.show_hook#parameter-branch:global_,hook,scope:4704725be4dd
// libtmux:parity libtmux.hooks.HooksMixin.show_hook#parameter-branch:global_,hook,scope:52fe2fc27f5b
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#parameter-branch:global_:746212d66b3a
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:global_:bcf3b5284452
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#parameter-branch:global_:fabe8cef3aca
// libtmux:parity libtmux.options.OptionsMixin.show_option
// libtmux:parity libtmux.options.OptionsMixin.show_option#parameter-branch:g:03e14ffa90c7
// libtmux:parity libtmux.options.OptionsMixin.show_options
func TestGlobalScopeHookOperationsBuildExactArguments(t *testing.T) {
	t.Parallel()

	values, err := NewSparseArray(SparseEntry[string]{Index: 2, Value: "display-message bulk"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		responses int
		want      [][]string
		run       func(GlobalSessionScope, GlobalWindowScope) error
	}{
		{
			name:      "global session hooks",
			responses: 1,
			want:      [][]string{{"show-options", "-g", "-A", "-H"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				_, err := scope.Hooks(context.Background())
				return err
			},
		},
		{
			name:      "raw global session hook",
			responses: 1,
			want:      [][]string{{"show-options", "-g", "-H", "-q", "-v", "--", "session-renamed[0]"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				_, _, err := scope.RawHook(context.Background(), "session-renamed[0]")
				return err
			},
		},
		{
			name:      "set global session hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "--", "session-renamed[0]", "display-message set"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.SetHook(context.Background(), "session-renamed[0]", "display-message set")
			},
		},
		{
			name:      "append global session hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-a", "--", "session-renamed", "display-message append"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.AppendHook(context.Background(), "session-renamed", "display-message append")
			},
		},
		{
			name:      "unset global session hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-u", "--", "session-renamed"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.UnsetHook(context.Background(), "session-renamed")
			},
		},
		{
			name:      "run global session hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-R", "--", "session-renamed[0]"}},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				return scope.RunHook(context.Background(), "session-renamed[0]")
			},
		},
		{
			name:      "replace global session hooks",
			responses: 2,
			want: [][]string{
				{"set-hook", "-g", "-u", "--", "session-renamed"},
				{"set-hook", "-g", "--", "session-renamed[2]", "display-message bulk"},
			},
			run: func(scope GlobalSessionScope, _ GlobalWindowScope) error {
				result, err := scope.SetHooks(
					context.Background(), "session-renamed", values,
					SetHooksOptions{ClearExisting: true},
				)
				if err == nil && (!result.Cleared || !slices.Equal(result.AppliedIndices, []int{2})) {
					return errors.New("unexpected global session SetHooks progress")
				}
				return err
			},
		},
		{
			name:      "global window hooks",
			responses: 1,
			want:      [][]string{{"show-options", "-g", "-w", "-A", "-H"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				_, err := scope.Hooks(context.Background())
				return err
			},
		},
		{
			name:      "raw global window hook",
			responses: 1,
			want:      [][]string{{"show-options", "-g", "-w", "-H", "-q", "-v", "--", "pane-died[0]"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				_, _, err := scope.RawHook(context.Background(), "pane-died[0]")
				return err
			},
		},
		{
			name:      "set global window hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-w", "--", "pane-died[0]", "display-message set"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.SetHook(context.Background(), "pane-died[0]", "display-message set")
			},
		},
		{
			name:      "append global window hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-w", "-a", "--", "pane-died", "display-message append"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.AppendHook(context.Background(), "pane-died", "display-message append")
			},
		},
		{
			name:      "unset global window hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-w", "-u", "--", "pane-died"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.UnsetHook(context.Background(), "pane-died")
			},
		},
		{
			name:      "run global window hook",
			responses: 1,
			want:      [][]string{{"set-hook", "-g", "-w", "-R", "--", "pane-died[0]"}},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				return scope.RunHook(context.Background(), "pane-died[0]")
			},
		},
		{
			name:      "replace global window hooks",
			responses: 2,
			want: [][]string{
				{"set-hook", "-g", "-w", "-u", "--", "pane-died"},
				{"set-hook", "-g", "-w", "--", "pane-died[2]", "display-message bulk"},
			},
			run: func(_ GlobalSessionScope, scope GlobalWindowScope) error {
				result, err := scope.SetHooks(
					context.Background(),
					"pane-died",
					values,
					SetHooksOptions{ClearExisting: true},
				)
				if err == nil && (!result.Cleared || !slices.Equal(result.AppliedIndices, []int{2})) {
					return errors.New("unexpected global window SetHooks progress")
				}
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := make([]versionResponse, test.responses)
			for index := range responses {
				responses[index].result.ExitCode = 0
			}
			runner := &versionQueueRunner{responses: responses}
			server := serverWithRunner(runner)
			if err := test.run(server.GlobalSessionScope(), server.GlobalWindowScope()); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != len(test.want) {
				t.Fatalf("requests = %#v, want %#v", requests, test.want)
			}
			for index := range test.want {
				if !slices.Equal(requests[index].Arguments, test.want[index]) {
					t.Fatalf("request %d = %#v, want %#v", index, requests[index].Arguments, test.want[index])
				}
			}
		})
	}
}

func TestGlobalWindowHookVersionPreflightUsesHistoricalScope(t *testing.T) {
	t.Parallel()

	t.Run("window scope on 3.2a", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		err := serverWithRunner(runner).GlobalWindowScope().SetHook(
			context.Background(), "window-linked", "display-message value",
		)
		if err != nil {
			t.Fatalf("GlobalWindowScope.SetHook(3.2a) error = %v", err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"-V"},
			{"set-hook", "-g", "-w", "--", "window-linked", "display-message value"},
		}
		if len(requests) != len(want) {
			t.Fatalf("requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})

	t.Run("session scope from 3.3 shares the version cache", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
		}}
		server := serverWithRunner(runner)
		if err := server.GlobalSessionScope().SetHook(
			context.Background(), "window-linked", "display-message session",
		); err != nil {
			t.Fatalf("GlobalSessionScope.SetHook(3.3) error = %v", err)
		}
		err := server.GlobalWindowScope().SetHook(
			context.Background(), "window-linked", "display-message value",
		)
		if !errors.Is(err, ErrInvalidOption) {
			t.Fatalf("GlobalWindowScope.SetHook(3.3) error = %v, want ErrInvalidOption", err)
		}
		requests := runner.recordedRequests()
		want := [][]string{
			{"-V"},
			{"set-hook", "-g", "--", "window-linked", "display-message session"},
		}
		if len(requests) != len(want) {
			t.Fatalf("requests = %#v, want %#v", requests, want)
		}
		for index := range want {
			if !slices.Equal(requests[index].Arguments, want[index]) {
				t.Fatalf("request %d = %#v, want %#v", index, requests[index].Arguments, want[index])
			}
		}
	})
}
