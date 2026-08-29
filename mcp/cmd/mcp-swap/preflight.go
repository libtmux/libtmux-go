package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const expectedServerName = "libtmux"

// preflightTimeout includes cold compilation or module download.
const preflightTimeout = 180 * time.Second

const (
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
	arguments := entryArguments(entry)
	environment := map[string]string{}
	for name, value := range entryEnvironment(entry) {
		environment[name] = fmt.Sprint(value)
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
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	process := exec.CommandContext(runCtx, spec.command, spec.args...)
	process.Env = mergedEnvironment(os.Environ(), spec.env)
	cleanupProcess := ownPreflightProcess(process)
	defer func() {
		if err := cleanupProcess(); err != nil {
			failure := fmt.Sprintf("terminate MCP process group: %v", err)
			if reason == "" {
				reason = failure
			} else {
				reason += "\n" + failure
			}
		}
	}()
	// WaitDelay bounds pipe drainage; Unix cleanup separately sweeps the
	// inherited process group.
	process.WaitDelay = preflightWaitDelay
	complaints := newBoundedTail(preflightStderrLimit)
	process.Stderr = complaints
	transport := &recordingTransport{transport: &sdk.CommandTransport{
		Command:           process,
		TerminateDuration: preflightTerminateDuration,
	}}
	client := sdk.NewClient(&sdk.Implementation{
		Name: "mcp-swap-preflight", Version: "1",
	}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
	session, err := client.Connect(runCtx, transport, nil)
	if err != nil {
		if transport.connection != nil {
			_ = transport.connection.Close()
		}
		if process.Process == nil {
			return preflightFailure(
				fmt.Sprintf("could not launch %s", spec.command), err, complaints)
		}
		return preflightFailure("initialize MCP server", err, complaints)
	}
	defer func() {
		if err := session.Close(); reason == "" && err != nil {
			reason = preflightFailure("close MCP server", err, complaints)
		}
	}()

	initialized := session.InitializeResult()
	switch {
	case initialized == nil:
		return "server returned no initialize result"
	case initialized.Capabilities == nil:
		return "server returned no capabilities"
	case initialized.ServerInfo == nil:
		return "server returned no server information"
	case initialized.ServerInfo.Name != expectedServerName:
		return fmt.Sprintf(
			"server identified itself as %q, want %q",
			initialized.ServerInfo.Name,
			expectedServerName,
		)
	case initialized.ServerInfo.Version == "":
		return "server reported no version"
	}
	if err := session.Ping(runCtx, nil); err != nil {
		return preflightFailure("ping MCP server", err, complaints)
	}
	return ""
}

// recordingTransport retains a connection when the SDK opens it but rejects
// initialization before it can return a session to close.
type recordingTransport struct {
	transport  sdk.Transport
	connection sdk.Connection
}

func (t *recordingTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.transport.Connect(ctx)
	if err == nil {
		t.connection = connection
	}
	return connection, err
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
