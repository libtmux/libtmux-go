package mcp

import (
	"errors"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pane-text bounds preserve the newest output and report every truncation.
const (
	// defaultMaxLines applies when the caller omits a limit.
	defaultMaxLines = 500
	ceilingMaxLines = 5000
	// A line count alone does not bound joined or otherwise long lines.
	defaultMaxBytes = 128_000
	ceilingMaxBytes = 1_000_000
)

// truncation gives every bounded result the same loss report.
type truncation struct {
	Truncated bool `json:"truncated"`
	// TruncatedLines is how many lines were dropped from the start.
	TruncatedLines int `json:"truncatedLines,omitempty"`
	TruncatedBytes int `json:"truncatedBytes,omitempty"`
}

type bounds struct {
	lines int
	bytes int
}

// resolveBounds applies defaults for zero and clamps requests to the ceilings.
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

// apply keeps the newest text and truncates an oversized final line.
func (b bounds) apply(lines []string) ([]string, truncation) {
	// MCP schemas require an empty array rather than null.
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
		// encodedSize includes the removed separator.
		report.TruncatedBytes += len(kept[0]) + 1
		kept = kept[1:]
	}
	if len(kept) == 1 && len(kept[0]) > b.bytes {
		// Keep the tail and discard any partial leading UTF-8 rune.
		original := kept[0]
		tail := strings.ToValidUTF8(original[len(original)-b.bytes:], "")
		report.TruncatedBytes += len(original) - len(tail)
		kept = []string{tail}
	}

	report.Truncated = report.TruncatedLines > 0 || report.TruncatedBytes > 0
	return kept, report
}

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

// textResult exposes terminal output as text while retaining structured output.
func textResult(lines []string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(lines, "\n")}},
	}
}
