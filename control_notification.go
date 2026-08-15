package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrMalformedControlNotification identifies a structurally invalid
	// control-mode notification.
	ErrMalformedControlNotification = errors.New("tmux: malformed control notification")
	// ErrUnknownControlNotification identifies a well-framed notification kind
	// outside the pinned control-mode vocabulary.
	ErrUnknownControlNotification = errors.New("tmux: unknown control notification")
)

// ControlNotificationError reports where a control-mode notification failed
// validation without retaining or disclosing the notification contents.
// Callers can use [errors.Is] with Category or [errors.As] to inspect its
// secret-safe location metadata. Category is [ErrMalformedControlNotification]
// or [ErrUnknownControlNotification].
type ControlNotificationError struct {
	// Offset is the byte offset of the malformed protocol element.
	Offset int
	// Reason describes the framing or vocabulary error without retaining input.
	Reason string
	// Category is ErrMalformedControlNotification or ErrUnknownControlNotification.
	Category error
}

// Error implements error.
func (e *ControlNotificationError) Error() string {
	return fmt.Sprintf("%v at byte %d: %s", e.Category, e.Offset, e.Reason)
}

// Unwrap makes ControlNotificationError compatible with its Category.
func (e *ControlNotificationError) Unwrap() error { return e.Category }

// ControlNotification is an immutable parsed tmux control-mode notification.
// Its zero value has an empty kind and no arguments. Values returned by
// [ParseControlNotification] own copied arguments and are safe to retain.
type ControlNotification struct {
	kind       ControlNotificationKind
	arguments  []string
	outputPane PaneID
	output     []byte
	hasOutput  bool
}

// Kind returns the notification's protocol kind.
func (n ControlNotification) Kind() ControlNotificationKind { return n.kind }

// Arguments returns an owned copy of the notification arguments. Arguments
// before a documented free-form tail are individual values; the tail is the
// final value and preserves its spacing and tmux escaping.
func (n ControlNotification) Arguments() []string { return slices.Clone(n.arguments) }

// Output returns the pane identity and decoded caller-owned bytes carried by
// an output or extended-output notification. Other notification kinds return
// zero values and false.
func (n ControlNotification) Output() (PaneID, []byte, bool) {
	if !n.hasOutput {
		return "", nil, false
	}
	return n.outputPane, bytes.Clone(n.output), true
}

// ParseControlNotification parses one newline-free tmux control-mode
// notification record. It requires exact control framing and a supported kind,
// returns errors compatible with [ErrMalformedControlNotification] or
// [ErrUnknownControlNotification], and copies all caller-owned input before
// returning. It never returns a partial notification.
func ParseControlNotification(line []byte) (ControlNotification, error) {
	if len(line) == 0 {
		return ControlNotification{}, malformedControlNotification(0, "record is empty")
	}
	if offset := bytes.IndexAny(line, "\x00\r\n"); offset >= 0 {
		return ControlNotification{}, malformedControlNotification(offset, "record contains framing byte")
	}
	if line[0] != '%' {
		return ControlNotification{}, malformedControlNotification(0, "record does not start with a notification kind")
	}

	record := string(line)
	kindEnd := strings.IndexByte(record, ' ')
	if kindEnd < 0 {
		kindEnd = len(record)
	}
	if offset, ok := validControlNotificationKind(record[:kindEnd]); !ok {
		return ControlNotification{}, malformedControlNotification(offset, "notification kind is malformed")
	}
	kind := ControlNotificationKind(record[:kindEnd])
	definition, ok := controlNotificationDefinition(kind)
	if !ok {
		return ControlNotification{}, &ControlNotificationError{
			Offset:   0,
			Reason:   "notification kind is not recognized",
			Category: ErrUnknownControlNotification,
		}
	}

	arguments, err := parseControlNotificationArguments(record, kindEnd, definition)
	if err != nil {
		return ControlNotification{}, err
	}
	notification := ControlNotification{kind: kind, arguments: arguments}
	if kind == ControlNotificationOutput || kind == ControlNotificationExtendedOutput {
		tail := arguments[len(arguments)-1]
		output, err := decodeControlOutput(tail, len(record)-len(tail))
		if err != nil {
			return ControlNotification{}, err
		}
		notification.outputPane = PaneID(arguments[0])
		notification.output = output
		notification.hasOutput = true
	}
	return notification, nil
}

func decodeControlOutput(value string, baseOffset int) ([]byte, error) {
	output := make([]byte, 0, len(value))
	for offset := 0; offset < len(value); {
		if value[offset] != '\\' {
			output = append(output, value[offset])
			offset++
			continue
		}
		if offset+4 > len(value) {
			return nil, malformedControlNotification(
				baseOffset+offset,
				"output escape is incomplete",
			)
		}
		decoded := 0
		for digitOffset := 1; digitOffset <= 3; digitOffset++ {
			digit := value[offset+digitOffset]
			if digit < '0' || digit > '7' {
				return nil, malformedControlNotification(
					baseOffset+offset+digitOffset,
					"output escape is not three-digit octal",
				)
			}
			decoded = decoded*8 + int(digit-'0')
		}
		if decoded > 0xff {
			return nil, malformedControlNotification(
				baseOffset+offset,
				"output escape exceeds one byte",
			)
		}
		output = append(output, byte(decoded))
		offset += 4
	}
	return output, nil
}

