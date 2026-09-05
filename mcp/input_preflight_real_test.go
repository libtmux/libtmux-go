package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestSendConfiguredMembershipReal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	client := inputTestClient(ctx, t, target, nil)

	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := panes[2].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{panes[0].ID().String(), panes[1].ID().String()}
	slices.Sort(want)
	result := callInputTool(ctx, t, client, "send_keys", map[string]any{
		"pane_id": panes[0].ID().String(), "keys": []string{"C-l"},
	})
	if result.IsError {
		t.Fatalf("send_keys refusal = %q", callToolResultText(result))
	}
	if got := structuredStringSlice(t, result, "resolved_pane_ids"); !slices.Equal(got, want) {
		t.Errorf("send_keys configured membership = %v, want %v", got, want)
	}
	if _, camel := structuredMap(t, result)["resolvedPaneIds"]; camel {
		t.Error("send_keys exposed camel-case configured membership")
	}

	batch := callInputTool(ctx, t, client, "send_keys_batch", map[string]any{
		"operations": []map[string]any{
			{"pane_id": panes[0].ID().String(), "keys": []string{"C-l"}},
			{"pane_id": panes[2].ID().String(), "keys": []string{"C-l"}},
		},
	})
	rows := structuredRows(t, batch, "results")
	if len(rows) != 2 || !slices.Equal(anyStringSlice(rows[0]["resolved_pane_ids"]), want) ||
		!slices.Equal(anyStringSlice(rows[1]["resolved_pane_ids"]), []string{panes[2].ID().String()}) {
		t.Errorf("send_keys_batch configured memberships = %#v", rows)
	}
	if len(rows) != 0 {
		if _, camel := rows[0]["resolvedPaneIds"]; camel {
			t.Error("send_keys_batch exposed camel-case configured membership")
		}
	}

	if err := panes[1].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = panes[1].CopyMode(context.Background(), tmux.CopyModeRequest{Cancel: true}) })
	marker := "libtmux-modal-guard-red"
	refused := callInputTool(ctx, t, client, "send_keys", map[string]any{
		"pane_id": panes[0].ID().String(), "keys": []string{marker}, "literal": true,
	})
	if !refused.IsError || !strings.Contains(callToolResultText(refused), panes[1].ID().String()) {
		t.Errorf("modal configured member response = (%t, %q), want whole-operation refusal naming %s",
			refused.IsError, callToolResultText(refused), panes[1].ID())
	}
	lines, err := panes[0].Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), marker) {
		t.Error("source pane changed despite modal configured member")
	}
}

//libtmux:real-tmux
func TestRunConfiguredMembershipReal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	client := inputTestClient(ctx, t, target, nil)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}

	sideEffects := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	command := fmt.Sprintf("printf x > %s/\"$TMUX_PANE\"", shellQuote(sideEffects))
	result := callInputTool(ctx, t, client, "run_shell_command", map[string]any{
		"pane_id": panes[0].ID().String(), "command": command, "timeout": 5,
	})
	want := paneIDs(panes)
	if !result.IsError || !allStringsPresent(callToolResultText(result), want) {
		t.Errorf("multi-pane run response = (%t, %q), want refusal naming %v",
			result.IsError, callToolResultText(result), want)
	}
	entries, err := os.ReadDir(sideEffects)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("multi-pane run created side effects: %v", entryNames(entries))
	}

	if err := window.SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	single := callInputTool(ctx, t, client, "run_shell_command", map[string]any{
		"pane_id": panes[0].ID().String(), "command": "true", "timeout": 5,
	})
	if single.IsError {
		t.Fatalf("singleton run refusal = %q", callToolResultText(single))
	}
	if got := structuredStringSlice(t, single, "resolved_pane_ids"); !slices.Equal(got, []string{panes[0].ID().String()}) {
		t.Errorf("singleton run configured membership = %v", got)
	}
	if _, camel := structuredMap(t, single)["resolvedPaneIds"]; camel {
		t.Error("run_shell_command exposed camel-case configured membership")
	}
}

