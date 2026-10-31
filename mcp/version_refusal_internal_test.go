package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func refusalOf(t *testing.T, current, feature string) error {
	t.Helper()
	installed, err := tmux.ParseVersion(current)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion(tmux.MinimumSupportedVersion)
	if err != nil {
		t.Fatal(err)
	}
	return &tmux.VersionTooLowError{Current: installed, Minimum: minimum, Feature: feature}
}

// connectOldDaemonClient connects an in-memory MCP session to a server whose
// socket is empty at connect time, and returns the function that makes a 3.2a
// daemon appear there.
func connectOldDaemonClient(t *testing.T) (*sdk.ClientSession, func()) {
	t.Helper()
	flip := filepath.Join(t.TempDir(), "flip")
	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	socketDirectory, err := os.MkdirTemp("", "old")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socket := filepath.Join(socketDirectory, "s")
	options := executableFixtureOptions(t, fixtureOldDaemonAppears, tmux.ServerOptions{
		SocketPath: socket,
	})
	options.ProcessEnvironment = append(options.ProcessEnvironment, flipFileEnvironment+"="+flip)
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, options))
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(t.Context(), AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatalf("Connect() on an empty socket: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "refusal-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, func() {
		if err := os.WriteFile(flip, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// The 3.2a daemon that appears is listening on the socket.
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
	}
}

// An agent that meets a refused tmux needs the floor and what to do about it
// in the tool result itself. The refusal is raised while the request acquires
// its runtime, outside the handler, so this goes through the registered tool.
func TestEveryToolAnswersAMidSessionVersionRefusalWithOneRemedy(t *testing.T) {
	session, oldDaemonAppears := connectOldDaemonClient(t)
	oldDaemonAppears()
	for _, name := range []string{"list_sessions", "list_windows", "list_panes"} {
		t.Run(name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
				Name: name, Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("%s succeeded against a 3.2a daemon: %s", name, resultText(result))
			}
			text := resultText(result)
			for _, want := range []string{
				"installed 3.2a", "require " + tmux.MinimumSupportedVersion + " or newer",
				"restart the tmux server", BinaryEnvironmentVariable,
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s result %q lacks %q", name, text, want)
				}
			}
			for _, banned := range []string{"pin the previous", "get_server_info reports"} {
				if strings.Contains(text, banned) {
					t.Fatalf("%s result %q carries a second remedy: %q", name, text, banned)
				}
			}
		})
	}
}

// get_server_info is where an agent looks for the floor, so it answers a
// refused tmux with minimumVersion and the refusal instead of failing.
func TestServerInfoAnswersARefusedTmux(t *testing.T) {
	session, oldDaemonAppears := connectOldDaemonClient(t)
	oldDaemonAppears()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "get_server_info", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("get_server_info failed: %s", resultText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var info getServerInfoOutput
	if err := json.Unmarshal(encoded, &info); err != nil {
		t.Fatal(err)
	}
	if info.MinimumVersion != minimumTmuxVersion || info.Version != "3.2a" ||
		!strings.Contains(info.VersionRefusal, "restart the tmux server") || !info.Alive {
		t.Fatalf("get_server_info = %+v, want the floor, version 3.2a, the remedy and a running daemon", info)
	}
}

func resultText(result *sdk.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := content.(*sdk.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// Only the floor gets the remedy; an optional-capability refusal and any other
// failure keep their own words.
func TestVersionRemedyIsAddedOnlyToTheFloorRefusal(t *testing.T) {
	feature := refusalOf(t, "3.4", "no_keys")
	other := errors.New("no server running")
	var refused *versionRefusalError
	if errors.As(explainVersionRefusal(feature), &refused) ||
		errors.As(explainVersionRefusal(other), &refused) ||
		explainVersionRefusal(nil) != nil {
		t.Fatal("a non-floor error was rewritten")
	}
}

// Startup inspects the selected socket before any session exists; a refused
// daemon there gets the same remedy as a refused tool call.
func TestStartupInspectionAnswersARefusedTmuxWithTheRemedy(t *testing.T) {
	flip := filepath.Join(t.TempDir(), "flip")
	if err := os.WriteFile(flip, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	options := executableFixtureOptions(t, fixtureOldDaemonAppears, tmux.ServerOptions{
		SocketName: "old-daemon-at-startup",
	})
	options.ProcessEnvironment = append(options.ProcessEnvironment, flipFileEnvironment+"="+flip)
	target := mustInternalTmuxServer(t, options)
	for name, start := range map[string]func() error{
		"Run":                func() error { return Run(t.Context(), target) },
		"AdvertisedToolsFor": func() error { _, err := AdvertisedToolsFor(t.Context(), target); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := start()
			if !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("error = %v, want the library refusal", err)
			}
			for _, want := range []string{"installed 3.2a", "restart the tmux server", BinaryEnvironmentVariable} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "pin the previous") {
				t.Fatalf("error %q carries a second remedy", err)
			}
		})
	}
}

// A read batch calls its inner tools through dispatchers, bound for a tool
// the surface does not serve directly too. Each carries the remedy when the
// refusal comes from acquiring the runtime.
func TestBatchDispatchersAnswerAVersionRefusalWithTheRemedy(t *testing.T) {
	for name, toolsets := range map[string]string{"served": "inspect", "unserved": "manage"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(ToolsetsEnvironmentVariable, toolsets)
			t.Setenv(ToolsEnvironmentVariable, "call_read_tools_batch")
			flip := filepath.Join(t.TempDir(), "flip")
			if err := os.WriteFile(flip, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			options := executableFixtureOptions(t, fixtureOldDaemonAppears, tmux.ServerOptions{
				SocketName: "old-daemon-dispatcher",
			})
			options.ProcessEnvironment = append(options.ProcessEnvironment, flipFileEnvironment+"="+flip)
			instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, options))
			dispatch, bound := instance.tools.dispatchers["list_windows"]
			_, direct := instance.tools.surface.byName["list_windows"]
			if direct != (name == "served") {
				t.Fatalf("list_windows served directly = %v on the %s surface", direct, name)
			}
			if !bound {
				t.Fatal("list_windows has no dispatcher")
			}
			result, err := dispatch.call(t.Context(), &sdk.CallToolRequest{}, json.RawMessage("{}"))
			text := ""
			if err != nil {
				text = err.Error()
			}
			if result != nil {
				text += resultText(result)
			}
			if !strings.Contains(text, "restart the tmux server") || !strings.Contains(text, BinaryEnvironmentVariable) {
				t.Fatalf("dispatcher result %q lacks the remedy", text)
			}
		})
	}
}

// The remedy replaces exactly the library's own sentence, wherever it sits in
// the message, so context around it that holds "; " survives.
func TestVersionRemedyReplacesOnlyTheLibrarySentence(t *testing.T) {
	err := explainVersionRefusal(fmt.Errorf("inspect; retry: %w (see a; b)", refusalOf(t, "3.2a", "")))
	text := err.Error()
	if !strings.HasPrefix(text, "inspect; retry: ") || !strings.HasSuffix(text, " (see a; b)") || strings.Contains(text, "pin the previous") ||
		strings.Count(text, "restart the tmux server") != 1 {
		t.Fatalf("explained refusal = %q, want the context kept and one MCP remedy", text)
	}
}
