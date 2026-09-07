package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

const expectedServerName = "libtmux"

// preflightTimeout includes cold compilation or module download.
const preflightTimeout = 180 * time.Second

const (
	preflightStdoutLimit       = 1 << 20
	preflightStderrLimit       = 64 << 10
	preflightTerminateDuration = time.Second
	preflightWaitDelay         = 250 * time.Millisecond
)

type processSpec struct {
	command string
	args    []string
	env     map[string]string
}

func processSpecFromEntry(entry map[string]any) (processSpec, error) {
	command, ok := entry["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return processSpec{}, errors.New("command is empty or not a string")
	}
	arguments := []string{}
	if raw, found := entry["args"]; found {
		list, ok := raw.([]any)
		if !ok {
			return processSpec{}, errors.New("arguments are not an array")
		}
		for _, rawArgument := range list {
			argument, ok := rawArgument.(string)
			if !ok {
				return processSpec{}, errors.New("argument is not a string")
			}
			arguments = append(arguments, argument)
		}
	}
	environment := map[string]string{}
	if raw, found := entry["env"]; found {
		values, ok := raw.(map[string]any)
		if !ok {
			return processSpec{}, errors.New("environment is not an object")
		}
		for name, rawValue := range values {
			value, ok := rawValue.(string)
			if !ok {
				return processSpec{}, fmt.Errorf("environment value %q is not a string", name)
			}
			environment[name] = value
		}
	}
	return processSpec{command: command, args: arguments, env: environment}, nil
}

func (s processSpec) equal(other processSpec) bool {
	return s.command == other.command &&
		slices.Equal(s.args, other.args) && maps.Equal(s.env, other.env)
}

func (s processSpec) describe() string {
	return strings.Join(append([]string{s.command}, s.args...), " ")
}

// preflight normalizes a raw entry for focused transport tests.
func preflight(entry map[string]any) string {
	return preflightWithin(entry, preflightTimeout)
}

// preflightSpec completes the same initialize, initialized, and ping exchange
// as a real MCP client before it closes the server's input.
func preflightSpec(spec processSpec) string {
	return preflightSpecWithin(spec, preflightTimeout)
}

func preflightWithin(entry map[string]any, timeout time.Duration) string {
	spec, err := processSpecFromEntry(entry)
	if err != nil {
		return fmt.Sprintf("invalid process entry: %v", err)
	}
	return preflightSpecWithin(spec, timeout)
}

func preflightSpecWithin(spec processSpec, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return preflightContext(ctx, spec)
}

func preflightContext(ctx context.Context, spec processSpec) (reason string) {
	process := exec.CommandContext(ctx, spec.command, spec.args...)
	process.Env = mergedEnvironment(os.Environ(), spec.env)
	cleanupProcess := ownPreflightProcess(process)
	stdin, err := process.StdinPipe()
	if err != nil {
		return preflightFailure("open MCP stdin", err, newBoundedTail(preflightStderrLimit))
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return preflightFailure("open MCP stdout", err, newBoundedTail(preflightStderrLimit))
	}
	stderr, err := process.StderrPipe()
	if err != nil {
		return preflightFailure("open MCP stderr", err, newBoundedTail(preflightStderrLimit))
	}
	if err := process.Start(); err != nil {
		return fmt.Sprintf("could not launch %s: %v", spec.command, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- process.Wait() }()
	monitor := newPreflightMonitor(stdout, stderr)
	succeeded := false
	defer func() {
		cleanupErr := finishPreflight(stdin, wait, cleanupProcess, succeeded)
		monitor.stop()
		if cleanupErr == nil {
			return
		}
		failure := fmt.Sprintf("terminate MCP process group: %v", cleanupErr)
		if reason == "" {
			reason = failure
		} else {
			reason += "\n" + failure
		}
	}()

	initialize := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name": "mcp-swap-preflight", "version": "1",
			},
		},
	}
	if err := writePreflightMessage(stdin, initialize); err != nil {
		return preflightFailure("write MCP initialize", err, monitor.complaints)
	}
	message, err := monitor.next(ctx)
	if err != nil {
		return preflightFailure("initialize MCP server", err, monitor.complaints)
	}
	if err := validateInitializeResponse(message); err != nil {
		return preflightFailure("initialize MCP server", err, monitor.complaints)
	}
	if err := writePreflightMessage(stdin, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); err != nil {
		return preflightFailure("write MCP initialized notification", err, monitor.complaints)
	}
	if err := writePreflightMessage(stdin, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "ping",
	}); err != nil {
		return preflightFailure("write MCP ping", err, monitor.complaints)
	}
	message, err = monitor.next(ctx)
	if err != nil {
		return preflightFailure("ping MCP server", err, monitor.complaints)
	}
	if _, err := validateResponse(message, 2); err != nil {
		return preflightFailure("ping MCP server", err, monitor.complaints)
	}
	succeeded = true
	return ""
}

