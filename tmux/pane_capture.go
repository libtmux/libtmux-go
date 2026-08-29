package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

var (
	// ErrInvalidCaptureRequest identifies a capture request that cannot be
	// represented safely as tmux arguments.
	ErrInvalidCaptureRequest = errors.New("tmux: invalid capture request")

	captureVersion34 = Version{raw: "3.4", major: 3, minor: 4}
	captureVersion36 = Version{raw: "3.6", major: 3, minor: 6}
	captureVersion37 = Version{raw: "3.7", major: 3, minor: 7}
)

// CaptureRequestError reports a [CapturePaneRequest] field that cannot be
// represented as a tmux capture-pane argument. It matches
// [ErrInvalidCaptureRequest] through [errors.Is] and is available through
// [errors.As].
type CaptureRequestError struct {
	// Field names the invalid request field.
	Field string
	// Value is the rejected field value. It may be empty when absence is the
	// error.
	Value string
	// Reason describes the violated request constraint without promising a
	// stable complete error string.
	Reason string
}

// Error implements error.
func (e *CaptureRequestError) Error() string {
	return fmt.Sprintf("%v: %s %q %s", ErrInvalidCaptureRequest, e.Field, e.Value, e.Reason)
}

// Unwrap makes CaptureRequestError compatible with ErrInvalidCaptureRequest.
func (e *CaptureRequestError) Unwrap() error {
	return ErrInvalidCaptureRequest
}

// CapturePosition selects a line relative to tmux's visible pane or history.
// Its zero value omits the boundary and lets tmux use the visible-screen
// default. Construct numeric positions with [CaptureLine].
type CapturePosition string

const (
	// CaptureBoundary selects the start of history when used as Start and the
	// end of the visible pane when used as End.
	CaptureBoundary CapturePosition = "-"
)

// CaptureLine returns the canonical tmux representation of a capture line.
// Zero is the first visible line; negative values address history.
func CaptureLine(line int) CapturePosition {
	return CapturePosition(strconv.Itoa(line))
}

// CapturePaneRequest configures pane capture. Its zero value captures the
// visible screen with tmux's default text handling. Version-gated flags follow
// [UnsupportedPolicy]. [CaptureBoundary] selects the start of history for Start
// and the end of the visible pane for End.
type CapturePaneRequest struct {
	// Start selects the first captured line. Empty uses tmux's visible-screen
	// default; CaptureBoundary selects the start of history.
	Start CapturePosition
	// End selects the last captured line. Empty uses tmux's visible-screen
	// default; CaptureBoundary selects the end of the visible pane.
	End CapturePosition

	// EscapeSequences includes terminal attribute escape sequences.
	EscapeSequences bool
	// EscapeNonPrintable renders non-printable characters as octal escapes.
	EscapeNonPrintable bool
	// JoinWrapped joins wrapped lines, preserves their trailing spaces, and
	// implies tmux trimming independently of TrimTrailing.
	JoinWrapped bool
	// PreserveTrailing preserves trailing spaces at each line end.
	PreserveTrailing bool
	// TrimTrailing omits trailing positions without characters. It requires
	// tmux 3.4; see UnsupportedPolicy.
	TrimTrailing bool
	// AlternateScreen captures the alternate screen without history.
	AlternateScreen bool
	// Quiet suppresses tmux's error when AlternateScreen is requested but no
	// alternate screen exists.
	Quiet bool
	// ModeScreen captures the active mode screen. It requires tmux 3.6; see
	// UnsupportedPolicy.
	ModeScreen bool
	// Pending captures only the beginning of an incomplete escape sequence.
	Pending bool
	// Hyperlinks captures hyperlink metadata for the selected lines. It requires
	// tmux 3.7; see UnsupportedPolicy.
	Hyperlinks bool
	// LineNumbers prefixes each line with its tmux line number. It requires
	// tmux 3.7; see UnsupportedPolicy.
	LineNumbers bool
	// LineFlags prefixes each line with tmux line metadata flags. It requires
	// tmux 3.7; see UnsupportedPolicy.
	LineFlags bool
}

