package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestHasSessionAnswersForTheNameUnlessPatternMatchingIsRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  HasSessionRequest
		result   tmuxcmd.Result
		want     bool
		wantArgs []string
	}{
		{
			name:     "the name is carried by a session",
			request:  HasSessionRequest{Target: "work"},
			result:   tmuxcmd.Result{Stdout: []string{"$0 work"}},
			want:     true,
			wantArgs: []string{"list-sessions", "-F", "#{session_id} #{session_name}"},
		},
		{
			name:     "no session carries the name",
			request:  HasSessionRequest{Target: "missing"},
			result:   tmuxcmd.Result{Stdout: []string{"$0 work"}},
			want:     false,
			wantArgs: []string{"list-sessions", "-F", "#{session_id} #{session_name}"},
		},
		{
			// A name holding a space is one row, so the split has to be at the
			// first space and not the last.
			name:     "the name holds a space",
			request:  HasSessionRequest{Target: "two words"},
			result:   tmuxcmd.Result{Stdout: []string{"$4 two words"}},
			want:     true,
			wantArgs: []string{"list-sessions", "-F", "#{session_id} #{session_name}"},
		},
		{
			// tmux resolves an identifier before any name, and its exact-match
			// marker does not stop it, so asking the name has to not ask tmux.
			name:     "a name that looks like another session's identifier",
			request:  HasSessionRequest{Target: "$0"},
			result:   tmuxcmd.Result{Stdout: []string{"$0 work"}},
			want:     false,
			wantArgs: []string{"list-sessions", "-F", "#{session_id} #{session_name}"},
		},
		{
			name:     "no server holds a session by any name",
			request:  HasSessionRequest{Target: "work"},
			result:   tmuxcmd.Result{Stderr: []string{"no server running on /tmp/x"}, ExitCode: 1},
			want:     false,
			wantArgs: []string{"list-sessions", "-F", "#{session_id} #{session_name}"},
		},
		{
			name:     "tmux pattern",
			request:  HasSessionRequest{Target: "wo*", Pattern: true},
			result:   tmuxcmd.Result{ExitCode: 0},
			want:     true,
			wantArgs: []string{"has-session", "-t", "wo*"},
		},
		{
			name:     "a pattern miss stays a miss",
			request:  HasSessionRequest{Target: "wo*", Pattern: true},
			result:   tmuxcmd.Result{Stderr: []string{"can't find session"}, ExitCode: 1},
			want:     false,
			wantArgs: []string{"has-session", "-t", "wo*"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: test.result}}}
			got, err := serverWithRunner(runner).HasSession(context.Background(), test.request)
			if err != nil {
				t.Fatalf("HasSession() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("HasSession() = %t, want %t", got, test.want)
			}
			assertLifecycleArguments(t, runner, test.wantArgs)
		})
	}
}

func TestHasSessionSurfacesTransportErrors(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{err: context.DeadlineExceeded}}}
	_, err := serverWithRunner(runner).HasSession(
		context.Background(),
		HasSessionRequest{Target: "work"},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HasSession() error = %v, want context deadline", err)
	}
}

