package tmux

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

// libtmux:parity libtmux.pane.Pane.capture_pane#warning:03c1f413095c
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:0c19eb697b50
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:2922a48ac870
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:2ec07ea00289
// libtmux:parity libtmux.pane.Pane.capture_pane#warning:4aee8ceaa034
// libtmux:parity libtmux.hooks.HooksMixin.run_hook#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.set_hook#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.show_hooks#warning:21a25a5f2e9b
// libtmux:parity libtmux.hooks.HooksMixin.unset_hook#warning:21a25a5f2e9b
func TestCapturePaneWarningsAreConcreteAndOrdered(t *testing.T) {
	t.Parallel()

	warnings := make([]Warning, 0, 5)
	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{}},
	}}
	pane := newCaptureTestPane(runner, func(warning Warning) {
		warnings = append(warnings, warning)
	})

	_, err := pane.Capture(context.Background(), CapturePaneRequest{
		TrimTrailing: true,
		ModeScreen:   true,
		Hyperlinks:   true,
		LineNumbers:  true,
		LineFlags:    true,
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	current := mustCaptureVersion(t, "3.3")
	want := []Warning{
		unsupportedCaptureWarning("trim_trailing", current, mustCaptureVersion(t, "3.4")),
		unsupportedCaptureWarning("mode_screen", current, mustCaptureVersion(t, "3.6")),
		unsupportedCaptureWarning("hyperlinks", current, mustCaptureVersion(t, "3.7")),
		unsupportedCaptureWarning("line_numbers", current, mustCaptureVersion(t, "3.7")),
		unsupportedCaptureWarning("line_flags", current, mustCaptureVersion(t, "3.7")),
	}
	if !slices.Equal(warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", warnings, want)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and capture", requests)
	}
	wantArguments := []string{"capture-pane", "-t", "$5:0.%7", "-p"}
	if !slices.Equal(requests[1].Arguments, wantArguments) {
		t.Fatalf("capture arguments = %#v, want %#v", requests[1].Arguments, wantArguments)
	}
}

// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027:2
// libtmux:parity libtmux.pane.Pane.capture_pane#version-branch:tmux-version:c6a18af85027:3
func TestCapturePaneVersionGateBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version      string
		wantFlags    []string
		wantFeatures []string
	}{
		{
			version:      "3.3",
			wantFeatures: []string{"trim_trailing", "mode_screen", "hyperlinks", "line_numbers", "line_flags"},
		},
		{
			version:      "3.4",
			wantFlags:    []string{"-T"},
			wantFeatures: []string{"mode_screen", "hyperlinks", "line_numbers", "line_flags"},
		},
		{
			version:      "3.6",
			wantFlags:    []string{"-T", "-M"},
			wantFeatures: []string{"hyperlinks", "line_numbers", "line_flags"},
		},
		{
			version:   "3.7",
			wantFlags: []string{"-T", "-M", "-H", "-L", "-F"},
		},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			features := make([]string, 0, 5)
			runner := &captureQueueRunner{responses: []captureResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			pane := newCaptureTestPane(runner, func(warning Warning) {
				features = append(features, warning.Feature)
			})
			_, err := pane.Capture(context.Background(), CapturePaneRequest{
				TrimTrailing: true,
				ModeScreen:   true,
				Hyperlinks:   true,
				LineNumbers:  true,
				LineFlags:    true,
			})
			if err != nil {
				t.Fatalf("Capture() error = %v", err)
			}
			if !slices.Equal(features, test.wantFeatures) {
				t.Fatalf("warning features = %#v, want %#v", features, test.wantFeatures)
			}
			requests := runner.recordedRequests()
			wantArguments := append(
				[]string{"capture-pane", "-t", "$5:0.%7", "-p"},
				test.wantFlags...,
			)
			if !slices.Equal(requests[1].Arguments, wantArguments) {
				t.Fatalf("capture arguments = %#v, want %#v", requests[1].Arguments, wantArguments)
			}
		})
	}
}