type preflightEvent struct {
	stream string
	data   []byte
	err    error
	eof    bool
}

type preflightMonitor struct {
	events     chan preflightEvent
	done       chan struct{}
	stopOnce   sync.Once
	stdout     []byte
	complaints *boundedTail
}

func newPreflightMonitor(stdout, stderr io.Reader) *preflightMonitor {
	monitor := &preflightMonitor{
		events: make(chan preflightEvent, 16), done: make(chan struct{}),
		complaints: newBoundedTail(preflightStderrLimit),
	}
	go readPreflightStream(stdout, "stdout", preflightStdoutLimit, monitor.events, monitor.done)
	go readPreflightStream(stderr, "stderr", preflightStderrLimit, monitor.events, monitor.done)
	return monitor
}

func (m *preflightMonitor) stop() { m.stopOnce.Do(func() { close(m.done) }) }

func readPreflightStream(
	reader io.Reader,
	name string,
	limit int,
	events chan<- preflightEvent,
	done <-chan struct{},
) {
	send := func(event preflightEvent) bool {
		select {
		case events <- event:
			return true
		case <-done:
			return false
		}
	}
	total := 0
	for {
		buffer := make([]byte, 8<<10)
		count, err := reader.Read(buffer)
		if count > 0 {
			total += count
			if total > limit {
				send(preflightEvent{stream: name, err: fmt.Errorf("MCP %s exceeds %d bytes", name, limit)})
				return
			}
			if !send(preflightEvent{stream: name, data: buffer[:count]}) {
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				send(preflightEvent{stream: name, eof: true})
			} else {
				send(preflightEvent{stream: name, err: err})
			}
			return
		}
	}
}

func (m *preflightMonitor) next(ctx context.Context) ([]byte, error) {
	for {
		if newline := bytesIndexByte(m.stdout, '\n'); newline >= 0 {
			line := append([]byte(nil), m.stdout[:newline]...)
			m.stdout = append(m.stdout[:0], m.stdout[newline+1:]...)
			return line, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event := <-m.events:
			if event.err != nil {
				return nil, event.err
			}
			if event.stream == "stderr" {
				_, _ = m.complaints.Write(event.data)
				continue
			}
			if len(event.data) != 0 {
				m.stdout = append(m.stdout, event.data...)
			}
			if event.eof {
				if len(m.stdout) != 0 {
					line := append([]byte(nil), m.stdout...)
					m.stdout = nil
					return line, nil
				}
				return nil, io.EOF
			}
		}
	}
}

func bytesIndexByte(contents []byte, value byte) int {
	for index, candidate := range contents {
		if candidate == value {
			return index
		}
	}
	return -1
}

func writePreflightMessage(writer io.Writer, value any) error {
	return json.NewEncoder(writer).Encode(value)
}

