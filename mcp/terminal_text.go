package mcp

import (
	"unicode"
	"unicode/utf8"
)

type terminalTextState uint8

const (
	terminalTextGround terminalTextState = iota
	terminalTextEscape
	terminalTextEscapeIntermediate
	terminalTextCSI
	terminalTextOSC
	terminalTextOSCEscape
	terminalTextControlString
	terminalTextControlStringEscape
)

type terminalTextNormalizer struct {
	state          terminalTextState
	pendingUTF8    [utf8.UTFMax]byte
	pendingUTF8Len int
	afterCR        bool
}

func (n *terminalTextNormalizer) appendChunk(dst, chunk []byte) []byte {
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

func (n *terminalTextNormalizer) appendByte(dst []byte, value byte) []byte {
	if value >= 0x80 {
		if value <= 0x9f {
			n.appendC1(rune(value))
		}
		return dst
	}

	switch n.state {
	case terminalTextEscape:
		switch {
		case value == 0x1b:
		case value == '[':
			n.state = terminalTextCSI
		case value == ']':
			n.state = terminalTextOSC
		case value == 'P', value == 'X', value == '^', value == '_':
			n.state = terminalTextControlString
		case value >= 0x20 && value <= 0x2f:
			n.state = terminalTextEscapeIntermediate
		case value >= 0x30 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = terminalTextGround
		}
		return dst
	case terminalTextEscapeIntermediate:
		switch {
		case value == 0x1b:
			n.state = terminalTextEscape
		case value >= 0x30 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = terminalTextGround
		}
		return dst
	case terminalTextCSI:
		switch {
		case value == 0x1b:
			n.state = terminalTextEscape
		case value >= 0x40 && value <= 0x7e,
			value == 0x18, value == 0x1a:
			n.state = terminalTextGround
		}
		return dst
	case terminalTextOSC:
		switch value {
		case 0x07, 0x18, 0x1a:
			n.state = terminalTextGround
		case 0x1b:
			n.state = terminalTextOSCEscape
		}
		return dst
	case terminalTextOSCEscape:
		switch value {
		case 0x07, 0x18, 0x1a, '\\':
			n.state = terminalTextGround
		case 0x1b:
		default:
			n.state = terminalTextOSC
		}
		return dst
	case terminalTextControlString:
		switch value {
		case 0x18, 0x1a:
			n.state = terminalTextGround
		case 0x1b:
			n.state = terminalTextControlStringEscape
		}
		return dst
	case terminalTextControlStringEscape:
		switch value {
		case 0x18, 0x1a, '\\':
			n.state = terminalTextGround
		case 0x1b:
		default:
			n.state = terminalTextControlString
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
		n.state = terminalTextEscape
		return dst
	}
	if value >= 0x20 && value < 0x7f {
		n.afterCR = false
		return append(dst, value)
	}
	return dst
}

func (n *terminalTextNormalizer) appendRune(dst []byte, decoded rune, encoded []byte) []byte {
	if decoded >= 0x80 && decoded <= 0x9f {
		n.appendC1(decoded)
		return dst
	}
	switch n.state {
	case terminalTextOSCEscape:
		n.state = terminalTextOSC
		return dst
	case terminalTextControlStringEscape:
		n.state = terminalTextControlString
		return dst
	case terminalTextOSC, terminalTextControlString:
		return dst
	}
	if n.state != terminalTextGround {
		n.state = terminalTextGround
	}
	if unicode.IsPrint(decoded) {
		n.afterCR = false
		return append(dst, encoded...)
	}
	return dst
}

func (n *terminalTextNormalizer) appendC1(value rune) {
	if n.inControlString() {
		if value == 0x9c {
			n.state = terminalTextGround
		} else if n.state == terminalTextOSCEscape {
			n.state = terminalTextOSC
		} else if n.state == terminalTextControlStringEscape {
			n.state = terminalTextControlString
		}
		return
	}
	switch value {
	case 0x9b:
		n.state = terminalTextCSI
	case 0x9d:
		n.state = terminalTextOSC
	case 0x90, 0x98, 0x9e, 0x9f:
		n.state = terminalTextControlString
	default:
		n.state = terminalTextGround
	}
}

func (n *terminalTextNormalizer) inControlString() bool {
	return n.state == terminalTextOSC ||
		n.state == terminalTextOSCEscape ||
		n.state == terminalTextControlString ||
		n.state == terminalTextControlStringEscape
}
