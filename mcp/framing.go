package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errJSONRPCBatchUnsupported = errors.New("libtmux MCP: JSON-RPC batches are unsupported")

const (
	jsonRPCFrameMaxBytes     = 8 * 1024 * 1024
	jsonRPCRequestIDMaxBytes = 512 * 1024
)

// wholeJSONLines passes blank or valid JSON-RPC lines and reports malformed ones.
func wholeJSONLines(r io.ReadCloser, notify io.Writer) io.ReadCloser {
	return &jsonLineReader{lines: bufio.NewReader(r), source: r, notify: notify}
}

// jsonLineTransport serializes SDK replies with pre-dispatch framing errors.
func jsonLineTransport(
	reader io.ReadCloser,
	writer io.WriteCloser,
	notify io.Writer,
) *mcp.IOTransport {
	serialized := &serializedWriteCloser{writer: writer}
	return &mcp.IOTransport{
		Reader: &jsonLineReader{
			lines: bufio.NewReader(reader), source: reader, notify: notify,
			reply: serialized,
		},
		Writer: serialized,
	}
}

type jsonLineReader struct {
	lines   *bufio.Reader
	source  io.Closer
	notify  io.Writer
	reply   io.Writer
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
				if oversizedRequestID(trimmed) && r.reply != nil {
					if err := writeOversizedRequestID(r.reply); err != nil {
						return 0, err
					}
					continue
				}
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

func oversizedRequestID(frame []byte) bool {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(frame, &request); err != nil || request.Method == "" {
		return false
	}
	return len(bytes.TrimSpace(request.ID)) > jsonRPCRequestIDMaxBytes
}

func writeOversizedRequestID(writer io.Writer) error {
	_, err := fmt.Fprintf(writer,
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":`+
			`"request id exceeds %d bytes"}}`+"\n",
		jsonRPCRequestIDMaxBytes,
	)
	return err
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
	return jsonLineTransport(os.Stdin, nopClose{os.Stdout}, os.Stderr)
}

// nopClose keeps the SDK from closing this process's stdout.
type nopClose struct{ io.Writer }

func (nopClose) Close() error { return nil }

type serializedWriteCloser struct {
	mutex  sync.Mutex
	writer io.WriteCloser
}

func (w *serializedWriteCloser) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Write(data)
}

func (w *serializedWriteCloser) Close() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Close()
}
