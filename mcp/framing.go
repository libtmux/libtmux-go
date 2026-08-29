package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errJSONRPCBatchUnsupported = errors.New("libtmux MCP: JSON-RPC batches are unsupported")

const jsonRPCFrameMaxBytes = 8 * 1024 * 1024

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
		line, oversized, err := r.readLine()
		if oversized {
			_, _ = fmt.Fprintf(r.notify,
				"libtmux-mcp: ignoring a JSON-RPC frame past %d bytes\n",
				jsonRPCFrameMaxBytes)
		} else if len(line) > 0 {
			trimmed := trimFrame(line)
			if isJSONRPCBatch(trimmed) {
				return 0, errJSONRPCBatchUnsupported
			}
			if len(trimmed) == 0 || decodable(trimmed) {
				r.pending = line
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

func (r *jsonLineReader) readLine() (line []byte, oversized bool, err error) {
	for {
		fragment, readErr := r.lines.ReadSlice('\n')
		if !oversized && len(line)+len(fragment) <= jsonRPCFrameMaxBytes {
			line = append(line, fragment...)
		} else {
			line = nil
			oversized = true
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		return line, oversized, readErr
	}
}

func (r *jsonLineReader) Close() error { return r.source.Close() }

// decodable also rejects valid JSON that is not a JSON-RPC message.
func decodable(frame []byte) bool {
	_, err := jsonrpc.DecodeMessage(frame)
	return err == nil
}

func isJSONRPCBatch(frame []byte) bool {
	if len(frame) == 0 || frame[0] != '[' {
		return false
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(frame, &messages); err != nil || len(messages) == 0 {
		return false
	}
	for _, message := range messages {
		if !decodable(message) {
			return false
		}
	}
	return true
}

func trimFrame(line []byte) []byte {
	end := len(line)
	for end > 0 && (line[end-1] == '\n' || line[end-1] == '\r' || line[end-1] == ' ' ||
		line[end-1] == '\t') {
		end--
	}
	start := 0
	for start < end && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	return line[start:end]
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