func TestLifecycleRequestsValidateBeforeExecution(t *testing.T) {
	t.Parallel()

	negative := -1
	tooLarge := 65536
	underflow := -1
	overflow := 101
	tests := []struct {
		name string
		call func(Server) error
	}{
		{
			name: "empty has-session target",
			call: func(server Server) error {
				_, err := server.HasSession(context.Background(), HasSessionRequest{})
				return err
			},
		},
		{
			name: "invalid has-session target",
			call: func(server Server) error {
				_, err := server.HasSession(
					context.Background(),
					HasSessionRequest{Target: "bad:name"},
				)
				return err
			},
		},
		{
			name: "kill existing unnamed session",
			call: func(server Server) error {
				_, err := server.NewSession(
					context.Background(),
					NewSessionRequest{KillExisting: true},
				)
				return err
			},
		},
		{
			name: "invalid new session name",
			call: func(server Server) error {
				_, err := server.NewSession(
					context.Background(),
					NewSessionRequest{Name: "bad.name"},
				)
				return err
			},
		},
		{
			name: "negative new session width",
			call: func(server Server) error {
				_, err := server.NewSession(
					context.Background(), NewSessionRequest{Width: negative},
				)
				return err
			},
		},
		{
			name: "oversized new session height",
			call: func(server Server) error {
				_, err := server.NewSession(
					context.Background(), NewSessionRequest{Height: tooLarge},
				)
				return err
			},
		},
		{
			name: "negative window index",
			call: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{Index: &negative},
				)
				return err
			},
		},
		{
			name: "unknown new window direction",
			call: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{Direction: NewWindowDirection(99)},
				)
				return err
			},
		},
		{
			name: "select existing without name",
			call: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{SelectExisting: true},
				)
				return err
			},
		},
		{
			name: "unknown pane direction",
			call: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{Direction: PaneDirection(99)},
				)
				return err
			},
		},
		{
			name: "negative pane size",
			call: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{Size: &underflow},
				)
				return err
			},
		},
		{
			name: "pane percentage below range",
			call: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{Percentage: &underflow},
				)
				return err
			},
		},
		{
			name: "pane percentage out of range",
			call: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{Percentage: &overflow},
				)
				return err
			},
		},
		{
			name: "pane size and percentage",
			call: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{Size: &overflow, Percentage: &overflow},
				)
				return err
			},
		},
		{
			name: "negative floating pane width",
			call: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "@2",
				}).NewPane(
					context.Background(),
					NewPaneRequest{Width: &negative},
				)
				return err
			},
		},
		{
			name: "empty session rename",
			call: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).Rename(
					context.Background(),
					"",
				)
				return err
			},
		},
		{
			name: "invalid session rename",
			call: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).Rename(
					context.Background(),
					"bad:name",
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.call(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

// libtmux:parity libtmux.server.Server.has_session
// libtmux:parity libtmux.server.Server.has_session#parameter-branch:exact:17992787d42b
// libtmux:parity libtmux.session.Session.rename_session
func TestNewSessionBuildsEssentialArgumentsAndReturnsLiveModel(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	width := 132
	height := 43
	runner := &versionQueueRunner{responses: append(
		[]versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"$3 other"}, ExitCode: 0}},
			{result: tmuxcmd.Result{Stdout: []string{"$7"}, ExitCode: 0}},
		},
		lifecycleLookupResponses(t, version, "list-sessions", map[string]string{
			"session_id": "$7", "session_name": "alpha",
		})...,
	)}
	server := serverWithRunner(runner)

	session, err := server.NewSession(context.Background(), NewSessionRequest{
		Name:           "alpha",
		StartDirectory: "/work dir",
		WindowName:     "editor",
		Command:        "sleep 1m",
		Width:          width,
		Height:         height,
		Environment: map[string]string{
			"ZED":   "last",
			"ALPHA": "first",
		},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	name, _ := session.Name()
	producing := session.Server()
	if session.sessionID != "$7" || name != "alpha" ||
		producing.connectionState().executor != runner {
		t.Fatalf("NewSession() = %#v with name %q, want live $7 alpha model", session, name)
	}

	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{
		"list-sessions", "-F", "#{session_id} #{session_name}",
	})
	assertRequestArguments(t, requests[1], []string{
		"new-session", "-P", "-F#{session_id}", "-salpha", "-d",
		"-c", "/work dir", "-n", "editor", "-x", "132", "-y", "43",
		"-eALPHA=first", "-eZED=last", "sleep 1m",
	})
}

// libtmux:parity libtmux.server.Server.new_session
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:attach:58cb72758f2e
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:client_flags,session_name,start_directory,window_command,window_name,x,y:73e7c644693b
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:client_flags:54ec301cbb32
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:detach_others:6edab05acbf1
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:kill_session:84e924322fc0
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:no_size:4872209d09e2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3:2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3:3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:e704ec4d7e25
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:start_directory:bef78f09efe5
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:window_command:9439fd3d9eb2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:window_name:52160acf82b3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:y:0cf048966732
func TestNewSessionDetachedTranslationOmitsClientOnlyFlags(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
	}}}
	_, err := serverWithRunner(runner).NewSession(
		context.Background(),
		NewSessionRequest{Width: 0, Height: 0},
	)
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("NewSession() error = %v, want ErrCommand", err)
	}
	arguments := runner.recordedRequests()[0].Arguments
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"new-session", "-P", "-F#{session_id}", "-d",
	})
	for _, clientOnly := range []string{"-D", "-X", "-f"} {
		if slices.Contains(arguments, clientOnly) {
			t.Fatalf("NewSession() arguments = %#v, want detached translation without %s", arguments, clientOnly)
		}
	}
}

