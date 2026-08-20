package mcp

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// One frame that will not parse should not cost the session.
//
// MCP frames a stdio connection with newlines and forbids a newline inside a
// message, so a frame the decoder rejects is exactly one line and the next
// line is a fresh one. The SDK decodes the stream rather than the line: a
// syntax error leaves its decoder mid-token with nothing to resync on, and it
// is returned up through the read loop, ending the process. One malformed
// frame -- a client bug, a stray write to the pipe -- took every tmux tool the
// conversation had with it.
//
// Skipping the error after the fact does not help, because the decoder cannot
// find the next frame either; the line has to be dropped before the decoder
// sees it. So the reader below hands on only whole lines that parse as JSON,
// which is what keeps the decoder in sync.
//
// The dropped line is reported on stderr rather than answered with a JSON-RPC
// parse error: an unparsable frame carries no id to answer, and a reply to
// nobody is harder to notice than a line in the log a client already shows.

// wholeJSONLines passes on the lines of r that parse as JSON and drops the
// rest, reporting each drop to notify.
func wholeJSONLines(r io.ReadCloser, notify io.Writer) io.ReadCloser {
	return &jsonLineReader{lines: bufio.NewReader(r), source: r, notify: notify}
}

type jsonLineReader struct {
	lines   *bufio.Reader
	source  io.Closer
	notify  io.Writer
	pending []byte
}

// Read serves the current line, fetching lines until one parses.
func (r *jsonLineReader) Read(into []byte) (int, error) {
	for len(r.pending) == 0 {
		// ReadString rather than a Scanner: a scanner caps a token, and a
		// capture of a wide pane's scrollback is a legitimately long frame.
		line, err := r.lines.ReadString('\n')
		if len(line) > 0 {
			if trimmed := trimFrame(line); len(trimmed) == 0 || decodable(trimmed) {
				r.pending = []byte(line)
			} else {
				_, _ = fmt.Fprintf(r.notify,
					"libtmux-mcp: ignoring a frame that is not a JSON-RPC "+
						"message (%d bytes)\n", len(trimmed))
			}
		}
		if err != nil {
			if len(r.pending) > 0 {
				break
			}
			return 0, err
		}
	}
	copied := copy(into, r.pending)
	r.pending = r.pending[copied:]
	return copied, nil
}

// Close closes the wrapped reader.
func (r *jsonLineReader) Close() error { return r.source.Close() }

// decodable reports whether a frame is one the decoder downstream will accept.
//
// Valid JSON is not enough: "{}" parses and is not a JSON-RPC message, and the
// decoder ends the read loop on it exactly as it does on a syntax error. The
// test here is the one it will apply.
func decodable(frame []byte) bool {
	_, err := jsonrpc.DecodeMessage(frame)
	return err == nil
}

// trimFrame drops the line ending and any surrounding space, so a blank line
// stays blank rather than becoming a frame that will not parse.
func trimFrame(line string) []byte {
	end := len(line)
	for end > 0 && (line[end-1] == '\n' || line[end-1] == '\r' || line[end-1] == ' ' ||
		line[end-1] == '\t') {
		end--
	}
	start := 0
	for start < end && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	return []byte(line[start:end])
}

// stdio is the transport Run serves on: the SDK's own, reading through the
// filter above so a malformed frame is dropped rather than fatal.
func stdio() *mcp.IOTransport {
	return &mcp.IOTransport{
		Reader: wholeJSONLines(os.Stdin, os.Stderr),
		Writer: nopClose{os.Stdout},
	}
}

// nopClose keeps the SDK from closing this process's stdout.
type nopClose struct{ io.Writer }

func (nopClose) Close() error { return nil }
