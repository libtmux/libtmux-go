package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestASafetyLevelWithholdsTheToolsItSays(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is advertised at the readonly level", tool.Name)
		}
	}

	refused := call(ctx, t, session, "call_readonly_tools_batch", map[string]any{
		"calls": []map[string]any{
			{"tool": "kill_session", "arguments": map[string]any{"sessionName": "anything"}},
		},
	}, nil)
	if !refused.IsError {
		t.Error("a batch reached a tool the safety level withheld")
	}

	if _, err := os.Stat("."); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

// insidePaneVariables let this test re-run itself inside a tmux pane. The pane
// this process runs in is not something a test can arrange after the fact, so
// the test becomes the program the pane runs.
const (
	insidePaneSocket = "LIBTMUX_MCP_TEST_SOCKET"
	insidePaneReport = "LIBTMUX_MCP_TEST_REPORT"
)

//libtmux:real-tmux
func TestTheServerFindsItsOwnPaneWithoutTheEnvironment(t *testing.T) {
	if socket := os.Getenv(insidePaneSocket); socket != "" {
		reportOwnPaneFromInside(t, socket, os.Getenv(insidePaneReport))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "host"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// The pane runs this test binary again, with TMUX and TMUX_PANE removed so
	// only the process tree can answer, and with the pane held open long enough
	// for the report to be written.
	report := filepath.Join(t.TempDir(), "report")
	command := fmt.Sprintf(
		"env -u TMUX -u TMUX_PANE %s=%s %s=%s %s -test.run='^%s$' >/dev/null 2>&1; sleep 30",
		insidePaneSocket, socket,
		insidePaneReport, report,
		os.Args[0], t.Name(),
	)
	window, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "probe", Command: command,
	})
	if err != nil {
		t.Fatalf("start the probe pane: %v", err)
	}
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("find the probe pane: %v", err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var found string
	for time.Now().Before(deadline) {
		if written, err := os.ReadFile(report); err == nil {
			found = strings.TrimSpace(string(written))
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("the server running inside a pane reported nothing")
	}
	if want := pane.ID().String(); found != want {
		t.Errorf("the server inside pane %s reported %q", want, found)
	}
}

// reportOwnPaneFromInside is the half of the test that runs in the pane. It
// asks the server which pane it is in and writes the answer where the other
// half will read it.
func reportOwnPaneFromInside(t *testing.T, socket, report string) {
	t.Helper()
	if report == "" {
		t.Fatal("no report path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	client := sdk.NewClient(&sdk.Implementation{Name: "inside"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	var info struct {
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	answer := ""
	if info.InsideThisServer {
		answer = info.CallerPaneID
	}
	if err := os.WriteFile(report, []byte(answer), 0o600); err != nil {
		t.Fatalf("write the report: %v", err)
	}
}

// connectAsking is connect with a client that can be asked questions. The
// handler decides what the person says.
func connectAsking(
	t *testing.T,
	answer func(*sdk.ElicitRequest) *sdk.ElicitResult,
) (*sdk.ClientSession, tmux.Server, context.Context, *int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	asked := 0
	options := &sdk.ClientOptions{}
	if answer != nil {
		options.ElicitationHandler = func(
			_ context.Context,
			request *sdk.ElicitRequest,
		) (*sdk.ElicitResult, error) {
			asked++
			return answer(request), nil
		}
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "asking-client"}, options)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, target, ctx, &asked
}

// answering is an elicitation handler that always gives the same action.
func answering(action string) func(*sdk.ElicitRequest) *sdk.ElicitResult {
	return func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: action}
	}
}

// propertiesOf is the form an elicitation asked to have filled in.
func propertiesOf(t *testing.T, request *sdk.ElicitRequest) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(request.Params.RequestedSchema)
	if err != nil {
		t.Fatalf("marshal the requested schema: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("decode the requested schema: %v", err)
	}
	return schema.Properties
}