func TestCapturePaneWarningHandlerIsSynchronous(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{}},
	}}
	warningDelivered := false
	pane := newCaptureTestPane(runner, func(Warning) {
		if runner.callCount() != 1 {
			t.Errorf("runner calls during warning = %d, want version probe only", runner.callCount())
		}
		warningDelivered = true
	})

	if _, err := pane.Capture(
		context.Background(),
		CapturePaneRequest{TrimTrailing: true},
	); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !warningDelivered {
		t.Fatal("Capture() returned before warning delivery")
	}
}

func TestCapturePaneWarningHandlerMayBeCalledConcurrently(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandlers := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHandlers()
	pane := newCaptureTestPane(runner, func(Warning) {
		entered <- struct{}{}
		<-release
	})
	if _, err := pane.Server().Version(context.Background()); err != nil {
		t.Fatalf("Version() error = %v", err)
	}

	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := pane.Capture(
				context.Background(),
				CapturePaneRequest{TrimTrailing: true},
			)
			errorsChannel <- err
		}()
	}
	for call := 1; call <= 2; call++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("warning handler call %d did not overlap", call)
		}
	}
	releaseHandlers()
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
	}
}

func TestCapturePaneWithNilWarningHandlerIsSilent(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3"}}},
		{result: tmuxcmd.Result{}},
	}}
	pane := newCaptureTestPane(runner, nil)
	if _, err := pane.Capture(
		context.Background(),
		CapturePaneRequest{TrimTrailing: true},
	); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
}

func TestCapturePaneFailuresDoNotDeliverWarnings(t *testing.T) {
	t.Parallel()

	t.Run("validation", func(t *testing.T) {
		t.Parallel()

		warnings := 0
		runner := &captureQueueRunner{}
		pane := newCaptureTestPane(runner, func(Warning) { warnings++ })
		_, err := pane.Capture(context.Background(), CapturePaneRequest{
			Start:        "invalid",
			TrimTrailing: true,
		})
		if !errors.Is(err, ErrInvalidCaptureRequest) {
			t.Fatalf("Capture() error = %v, want ErrInvalidCaptureRequest", err)
		}
		if warnings != 0 {
			t.Fatalf("warning count = %d, want 0", warnings)
		}
	})

	for _, test := range []struct {
		name     string
		response captureResponse
		want     error
	}{
		{
			name:     "context",
			response: captureResponse{err: context.Canceled},
			want:     context.Canceled,
		},
		{
			name: "query result",
			response: captureResponse{result: tmuxcmd.Result{
				Stderr:   []string{"version unavailable"},
				ExitCode: 1,
			}},
			want: ErrVersionQuery,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			warnings := 0
			runner := &captureQueueRunner{responses: []captureResponse{test.response}}
			pane := newCaptureTestPane(runner, func(Warning) { warnings++ })
			_, err := pane.Capture(
				context.Background(),
				CapturePaneRequest{TrimTrailing: true},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Capture() error = %v, want %v", err, test.want)
			}
			if warnings != 0 {
				t.Fatalf("warning count = %d, want 0", warnings)
			}
			if runner.callCount() != 1 {
				t.Fatalf("runner calls = %d, want version probe only", runner.callCount())
			}
		})
	}
}

func unsupportedCaptureWarning(feature string, current, required Version) Warning {
	return Warning{
		Kind:            WarningUnsupportedFeature,
		Subcommand:      "capture-pane",
		Feature:         feature,
		CurrentVersion:  current,
		RequiredVersion: required,
		Message: "capture-pane: " + feature + " requires tmux " + required.String() +
			" or newer; current " + current.String() + "; feature ignored",
	}
}

func mustCaptureVersion(t *testing.T, raw string) Version {
	t.Helper()
	version, err := ParseVersion(raw)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
