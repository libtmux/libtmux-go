// Package layout checks tmux's serialized layout grammar without applying geometry.
package layout

import "strconv"

// Cells returns the number of leaves in a checksummed layout tree. It rejects
// malformed trees, integers outside uint32, and more than 256 nested parent
// cells. tmux owns geometry and removal of excess leaves when a window contains
// fewer panes.
func Cells(value string) (int, bool) {
	if len(value) < 6 || value[4] != ',' {
		return 0, false
	}
	for _, c := range value[:4] {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return 0, false
		}
	}
	want, err := strconv.ParseUint(value[:4], 16, 16)
	if err != nil {
		return 0, false
	}
	var checksum uint16
	for i := 5; i < len(value); i++ {
		checksum = checksum>>1 | checksum<<15
		checksum += uint16(value[i])
	}
	if checksum != uint16(want) {
		return 0, false
	}
	p := parser{text: value, offset: 5}
	cells, ok := p.cell(0)
	return cells, ok && p.offset == len(value)
}

type parser struct {
	text   string
	offset int
}

func (p *parser) take(c byte) bool {
	if p.offset == len(p.text) || p.text[p.offset] != c {
		return false
	}
	p.offset++
	return true
}

func (p *parser) number() bool {
	start := p.offset
	for p.offset < len(p.text) && p.text[p.offset] >= '0' && p.text[p.offset] <= '9' {
		p.offset++
	}
	if p.offset == start {
		return false
	}
	_, err := strconv.ParseUint(p.text[start:p.offset], 10, 32)
	return err == nil
}

func (p *parser) cell(depth int) (int, bool) {
	if depth > 256 || !p.number() || !p.take('x') || !p.number() || !p.take(',') || !p.number() || !p.take(',') || !p.number() {
		return 0, false
	}
	if start := p.offset; p.take(',') {
		if !p.number() {
			return 0, false
		}
		// A following width belongs to the sibling cell, not a pane ID.
		if p.offset < len(p.text) && p.text[p.offset] == 'x' {
			p.offset = start
		}
	}
	var closing byte
	switch {
	case p.take('{'):
		closing = '}'
	case p.take('['):
		closing = ']'
	default:
		return 1, true
	}
	cells := 0
	for {
		children, ok := p.cell(depth + 1)
		if !ok {
			return 0, false
		}
		cells += children
		if !p.take(',') {
			return cells, p.take(closing)
		}
	}
}
