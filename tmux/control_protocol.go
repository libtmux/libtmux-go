package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrControlProtocol identifies malformed tmux control-mode framing.
var ErrControlProtocol = errors.New("tmux: malformed control protocol")

// ControlProtocolError reports a control-stream state violation without
// retaining or disclosing command output or notification contents. It matches
// [ErrControlProtocol] through errors.Is.
type ControlProtocolError struct {
	// State is the parser state in which the violation occurred.
	State string
	// Reason describes the structural violation without quoting input.
	Reason string
}

// Error implements error.
func (e *ControlProtocolError) Error() string {
	return fmt.Sprintf("%v in %s state: %s", ErrControlProtocol, e.State, e.Reason)
}

// Unwrap makes ControlProtocolError compatible with ErrControlProtocol.
func (e *ControlProtocolError) Unwrap() error { return ErrControlProtocol }

// ControlCommandResult is one completed control-mode command frame. A failed
// tmux command remains result data through Failed; local, protocol, transport,
// and context failures are returned separately by [ControlClient.Cmd]. All
// slices are owned by the caller.
type ControlCommandResult struct {
	// Command is the safely encoded command's original argument vector.
	Command []string
	// RawStdout contains exact frame payload bytes, including each line's LF.
	RawStdout []byte
	// Timestamp is tmux's frame timestamp in Unix seconds.
	Timestamp int64
	// Number is tmux's command number for the frame. tmux counts commands the
	// server processed rather than commands this client sent, so a command from
	// any other client advances it too and the gap between two frames is not a
	// count of this client's work.
	Number uint64
	// Flags contains tmux's frame flags.
	Flags int
	// Failed reports whether tmux closed the frame with %error instead of %end.
	Failed bool
}

type controlGuardKind uint8

const (
	controlGuardNone controlGuardKind = iota
	controlGuardBegin
	controlGuardEnd
	controlGuardError
)

type controlGuard struct {
	timestamp int64
	number    uint64
	flags     int
}

type controlFrame struct {
	timestamp int64
	number    uint64
	flags     int
	rawStdout []byte
	failed    bool
}

func (f controlFrame) result(command []string) ControlCommandResult {
	return ControlCommandResult{
		Command:   slices.Clone(command),
		RawStdout: bytes.Clone(f.rawStdout),
		Timestamp: f.timestamp,
		Number:    f.number,
		Flags:     f.flags,
		Failed:    f.failed,
	}
}

type controlStreamParser struct {
	guard  *controlGuard
	output bytes.Buffer
}

func (p *controlStreamParser) consume(
	line []byte,
) (*controlFrame, []byte, error) {
	kind, guard, isGuard, err := parseControlGuard(line)
	if p.guard != nil {
		if err == nil && isGuard && guard == *p.guard &&
			(kind == controlGuardEnd || kind == controlGuardError) {
			frame := &controlFrame{
				timestamp: p.guard.timestamp,
				number:    p.guard.number,
				flags:     p.guard.flags,
				rawStdout: bytes.Clone(p.output.Bytes()),
				failed:    kind == controlGuardError,
			}
			p.guard = nil
			p.output.Reset()
			return frame, nil, nil
		}
		_, _ = p.output.Write(line)
		_ = p.output.WriteByte('\n')
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	if isGuard {
		if kind != controlGuardBegin {
			return nil, nil, controlProtocolError("idle", "closing guard has no active frame")
		}
		p.guard = &guard
		return nil, nil, nil
	}
	if len(line) == 0 || line[0] != '%' {
		return nil, nil, controlProtocolError("idle", "record is neither a begin guard nor notification")
	}
	return nil, bytes.Clone(line), nil
}

func (p *controlStreamParser) finish() error {
	if p.guard != nil {
		return controlProtocolError("frame", "stream ended before closing guard")
	}
	return nil
}

func parseControlGuard(
	line []byte,
) (controlGuardKind, controlGuard, bool, error) {
	record := string(line)
	var kind controlGuardKind
	switch {
	case record == "%begin" || strings.HasPrefix(record, "%begin "):
		kind = controlGuardBegin
	case record == "%end" || strings.HasPrefix(record, "%end "):
		kind = controlGuardEnd
	case record == "%error" || strings.HasPrefix(record, "%error "):
		kind = controlGuardError
	default:
		return controlGuardNone, controlGuard{}, false, nil
	}

	fields := strings.Split(record, " ")
	if len(fields) != 4 {
		return controlGuardNone, controlGuard{}, false,
			controlProtocolError("guard", "guard must contain three numeric fields")
	}
	timestamp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || timestamp < 0 {
		return controlGuardNone, controlGuard{}, false,
			controlProtocolError("guard", "timestamp is not a nonnegative integer")
	}
	number, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return controlGuardNone, controlGuard{}, false,
			controlProtocolError("guard", "command number is not a nonnegative integer")
	}
	flags, err := strconv.ParseInt(fields[3], 10, strconv.IntSize)
	if err != nil || flags < 0 {
		return controlGuardNone, controlGuard{}, false,
			controlProtocolError("guard", "flags are not a nonnegative integer")
	}
	return kind, controlGuard{
		timestamp: timestamp,
		number:    number,
		flags:     int(flags),
	}, true, nil
}

// encodeControlCommand quotes every value. A bare semicolon is emitted only for
// command lists; quoting it makes it an operand, and send-keys may type the
// remaining commands into a pane.
func encodeControlCommand(arguments []string, commandList bool) (string, error) {
	if len(arguments) == 0 || arguments[0] == "" {
		return "", invalidServerCommandRequest(
			"control", "Command", "", "must not be empty",
		)
	}
	var encoded strings.Builder
	for argumentIndex, argument := range arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return "", invalidServerCommandRequest(
				"control", "Arguments", "[redacted]", "contains NUL, which tmux cannot represent",
			)
		}
		if argumentIndex != 0 {
			_ = encoded.WriteByte(' ')
		}
		if commandList && argument == ";" {
			_ = encoded.WriteByte(';')
			continue
		}
		if argument == "" {
			_, _ = encoded.WriteString("''")
			continue
		}
		quoted := false
		for offset := range len(argument) {
			value := argument[offset]
			if value >= 0x20 && value <= 0x7e && value != '\'' {
				if !quoted {
					_ = encoded.WriteByte('\'')
					quoted = true
				}
				_ = encoded.WriteByte(value)
				continue
			}
			if quoted {
				_ = encoded.WriteByte('\'')
				quoted = false
			}
			if value == '\'' {
				_, _ = encoded.WriteString(`"'"`)
			} else {
				_, _ = fmt.Fprintf(&encoded, `\%03o`, value)
			}
		}
		if quoted {
			_ = encoded.WriteByte('\'')
		}
	}
	return encoded.String(), nil
}

func controlProtocolError(state, reason string) error {
	return &ControlProtocolError{State: state, Reason: reason}
}
