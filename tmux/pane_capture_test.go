package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestCaptureLineAndBoundaryValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		line int
		want CapturePosition
	}{
		{line: -12, want: "-12"},
		{line: 0, want: "0"},
		{line: 34, want: "34"},
	} {
		if got := CaptureLine(test.line); got != test.want {
			t.Errorf("CaptureLine(%d) = %q, want %q", test.line, got, test.want)
		}
	}
	if CaptureBoundary != CapturePosition("-") {
		t.Fatalf("CaptureBoundary = %q, want -", CaptureBoundary)
	}
}

// libtmux:parity libtmux.pane.Pane.capture_pane
// libtmux:parity libtmux.pane.Pane.capture_pane#overload:0653b86df0b2
// libtmux:parity libtmux.pane.Pane.capture_pane#overload:32a22433af94
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:alternate_screen:e91ecf2dceb1
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:end:303ee00bfa76
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:escape_non_printable:02b7db7578ad
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:escape_sequences:d40b84a4c7b9
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:hyperlinks:55a95c0433ba
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:join_wrapped:c9ba0c8a5e01
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:line_flags:06dd6ad5ce57
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:line_numbers:f20035bc8efc
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:mode_screen:b2ec82b89b59
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:pending:c991f82d2f68
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:preserve_trailing:f6e6a717b592
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:quiet:8573bc8befe4
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:start:e400519f5cf8
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:to_buffer:23593cb2f5d5
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:to_buffer:23593cb2f5d5:2
// libtmux:parity libtmux.pane.Pane.capture_pane#parameter-branch:trim_trailing:5505ae431ffc
func TestCapturePaneBuildsExactArguments(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{Stdout: []string{"captured"}}},
	}}
	pane := newCaptureTestPane(runner, nil)

	output, err := pane.Capture(context.Background(), CapturePaneRequest{
		Start:              CaptureLine(-2),
		End:                CaptureBoundary,
		EscapeSequences:    true,
		EscapeNonPrintable: true,
		JoinWrapped:        true,
		PreserveTrailing:   true,
		TrimTrailing:       true,
		AlternateScreen:    true,
		Quiet:              true,
		ModeScreen:         true,
		Pending:            true,
		Hyperlinks:         true,
		LineNumbers:        true,
		LineFlags:          true,
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !slices.Equal(output, []string{"captured"}) {
		t.Fatalf("Capture() = %#v, want captured", output)
	}

	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and capture", requests)
	}
	if !slices.Equal(requests[0].Arguments, []string{"-V"}) {
		t.Fatalf("version arguments = %#v, want -V", requests[0].Arguments)
	}
	want := []string{
		"capture-pane", "-t", "$5:0.%7", "-p",
		"-S", "-2", "-E", "-",
		"-e", "-C", "-J", "-N", "-T", "-a", "-q", "-M", "-P", "-H", "-L", "-F",
	}
	if !slices.Equal(requests[1].Arguments, want) {
		t.Fatalf("capture arguments = %#v, want %#v", requests[1].Arguments, want)
	}
}

func TestCapturePaneSkipsVersionProbeWithoutGatedFeatures(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{{
		result: tmuxcmd.Result{Stdout: []string{"plain"}},
	}}}
	pane := newCaptureTestPane(runner, nil)

	output, err := pane.Capture(context.Background(), CapturePaneRequest{
		Start:              CaptureLine(0),
		End:                CaptureLine(1),
		EscapeSequences:    true,
		EscapeNonPrintable: true,
		JoinWrapped:        true,
		PreserveTrailing:   true,
		AlternateScreen:    true,
		Quiet:              true,
		Pending:            true,
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !slices.Equal(output, []string{"plain"}) {
		t.Fatalf("Capture() = %#v, want plain", output)
	}

	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one capture", requests)
	}
	want := []string{
		"capture-pane", "-t", "$5:0.%7", "-p", "-S", "0", "-E", "1",
		"-e", "-C", "-J", "-N", "-a", "-q", "-P",
	}
	if !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("capture arguments = %#v, want %#v", requests[0].Arguments, want)
	}
}