// libtmux:parity libtmux.exc.TmuxSessionExists
func TestNewSessionRejectsDuplicateOrKillsItWhenRequested(t *testing.T) {
	t.Parallel()

	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"$2 alpha"}},
		}}}
		_, err := serverWithRunner(runner).NewSession(
			context.Background(),
			NewSessionRequest{Name: "alpha"},
		)
		if !errors.Is(err, ErrSessionExists) {
			t.Fatalf("NewSession() error = %v, want ErrSessionExists", err)
		}
		if calls := runner.callCount(); calls != 1 {
			t.Fatalf("runner calls = %d, want one listing call", calls)
		}
	})

	t.Run("kill existing", func(t *testing.T) {
		t.Parallel()

		version := mustParseVersion(t, "3.7")
		runner := &versionQueueRunner{responses: append(
			[]versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"$2 alpha"}}},
				{result: tmuxcmd.Result{ExitCode: 0}},
				{result: tmuxcmd.Result{Stdout: []string{"$7"}, ExitCode: 0}},
			},
			lifecycleLookupResponses(t, version, "list-sessions", map[string]string{
				"session_id": "$7", "session_name": "alpha",
			})...,
		)}
		server := serverWithRunner(runner)

		if _, err := server.NewSession(
			context.Background(),
			NewSessionRequest{Name: "alpha", KillExisting: true},
		); err != nil {
			t.Fatalf("NewSession() error = %v", err)
		}
		requests := runner.recordedRequests()
		// The identifier the listing found, not the name that found it.
		assertRequestArguments(t, requests[1], []string{"kill-session", "-t", "$2"})
		assertRequestArguments(t, requests[2], []string{
			"new-session", "-P", "-F#{session_id}", "-salpha", "-d",
		})
	})
}

func TestNewWindowBuildsEssentialArgumentsAndReturnsLiveModel(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	index := 4
	requestName := "editor"
	runner := &versionQueueRunner{responses: append(
		[]versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"@8"}, ExitCode: 0},
		}},
		lifecycleSnapshotResponses(
			t,
			version,
			map[string]string{"session_id": "$1", "session_name": "work"},
			map[string]string{
				"session_id": "$1", "window_id": "@8", "window_index": "4", "window_name": "editor",
			},
			nil,
		)...,
	)}
	server := serverWithRunner(runner)
	session := Session{server: server, sessionID: "$1"}

	window, err := session.NewWindow(context.Background(), NewWindowRequest{
		Name:           &requestName,
		Index:          &index,
		StartDirectory: "/work",
		Command:        "sleep 1m",
	})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	name, _ := window.Name()
	if window.windowID != "@8" || window.windowIndex != 4 || name != "editor" ||
		window.Server().connectionState() != server.connectionState() || window.Server().daemon == nil {
		t.Fatalf("NewWindow() = %#v with name %q, want live @8 index 4 model", window, name)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"new-window", "-t", "$1:4", "-d", "-P", "-c/work",
		"-F#{window_id}", "-n", "editor", "sleep 1m",
	})
}

func TestSplitPaneBuildsEssentialArgumentsAndReturnsLiveModel(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	size := 10
	runner := &versionQueueRunner{responses: append(
		[]versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"%9"}, ExitCode: 0},
		}},
		lifecycleSnapshotResponses(
			t,
			version,
			map[string]string{"session_id": "$1", "session_name": "work"},
			map[string]string{
				"session_id": "$1", "window_id": "@8", "window_index": "4",
			},
			map[string]string{
				"session_id": "$1", "window_id": "@8", "window_index": "4",
				"pane_id": "%9", "pane_index": "1",
			},
		)...,
	)}
	server := serverWithRunner(runner)
	window := Window{server: server, sessionID: "$1", windowID: "@8", windowIndex: 4}

	pane, err := window.SplitPane(context.Background(), SplitPaneRequest{
		Direction:      PaneDirectionLeft,
		Size:           &size,
		StartDirectory: "/work",
		Command:        "sleep 1m",
	})
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	if pane.paneID != "%9" || pane.paneIndex != 1 ||
		pane.Server().connectionState() != server.connectionState() || pane.Server().daemon == nil {
		t.Fatalf("SplitPane() = %#v, want live %%9 model", pane)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"split-window", "-t", "$1:4", "-h", "-b", "-l10", "-P",
		"-F#{pane_id}", "-c/work", "-d", "sleep 1m",
	})
}

func TestSplitPaneAllowsZeroSizeAndPercentage(t *testing.T) {
	t.Parallel()

	zero := 0
	tests := []struct {
		name     string
		request  SplitPaneRequest
		wantFlag string
	}{
		{name: "size", request: SplitPaneRequest{Size: &zero}, wantFlag: "-l0"},
		{name: "percentage", request: SplitPaneRequest{Percentage: &zero}, wantFlag: "-l0%"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			_, err := (Window{
				server: serverWithRunner(runner), sessionID: "$1", windowID: "@2",
			}).SplitPane(
				context.Background(),
				test.request,
			)
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("SplitPane() error = %v, want ErrCommand", err)
			}
			arguments := runner.recordedRequests()[0].Arguments
			if !slices.Contains(arguments, test.wantFlag) {
				t.Fatalf("SplitPane() arguments = %#v, want %q", arguments, test.wantFlag)
			}
		})
	}
}