//libtmux:real-tmux
func TestPasteAndCallerPreflightReal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	client := inputTestClient(ctx, t, target, nil)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}

	withoutEnter := callInputTool(ctx, t, client, "paste_text", map[string]any{
		"pane_id": panes[0].ID().String(), "text": "target-only",
	})
	if withoutEnter.IsError {
		t.Fatalf("target-only paste refusal = %q", callToolResultText(withoutEnter))
	}
	if _, present := structuredMap(t, withoutEnter)["enter_pane_ids"]; present {
		t.Error("paste_text exposed obsolete Enter membership")
	}
	clearKey := "C-u"
	if err := panes[0].SendKeys(ctx, tmux.SendKeysRequest{Command: &clearKey, SkipEnter: true}); err != nil {
		t.Fatal(err)
	}

	if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatal(err)
	}
	modal := callInputTool(ctx, t, client, "paste_text", map[string]any{
		"pane_id": panes[0].ID().String(), "text": "must-not-stage", "enter": true,
	})
	if !modal.IsError || !strings.Contains(callToolResultText(modal), panes[0].ID().String()) {
		t.Errorf("modal Enter response = (%t, %q), want refusal naming %s",
			modal.IsError, callToolResultText(modal), panes[0].ID())
	}
	lines, err := panes[0].Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), "must-not-stage") {
		t.Error("paste text reached the target before modal Enter-member refusal")
	}
	if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatal(err)
	}

	safe := callInputTool(ctx, t, client, "paste_text", map[string]any{
		"pane_id": panes[0].ID().String(), "text": "printf safe", "enter": true,
	})
	if safe.IsError {
		t.Fatalf("safe paste refusal = %q", callToolResultText(safe))
	}
	if _, present := structuredMap(t, safe)["enter_pane_ids"]; present {
		t.Error("safe paste exposed obsolete Enter membership")
	}

	t.Run("caller source refuses before a buffer", func(t *testing.T) {
		callerTarget, _, callerPanes := threePaneInputFixture(ctx, t)
		instance := mustInternalMCPServer(t, callerTarget)
		instance.tools.caller = callerIdentity{
			paneID: callerPanes[0].ID().String(),
			socket: resolvePath(callerTarget.SocketPath()),
			inside: true,
		}
		instance.tools.callerCached = true
		setBufferCalls := 0
		instance.runtime.deps.setBuffer = func(
			context.Context,
			tmux.Server,
			tmux.SetBufferRequest,
		) error {
			setBufferCalls++
			return nil
		}
		prompts := 0
		declining := connectInputTestClient(ctx, t, instance, &sdk.ClientOptions{
			ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
				prompts++
				return &sdk.ElicitResult{Action: "decline"}, nil
			},
		})
		declined := callInputTool(ctx, t, declining, "paste_text", map[string]any{
			"pane_id": callerPanes[0].ID().String(), "text": "declined",
		})
		if !declined.IsError || prompts != 1 || setBufferCalls != 0 {
			t.Fatalf("declined caller paste = (%t, prompts=%d, buffers=%d)",
				declined.IsError, prompts, setBufferCalls)
		}

		unaskable := connectInputTestClient(ctx, t, instance, nil)
		refused := callInputTool(ctx, t, unaskable, "paste_text", map[string]any{
			"pane_id": callerPanes[0].ID().String(), "text": "unaskable",
		})
		if !refused.IsError || setBufferCalls != 0 {
			t.Fatalf("unaskable caller paste = (%t, buffers=%d)", refused.IsError, setBufferCalls)
		}
	})

	t.Run("non-target modal pane is not part of paste authority", func(t *testing.T) {
		callerTarget, callerWindow, callerPanes := threePaneInputFixture(ctx, t)
		if err := callerWindow.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := callerPanes[1].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
			t.Fatal(err)
		}
		instance := mustInternalMCPServer(t, callerTarget)
		instance.tools.caller = callerIdentity{
			paneID: callerPanes[0].ID().String(),
			socket: resolvePath(callerTarget.SocketPath()),
			inside: true,
		}
		instance.tools.callerCached = true
		prompts, buffers := 0, 0
		setBuffer := instance.runtime.deps.setBuffer
		instance.runtime.deps.setBuffer = func(
			bufferCtx context.Context,
			server tmux.Server,
			buffer tmux.SetBufferRequest,
		) error {
			buffers++
			return setBuffer(bufferCtx, server, buffer)
		}
		client := connectInputTestClient(ctx, t, instance, &sdk.ClientOptions{
			ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
				prompts++
				return &sdk.ElicitResult{
					Action: "accept", Content: map[string]any{"remember": true},
				}, nil
			},
		})
		pasted := callInputTool(ctx, t, client, "paste_text", map[string]any{
			"pane_id": callerPanes[0].ID().String(), "text": "modal-first", "enter": true,
		})
		if pasted.IsError || prompts != 1 || buffers != 1 {
			t.Fatalf("target-only modal-peer paste = (%t, prompts=%d, buffers=%d)",
				pasted.IsError, prompts, buffers)
		}
	})

	t.Run("non-target caller is not affected by paste", func(t *testing.T) {
		callerTarget, callerWindow, callerPanes := threePaneInputFixture(ctx, t)
		if err := callerWindow.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
			t.Fatal(err)
		}
		instance := mustInternalMCPServer(t, callerTarget)
		instance.tools.caller = callerIdentity{
			paneID: callerPanes[1].ID().String(),
			socket: resolvePath(callerTarget.SocketPath()),
			inside: true,
		}
		instance.tools.callerCached = true
		prompts, buffers := 0, 0
		setBuffer := instance.runtime.deps.setBuffer
		instance.runtime.deps.setBuffer = func(
			bufferCtx context.Context,
			server tmux.Server,
			buffer tmux.SetBufferRequest,
		) error {
			buffers++
			return setBuffer(bufferCtx, server, buffer)
		}
		client := connectInputTestClient(ctx, t, instance, &sdk.ClientOptions{
			ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
				prompts++
				return &sdk.ElicitResult{Action: "decline"}, nil
			},
		})
		pasted := callInputTool(ctx, t, client, "paste_text", map[string]any{
			"pane_id": callerPanes[0].ID().String(), "text": "peer-caller", "enter": true,
		})
		if pasted.IsError || prompts != 0 || buffers != 1 {
			t.Fatalf("non-target caller paste = (%t, prompts=%d, buffers=%d)",
				pasted.IsError, prompts, buffers)
		}
	})
}