func TestCaptureBytesReturnsExactOwnedOutput(t *testing.T) {
	t.Parallel()

	source := []byte{0xff, 'A', '\n', '\n'}
	runner := &captureQueueRunner{responses: []captureResponse{{result: tmuxcmd.Result{
		Stdout:    []string{`\xffA`},
		RawStdout: source,
	}}}}
	pane := newCaptureTestPane(runner, nil)

	output, err := pane.CaptureBytes(context.Background(), CapturePaneRequest{
		Start: CaptureLine(0),
		End:   CaptureLine(1),
	})
	if err != nil {
		t.Fatalf("CaptureBytes() error = %v", err)
	}
	want := []byte{0xff, 'A', '\n', '\n'}
	if !slices.Equal(output, want) {
		t.Fatalf("CaptureBytes() = %q, want %q", output, want)
	}
	output[0] = 'X'
	if source[0] != 0xff {
		t.Fatalf("CaptureBytes() aliases runner storage: %q", source)
	}

	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one capture", requests)
	}
	wantArguments := []string{
		"capture-pane", "-t", "$5:0.%7", "-p", "-S", "0", "-E", "1",
	}
	if !slices.Equal(requests[0].Arguments, wantArguments) {
		t.Fatalf("capture arguments = %#v, want %#v", requests[0].Arguments, wantArguments)
	}
}

func TestCaptureBytesReturnsPartialTransportOutput(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{{
		result: tmuxcmd.Result{RawStdout: []byte("partial\n"), ExitCode: -1},
		err:    context.Canceled,
	}}}
	pane := newCaptureTestPane(runner, nil)

	output, err := pane.CaptureBytes(context.Background(), CapturePaneRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureBytes() error = %v, want context canceled", err)
	}
	if !slices.Equal(output, []byte("partial\n")) {
		t.Fatalf("CaptureBytes() = %q, want partial output", output)
	}
}

func TestCapturePaneUsesCachedVersion(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	pane := newCaptureTestPane(runner, nil)

	for range 2 {
		if _, err := pane.Capture(
			context.Background(),
			CapturePaneRequest{TrimTrailing: true},
		); err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
	}
	requests := runner.recordedRequests()
	if len(requests) != 3 {
		t.Fatalf("runner requests = %#v, want one version and two captures", requests)
	}
	if !slices.Equal(requests[0].Arguments, []string{"-V"}) {
		t.Fatalf("first request arguments = %#v, want -V", requests[0].Arguments)
	}
}

func TestCapturePaneRejectsInvalidPositionsBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request CapturePaneRequest
		field   string
		value   CapturePosition
	}{
		{name: "start text", request: CapturePaneRequest{Start: "history"}, field: "Start", value: "history"},
		{name: "end plus", request: CapturePaneRequest{End: "+1"}, field: "End", value: "+1"},
		{name: "start leading zero", request: CapturePaneRequest{Start: "01"}, field: "Start", value: "01"},
		{name: "end negative zero", request: CapturePaneRequest{End: "-0"}, field: "End", value: "-0"},
		{name: "start whitespace", request: CapturePaneRequest{Start: " 1"}, field: "Start", value: " 1"},
		{
			name:    "end overflow",
			request: CapturePaneRequest{End: "999999999999999999999999999999999999"},
			field:   "End",
			value:   "999999999999999999999999999999999999",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &captureQueueRunner{}
			pane := newCaptureTestPane(runner, nil)
			_, err := pane.Capture(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidCaptureRequest) {
				t.Fatalf("Capture() error = %v, want ErrInvalidCaptureRequest", err)
			}
			var requestError *CaptureRequestError
			if !errors.As(err, &requestError) {
				t.Fatalf("Capture() error = %#v, want CaptureRequestError", err)
			}
			if requestError.Field != test.field || requestError.Value != string(test.value) {
				t.Fatalf(
					"CaptureRequestError = %#v, want field %s value %q",
					requestError,
					test.field,
					test.value,
				)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestCapturePaneValidatesTargetBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		paneID PaneID
		want   error
	}{
		{name: "missing", want: ErrMissingTarget},
		{name: "invalid", paneID: "pane", want: ErrInvalidTarget},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &captureQueueRunner{}
			pane := newCaptureTestPane(runner, nil)
			pane.paneID = test.paneID
			_, err := pane.Capture(
				context.Background(),
				CapturePaneRequest{TrimTrailing: true},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Capture() error = %v, want %v", err, test.want)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestCapturePaneToBufferUsesStaticResultShape(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{{result: tmuxcmd.Result{
		Stdout: []string{"ignored"},
	}}}}
	pane := newCaptureTestPane(runner, nil)

	if err := pane.CaptureToBuffer(
		context.Background(),
		"saved-capture",
		CapturePaneRequest{Start: CaptureBoundary, End: CaptureLine(4)},
	); err != nil {
		t.Fatalf("CaptureToBuffer() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one capture", requests)
	}
	want := []string{
		"capture-pane", "-t", "$5:0.%7", "-b", "saved-capture", "-S", "-", "-E", "4",
	}
	if !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("capture arguments = %#v, want %#v", requests[0].Arguments, want)
	}
}

