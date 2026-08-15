package tmux

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestEncodeControlCommandQuotesEveryArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"A", ""}, want: `'A' ''`},
		{arguments: []string{" ;$\"\\"}, want: `' ;$"\'`},
		{arguments: []string{"a\nb"}, want: `'a'\012'b'`},
		{arguments: []string{"a'b"}, want: `'a'"'"'b'`},
		{arguments: []string{"café"}, want: `'caf'\303\251`},
		{arguments: []string{"\t\r\x7f\xff"}, want: `\011\015\177\377`},
	}
	for _, test := range tests {
		got, err := encodeControlCommand(test.arguments, false)
		if err != nil {
			t.Fatalf("encodeControlCommand() error = %v", err)
		}
		if got != test.want {
			t.Fatalf("encodeControlCommand(%q) = %q, want %q", test.arguments, got, test.want)
		}
	}

	for _, arguments := range [][]string{nil, {}, {"display-message", "bad\x00value"}} {
		_, err := encodeControlCommand(arguments, false)
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("encodeControlCommand(%q) error = %v", arguments, err)
		}
		if arguments != nil && strings.Contains(err.Error(), "bad") {
			t.Fatalf("encodeControlCommand() disclosed rejected argument: %v", err)
		}
	}
}

func TestControlStreamParserSeparatesFramesAndNotifications(t *testing.T) {
	t.Parallel()

	parser := controlStreamParser{}
	notificationLine := []byte("%session-renamed $1 original name")
	frame, notification, err := parser.consume(notificationLine)
	if err != nil || frame != nil || !bytes.Equal(notification, notificationLine) {
		t.Fatalf("notification consume = (%#v, %q, %v)", frame, notification, err)
	}
	notificationLine[0] = 'X'
	if !bytes.Equal(notification, []byte("%session-renamed $1 original name")) {
		t.Fatalf("notification aliases input: %q", notification)
	}

	lines := [][]byte{
		[]byte("%begin 1363006971 2 1"),
		[]byte("first"),
		[]byte("%message payload inside frame"),
		[]byte("%begin 4 5 6"),
		[]byte("%end 1 2 3"),
		[]byte("%error malformed payload"),
		[]byte("%end 1363006971 2 1"),
	}
	for index, line := range lines {
		frame, notification, err = parser.consume(line)
		if err != nil {
			t.Fatalf("consume line %d error = %v", index, err)
		}
		if notification != nil {
			t.Fatalf("consume line %d notification = %q", index, notification)
		}
		if index != len(lines)-1 && frame != nil {
			t.Fatalf("consume line %d returned early frame %#v", index, frame)
		}
	}
	if frame == nil || frame.failed || frame.timestamp != 1363006971 ||
		frame.number != 2 || frame.flags != 1 ||
		!bytes.Equal(frame.rawStdout, []byte(
			"first\n%message payload inside frame\n%begin 4 5 6\n"+
				"%end 1 2 3\n%error malformed payload\n",
		)) {
		t.Fatalf("completed frame = %#v", frame)
	}
	frame.rawStdout[0] = 'X'
	if err := parser.finish(); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
}

func TestControlStreamParserReturnsErrorFrame(t *testing.T) {
	t.Parallel()

	parser := controlStreamParser{}
	for _, line := range []string{
		"%begin 20 9 1",
		"unknown command: sensitive-command",
		"%error 20 9 1",
	} {
		frame, _, err := parser.consume([]byte(line))
		if err != nil {
			t.Fatalf("consume(%q) error = %v", line, err)
		}
		if line == "%error 20 9 1" {
			if frame == nil || !frame.failed ||
				!bytes.Equal(frame.rawStdout, []byte("unknown command: sensitive-command\n")) {
				t.Fatalf("error frame = %#v", frame)
			}
		}
	}
}

func TestControlStreamParserRejectsMalformedFramingWithoutDisclosure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
		end   bool
	}{
		{name: "orphan end", lines: []string{"%end 1 2 3 sensitive"}},
		{name: "ordinary outside", lines: []string{"ordinary sensitive output"}},
		{name: "malformed begin", lines: []string{"%begin 1 two 3 sensitive"}},
		{name: "EOF inside frame", lines: []string{"%begin 1 2 3", "sensitive output"}, end: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := controlStreamParser{}
			var err error
			for _, line := range test.lines {
				_, _, err = parser.consume([]byte(line))
				if err != nil {
					break
				}
			}
			if test.end {
				err = parser.finish()
			}
			if !errors.Is(err, ErrControlProtocol) {
				t.Fatalf("protocol error = %v, want ErrControlProtocol", err)
			}
			var protocolError *ControlProtocolError
			if !errors.As(err, &protocolError) || protocolError.State == "" || protocolError.Reason == "" {
				t.Fatalf("protocol error = %#v, want state and reason", err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "sensitive") {
				t.Fatalf("protocol error disclosed input: %v", err)
			}
		})
	}
}

func TestControlCommandResultOwnsFrameData(t *testing.T) {
	t.Parallel()

	command := []string{"display-message", "value"}
	frame := controlFrame{
		timestamp: 9,
		number:    7,
		flags:     1,
		rawStdout: []byte("value\n"),
		failed:    true,
	}
	result := frame.result(command)
	command[0] = "mutated"
	frame.rawStdout[0] = 'X'
	if !slices.Equal(result.Command, []string{"display-message", "value"}) ||
		!bytes.Equal(result.RawStdout, []byte("value\n")) ||
		result.Timestamp != 9 || result.Number != 7 || result.Flags != 1 || !result.Failed {
		t.Fatalf("ControlCommandResult = %#v, want owned frame projection", result)
	}
}

// TestPaneContentCanCloseAFrameEarly pins the hazard that keeps a printed
// capture on a tmux process no matter which engine a handle selected. Control
// mode escapes %output and does not escape a command's output, so pane content
// is delivered verbatim and a line identical to the closing guard ends the
// frame. The cost is not a truncated read: tmux's real guard then arrives with
// no frame open, which fails the connection for every later command.
func TestPaneContentCanCloseAFrameEarly(t *testing.T) {
	t.Parallel()

	parser := controlStreamParser{}
	for _, line := range []string{"%begin 1363006971 2 1", "captured output"} {
		if _, _, err := parser.consume([]byte(line)); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
	}

	forged, _, err := parser.consume([]byte("%end 1363006971 2 1"))
	if err != nil || forged == nil {
		t.Fatalf("forged guard = (%#v, %v), want a closed frame", forged, err)
	}
	if got := string(forged.rawStdout); got != "captured output\n" {
		t.Fatalf("frame payload = %q, want the lines before the forged guard", got)
	}

	if _, _, err := parser.consume([]byte("%end 1363006971 2 1")); err == nil {
		t.Fatal("tmux's real guard after an early close must fail the connection")
	}
}
