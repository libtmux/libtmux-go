package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// Captured by hand from tmux next-3.9 (master-e880cf63) with a control
// client attached with -f new-layouts: display-message -p '#{window_layout}'
// for a two-pane vertical split.
const layoutListsPaneJSONFixture = `{"V":2,"L":{"t":"v","w":80,"h":24,"x":0,"y":0,` +
	`"c":[{"t":"p","w":80,"h":12,"x":0,"y":0,"l":0,"i":0,"I":"%0"},` +
	`{"t":"p","w":80,"h":11,"x":0,"y":13,"a":true,"i":1,"I":"%1"}]}}`

// The classic grammar's cell pattern never matches inside JSON, so a JSON
// layout must be read through its own pane-id shape or every JSON layout
// reports every pane present, including one that already left.
func TestLayoutListsPaneReadsJSONShape(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		pane PaneID
		want bool
	}{
		{name: "present", pane: "%1", want: true},
		{name: "absent", pane: "%99", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := layoutListsPane(layoutListsPaneJSONFixture, test.pane); got != test.want {
				t.Fatalf("layoutListsPane(JSON, %q) = %t, want %t", test.pane, got, test.want)
			}
		})
	}
}

// The refusal exists because tmux 3.3a exited the whole server on an
// unrecognised layout - history that justifies refusing on every version,
// not a live status report about whichever version answered this call. tmux
// 3.3a's own crash can never be reached, on any version, because this check
// runs first; wording that reads as "you are at risk on 3.3a" would mislead a
// caller connected to a version this exact crash cannot happen on.
func TestInvalidLayoutRefusalReadsAsHistoryNotALiveWarning(t *testing.T) {
	t.Parallel()

	err := (Window{
		server:    serverWithRunner(&versionQueueRunner{}),
		sessionID: "$7",
		windowID:  "@8",
	}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "garbage"})
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
	}

	message := err.Error()
	if strings.Contains(message, "tmux 3.3a exits") {
		t.Fatalf("message = %q, reads as a live warning about the connected version", message)
	}
	if !strings.Contains(message, "every version") {
		t.Fatalf("message = %q, want it to state the refusal is unconditional", message)
	}
}

// GO2-1: tmux's own layout_set_lookup is a prefix match, so "tile" and
// "even-h" apply on every version and can never reach layout_parse (the
// 3.3a crash path this package's exact-match guard existed to avoid). A
// unique prefix resolves to its preset's canonical full name - never the
// prefix itself - so tmux's own prefix resolution never runs on a value
// this package already classified; an ambiguous prefix is refused, naming
// its candidates, without ever reaching tmux.
func TestValidateLayoutAcceptsAUniquePrefixAndRefusesAnAmbiguousOne(t *testing.T) {
	t.Parallel()

	pre35, err := ParseVersion("3.2a")
	if err != nil {
		t.Fatal(err)
	}
	post35, err := ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		layout  string
		version Version
		want    string
		wantErr bool
	}{
		{name: "tile resolves to tiled", layout: "tile", version: pre35, want: "tiled"},
		{name: "even-h resolves to even-horizontal", layout: "even-h", version: pre35, want: "even-horizontal"},
		{
			name:   "even- is ambiguous between even-horizontal and even-vertical",
			layout: "even-", version: pre35, wantErr: true,
		},
		{
			name:   "main-v is unambiguous before mirrored presets exist",
			layout: "main-v", version: pre35, want: "main-vertical",
		},
		{
			name:   "main-v is ambiguous once mirrored presets exist",
			layout: "main-v", version: post35, wantErr: true,
		},
		{
			name:   "main-vertical-m resolves to the mirrored preset on 3.5+",
			layout: "main-vertical-m", version: post35, want: "main-vertical-mirrored",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := validateLayout(test.layout, test.version)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidServerCommandRequest) {
					t.Fatalf("validateLayout(%q) error = %v, want ErrInvalidServerCommandRequest", test.layout, err)
				}
				if strings.Contains(err.Error(), "is neither a layout preset") {
					t.Fatalf("validateLayout(%q) error = %q, want it to name the ambiguous candidates, "+
						"not claim tmux does not know the spelling", test.layout, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateLayout(%q) error = %v", test.layout, err)
			}
			if resolved != test.want {
				t.Fatalf("validateLayout(%q) = %q, want %q", test.layout, resolved, test.want)
			}
		})
	}
}