// libtmux:parity libtmux.window.Window.rename_window
// libtmux:parity libtmux.window.Window.select
func TestRenameAndSelectReturnRefreshedModels(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name        string
		command     string
		wantArgs    []string
		listing     string
		row         map[string]string
		invoke      func(Server) (string, error)
		wantFormat  string
		wantModelID string
	}{
		{
			name:     "rename session",
			command:  "rename-session",
			wantArgs: []string{"rename-session", "-t", "$7", "renamed"},
			listing:  "list-sessions",
			row: map[string]string{
				"session_id": "$7", "session_name": "renamed",
			},
			invoke: func(server Server) (string, error) {
				value, err := (Session{server: server, sessionID: "$7"}).Rename(
					context.Background(),
					"renamed",
				)
				name, _ := value.Name()
				return value.sessionID.String() + ":" + name, err
			},
			wantFormat: "$7:renamed",
		},
		{
			name:     "rename window",
			command:  "rename-window",
			wantArgs: []string{"rename-window", "-t", "$7:0", "renamed"},
			listing:  "list-windows",
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "2", "window_name": "renamed",
			},
			invoke: func(server Server) (string, error) {
				value, err := (Window{server: server, sessionID: "$7", windowID: "@8"}).Rename(
					context.Background(),
					"renamed",
				)
				name, _ := value.Name()
				return value.windowID.String() + ":" + name, err
			},
			wantFormat: "@8:renamed",
		},
		{
			name:     "select window",
			command:  "select-window",
			wantArgs: []string{"select-window", "-t", "$7:0"},
			listing:  "list-windows",
			row: map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "2", "window_active": "1",
			},
			invoke: func(server Server) (string, error) {
				value, err := (Window{
					server: server, sessionID: "$7", windowID: "@8",
				}).Select(context.Background())
				active, _ := value.Active()
				activeRaw := "0"
				if active {
					activeRaw = "1"
				}
				return value.windowID.String() + ":" + activeRaw, err
			},
			wantFormat: "@8:1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
			responses = append(responses, lifecycleLookupResponses(t, version, test.listing, test.row)...)
			runner := &versionQueueRunner{responses: responses}
			got, err := test.invoke(serverWithRunner(runner))
			if err != nil {
				t.Fatalf("%s operation error = %v", test.command, err)
			}
			if got != test.wantFormat {
				t.Fatalf("%s refreshed model = %q, want %q", test.command, got, test.wantFormat)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

func TestEveryLifecycleMutatorTurnsNonzeroExitIntoCommandError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subcommand string
		redacted   bool
		invoke     func(Server) error
	}{
		{
			name:       "new session",
			subcommand: "new-session",
			redacted:   true,
			invoke: func(server Server) error {
				_, err := server.NewSession(context.Background(), NewSessionRequest{})
				return err
			},
		},
		{
			name:       "new window",
			subcommand: "new-window",
			redacted:   true,
			invoke: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{},
				)
				return err
			},
		},
		{
			name:       "split pane",
			subcommand: "split-window",
			redacted:   true,
			invoke: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{},
				)
				return err
			},
		},
		{
			name:       "rename session",
			subcommand: "rename-session",
			invoke: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).Rename(
					context.Background(),
					"renamed",
				)
				return err
			},
		},
		{
			name:       "rename window",
			subcommand: "rename-window",
			invoke: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).Rename(
					context.Background(),
					"renamed",
				)
				return err
			},
		},
		{
			name:       "kill server",
			subcommand: "kill-server",
			invoke: func(server Server) error {
				return server.Kill(context.Background())
			},
		},
		{
			name:       "kill session",
			subcommand: "kill-session",
			invoke: func(server Server) error {
				return (Session{server: server, sessionID: "$1"}).Kill(context.Background())
			},
		},
		{
			name:       "kill window",
			subcommand: "kill-window",
			invoke: func(server Server) error {
				return (Window{server: server, windowID: "@2"}).Kill(context.Background())
			},
		},
		{
			name:       "kill pane",
			subcommand: "kill-pane",
			invoke: func(server Server) error {
				return (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Kill(context.Background())
			},
		},
		{
			name:       "select window",
			subcommand: "select-window",
			invoke: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "@2",
				}).Select(context.Background())
				return err
			},
		},
		{
			name:       "select pane",
			subcommand: "select-pane",
			invoke: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Select(context.Background(), PaneSelectRequest{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"injected failure"}, ExitCode: 7,
			}}}}
			err := test.invoke(serverWithRunner(runner))
			var commandError *CommandError
			if !errors.As(err, &commandError) || commandError.Subcommand != test.subcommand {
				t.Fatalf("operation error = %#v, want %s CommandError", err, test.subcommand)
			}
			if commandError.Result.ExitCode != 7 {
				t.Fatalf("CommandError exit = %d, want 7", commandError.Result.ExitCode)
			}
			if test.redacted {
				if commandError.Result.Command != nil || commandError.Result.Stdout != nil ||
					commandError.Result.Stderr != nil {
					t.Fatalf("CommandError result = %#v, want exit-code-only result", commandError.Result)
				}
				return
			}
			if !slices.Equal(commandError.Result.Stderr, []string{"injected failure"}) {
				t.Fatalf("CommandError stderr = %#v, want tmux diagnostic", commandError.Result.Stderr)
			}
		})
	}
}