//libtmux:real-tmux
func TestAYesAboutTheCallerPaneCanBeKept(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")

	asked := 0
	remembering, keeping := true, false
	session, target, ctx, _ := connectAsking(t, func(request *sdk.ElicitRequest) *sdk.ElicitResult {
		asked++
		_, offered := propertiesOf(t, request)["remember"]
		if offered != remembering {
			t.Errorf("the form offered to keep the yes = %v, want %v",
				offered, remembering)
		}
		if !offered {
			return &sdk.ElicitResult{Action: "accept"}
		}
		return &sdk.ElicitResult{
			Action:  "accept",
			Content: map[string]any{"remember": keeping},
		}
	})
	workspace(ctx, t, session, "session_name: kept\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	// Without an answer that keeps it, every write asks.
	for range 2 {
		call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo asked",
		}, nil)
	}
	if asked != 2 {
		t.Fatalf("two writes asked %d times, want one each", asked)
	}

	// Then one that keeps it, and the writes after it go through unasked --
	// including through a different tool, because the yes is about the pane.
	keeping = true
	asked = 0
	call(ctx, t, session, "send_keys", map[string]any{
		"paneId": ownPane, "command": "echo keeping",
	}, nil)
	if asked != 1 {
		t.Fatalf("keeping the yes asked %d times, want once", asked)
	}
	for _, kept := range []struct {
		tool      string
		arguments map[string]any
	}{
		{"send_keys", map[string]any{"paneId": ownPane, "command": "echo after"}},
		{"paste_text", map[string]any{"paneId": ownPane, "text": "echo pasted"}},
		{"clear_pane", map[string]any{"paneId": ownPane}},
	} {
		if result := call(ctx, t, session, kept.tool, kept.arguments, nil); result.IsError {
			t.Errorf("%s was refused after the yes was kept: %s", kept.tool, resultText(result))
		}
	}
	if asked != 1 {
		t.Errorf("writes after the kept yes asked %d more times", asked-1)
	}

	// Ending the pane is a different yes, so it asks whatever was said about
	// typing there, and its form never offers to keep it.
	remembering = false
	if result := call(ctx, t, session, "kill_pane",
		map[string]any{"paneId": ownPane}, nil); result.IsError {
		t.Errorf("kill_pane was refused by an accepting client: %s", resultText(result))
	}
	if asked != 2 {
		t.Errorf("ending the pane asked %d times in total, want one of its own", asked)
	}
}

//libtmux:real-tmux
func TestOneSessionsYesDoesNotSilenceAnother(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	server := mustMCPServer(t, target)

	// Two clients of one server, as an embedder holds them.
	join := func(name string, answer func(*sdk.ElicitRequest) *sdk.ElicitResult) (*sdk.ClientSession, *int) {
		clientTransport, serverTransport := sdk.NewInMemoryTransports()
		serverSession, err := server.Connect(
			ctx, assumeResponseCommit(serverTransport), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = serverSession.Close() })
		asked := 0
		client := sdk.NewClient(&sdk.Implementation{Name: name}, &sdk.ClientOptions{
			ElicitationHandler: func(
				_ context.Context, request *sdk.ElicitRequest,
			) (*sdk.ElicitResult, error) {
				asked++
				return answer(request), nil
			},
		})
		session, err := client.Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session, &asked
	}

	first, askedFirst := join("first", func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"remember": true}}
	})
	second, askedSecond := join("second", func(*sdk.ElicitRequest) *sdk.ElicitResult {
		return &sdk.ElicitResult{Action: "decline"}
	})

	workspace(ctx, t, first, "session_name: shared\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	// The first client says yes and keeps it, then writes again unasked.
	for range 2 {
		if result := call(ctx, t, first, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo first",
		}, nil); result.IsError {
			t.Fatalf("the first client was refused: %s", resultText(result))
		}
	}
	if *askedFirst != 1 {
		t.Errorf("the first client was asked %d times, want once", *askedFirst)
	}

	// The second client never saw that question, so it must be asked its own
	// -- and its answer must be the one that governs its own call.
	result := call(ctx, t, second, "send_keys", map[string]any{
		"paneId": ownPane, "command": "echo second",
	}, nil)
	if *askedSecond != 1 {
		t.Errorf("the second client was asked %d times, want once of its own", *askedSecond)
	}
	if !result.IsError {
		t.Error("the second client wrote to the caller pane on another client's yes")
	}
}