// The refusal for a value that names nothing must still say so - only an
// ambiguous prefix gets the "ambiguous among" wording above.
func TestValidateLayoutStillRefusesAnUnrecognisedValue(t *testing.T) {
	t.Parallel()

	version, err := ParseVersion("3.7c")
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateLayout("zzzz", version)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("validateLayout(\"zzzz\") error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if !strings.Contains(err.Error(), "is neither a layout preset") {
		t.Fatalf("validateLayout(\"zzzz\") error = %q, want the unrecognised-value wording", err)
	}
}

// libtmux:parity libtmux.window.Window.next_layout
// libtmux:parity libtmux.window.Window.previous_layout
// libtmux:parity libtmux.window.Window.select_layout
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout,next_layout,previous_layout,spread:0fa948c7463a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout:2a9f9e28d66a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:next_layout:e5241b7cc5f9
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:previous_layout:146fb5ba152c
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:spread:ef0ecf84f4e8
func TestWindowLayoutCommandsBuildLiteralArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantArgs []string
		invoke   func(Window) error
	}{
		{
			name:     "reapply last preset",
			wantArgs: []string{"select-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{})
			},
		},
		{
			name:     "named layout",
			wantArgs: []string{"select-layout", "-t", "$7:0", "even-horizontal"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{
					Layout: "even-horizontal",
				})
			},
		},
		{
			name:     "spread",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-E"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Spread: true})
			},
		},
		{
			name:     "next flag",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-n"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Next: true})
			},
		},
		{
			name:     "previous flag",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-p"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Previous: true})
			},
		},
		{
			name:     "next command",
			wantArgs: []string{"next-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.NextLayout(context.Background())
			},
		},
		{
			name:     "previous command",
			wantArgs: []string{"previous-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.PreviousLayout(context.Background())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			err := test.invoke(Window{
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			})
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("layout operation error = %v, want ErrCommand", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

// tmux 3.8 started reporting and accepting a JSON layout shape alongside the
// classic grammar (CHANGES, 3.7c to 3.8: "Layout strings now use a JSON
// subset format... The old format is still accepted."). SelectLayout must
// forward that shape only once the server can actually understand it -
// tmux 3.3/3.3a exits the whole server on a layout string it does not
// recognise, and pre-3.8 tmux does not recognise JSON.
func TestSelectLayoutGatesJSONShapedLayoutByVersion(t *testing.T) {
	t.Parallel()

	// A value tmux would never itself produce, to prove this checks shape and
	// not this round's specific fixture.
	const jsonLayout = `{"V":2,"L":{"t":"p","w":81,"h":25,"x":0,"y":0,"a":false,"i":9,"I":"%9"}}`

	t.Run("below 3.8 refuses without reaching tmux", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}},
		}}}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: jsonLayout})

		var tooLow *VersionTooLowError
		if !errors.As(err, &tooLow) {
			t.Fatalf("SelectLayout() error = %v, want *VersionTooLowError", err)
		}
		if tooLow.Minimum.String() != "3.8" {
			t.Errorf("Minimum = %s, want 3.8", tooLow.Minimum)
		}
		if calls := runner.callCount(); calls != 1 {
			t.Fatalf("runner calls = %d, want 1 (version probe only, no select-layout sent)", calls)
		}
	})

	t.Run("3.8 and newer forwards it like any other layout string", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}},
			{result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7}},
		}}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: jsonLayout})

		if !errors.Is(err, ErrCommand) {
			t.Fatalf("SelectLayout() error = %v, want ErrCommand", err)
		}
		requests := runner.recordedRequests()
		if len(requests) != 2 {
			t.Fatalf("request count = %d, want 2 (version probe, then select-layout)", len(requests))
		}
		assertRequestArguments(t, requests[1], []string{
			"select-layout", "-t", "$7:0", jsonLayout,
		})
	})

	t.Run("text that merely starts with a brace is never mistaken for JSON", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "{not json"})

		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0 (rejected before any probe or command)", calls)
		}
	})
}

func TestSelectLayoutRejectsMultipleModesBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request SelectLayoutRequest
	}{
		{
			name:    "layout and spread",
			request: SelectLayoutRequest{Layout: "tiled", Spread: true},
		},
		{
			name:    "next and previous",
			request: SelectLayoutRequest{Next: true, Previous: true},
		},
		{
			name:    "spread and next",
			request: SelectLayoutRequest{Spread: true, Next: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := (Window{
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			}).SelectLayout(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestSelectLayoutUsesLiteralCommandBoundary(t *testing.T) {
	t.Parallel()

	// Reject separators before tmux 3.3a can exit the server for an unknown layout.
	t.Run("trailing separator", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "custom;"})
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	})

	t.Run("NUL", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "bad\x00layout"})
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	})
}
