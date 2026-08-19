package mcp

import (
	"errors"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Text a tool returns is bounded, because the caller reading it has a context
// window and a pane has a scrollback.
//
// A pane's history is commonly tens of thousands of lines. A tool that returns
// all of it on request spends the caller's whole context on one answer, and
// the caller cannot tell that is about to happen before it asks: the size is
// the pane's, not the request's. So every tool that returns pane text caps
// what it sends and says what it dropped.
//
// The cap has a ceiling rather than an off switch. A caller that wants
// everything wants it because it does not know how much there is, which is the
// case the ceiling exists for; a request above it is honored as far as the
// ceiling allows and reported, rather than refused. Reading past the ceiling
// is what capture_since is for, which returns what is new instead of what
// there is.
//
// The tail is kept rather than the head. What a pane most recently wrote is
// what a caller asking about it wants, and a command's error is at the end.
const (
	// defaultMaxLines is what a tool returns when a caller does not choose.
	// Roughly two screens, which is enough to see a command and its output.
	defaultMaxLines = 500
	// ceilingMaxLines bounds what a caller may ask for.
	ceilingMaxLines = 5000
	// defaultMaxBytes bounds a reply whose lines are long. A pane joining
	// wrapped lines can produce single lines of many kilobytes, so a line
	// count alone does not bound the reply.
	defaultMaxBytes = 128_000
	// ceilingMaxBytes bounds what a caller may ask for.
	ceilingMaxBytes = 1_000_000
)

// truncation reports what a bound removed, so a caller can tell a short answer
// from a shortened one.
//
// It is embedded in the outputs of the tools that return text, which flattens
// its fields into their schemas, so every one of them reports truncation the
// same way and a caller learns the shape once.
type truncation struct {
	// Truncated reports that something was dropped to fit the bounds.
	Truncated bool `json:"truncated"`
	// TruncatedLines is how many lines were dropped from the start.
	TruncatedLines int `json:"truncatedLines,omitempty"`
	// TruncatedBytes is how many bytes were dropped with them.
	TruncatedBytes int `json:"truncatedBytes,omitempty"`
}

// bounds is a resolved pair of caps, built from what a caller asked for.
type bounds struct {
	lines int
	bytes int
}

// resolveBounds turns a caller's request into caps this server will honor.
//
// Zero means the default rather than none, because zero is what a client sends
// for a field it did not fill in, and reading that as "no limit" would make
// the unbounded reply the one a caller gets by accident.
func resolveBounds(maxLines, maxBytes int) (bounds, error) {
	if maxLines < 0 || maxBytes < 0 {
		return bounds{}, errors.New("maxLines and maxBytes must not be negative")
	}
	resolved := bounds{lines: maxLines, bytes: maxBytes}
	if resolved.lines == 0 {
		resolved.lines = defaultMaxLines
	}
	if resolved.bytes == 0 {
		resolved.bytes = defaultMaxBytes
	}
	if resolved.lines > ceilingMaxLines {
		resolved.lines = ceilingMaxLines
	}
	if resolved.bytes > ceilingMaxBytes {
		resolved.bytes = ceilingMaxBytes
	}
	return resolved, nil
}

// apply keeps the tail of lines that fits, and reports what it dropped.
//
// The line cap is applied first because it is cheap, then the byte cap, which
// has to measure. A single line longer than the byte cap is cut rather than
// dropped: a caller that asked for one line and got none would read the empty
// answer as an empty pane.
func (b bounds) apply(lines []string) ([]string, truncation) {
	// An empty list rather than no list. A pane that wrote nothing is the
	// ordinary case for capture_since, and the SDK validates a reply against
	// the schema it generated, where a nil slice is null and not an array.
	kept := lines
	if kept == nil {
		kept = []string{}
	}
	var report truncation

	if len(kept) > b.lines {
		dropped := kept[:len(kept)-b.lines]
		kept = kept[len(kept)-b.lines:]
		report.TruncatedLines += len(dropped)
		report.TruncatedBytes += encodedSize(dropped)
	}

	for len(kept) > 1 && encodedSize(kept) > b.bytes {
		report.TruncatedLines++
		// The newline that joined it to the next line goes too, which is what
		// encodedSize counts and so what has to be subtracted here.
		report.TruncatedBytes += len(kept[0]) + 1
		kept = kept[1:]
	}
	if len(kept) == 1 && len(kept[0]) > b.bytes {
		// Cut from the front, keeping the end, for the same reason the tail of
		// a pane is kept: what was written last is what was asked about. The
		// cut lands on a byte rather than a rune boundary, so the leading
		// partial rune is trimmed rather than shipped as invalid UTF-8.
		report.TruncatedBytes += len(kept[0]) - b.bytes
		kept = []string{strings.ToValidUTF8(kept[0][len(kept[0])-b.bytes:], "")}
	}

	report.Truncated = report.TruncatedLines > 0 || report.TruncatedBytes > 0
	return kept, report
}

// encodedSize is how many bytes the lines occupy once joined, which is the
// size that reaches the caller rather than the size of the strings alone.
func encodedSize(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	size := len(lines) - 1
	for _, line := range lines {
		size += len(line)
	}
	return size
}

// textResult carries pane text as text as well as structured output.
//
// The SDK renders a structured result as JSON and puts that in the content
// block, so a pane's contents would reach a model as a quoted array of quoted
// strings. That is the same text with escapes over it and one line's relation
// to the next hidden behind a comma. A model reads a terminal better as a
// terminal, so the lines are joined into a text block and the structured
// output is left for a client parsing fields.
func textResult(lines []string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(lines, "\n")}},
	}
}