func TestCreatedIdentityMustBeOneValidStableID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stdout []string
	}{
		{name: "missing", stdout: nil},
		{name: "multiple", stdout: []string{"$1", "$2"}},
		{name: "wrong sigil", stdout: []string{"@1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stdout: test.stdout, ExitCode: 0,
			}}}}
			_, err := serverWithRunner(runner).NewSession(context.Background(), NewSessionRequest{})
			if !errors.Is(err, ErrInvalidCommandOutput) {
				t.Fatalf("NewSession() error = %v, want ErrInvalidCommandOutput", err)
			}
			if calls := runner.callCount(); calls != 1 {
				t.Fatalf("runner calls = %d, want no lookup after invalid output", calls)
			}
		})
	}
}

func TestCreationReturnsIdentityHandleWhenLiveLookupFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		wantSameServer bool
		invoke         func(Server) (string, Server, error)
	}{
		{
			name: "session",
			invoke: func(server Server) (string, Server, error) {
				value, err := server.NewSession(context.Background(), NewSessionRequest{})
				return value.sessionID.String(), value.Server(), err
			},
		},
		{
			name:           "window",
			wantSameServer: true,
			invoke: func(server Server) (string, Server, error) {
				value, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{},
				)
				return value.windowID.String(), value.Server(), err
			},
		},
		{
			name:           "pane",
			wantSameServer: true,
			invoke: func(server Server) (string, Server, error) {
				value, err := (Window{
					server: server, sessionID: "$1", windowID: "@2",
				}).SplitPane(
					context.Background(),
					SplitPaneRequest{},
				)
				return value.paneID.String(), value.Server(), err
			},
		},
	}
	identities := map[string]string{"session": "$7", "window": "@8", "pane": "%9"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{identities[test.name]}, ExitCode: 0}},
				{err: context.Canceled},
			}}
			server := serverWithRunner(runner)
			identity, producingServer, err := test.invoke(server)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("create error = %v, want context canceled lookup error", err)
			}
			if identity != identities[test.name] {
				t.Fatalf("partial-success identity = %q, want %q", identity, identities[test.name])
			}
			if same := producingServer == server; same != test.wantSameServer {
				t.Fatalf(
					"partial-success Server() identity equality = %t, want %t",
					same,
					test.wantSameServer,
				)
			}
			if producingServer.connectionState().executor != runner {
				t.Fatal("partial-success Server() lost the creating command executor")
			}
		})
	}
}

func TestCreationTransportErrorPreservesPrintedIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity string
		invoke   func(Server) (string, Server, error)
	}{
		{
			name:     "session",
			identity: "$7",
			invoke: func(server Server) (string, Server, error) {
				value, err := server.NewSession(context.Background(), NewSessionRequest{})
				return value.sessionID.String(), value.Server(), err
			},
		},
		{
			name:     "window",
			identity: "@8",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{},
				)
				return value.windowID.String(), value.Server(), err
			},
		},
		{
			name:     "pane",
			identity: "%9",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Window{
					server: server, sessionID: "$1", windowID: "@2",
				}).SplitPane(
					context.Background(),
					SplitPaneRequest{},
				)
				return value.paneID.String(), value.Server(), err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{test.identity}, ExitCode: -1},
				err:    context.Canceled,
			}}}
			server := serverWithRunner(runner)
			identity, producingServer, err := test.invoke(server)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("create error = %v, want context canceled", err)
			}
			if identity != test.identity {
				t.Fatalf("transport-error identity = %q, want %q", identity, test.identity)
			}
			if producingServer.connectionState().executor != runner {
				t.Fatal("transport-error handle lost the creating command executor")
			}
			if calls := runner.callCount(); calls != 1 {
				t.Fatalf("runner calls = %d, want no lookup after transport error", calls)
			}
		})
	}
}

func TestLifecycleSuccessRejectsStderrAtZeroExit(t *testing.T) {
	t.Parallel()

	result := CommandResult{Stderr: []string{"warning promoted to failure"}, ExitCode: 0}
	_, err := requireLifecycleSuccess("kill-server", result, nil)
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("requireLifecycleSuccess() error = %#v, want CommandError", err)
	}
	if commandError.Result.ExitCode != 0 ||
		!slices.Equal(commandError.Result.Stderr, []string{"warning promoted to failure"}) {
		t.Fatalf("CommandError result = %#v, want retained stderr", commandError.Result)
	}
}

func TestLifecycleCreationFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "lifecycle-secret"
	failure := versionResponse{result: tmuxcmd.Result{
		Command:  []string{"tmux", "create", secret},
		Stdout:   []string{"stdout " + secret},
		Stderr:   []string{"stderr " + secret},
		ExitCode: 1,
	}}
	tests := []struct {
		name      string
		responses []versionResponse
		create    func(Server) error
	}{
		{
			name:      "session",
			responses: []versionResponse{failure},
			create: func(server Server) error {
				_, err := server.NewSession(context.Background(), NewSessionRequest{
					Command: secret, Environment: map[string]string{"TOKEN": secret},
				})
				return err
			},
		},
		{
			name:      "window",
			responses: []versionResponse{failure},
			create: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{
						Command: secret, Environment: map[string]string{"TOKEN": secret},
					},
				)
				return err
			},
		},
		{
			name:      "split pane",
			responses: []versionResponse{failure},
			create: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "@2", windowIndex: 0,
				}).SplitPane(context.Background(), SplitPaneRequest{
					Command: secret, Environment: map[string]string{"TOKEN": secret},
				})
				return err
			},
		},
		{
			name: "floating pane",
			responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
				failure,
			},
			create: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "@2", windowIndex: 0,
				}).NewPane(context.Background(), NewPaneRequest{
					Command: secret, Environment: map[string]string{"TOKEN": secret},
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.create(serverWithRunner(&versionQueueRunner{responses: test.responses}))
			var commandError *CommandError
			if !errors.As(err, &commandError) {
				t.Fatalf("creation error = %#v, want *CommandError", err)
			}
			if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
				commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
				t.Fatalf("creation CommandError = %#v, want exit-code-only result", commandError)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("creation error disclosed payload: %v", err)
			}
		})
	}
}

func TestLifecycleCreationExpandsCurrentUserStartDirectory(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(home, "lifecycle-work")
	tests := []struct {
		name     string
		invoke   func(Server) error
		wantArgs []string
	}{
		{
			name: "session",
			invoke: func(server Server) error {
				_, err := server.NewSession(context.Background(), NewSessionRequest{
					StartDirectory: "~/lifecycle-work",
				})
				return err
			},
			wantArgs: []string{
				"new-session", "-P", "-F#{session_id}", "-d", "-c", wantDirectory,
			},
		},
		{
			name: "window",
			invoke: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{StartDirectory: "~/lifecycle-work"},
				)
				return err
			},
			wantArgs: []string{
				"new-window", "-t", "$1", "-d", "-P", "-c" + wantDirectory,
				"-F#{window_id}",
			},
		},
		{
			name: "pane",
			invoke: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{StartDirectory: "~/lifecycle-work"},
				)
				return err
			},
			wantArgs: []string{
				"split-window", "-t", "$1:0", "-v", "-P", "-F#{pane_id}",
				"-c" + wantDirectory, "-d",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			if err := test.invoke(serverWithRunner(runner)); !errors.Is(err, ErrCommand) {
				t.Fatalf("create error = %v, want ErrCommand", err)
			}
			assertLifecycleArguments(t, runner, test.wantArgs)
		})
	}
}

func TestLifecycleCreationRejectsNamedUserExpansionBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(Server) error
	}{
		{
			name: "session",
			invoke: func(server Server) error {
				_, err := server.NewSession(context.Background(), NewSessionRequest{
					StartDirectory: "~other/work",
				})
				return err
			},
		},
		{
			name: "window",
			invoke: func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{StartDirectory: "~other/work"},
				)
				return err
			},
		},
		{
			name: "pane",
			invoke: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SplitPane(
					context.Background(),
					SplitPaneRequest{StartDirectory: "~other/work"},
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.invoke(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("create error = %v, want ErrInvalidRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestWindowRenameAllowsEmptyName(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
	responses = append(responses, lifecycleLookupResponses(t, version, "list-windows", map[string]string{
		"session_id": "$7", "window_id": "@8", "window_index": "2", "window_name": "",
	})...)
	runner := &versionQueueRunner{responses: responses}
	window, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).Rename(
		context.Background(),
		"",
	)
	if err != nil {
		t.Fatalf("Window.Rename() error = %v", err)
	}
	name, _ := window.Name()
	if name != "" {
		t.Fatalf("Window.Rename() name = %q, want empty", name)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"rename-window", "-t", "$7:0", "",
	})
}

func TestMutationReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity string
		invoke   func(Server) (string, Server, error)
	}{
		{
			name:     "rename session",
			identity: "$7",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Session{server: server, sessionID: "$7"}).Rename(
					context.Background(),
					"renamed",
				)
				return value.sessionID.String(), value.Server(), err
			},
		},
		{
			name:     "rename window",
			identity: "@8",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Window{server: server, sessionID: "$7", windowID: "@8"}).Rename(
					context.Background(),
					"renamed",
				)
				return value.windowID.String(), value.Server(), err
			},
		},
		{
			name:     "select window",
			identity: "@8",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Window{
					server: server, sessionID: "$7", windowID: "@8",
				}).Select(context.Background())
				return value.windowID.String(), value.Server(), err
			},
		},
		{
			name:     "select pane",
			identity: "%9",
			invoke: func(server Server) (string, Server, error) {
				value, err := (Pane{
					server: server, sessionID: "$7", windowID: "@8", paneID: "%9",
				}).Select(context.Background(), PaneSelectRequest{})
				return value.paneID.String(), value.Server(), err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{ExitCode: 0}},
				{err: context.Canceled},
			}}
			server := serverWithRunner(runner)
			identity, producingServer, err := test.invoke(server)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mutation error = %v, want context canceled refresh error", err)
			}
			if identity != test.identity {
				t.Fatalf("partial-success identity = %q, want %q", identity, test.identity)
			}
			if producingServer != server {
				t.Fatalf("partial-success Server() = %#v, want receiver server", producingServer)
			}
		})
	}
}