//libtmux:real-tmux
func TestEndingWhatHoldsTheCallerPaneIsAskedAbout(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: holding\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	var listed struct {
		Panes []struct {
			WindowID string `json:"windowId"`
			Session  string `json:"session"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to be held by anything")
	}
	holder := listed.Panes[0]

	for _, ending := range []struct {
		tool      string
		arguments map[string]any
		names     string
	}{
		{"kill_window", map[string]any{"windowId": holder.WindowID}, holder.WindowID},
		{"kill_session", map[string]any{"sessionName": holder.Session}, holder.Session},
		{"kill_server", map[string]any{"confirm": true}, "tmux server"},
	} {
		t.Run(ending.tool, func(t *testing.T) {
			result := call(ctx, t, session, ending.tool, ending.arguments, nil)
			if !result.IsError {
				t.Fatalf("%s ended what holds the caller pane", ending.tool)
			}
			said := resultText(result)
			if !strings.Contains(said, ending.names) {
				t.Errorf("the refusal does not name %s: %q", ending.names, said)
			}
			if !strings.Contains(said, "holds the pane this server is running in") {
				t.Errorf("the refusal does not say why: %q", said)
			}
		})
	}

	// The same tools still work on something that does not hold it, which is
	// what keeps this a guard rather than a ban.
	var made struct {
		WindowID string `json:"windowId"`
	}
	call(ctx, t, session, "create_window", map[string]any{
		"sessionName": holder.Session, "command": "sleep 300",
	}, &made)
	if made.WindowID == "" {
		t.Fatal("no second window to end")
	}
	if result := call(ctx, t, session, "kill_window",
		map[string]any{"windowId": made.WindowID}, nil); result.IsError {
		t.Errorf("a window holding no caller pane was refused: %s", resultText(result))
	}
}

//libtmux:real-tmux
func TestWritingToTheCallerPaneIsAskedAbout(t *testing.T) {
	// A fresh server hands out predictable pane ids, which is what lets the
	// environment name one before the server reporting it exists.
	const ownPane = "%0"

	arrange := func(
		t *testing.T,
		answer func(*sdk.ElicitRequest) *sdk.ElicitResult,
	) (*sdk.ClientSession, context.Context, *int) {
		t.Helper()
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx, asked := connectAsking(t, answer)
		workspace(ctx, t, session, "session_name: guarded\nwindows:\n  - panes:\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")
		return session, ctx, asked
	}

	t.Run("declining stops the write", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("decline"))
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "echo into-my-own-terminal",
		}, nil)
		if !result.IsError {
			t.Error("a declined write to the caller pane went through anyway")
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times, want once", *asked)
		}
	})

	t.Run("a client that cannot be asked is refused", func(t *testing.T) {
		// Elicitation is optional, and letting the write through when nobody
		// can be asked makes the guard advisory: the client least able to warn
		// its person is the one that types into their terminal unannounced. It
		// happened -- a peer session identifying its own server ran a command
		// against the caller pane and put the text in its user's prompt box.
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx := connect(t)
		workspace(ctx, t, session, "session_name: unasked\nwindows:\n  - panes:\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		for _, tool := range []string{"send_keys", "run_command"} {
			arguments := map[string]any{"paneId": ownPane, "command": "echo into-my-own-terminal"}
			if tool == "run_command" {
				arguments["timeoutSeconds"] = 5
			}
			result := call(ctx, t, session, tool, arguments, nil)
			if !result.IsError {
				t.Errorf("%s reached the caller pane with nobody able to be asked", tool)
				continue
			}
			said := resultText(result)
			if !strings.Contains(said, ownPane) {
				t.Errorf("the %s refusal does not name the pane: %q", tool, said)
			}
			// The caller whose only pane is this one has nowhere to be sent,
			// so making a pane is named beside finding one.
			for _, way := range []string{"split_window", "create_session", "isCaller"} {
				if !strings.Contains(said, way) {
					t.Errorf("the %s refusal does not offer %s: %q", tool, way, said)
				}
			}
		}
	})

	t.Run("accepting lets it through", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("accept"))
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "true",
		}, nil)
		if result.IsError {
			t.Errorf("an accepted write was refused: %#v", result.Content)
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times, want once", *asked)
		}
	})

	t.Run("a batch is asked about too", func(t *testing.T) {
		session, ctx, asked := arrange(t, answering("decline"))
		var out struct {
			Completed int `json:"completed"`
			Results   []struct {
				Tool  string `json:"tool"`
				Error string `json:"error"`
			} `json:"results"`
		}
		call(ctx, t, session, "call_mutating_tools_batch", map[string]any{
			"calls": []map[string]any{{
				"tool": "send_keys",
				"arguments": map[string]any{
					"paneId": ownPane, "command": "echo into-my-own-terminal",
				},
			}},
		}, &out)
		if out.Completed != 0 {
			t.Error("a batch typed into the caller pane a direct call was refused")
		}
		if len(out.Results) == 0 || !strings.Contains(out.Results[0].Error, "declined") {
			t.Errorf("the batch does not report the decline: %+v", out.Results)
		}
		if *asked != 1 {
			t.Errorf("the person was asked %d times for a batched write, want once", *asked)
		}
	})

	t.Run("another pane is not asked about", func(t *testing.T) {
		t.Setenv("TMUX_PANE", ownPane)
		t.Setenv("TMUX", "")
		session, target, ctx, asked := connectAsking(t,
			answering("decline"))
		workspace(ctx, t, session,
			"session_name: elsewhere\nwindows:\n  - panes:\n      - {}\n      - {}\n")
		socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
		if err != nil || len(socket.Stdout) == 0 {
			t.Fatalf("read the socket path: %v", err)
		}
		t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

		panes := paneIDs(ctx, t, session)
		other := ""
		for _, pane := range panes {
			if pane != ownPane {
				other = pane
				break
			}
		}
		if other == "" {
			t.Fatal("the workspace built no pane other than the caller's")
		}
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": other, "command": "true",
		}, nil)
		if result.IsError {
			t.Errorf("writing to somebody else's pane was refused: %#v", result.Content)
		}
		if *asked != 0 {
			t.Errorf("the person was asked about a pane that is not the caller's")
		}
	})

	t.Run("a client that cannot be asked is still stopped", func(t *testing.T) {
		// This reverses what this test asserted before. Letting the write
		// through when nobody could be asked was chosen so a client without
		// elicitation kept the behaviour it had; what that bought in practice
		// was a guard that is absent from the clients most likely to need it.
		// Writing to somebody's own terminal unannounced is the harm, and it
		// does not become acceptable because their client cannot show a prompt.
		session, ctx, _ := arrange(t, nil)
		result := call(ctx, t, session, "send_keys", map[string]any{
			"paneId": ownPane, "command": "true",
		}, nil)
		if !result.IsError {
			t.Error("a client that cannot be asked typed into the caller pane")
		}

		// Every other pane is unaffected, which is what keeps this a guard on
		// one pane rather than a restriction on the tool.
		var listed struct {
			Panes []struct {
				ID       string `json:"id"`
				IsCaller *bool  `json:"isCaller"`
			} `json:"panes"`
		}
		call(ctx, t, session, "list_panes", map[string]any{}, &listed)
		for _, pane := range listed.Panes {
			if pane.IsCaller != nil && *pane.IsCaller {
				continue
			}
			if result := call(ctx, t, session, "send_keys", map[string]any{
				"paneId": pane.ID, "command": "true",
			}, nil); result.IsError {
				t.Errorf("writing to %s was refused: %#v", pane.ID, result.Content)
			}
		}
	})
}

//libtmux:real-tmux
func TestEnteringCopyModeOnTheCallerPaneIsAskedAbout(t *testing.T) {
	const ownPane = "%0"
	t.Setenv("TMUX_PANE", ownPane)
	t.Setenv("TMUX", "")
	session, target, ctx, asked := connectAsking(t,
		answering("decline"))
	workspace(ctx, t, session, "session_name: copymode\nwindows:\n  - panes:\n      - {}\n")
	socket, err := target.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil || len(socket.Stdout) == 0 {
		t.Fatalf("read the socket path: %v", err)
	}
	t.Setenv("TMUX", socket.Stdout[0]+",1234,0")

	if result := call(ctx, t, session, "enter_copy_mode",
		map[string]any{"paneId": ownPane}, nil); !result.IsError {
		t.Error("entering copy mode on the caller pane was not asked about")
	}
	if *asked != 1 {
		t.Errorf("the person was asked %d times, want once", *asked)
	}

	// The way out is never blocked, or a declined entry would be unrecoverable
	// through this server.
	if result := call(ctx, t, session, "exit_copy_mode",
		map[string]any{"paneId": ownPane}, nil); result.IsError {
		t.Errorf("leaving copy mode was refused: %#v", result.Content)
	}
	if *asked != 1 {
		t.Errorf("leaving copy mode asked the person as well")
	}
}

//libtmux:real-tmux
func TestToolsRejectAReplacementTmuxDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	// A socket of its own, not the shared harness's: this test kills the tmux
	// server it is using, and the harness owns the cleanup of the one it made.
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	target := mustTmuxServer(t, tmux.ServerOptions{SocketPath: socket})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})
	// The runtime attaches to an existing session, so there has to be one.
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "before", Width: 80, Height: 24,
	}); err != nil {
		t.Fatalf("start the first session: %v", err)
	}

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "restart"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if result := call(ctx, t, session, "list_panes", map[string]any{}, nil); result.IsError {
		t.Fatalf("list_panes before the restart: %#v", result.Content)
	}

	if err := target.Kill(ctx); err != nil {
		t.Fatalf("kill the tmux server: %v", err)
	}
	// Bring one back on the same socket, as a person restarting tmux would.
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "after", Width: 80, Height: 24,
	}); err != nil {
		t.Fatalf("start a replacement tmux server: %v", err)
	}

	// The first failed call makes the whole instance terminal. It may close the
	// SDK session before the tool error reaches the caller; either result is a
	// refusal, and every later call must remain refused.
	first, firstErr := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "list_panes", Arguments: map[string]any{},
	})
	if firstErr == nil && !first.IsError {
		t.Fatal("the first call adopted a replacement tmux daemon")
	}
	second, secondErr := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "list_panes", Arguments: map[string]any{},
	})
	if secondErr == nil && !second.IsError {
		t.Fatal("a later call adopted a replacement tmux daemon")
	}
}

//libtmux:real-tmux
func TestTypingIntoAPaneInAModeIsRefused(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: moded\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	send(ctx, t, session, pane, "seq 1 200")
	if result := call(ctx, t, session, "enter_copy_mode", map[string]any{
		"paneId": pane, "scrollUp": true,
	}, nil); result.IsError {
		t.Fatalf("enter_copy_mode: %#v", result.Content)
	}
	call(ctx, t, session, "load_buffer", map[string]any{
		"text": "staged", "name": "moded",
	}, nil)

	for tool, arguments := range map[string]map[string]any{
		"send_keys":       {"paneId": pane, "command": "echo NOPE"},
		"send_keys_batch": {"paneId": pane, "keys": []string{"h", "i"}},
		"paste_text":      {"paneId": pane, "text": "echo NOPE"},
		"paste_buffer":    {"paneId": pane, "name": "moded"},
		"run_command":     {"paneId": pane, "command": "echo NOPE", "timeoutSeconds": 5},
	} {
		result := call(ctx, t, session, tool, arguments, nil)
		if !result.IsError {
			t.Errorf("%s into a pane in copy mode was accepted", tool)
			continue
		}
		said := resultText(result)
		// Both ways on, because a caller who entered the mode deliberately
		// wants to read rather than to undo it.
		for _, wanted := range []string{"capture_pane", "exit_copy_mode", tool} {
			if !strings.Contains(said, wanted) {
				t.Errorf("the %s refusal does not name %s: %q", tool, wanted, said)
			}
		}
		// And the connection is still there, which run_command used to cost.
		if listed := call(ctx, t, session, "list_panes", map[string]any{}, nil); listed.IsError {
			t.Fatalf("the connection did not survive refusing %s: %#v", tool, listed.Content)
		}
	}

	// The way out works, after which typing lands.
	if result := call(ctx, t, session, "exit_copy_mode", map[string]any{
		"paneId": pane,
	}, nil); result.IsError {
		t.Fatalf("exit_copy_mode: %#v", result.Content)
	}
	if result := call(ctx, t, session, "send_keys", map[string]any{
		"paneId": pane, "command": "echo LANDS",
	}, nil); result.IsError {
		t.Fatalf("send_keys after leaving copy mode: %#v", result.Content)
	}
}
