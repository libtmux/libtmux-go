// Package termtext turns the bytes a terminal program writes into text a
// match can read.
package termtext

import (
	"unicode"
	"unicode/utf8"
)

type parseState uint8

const (
	stateGround parseState = iota
	stateEscape
	stateEscapeIntermediate
	stateCSI
	stateOSC
	stateOSCEscape
	stateControlString
	stateControlStringEscape
)

// Normalizer converts a terminal byte stream to plain text, chunk by chunk.
// Escape and control sequences are dropped, UTF-8 split across chunks is
// joined, and a carriage return or backspace that would overwrite text starts
// a new line instead, so nothing a program wrote is hidden from a match. The
// zero value is ready to use.
type Normalizer struct {
	state          parseState
	pendingUTF8    [utf8.UTFMax]byte
	pendingUTF8Len int
	afterCR        bool
}

// AppendChunk appends chunk's text to dst and returns the extended slice. A
// sequence or character that chunk ends partway through is completed by the
// next call.
func (n *Normalizer) AppendChunk(dst, chunk []byte) []byte {
	for n.pendingUTF8Len > 0 {
		for !utf8.FullRune(n.pendingUTF8[:n.pendingUTF8Len]) && len(chunk) > 0 {
			n.pendingUTF8[n.pendingUTF8Len] = chunk[0]
			n.pendingUTF8Len++
			chunk = chunk[1:]
		}
		if !utf8.FullRune(n.pendingUTF8[:n.pendingUTF8Len]) {
			return dst
		}
		decoded, size := utf8.DecodeRune(n.pendingUTF8[:n.pendingUTF8Len])
		if size == 1 {
			dst = n.appendByte(dst, n.pendingUTF8[0])
		} else {
			dst = n.appendRune(dst, decoded, n.pendingUTF8[:size])
		}
		copy(n.pendingUTF8[:], n.pendingUTF8[size:n.pendingUTF8Len])
		n.pendingUTF8Len -= size
	}

	for len(chunk) > 0 {
		if chunk[0] < utf8.RuneSelf {
			dst = n.appendByte(dst, chunk[0])
			chunk = chunk[1:]
			continue
		}
		if !utf8.FullRune(chunk) {
			n.pendingUTF8Len = copy(n.pendingUTF8[:], chunk)
			return dst
		}
		decoded, size := utf8.DecodeRune(chunk)
		if decoded == utf8.RuneError && size == 1 {
			dst = n.appendByte(dst, chunk[0])
		} else {
			dst = n.appendRune(dst, decoded, chunk[:size])
		}
		chunk = chunk[size:]
	}
	return dst
}

func (n *Normalizer) appendByte(dst []byte, value byte) []byte {
	if value >= 0x80 {
		if value <= 0x9f {
			n.appendC1(rune(value))
		}
		return dst
	}

	switch n.state {
	case stateGround:
	case stateEscape:
		switch {
		case value == 0x1b:
		case value == '[':
			n.state = stateCSI
		case value == ']':
			n.state = stateOSC
		case value == 'P', value == 'X', value == '^', value == '_':
			n.state = stateControlString
		case value >= 0x20 && value <= 0x2f:
			n.state = stateEscapeIntermediate
		case value >= 0x30 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = stateGround
		}
		return dst
	case stateEscapeIntermediate:
		switch {
		case value == 0x1b:
			n.state = stateEscape
		case value >= 0x30 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = stateGround
		}
		return dst
	case stateCSI:
		switch {
		case value == 0x1b:
			n.state = stateEscape
		case value >= 0x40 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = stateGround
		}
		return dst
	case stateOSC:
		switch value {
		case 0x07, 0x18, 0x1a:
			n.state = stateGround
		case 0x1b:
			n.state = stateOSCEscape
		}
		return dst
	case stateOSCEscape:
		switch value {
		case 0x07, 0x18, 0x1a, '\\':
			n.state = stateGround
		case 0x1b:
		default:
			n.state = stateOSC
		}
		return dst
	case stateControlString:
		switch value {
		case 0x18, 0x1a:
			n.state = stateGround
		case 0x1b:
			n.state = stateControlStringEscape
		}
		return dst
	case stateControlStringEscape:
		switch value {
		case 0x18, 0x1a, '\\':
			n.state = stateGround
		case 0x1b:
		default:
			n.state = stateControlString
		}
		return dst
	}

	switch value {
	case '\r':
		n.afterCR = true
		return append(dst, '\n')
	case '\n':
		if n.afterCR {
			n.afterCR = false
			return dst
		}
		return append(dst, '\n')
	case '\b':
		n.afterCR = false
		return append(dst, '\n')
	case '\t':
		n.afterCR = false
		return append(dst, value)
	case 0x1b:
		n.state = stateEscape
		return dst
	}
	if value >= 0x20 && value < 0x7f {
		n.afterCR = false
		return append(dst, value)
	}
	return dst
}

func (n *Normalizer) appendRune(dst []byte, decoded rune, encoded []byte) []byte {
	if decoded >= 0x80 && decoded <= 0x9f {
		n.appendC1(decoded)
		return dst
	}
	switch n.state {
	case stateGround, stateEscape,
		stateEscapeIntermediate, stateCSI:
	case stateOSCEscape:
		n.state = stateOSC
		return dst
	case stateControlStringEscape:
		n.state = stateControlString
		return dst
	case stateOSC, stateControlString:
		return dst
	}
	if n.state != stateGround {
		n.state = stateGround
	}
	if unicode.IsPrint(decoded) {
		n.afterCR = false
		return append(dst, encoded...)
	}
	return dst
}

func (n *Normalizer) appendC1(value rune) {
	if n.inControlString() {
		if value == 0x9c {
			n.state = stateGround
		} else if n.state == stateOSCEscape {
			n.state = stateOSC
		} else if n.state == stateControlStringEscape {
			n.state = stateControlString
		}
		return
	}
	switch value {
	case 0x9b:
		n.state = stateCSI
	case 0x9d:
		n.state = stateOSC
	case 0x90, 0x98, 0x9e, 0x9f:
		n.state = stateControlString
	default:
		n.state = stateGround
	}
}

func (n *Normalizer) inControlString() bool {
	return n.state == stateOSC ||
		n.state == stateOSCEscape ||
		n.state == stateControlString ||
		n.state == stateControlStringEscape
}