func TestNewSessionScrubsTMUXFromOnlyTheChildEnvironment(t *testing.T) {
	t.Setenv("TMUX", "/tmp/foreign,123,0")
	t.Setenv("LIBTMUX_LIFECYCLE_KEEP", "present")
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"stop after environment capture"}, ExitCode: 7,
	}}}}

	_, err := serverWithRunner(runner).NewSession(context.Background(), NewSessionRequest{})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("NewSession() error = %v, want ErrCommand", err)
	}
	request := runner.recordedRequests()[0]
	if value, ok := lifecycleEnvironmentValue(request.Environment, "TMUX"); ok {
		t.Fatalf("new-session child TMUX = %q, want absent", value)
	}
	if value, ok := lifecycleEnvironmentValue(request.Environment, "LIBTMUX_LIFECYCLE_KEEP"); !ok || value != "present" {
		t.Fatalf("new-session child keep variable = (%q, %t), want (present, true)", value, ok)
	}
	if got := os.Getenv("TMUX"); got != "/tmp/foreign,123,0" {
		t.Fatalf("process TMUX after NewSession() = %q, want unchanged", got)
	}
}

func TestNewSessionScrubsTMUXFromExplicitEnvironment(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"stop after environment capture"}, ExitCode: 7,
	}}}}
	server := serverWithOptionsAndRunner(ServerOptions{
		ProcessEnvironment: []string{"TMUX=/tmp/foreign,123,0", "KEEP=value"},
	}, runner)

	_, err := server.NewSession(context.Background(), NewSessionRequest{})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("NewSession() error = %v, want ErrCommand", err)
	}
	request := runner.recordedRequests()[0]
	if !slices.Equal(request.Environment, []string{"KEEP=value"}) {
		t.Fatalf("new-session child environment = %#v, want only KEEP", request.Environment)
	}
	if !slices.Contains(request.Arguments, "-S/tmp/foreign") {
		t.Fatalf(
			"new-session arguments = %#v, want frozen TMUX socket selector",
			request.Arguments,
		)
	}
}