// Capture captures printed content from the receiver's exact linked pane.
// It returns a caller-owned slice. A completed nonzero exit or stderr does not
// become a [CommandError]; any stdout is returned without an error.
//
// This is a point-in-time capture rather than a stream; the zero request reads
// the visible screen. It may include a shell's echo of [Pane.SendKeys] input.
// Compare whole lines when polling; use [ControlClient.NextNotification] or
// [Server.WaitFor] when the application exposes a stream or explicit signal.
//
// Invalid positions fail before execution. Transport and context failures return
// any partial stdout with the error.
func (p Pane) Capture(
	ctx context.Context,
	request CapturePaneRequest,
) ([]string, error) {
	result, err := p.capturePane(ctx, "", false, request)
	return result.Stdout, err
}

// CaptureBytes captures printed content from the receiver's exact linked pane
// as caller-owned stdout bytes. It preserves tmux's output delimiters and
// trailing newlines after tmux has interpreted the pane's terminal contents.
//
// Its validation and failure behavior match [Pane.Capture], including returning
// partial bytes with transport or context errors.
func (p Pane) CaptureBytes(
	ctx context.Context,
	request CapturePaneRequest,
) ([]byte, error) {
	result, err := p.capturePane(ctx, "", false, request)
	return result.RawStdout, err
}

// CaptureToBuffer captures content from the receiver's exact linked pane
// into the nonempty named tmux buffer. The buffer is owned by tmux and no
// printed output is returned. A completed nonzero exit or stderr does not
// become a [CommandError].
//
// Invalid requests fail before execution. Transport and context errors are
// delivery-ambiguous: the buffer may already have changed.
func (p Pane) CaptureToBuffer(
	ctx context.Context,
	buffer string,
	request CapturePaneRequest,
) error {
	_, err := p.capturePane(ctx, buffer, true, request)
	return err
}

// captureFileSequence names one call's scratch buffer apart from every other
// call's. Two captures of one pane can ask for different lines, so a buffer
// name shared between them would let one call save the other's screen.
var captureFileSequence atomic.Uint64

// CaptureToFile captures the receiver's exact linked pane through a tmux buffer
// and the file at path, returning the same lines [Pane.Capture] returns.
//
// Printed captures cannot safely cross a control connection, so [Pane.Capture]
// and [Pane.CaptureBytes] use a subprocess or reject fallback. CaptureToFile
// stages through a scratch buffer so capture, save, and cleanup remain
// engine-eligible; unsupported engines follow fallback.
//
// path must name a file the tmux server can write and this process can read.
// It is replaced and left for the caller. Concurrent calls must use distinct
// paths. The scratch tmux buffer is deleted before return when possible.
//
// Validation and version gating match [Pane.Capture]. Capture, save, and
// file-read failures return an error with no lines; buffer cleanup is best effort.
func (p Pane) CaptureToFile(
	ctx context.Context,
	path string,
	request CapturePaneRequest,
) ([]string, error) {
	expanded, err := expandBufferPath("save-buffer", path)
	if err != nil {
		return nil, err
	}
	buffer := fmt.Sprintf(
		"libtmux-go-capture-%d-%d",
		os.Getpid(),
		captureFileSequence.Add(1),
	)
	if err := p.CaptureToBuffer(ctx, buffer, request); err != nil {
		return nil, err
	}
	// The buffer is deleted whichever way this returns: tmux's most recent
	// buffer is what an interactive paste reaches, so leaving a pane's screen
	// there would change what the user's own prefix-] does.
	defer func() { _ = p.server.DeleteBuffer(ctx, &buffer) }()
	if err := p.server.SaveBuffer(ctx, SaveBufferRequest{
		Path: expanded,
		Name: &buffer,
	}); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(expanded)
	if err != nil {
		return nil, err
	}
	// tmux writes the buffer to the file exactly as it would have printed it, so
	// the same decoder produces the same lines Pane.Capture reports.
	return tmuxcmd.SplitStdout(contents), nil
}

