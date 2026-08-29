package mcp

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP stdio frames are newline-delimited. Pass blank or valid JSON-RPC lines
// and drop malformed ones so the SDK can resynchronize on the next frame.

// wholeJSONLines passes blank or valid JSON-RPC lines and reports malformed ones.
func wholeJSONLines(r io.ReadCloser, notify io.Writer) io.ReadCloser {
	return &jsonLineReader{lines: bufio.NewReader(r), source: r, notify: notify}
}

type jsonLineReader struct {
	lines   *bufio.Reader
	source  io.Closer
	notify  io.Writer
	pending []byte
}

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

func (r *jsonLineReader) Close() error { return r.source.Close() }

// decodable also rejects valid JSON that is not a JSON-RPC message.
func decodable(frame []byte) bool {
	_, err := jsonrpc.DecodeMessage(frame)
	return err == nil
}

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

func stdio() *mcp.IOTransport {
	return &mcp.IOTransport{
		Reader: wholeJSONLines(os.Stdin, os.Stderr),
		Writer: nopClose{os.Stdout},
	}
}

// nopClose keeps the SDK from closing this process's stdout.
type nopClose struct{ io.Writer }

func (nopClose) Close() error { return nil }
