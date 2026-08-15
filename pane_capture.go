package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
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
// visible screen with tmux's default text handling. Version-gated flags are
// checked synchronously: unsupported flags are omitted and reported through
// the server's [WarningHandler]. [CaptureBoundary] selects the start of history
// for Start and the end of the visible pane for End.
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
	// tmux 3.4; older versions warn and omit the flag.
	TrimTrailing bool
	// AlternateScreen captures the alternate screen without history.
	AlternateScreen bool
	// Quiet suppresses tmux's error when AlternateScreen is requested but no
	// alternate screen exists.
	Quiet bool
	// ModeScreen captures the active mode screen. It requires tmux 3.6; older
	// versions warn and omit the flag.
	ModeScreen bool
	// Pending captures only the beginning of an incomplete escape sequence.
	Pending bool
	// Hyperlinks captures hyperlink metadata for the selected lines. It
	// requires tmux 3.7; older versions warn and omit the flag.
	Hyperlinks bool
	// LineNumbers prefixes each line with its tmux line number. It requires
	// tmux 3.7; older versions warn and omit the flag.
	LineNumbers bool
	// LineFlags prefixes each line with tmux line metadata flags. It requires
	// tmux 3.7; older versions warn and omit the flag.
	LineFlags bool
}

// Capture captures printed content from the receiver's exact linked pane.
// It returns a caller-owned slice. A completed nonzero exit or stderr does not
// become a [CommandError]; any stdout is returned without an error.
//
// The result is the pane's visible screen rather than a stream, and includes a
// shell's echo of whatever [Pane.SendKeys] typed. Compare whole lines when
// waiting for output; see "Reading a pane back" in the package documentation.
//
// Noncanonical positions return a [CaptureRequestError] before execution. A
// version probe may fail before capture when a gated option is requested.
// Transport and context failures return any caller-owned partial stdout with
// the error; context cancellation remains detectable with [errors.Is].
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
// Its request, completed-exit, stderr, version, transport, and context behavior
// matches [Pane.Capture]. A transport or context error returns any captured
// partial stdout bytes with the error.
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
// Invalid requests fail before execution. Transport and context errors remain
// detectable with [errors.Is] but are delivery-ambiguous: the buffer may already
// have changed when the local wait is canceled.
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
// It exists because a printed capture cannot cross a control-mode connection,
// so [Pane.Capture] and [Pane.CaptureBytes] start a tmux process even on a
// handle that selected an [Engine]. Every tmux command this issues prints
// nothing, so all of them ride the engine and a watch loop built on it starts no
// process at all. That is the trade in full: on a handle with no engine this is
// three tmux processes where [Pane.Capture] is one.
//
// It returns the lines it captured, where [Pane.CaptureToBuffer] returns only
// an error, because a tmux buffer needs a further command to read while this
// has already read the file. That makes it usable where [Pane.Capture] was.
//
// path must name a file the tmux server can write and this process can read.
// tmux writes it, so a path only this process can reach fails in tmux rather
// than here. It is replaced on every call and left behind on return: the caller
// owns it, and its exact bytes are what [Pane.CaptureBytes] would have returned.
// The tmux buffer is this package's own and is deleted before returning, though
// a failure after the capture can leave one named for this process.
//
// Concurrent calls sharing one path race for its contents. Give each caller its
// own path.
//
// Its request validation, version gating, and context behavior match
// [Pane.Capture]. Its failures do not: a printed capture hands back whatever
// tmux printed before failing, while this reports a failure of any of its three
// commands, or of the read, as an error with no lines.
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
	arguments = p.appendCaptureFeature(
		arguments,
		request.TrimTrailing,
		"-T",
		"trim_trailing",
		current,
		captureVersion34,
	)
	if request.AlternateScreen {
		arguments = append(arguments, "-a")
	}
	if request.Quiet {
		arguments = append(arguments, "-q")
	}
	arguments = p.appendCaptureFeature(
		arguments,
		request.ModeScreen,
		"-M",
		"mode_screen",
		current,
		captureVersion36,
	)
	if request.Pending {
		arguments = append(arguments, "-P")
	}
	arguments = p.appendCaptureFeature(
		arguments,
		request.Hyperlinks,
		"-H",
		"hyperlinks",
		current,
		captureVersion37,
	)
	arguments = p.appendCaptureFeature(
		arguments,
		request.LineNumbers,
		"-L",
		"line_numbers",
		current,
		captureVersion37,
	)
	arguments = p.appendCaptureFeature(
		arguments,
		request.LineFlags,
		"-F",
		"line_flags",
		current,
		captureVersion37,
	)
	if !toBuffer {
		// A printed capture is arbitrary pane content on tmux's stdout, and
		// control mode does not escape a command's output the way it escapes
		// %output. A pane holding a line identical to the frame's closing
		// guard therefore closes the frame early: this read is truncated, and
		// tmux's real guard then arrives with no frame open and fails the
		// connection for every later command on it. So a printed capture stays
		// on a tmux process whatever engine the handle selected. A capture into
		// a tmux buffer prints nothing and carries no pane content.
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
) []string {
	if !requested {
		return arguments
	}
	if current.AtLeast(required) {
		return append(arguments, flag)
	}
	p.server.warn(newUnsupportedFeatureWarning(
		"capture-pane",
		feature,
		current,
		required,
	))
	return arguments
}