func validateInitializeResponse(message []byte) error {
	result, err := validateResponse(message, 1)
	if err != nil {
		return err
	}
	var initialized struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	switch {
	case strings.TrimSpace(initialized.ProtocolVersion) == "":
		return errors.New("server returned no protocol version")
	case !jsonObject(initialized.Capabilities):
		return errors.New("server returned no capabilities")
	case strings.TrimSpace(initialized.ServerInfo.Name) == "":
		return errors.New("server returned no server information")
	case initialized.ServerInfo.Name != expectedServerName:
		return fmt.Errorf("server identified itself as %q, want %q",
			initialized.ServerInfo.Name, expectedServerName)
	case strings.TrimSpace(initialized.ServerInfo.Version) == "":
		return errors.New("server reported no version")
	}
	return nil
}

func validateResponse(message []byte, id int) (json.RawMessage, error) {
	if !json.Valid(message) {
		return nil, errors.New("server returned malformed JSON")
	}
	if err := rejectDuplicateJSONNames(message); err != nil {
		return nil, fmt.Errorf("server returned ambiguous JSON: %w", err)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(message, &response); err != nil {
		return nil, err
	}
	if response.JSONRPC != "2.0" {
		return nil, errors.New("server returned a non-2.0 JSON-RPC response")
	}
	if string(response.ID) != fmt.Sprint(id) {
		return nil, fmt.Errorf("server returned response id %s, want %d", response.ID, id)
	}
	if len(response.Error) != 0 && string(response.Error) != "null" {
		return nil, errors.New("server returned a JSON-RPC error")
	}
	if !jsonObject(response.Result) {
		return nil, errors.New("server returned no result object")
	}
	return response.Result, nil
}

func jsonObject(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func finishPreflight(
	stdin io.Closer,
	wait <-chan error,
	kill func() error,
	graceful bool,
) error {
	closeErr := stdin.Close()
	if errors.Is(closeErr, os.ErrClosed) {
		// The deadline already tore the pipes down. Reporting that as part of
		// the failure buries the reason the caller needs under plumbing.
		closeErr = nil
	}
	waited := false
	if graceful {
		select {
		case <-wait:
			waited = true
		case <-time.After(preflightWaitDelay):
		}
	}
	killErr := kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	if !waited {
		select {
		case <-wait:
		case <-time.After(preflightTerminateDuration):
			killErr = errors.Join(killErr, errors.New("MCP process did not reap"))
		}
	}
	return errors.Join(closeErr, killErr)
}

func preflightFailure(action string, err error, complaints *boundedTail) string {
	reason := fmt.Sprintf("%s: %v", action, err)
	if tail := strings.TrimSpace(complaints.String()); tail != "" {
		return reason + "\n" + tail
	}
	return reason
}

type boundedTail struct {
	mu       sync.Mutex
	limit    int
	contents []byte
}

func newBoundedTail(limit int) *boundedTail {
	return &boundedTail{limit: limit}
}

func (b *boundedTail) Write(contents []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(contents)
	if written >= b.limit {
		b.contents = append(b.contents[:0], contents[written-b.limit:]...)
		return written, nil
	}
	overflow := len(b.contents) + written - b.limit
	if overflow > 0 {
		copy(b.contents, b.contents[overflow:])
		b.contents = b.contents[:len(b.contents)-overflow]
	}
	b.contents = append(b.contents, contents...)
	return written, nil
}

func (b *boundedTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.contents)
}

func entryEnvironment(entry map[string]any) map[string]any {
	environment, _ := entry["env"].(map[string]any)
	return environment
}

func mergedEnvironment(inherited []string, overrides map[string]string) []string {
	merged := append([]string(nil), inherited...)
	indexes := make(map[string]int, len(merged))
	for index, item := range merged {
		if name, _, found := strings.Cut(item, "="); found {
			indexes[name] = index
		}
	}
	for name, value := range overrides {
		item := name + "=" + value
		if index, found := indexes[name]; found {
			merged[index] = item
		} else {
			indexes[name] = len(merged)
			merged = append(merged, item)
		}
	}
	return merged
}