func validControlNotificationKind(kind string) (int, bool) {
	if len(kind) < 2 || kind[0] != '%' || kind[1] < 'a' || kind[1] > 'z' {
		return min(1, len(kind)), false
	}
	previousHyphen := false
	for offset := 2; offset < len(kind); offset++ {
		value := kind[offset]
		letter := value >= 'a' && value <= 'z'
		digit := value >= '0' && value <= '9'
		if letter || digit {
			previousHyphen = false
			continue
		}
		if value != '-' || previousHyphen {
			return offset, false
		}
		previousHyphen = true
	}
	if previousHyphen {
		return len(kind) - 1, false
	}
	return 0, true
}

func controlNotificationDefinition(
	kind ControlNotificationKind,
) (generatedControlNotificationDefinition, bool) {
	for _, definition := range generatedControlNotificationDefinitions {
		if definition.kind == kind {
			return definition, true
		}
	}
	return generatedControlNotificationDefinition{}, false
}

func parseControlNotificationArguments(
	record string,
	position int,
	definition generatedControlNotificationDefinition,
) ([]string, error) {
	arguments := make([]string, 0, definition.prefixArguments+1)
	for range definition.prefixArguments {
		if position == len(record) {
			return nil, malformedControlNotification(position, "required argument is missing")
		}
		if record[position] != ' ' {
			return nil, malformedControlNotification(position, "argument delimiter is missing")
		}
		position++
		if position == len(record) || record[position] == ' ' {
			return nil, malformedControlNotification(position, "required argument is empty")
		}
		end := strings.IndexByte(record[position:], ' ')
		if end < 0 {
			end = len(record)
		} else {
			end += position
		}
		arguments = append(arguments, record[position:end])
		position = end
	}

	switch definition.tail {
	case generatedControlNotificationTailNone:
		if position != len(record) {
			return nil, malformedControlNotification(position, "record has unexpected trailing data")
		}
	case generatedControlNotificationTailRequired:
		var err error
		arguments, err = appendControlNotificationTail(
			record,
			position,
			arguments,
			definition.allowEmptyTail,
			false,
		)
		if err != nil {
			return nil, err
		}
	case generatedControlNotificationTailOptional:
		if position != len(record) {
			var err error
			arguments, err = appendControlNotificationTail(
				record,
				position,
				arguments,
				definition.allowEmptyTail,
				true,
			)
			if err != nil {
				return nil, err
			}
		}
	case generatedControlNotificationTailColon:
		var err error
		arguments, err = appendColonControlNotificationTail(
			record,
			position,
			arguments,
			definition.allowEmptyTail,
		)
		if err != nil {
			return nil, err
		}
	default:
		return nil, malformedControlNotification(position, "notification definition is invalid")
	}
	return arguments, nil
}

func appendControlNotificationTail(
	record string,
	position int,
	arguments []string,
	allowEmpty bool,
	optional bool,
) ([]string, error) {
	if position == len(record) {
		if optional {
			return arguments, nil
		}
		return nil, malformedControlNotification(position, "required tail is missing")
	}
	if record[position] != ' ' {
		return nil, malformedControlNotification(position, "tail delimiter is missing")
	}
	tail := record[position+1:]
	if tail == "" && !allowEmpty {
		return nil, malformedControlNotification(position+1, "tail is empty")
	}
	return append(arguments, tail), nil
}

func appendColonControlNotificationTail(
	record string,
	position int,
	arguments []string,
	allowEmpty bool,
) ([]string, error) {
	separatorOffset := strings.Index(record[position:], " : ")
	if separatorOffset < 0 {
		return nil, malformedControlNotification(position, "colon tail delimiter is missing")
	}
	separator := position + separatorOffset
	if separator != position {
		futureArguments := record[position:separator]
		if futureArguments[0] != ' ' {
			return nil, malformedControlNotification(position, "argument delimiter is missing")
		}
		for _, argument := range strings.Split(futureArguments[1:], " ") {
			if argument == "" {
				return nil, malformedControlNotification(position, "future argument is empty")
			}
			arguments = append(arguments, argument)
		}
	}
	tail := record[separator+3:]
	if tail == "" && !allowEmpty {
		return nil, malformedControlNotification(separator+3, "tail is empty")
	}
	return append(arguments, tail), nil
}

func malformedControlNotification(offset int, reason string) error {
	return &ControlNotificationError{
		Offset:   offset,
		Reason:   reason,
		Category: ErrMalformedControlNotification,
	}
}