func (p Pane) capturePane(
	ctx context.Context,
	buffer string,
	toBuffer bool,
	request CapturePaneRequest,
) (CommandResult, error) {
	if err := validateServerCommandArguments(
		"capture-pane",
		serverCommandArgument{field: "Buffer", value: buffer},
		serverCommandArgument{field: "Start", value: string(request.Start)},
		serverCommandArgument{field: "End", value: string(request.End)},
		serverCommandArgument{field: "Pane", value: p.paneID.String()},
	); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if toBuffer && buffer == "" {
		return CommandResult{ExitCode: -1}, &CaptureRequestError{
			Field:  "Buffer",
			Reason: "must not be empty",
		}
	}
	if err := validateCapturePosition("Start", request.Start); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if err := validateCapturePosition("End", request.End); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if err := validateTypedTarget(
		"capture-pane", "Pane", "pane", p.paneID.String(),
	); err != nil {
		return CommandResult{ExitCode: -1}, err
	}

	var current Version
	if captureNeedsVersion(request) {
		var err error
		current, err = p.server.Version(ctx)
		if err != nil {
			return CommandResult{ExitCode: -1}, err
		}
	}

	arguments := make([]string, 0, 22)
	var err error
	arguments = append(arguments, "capture-pane")
	if toBuffer {
		arguments = append(arguments, "-b", buffer)
	} else {
		arguments = append(arguments, "-p")
	}
	if request.Start != "" {
		arguments = append(arguments, "-S", string(request.Start))
	}
	if request.End != "" {
		arguments = append(arguments, "-E", string(request.End))
	}
	if request.EscapeSequences {
		arguments = append(arguments, "-e")
	}
	if request.EscapeNonPrintable {
		arguments = append(arguments, "-C")
	}
	if request.JoinWrapped {
		arguments = append(arguments, "-J")
	}
	if request.PreserveTrailing {
		arguments = append(arguments, "-N")
	}
	arguments, err = p.appendCaptureFeature(
		arguments,
		request.TrimTrailing,
		"-T",
		"trim_trailing",
		current,
		captureVersion34,
	)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if request.AlternateScreen {
		arguments = append(arguments, "-a")
	}
	if request.Quiet {
		arguments = append(arguments, "-q")
	}
	arguments, err = p.appendCaptureFeature(
		arguments,
		request.ModeScreen,
		"-M",
		"mode_screen",
		current,
		captureVersion36,
	)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if request.Pending {
		arguments = append(arguments, "-P")
	}
	arguments, err = p.appendCaptureFeature(
		arguments,
		request.Hyperlinks,
		"-H",
		"hyperlinks",
		current,
		captureVersion37,
	)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments, err = p.appendCaptureFeature(
		arguments,
		request.LineNumbers,
		"-L",
		"line_numbers",
		current,
		captureVersion37,
	)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments, err = p.appendCaptureFeature(
		arguments,
		request.LineFlags,
		"-F",
		"line_flags",
		current,
		captureVersion37,
	)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if !toBuffer {
		// Control mode does not escape command stdout. Pane content matching a
		// closing guard could truncate the reply and desynchronize later commands,
		// so printed captures always use a subprocess.
		p.server = p.server.withoutEngine()
	}
	return p.literalCmd(ctx, arguments...)
}

func validateCapturePosition(field string, position CapturePosition) error {
	value := string(position)
	if value == "" || position == CaptureBoundary {
		return nil
	}
	line, err := strconv.Atoi(value)
	if err == nil && strconv.Itoa(line) == value {
		return nil
	}
	return &CaptureRequestError{
		Field:  field,
		Value:  value,
		Reason: "must be empty, CaptureBoundary, or a canonical base-10 integer",
	}
}

func captureNeedsVersion(request CapturePaneRequest) bool {
	return request.TrimTrailing || request.ModeScreen || request.Hyperlinks ||
		request.LineNumbers || request.LineFlags
}

func (p Pane) appendCaptureFeature(
	arguments []string,
	requested bool,
	flag string,
	feature string,
	current Version,
	required Version,
) ([]string, error) {
	if !requested {
		return arguments, nil
	}
	if current.AtLeast(required) {
		return append(arguments, flag), nil
	}
	if err := p.server.unsupportedFeature("capture-pane", feature, current, required); err != nil {
		return nil, err
	}
	return arguments, nil
}