func TestCaptureToFileUsesOnlyCommandsThatPrintNothing(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	pane := newCaptureTestPane(runner, nil)
	path := filepath.Join(t.TempDir(), "pane.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lines, err := pane.CaptureToFile(
		context.Background(),
		path,
		CapturePaneRequest{Start: CaptureBoundary},
	)
	if err != nil {
		t.Fatalf("CaptureToFile() error = %v", err)
	}
	if want := []string{"first", "second"}; !slices.Equal(lines, want) {
		t.Fatalf("CaptureToFile() = %#v, want %#v", lines, want)
	}

	requests := runner.recordedRequests()
	if len(requests) != 3 {
		t.Fatalf("runner requests = %#v, want capture, save, and delete", requests)
	}
	// tmux prints a reply for none of these three, which is what lets an engine
	// carry them where a printed capture-pane cannot go.
	buffer := requests[0].Arguments[4]
	if !strings.HasPrefix(buffer, "libtmux-go-capture-") {
		t.Fatalf("capture buffer = %q, want a name owned by this package", buffer)
	}
	for index, want := range [][]string{
		{"capture-pane", "-t", "$5:0.%7", "-b", buffer, "-S", "-"},
		{"save-buffer", "-b", buffer, "--", path},
		{"delete-buffer", "-b", buffer},
	} {
		if !slices.Equal(requests[index].Arguments, want) {
			t.Fatalf("request %d = %#v, want %#v", index, requests[index].Arguments, want)
		}
	}
}

func TestCaptureToFileNamesEachCallsBufferApart(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{responses: []captureResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	pane := newCaptureTestPane(runner, nil)
	path := filepath.Join(t.TempDir(), "pane.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := pane.CaptureToFile(context.Background(), path, CapturePaneRequest{}); err != nil {
			t.Fatalf("CaptureToFile() error = %v", err)
		}
	}
	requests := runner.recordedRequests()
	if len(requests) != 6 {
		t.Fatalf("runner requests = %d, want six", len(requests))
	}
	// Two captures of one pane can ask for different lines, so a shared buffer
	// name would let one call save the other call's screen.
	if requests[0].Arguments[4] == requests[3].Arguments[4] {
		t.Fatalf("both calls used the buffer %q", requests[0].Arguments[4])
	}
}