//libtmux:real-tmux
func TestSendRechecksAfterCallerConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	instance.tools.caller = callerIdentity{
		paneID: panes[0].ID().String(),
		socket: resolvePath(target.SocketPath()),
		inside: true,
	}
	instance.tools.callerCached = true
	t.Cleanup(func() {
		_ = panes[0].CopyMode(context.Background(), tmux.CopyModeRequest{Cancel: true})
	})
	sends := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		sends++
		return nil
	}
	client := connectInputTestClient(ctx, t, instance, &sdk.ClientOptions{
		ElicitationHandler: func(promptCtx context.Context, _ *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			if err := panes[0].CopyMode(promptCtx, tmux.CopyModeRequest{}); err != nil {
				return nil, err
			}
			return &sdk.ElicitResult{Action: "accept"}, nil
		},
	})

	result := callInputTool(ctx, t, client, "send_keys", map[string]any{
		"pane_id": panes[0].ID().String(), "keys": []string{"C-l"},
	})
	if !result.IsError || sends != 0 {
		t.Fatalf("send after confirmation transition = (error %t, sends %d)", result.IsError, sends)
	}
}

func threePaneInputFixture(
	ctx context.Context,
	t *testing.T,
) (tmux.Server, tmux.Window, []tmux.Pane) {
	t.Helper()
	request := tmux.NewSessionRequest{Name: "work"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%v, %v)", sessions, err)
	}
	window, err := sessions[0].ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, ok, err := window.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%v, %t, %v)", source, ok, err)
	}
	second, err := source.Split(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	third, err := source.Split(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	panes := []tmux.Pane{source, second, third}
	for _, pane := range panes {
		tmuxtest.WaitForShellReady(ctx, t, pane)
	}
	return target, window, panes
}

func inputTestClient(
	ctx context.Context,
	t *testing.T,
	target tmux.Server,
	options *sdk.ClientOptions,
) *sdk.ClientSession {
	t.Helper()
	instance := mustInternalMCPServer(t, target)
	return connectInputTestClient(ctx, t, instance, options)
}

func connectInputTestClient(
	ctx context.Context,
	t *testing.T,
	instance *Instance,
	options *sdk.ClientOptions,
) *sdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "input-preflight-test", Version: "1"}, options)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callInputTool(
	ctx context.Context,
	t *testing.T,
	client *sdk.ClientSession,
	name string,
	arguments map[string]any,
) *sdk.CallToolResult {
	t.Helper()
	result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s CallTool() error = %v", name, err)
	}
	return result
}

func structuredMap(t *testing.T, result *sdk.CallToolResult) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func structuredStringSlice(t *testing.T, result *sdk.CallToolResult, field string) []string {
	t.Helper()
	return anyStringSlice(structuredMap(t, result)[field])
}

func anyStringSlice(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func structuredRows(t *testing.T, result *sdk.CallToolResult, field string) []map[string]any {
	t.Helper()
	values, _ := structuredMap(t, result)[field].([]any)
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if row, ok := value.(map[string]any); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

func paneIDs(panes []tmux.Pane) []string {
	ids := make([]string, 0, len(panes))
	for _, pane := range panes {
		ids = append(ids, pane.ID().String())
	}
	slices.Sort(ids)
	return ids
}

func allStringsPresent(text string, values []string) bool {
	for _, value := range values {
		if !strings.Contains(text, value) {
			return false
		}
	}
	return true
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Name()))
	}
	return names
}