func TestNewSessionUsesOneScrubbedHandleAcrossTheLifecycle(t *testing.T) {
	t.Setenv("TMUX", "/tmp/foreign,123,0")
	t.Setenv("LIBTMUX_LIFECYCLE_KEEP", "present")
	version := mustParseVersion(t, "3.7")
	runner := &versionQueueRunner{responses: append(
		[]versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"$3 alpha"}, ExitCode: 0}},
			{result: tmuxcmd.Result{ExitCode: 0}},
			{result: tmuxcmd.Result{Stdout: []string{"$7"}, ExitCode: 0}},
		},
		lifecycleLookupResponses(t, version, "list-sessions", map[string]string{
			"session_id": "$7", "session_name": "alpha",
		})...,
	)}
	original := serverWithRunner(runner)

	session, err := original.NewSession(context.Background(), NewSessionRequest{
		Name: "alpha", KillExisting: true,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 6 {
		t.Fatalf("NewSession() request count = %d, want 6", len(requests))
	}
	for index, request := range requests {
		if value, ok := lifecycleEnvironmentValue(request.Environment, "TMUX"); ok {
			t.Fatalf("request %d child TMUX = %q, want absent", index, value)
		}
		if value, ok := lifecycleEnvironmentValue(request.Environment, "LIBTMUX_LIFECYCLE_KEEP"); !ok || value != "present" {
			t.Fatalf("request %d keep variable = (%q, %t), want (present, true)", index, value, ok)
		}
	}
	producing := session.Server()
	if producing == original {
		t.Fatal("NewSession() returned the unsanitized input server")
	}
	if value, ok := lifecycleEnvironmentValue(
		producing.ProcessEnvironment(),
		"TMUX",
	); ok {
		t.Fatalf("returned server TMUX = %q, want absent", value)
	}
	if got := os.Getenv("TMUX"); got != "/tmp/foreign,123,0" {
		t.Fatalf("process TMUX after NewSession() = %q, want unchanged", got)
	}
}

// libtmux:parity libtmux.server.Server.__exit__
// libtmux:parity libtmux.server.Server.kill
func TestServerKillUsesDaemonStateAfterCompletedFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		killStderr []string
		probe      versionResponse
		wantOK     bool
	}{
		{
			// tmux naming the server as gone proves the kill did what it was
			// asked, whatever it printed on the way out.
			name:       "dead after unfamiliar shutdown diagnostic",
			killStderr: []string{"server exited unexpectedly"},
			probe: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"no server running on /tmp/socket"}, ExitCode: 1,
			}},
			wantOK: true,
		},
		{
			// An unrecognized diagnostic proves nothing, so a kill that failed
			// stays failed rather than being read as a server that is gone.
			name:       "unproved after unfamiliar probe diagnostic",
			killStderr: []string{"server exited unexpectedly"},
			probe: versionResponse{result: tmuxcmd.Result{
				Stderr: []string{"unavailable"}, ExitCode: 1,
			}},
		},
		{
			name:       "alive despite familiar diagnostic",
			killStderr: []string{"no server running on socket"},
			probe:      versionResponse{result: tmuxcmd.Result{ExitCode: 0}},
		},
		{
			name:       "probe transport failure",
			killStderr: []string{"kill failed"},
			probe:      versionResponse{err: context.Canceled},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stderr: test.killStderr, ExitCode: 1}},
				test.probe,
			}}
			err := serverWithRunner(runner).Kill(context.Background())
			if test.wantOK {
				if err != nil {
					t.Fatalf("Server.Kill() error = %v, want nil", err)
				}
				return
			}
			var commandError *CommandError
			if !errors.As(err, &commandError) {
				t.Fatalf("Server.Kill() error = %#v, want CommandError", err)
			}
			if !slices.Equal(commandError.Result.Stderr, test.killStderr) {
				t.Fatalf("CommandError stderr = %#v, want %#v", commandError.Result.Stderr, test.killStderr)
			}
			if test.probe.err != nil && !errors.Is(err, test.probe.err) {
				t.Fatalf("Server.Kill() error = %v, want probe error %v", err, test.probe.err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 || !slices.Equal(requests[1].Arguments, []string{"list-sessions"}) {
				t.Fatalf("requests = %#v, want kill-server then list-sessions", requests)
			}
		})
	}
}

func lifecycleEnvironmentValue(environment []string, key string) (string, bool) {
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if name == key {
			return value, true
		}
	}
	return "", false
}

func lifecycleLookupResponses(
	t *testing.T,
	version Version,
	listing string,
	row map[string]string,
) []versionResponse {
	t.Helper()
	fields, err := formatFieldsFor(listing, version)
	if err != nil {
		t.Fatal(err)
	}
	return []versionResponse{
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(fields, snapshotRowValues(version, row)),
			ExitCode:  0,
		}},
		liveIdentityResponse(version),
	}
}

func lifecycleSnapshotResponses(
	t *testing.T,
	version Version,
	sessionRow map[string]string,
	windowRow map[string]string,
	paneRow map[string]string,
) []versionResponse {
	t.Helper()
	rows := []struct {
		listing string
		row     map[string]string
	}{
		{listing: "list-sessions", row: sessionRow},
		{listing: "list-windows", row: windowRow},
		{listing: "list-panes", row: paneRow},
		{listing: "list-clients"},
	}
	responses := []versionResponse{liveIdentityResponse(version)}
	for _, current := range rows {
		result := tmuxcmd.Result{ExitCode: 0}
		if current.row != nil {
			fields, err := formatFieldsFor(current.listing, version)
			if err != nil {
				t.Fatal(err)
			}
			result.RawStdout = framedSnapshotRecord(
				fields,
				snapshotRowValues(version, current.row),
			)
		}
		responses = append(responses, versionResponse{result: result})
	}
	return append(responses, liveIdentityResponse(version))
}

func assertLifecycleArguments(t *testing.T, runner *versionQueueRunner, want []string) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	assertRequestArguments(t, requests[0], want)
}

func assertRequestArguments(t *testing.T, request tmuxcmd.Request, want []string) {
	t.Helper()
	if !slices.Equal(request.Arguments, want) {
		t.Fatalf("request arguments = %#v, want %#v", request.Arguments, want)
	}
}