func TestCaptureToFileRejectsAPathTmuxCannotBeGiven(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{}
	pane := newCaptureTestPane(runner, nil)
	for _, path := range []string{"", "-"} {
		if _, err := pane.CaptureToFile(context.Background(), path, CapturePaneRequest{}); err == nil {
			t.Fatalf("CaptureToFile(%q) error = nil, want a request error", path)
		}
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}

func TestCapturePaneToBufferRejectsEmptyNameBeforeExecution(t *testing.T) {
	t.Parallel()

	runner := &captureQueueRunner{}
	pane := newCaptureTestPane(runner, nil)
	err := pane.CaptureToBuffer(context.Background(), "", CapturePaneRequest{})
	if !errors.Is(err, ErrInvalidCaptureRequest) {
		t.Fatalf("CaptureToBuffer() error = %v, want ErrInvalidCaptureRequest", err)
	}
	var requestError *CaptureRequestError
	if !errors.As(err, &requestError) || requestError.Field != "Buffer" || requestError.Value != "" {
		t.Fatalf("CaptureToBuffer() error = %#v, want empty Buffer CaptureRequestError", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}

func TestCapturePanePreservesCompletedRawFailures(t *testing.T) {
	t.Parallel()

	t.Run("printed", func(t *testing.T) {
		t.Parallel()

		runner := &captureQueueRunner{responses: []captureResponse{{result: tmuxcmd.Result{
			Stdout:   []string{"partial"},
			Stderr:   []string{"capture failed"},
			ExitCode: 1,
		}}}}
		pane := newCaptureTestPane(runner, nil)
		output, err := pane.Capture(context.Background(), CapturePaneRequest{})
		if err != nil {
			t.Fatalf("Capture() error = %v, want nil", err)
		}
		if !slices.Equal(output, []string{"partial"}) {
			t.Fatalf("Capture() = %#v, want partial stdout", output)
		}
	})

	t.Run("buffer", func(t *testing.T) {
		t.Parallel()

		runner := &captureQueueRunner{responses: []captureResponse{{result: tmuxcmd.Result{
			Stderr:   []string{"capture failed"},
			ExitCode: 1,
		}}}}
		pane := newCaptureTestPane(runner, nil)
		if err := pane.CaptureToBuffer(
			context.Background(),
			"failed-capture",
			CapturePaneRequest{},
		); err != nil {
			t.Fatalf("CaptureToBuffer() error = %v, want nil", err)
		}
	})
}

func TestCapturePaneSurfacesTransportErrors(t *testing.T) {
	t.Parallel()

	t.Run("printed retains partial stdout", func(t *testing.T) {
		t.Parallel()

		runner := &captureQueueRunner{responses: []captureResponse{{
			result: tmuxcmd.Result{Stdout: []string{"partial"}, ExitCode: -1},
			err:    context.Canceled,
		}}}
		pane := newCaptureTestPane(runner, nil)
		output, err := pane.Capture(context.Background(), CapturePaneRequest{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Capture() error = %v, want context canceled", err)
		}
		if !slices.Equal(output, []string{"partial"}) {
			t.Fatalf("Capture() = %#v, want partial stdout", output)
		}
	})

	t.Run("buffer", func(t *testing.T) {
		t.Parallel()

		runner := &captureQueueRunner{responses: []captureResponse{{
			result: tmuxcmd.Result{ExitCode: -1},
			err:    context.DeadlineExceeded,
		}}}
		pane := newCaptureTestPane(runner, nil)
		err := pane.CaptureToBuffer(
			context.Background(),
			"canceled-capture",
			CapturePaneRequest{},
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("CaptureToBuffer() error = %v, want context deadline", err)
		}
	})
}

type captureResponse struct {
	result tmuxcmd.Result
	err    error
}

type captureQueueRunner struct {
	mu        sync.Mutex
	responses []captureResponse
	requests  []tmuxcmd.Request
}

func (r *captureQueueRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request.Arguments = slices.Clone(request.Arguments)
	request.Environment = slices.Clone(request.Environment)
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		return tmuxcmd.Result{}, errors.New("unexpected capture runner call")
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response.result, response.err
}

func (r *captureQueueRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *captureQueueRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

// newCaptureTestPane builds a pane on a server that omits a capability the
// running tmux lacks. Capture's gated flags are what the warning path is
// exercised through; the default refuses, which
// TestUnsupportedFeaturesAreRefusedByDefault covers.
func newCaptureTestPane(runner commandRunner, handler WarningHandler) Pane {
	server := NewServer(ServerOptions{
		Unsupported:    DegradeUnsupported,
		WarningHandler: handler,
	})
	server.state.runner = runner
	return Pane{server: server, sessionID: "$5", windowID: "@6", paneID: "%7"}
}
