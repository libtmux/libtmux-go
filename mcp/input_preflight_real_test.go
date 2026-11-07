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
func TestSelectLayoutReusesNativeValidation(t *testing.T) {
	target := tmuxtest.NewServer(t.Context(), t)
	sessions, err := target.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	window, err := sessions[0].ResolveActiveWindow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := window.Layout()
	if !ok {
		t.Fatal("keeper window has no saved layout")
	}
	client := inputTestClient(t.Context(), t, target, nil)
	for _, value := range []string{saved, strings.ToUpper(saved[:4]) + saved[4:], "even-h"} {
		result := callInputTool(t.Context(), t, client, "select_layout", map[string]any{
			"window_id": window.ID().String(), "layout": value,
		})
		if result.IsError {
			t.Errorf("valid layout %q refused: %s", value, callToolResultText(result))
		}
	}
	result := callInputTool(t.Context(), t, client, "select_layout", map[string]any{
		"window_id": "@999999", "layout": "32d2,80x24,0,0{}",
	})
	if !result.IsError || !strings.Contains(callToolResultText(result), tmux.ErrInvalidServerCommandRequest.Error()) {
		t.Errorf("invalid syntax reached target lookup: %s", callToolResultText(result))
	}
	result = callInputTool(t.Context(), t, client, "select_layout", map[string]any{
		"window_id": "@999999", "layout": "even-h",
	})
	if !result.IsError || !strings.Contains(callToolResultText(result), "no window @999999") {
		t.Errorf("valid syntax did not preserve target lookup failure: %s", callToolResultText(result))
	}
	version, err := target.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mirrored, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	for value, wantError := range map[string]bool{
		"main-h":            version.AtLeast(mirrored),
		"main-horizontal-m": !version.AtLeast(mirrored),
	} {
		result := callInputTool(t.Context(), t, client, "select_layout", map[string]any{
			"window_id": window.ID().String(), "layout": value,
		})
		if result.IsError != wantError {
			t.Errorf("layout %q on %s: IsError = %t, want %t: %s",
				value, version, result.IsError, wantError, callToolResultText(result))
		}
	}
	if _, err := window.Refresh(t.Context()); err != nil {
		t.Fatalf("layout calls lost keeper identity: %v", err)
	}
}

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

// TestSendKeysEnterFlagSubmitsLiteralText pins that send_keys and
// send_keys_batch had no way to type text and submit it unambiguously.
// literal:true types "Enter" as five letters (tmux's own -l semantics), so a
// caller who put "Enter" in keys never ran the command; the enter field
// presses Enter as its own key, dispatched separately from the literal
// sequence, so it submits regardless of literal.
//
//libtmux:real-tmux
func TestSendKeysEnterFlagSubmitsLiteralText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	client := inputTestClient(ctx, t, target, nil)

	marker := "libtmux-go2-4-enter-single"
	result := callInputTool(ctx, t, client, "send_keys", map[string]any{
		"pane_id": panes[0].ID().String(),
		"keys":    []string{"echo " + marker},
		"literal": true,
		"enter":   true,
	})
	if result.IsError {
		t.Fatalf("send_keys(enter) refusal = %q", callToolResultText(result))
	}
	if sent := structuredMap(t, result)["sent"]; asFloat(sent) != 2 {
		t.Errorf("send_keys(enter) sent = %v, want 2 (the command plus the separate Enter)", sent)
	}
	tmuxtest.WaitForLine(ctx, t, panes[0], marker)

	batchMarker := "libtmux-go2-4-enter-batch"
	batch := callInputTool(ctx, t, client, "send_keys_batch", map[string]any{
		"operations": []map[string]any{
			{
				"pane_id": panes[1].ID().String(),
				"keys":    []string{"echo " + batchMarker},
				"literal": true,
				"enter":   true,
			},
		},
	})
	rows := structuredRows(t, batch, "results")
	if len(rows) != 1 || rows[0]["error"] != nil {
		t.Fatalf("send_keys_batch(enter) results = %#v", rows)
	}
	tmuxtest.WaitForLine(ctx, t, panes[1], batchMarker)
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
	tmuxtest.RequireVersion(ctx, t, target, "3.4")
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
		setPaneInputCallerEnvironment(t, callerTarget, callerPanes[0])
		loadCalls := 0
		instance.runtime.deps.setBuffer = func(
			context.Context,
			tmux.Server,
			tmux.SetBufferRequest,
		) error {
			loadCalls++
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
		if !declined.IsError || prompts != 1 || loadCalls != 0 {
			t.Fatalf("declined caller paste = (%t, prompts=%d, buffers=%d)",
				declined.IsError, prompts, loadCalls)
		}

		unaskable := connectInputTestClient(ctx, t, instance, nil)
		refused := callInputTool(ctx, t, unaskable, "paste_text", map[string]any{
			"pane_id": callerPanes[0].ID().String(), "text": "unaskable",
		})
		if !refused.IsError || loadCalls != 0 {
			t.Fatalf("unaskable caller paste = (%t, buffers=%d)", refused.IsError, loadCalls)
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
		setPaneInputCallerEnvironment(t, callerTarget, callerPanes[0])
		prompts, buffers := 0, 0
		setBuffer := instance.runtime.deps.setBuffer
		instance.runtime.deps.setBuffer = func(
			bufferCtx context.Context,
			server tmux.Server,
			request tmux.SetBufferRequest,
		) error {
			buffers++
			return setBuffer(bufferCtx, server, request)
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
		setPaneInputCallerEnvironment(t, callerTarget, callerPanes[1])
		prompts, buffers := 0, 0
		setBuffer := instance.runtime.deps.setBuffer
		instance.runtime.deps.setBuffer = func(
			bufferCtx context.Context,
			server tmux.Server,
			request tmux.SetBufferRequest,
		) error {
			buffers++
			return setBuffer(bufferCtx, server, request)
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
	setPaneInputCallerEnvironment(t, target, panes[0])
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

// inputRequestFixture serves a server running in the first pane of three, with
// the key presses it would send counted rather than delivered.
type inputRequestFixture struct {
	ctx      context.Context
	instance *Instance
	caller   tmux.Pane
	other    tmux.Pane
	socket   string
	sends    *int
}

func newInputRequestFixture(ctx context.Context, t *testing.T) inputRequestFixture {
	t.Helper()
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	setPaneInputCallerEnvironment(t, target, panes[0])
	sends := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		sends++
		return nil
	}
	return inputRequestFixture{
		ctx: ctx, instance: instance, caller: panes[0], other: panes[2],
		socket: target.SocketPath(), sends: &sends,
	}
}

// client connects at protocol 2026-07-28 and leaves each input-required result
// to the test. A server-initiated elicitation would reach the handler, which
// fails the test, because that protocol forbids one.
func (f inputRequestFixture) client(t *testing.T, elicits bool) *sdk.ClientSession {
	t.Helper()
	options := &sdk.ClientOptions{MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true}}
	if elicits {
		options.ElicitationHandler = func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			t.Error("the server sent elicitation/create on protocol 2026-07-28")
			return &sdk.ElicitResult{Action: "decline"}, nil
		}
	}
	session := connectInputTestClient(f.ctx, t, f.instance, options)
	if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("negotiated protocol = %q, want 2026-07-28", got)
	}
	return session
}

// call runs a tool, answering with responses when there are any.
func (f inputRequestFixture) call(
	t *testing.T,
	client *sdk.ClientSession,
	name string,
	arguments map[string]any,
	responses sdk.InputResponseMap,
) *sdk.CallToolResult {
	t.Helper()
	result, err := client.CallTool(f.ctx, &sdk.CallToolParams{
		Name: name, Arguments: arguments, InputResponses: responses,
	})
	if err != nil {
		t.Fatalf("%s CallTool() error = %v", name, err)
	}
	return result
}

// onlyRequest is the one question an input-required result asks, and its key.
func onlyRequest(t *testing.T, result *sdk.CallToolResult) (string, *sdk.ElicitParams) {
	t.Helper()
	if !result.NeedsInput() || len(result.InputRequests) != 1 {
		t.Fatalf("result = (needs input %t, %d requests, error %t, %q), want one question",
			result.NeedsInput(), len(result.InputRequests), result.IsError, callToolResultText(result))
	}
	for key, request := range result.InputRequests {
		params, ok := request.(*sdk.ElicitParams)
		if !ok {
			t.Fatalf("input request %q is %T, want elicitation", key, request)
		}
		return key, params
	}
	return "", nil
}

//libtmux:real-tmux
func TestCallerPaneAsksByInputRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	keys := func(f inputRequestFixture) map[string]any {
		return map[string]any{"pane_id": f.caller.ID().String(), "keys": []string{"C-l"}}
	}

	t.Run("accept lets the write through and can be remembered", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, true)
		asked := f.call(t, client, "send_keys", keys(f), nil)
		key, params := onlyRequest(t, asked)
		if *f.sends != 0 {
			t.Fatalf("keys sent before the person answered: %d", *f.sends)
		}
		if !strings.Contains(params.Message, f.caller.ID().String()) {
			t.Errorf("question = %q, want it to name %s", params.Message, f.caller.ID())
		}
		if !strings.HasPrefix(key, "consent-") ||
			strings.Contains(key, f.socket) || strings.Contains(key, "pane-input") {
			t.Errorf("input request key = %q, want an opaque one", key)
		}
		accepted := f.call(t, client, "send_keys", keys(f), sdk.InputResponseMap{
			key: &sdk.ElicitResult{Action: "accept", Content: map[string]any{"remember": true}},
		})
		if accepted.IsError || accepted.NeedsInput() || *f.sends != 1 {
			t.Fatalf("accepted write = (error %t, needs input %t, sends %d)",
				accepted.IsError, accepted.NeedsInput(), *f.sends)
		}
		remembered := f.call(t, client, "send_keys", keys(f), nil)
		if remembered.IsError || remembered.NeedsInput() || *f.sends != 2 {
			t.Fatalf("remembered write = (error %t, needs input %t, sends %d)",
				remembered.IsError, remembered.NeedsInput(), *f.sends)
		}
	})

	t.Run("decline refuses the write", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, true)
		key, _ := onlyRequest(t, f.call(t, client, "send_keys", keys(f), nil))
		declined := f.call(t, client, "send_keys", keys(f), sdk.InputResponseMap{
			key: &sdk.ElicitResult{Action: "decline"},
		})
		if !declined.IsError || !strings.Contains(callToolResultText(declined), "the person declined") ||
			*f.sends != 0 {
			t.Fatalf("declined write = (error %t, %q, sends %d)",
				declined.IsError, callToolResultText(declined), *f.sends)
		}
	})

	t.Run("a missing answer is asked for again", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, true)
		key, _ := onlyRequest(t, f.call(t, client, "send_keys", keys(f), nil))
		again, _ := onlyRequest(t, f.call(t, client, "send_keys", keys(f), sdk.InputResponseMap{
			"consent-something-else": &sdk.ElicitResult{Action: "accept"},
		}))
		if again != key || *f.sends != 0 {
			t.Fatalf("second ask = (%q, sends %d), want %q and no keys", again, *f.sends, key)
		}
	})

	t.Run("a client without elicitation is refused", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, false)
		refused := f.call(t, client, "send_keys", keys(f), nil)
		if !refused.IsError || refused.NeedsInput() ||
			!strings.Contains(callToolResultText(refused), "cannot be asked") || *f.sends != 0 {
			t.Fatalf("unaskable write = (error %t, needs input %t, %q, sends %d)",
				refused.IsError, refused.NeedsInput(), callToolResultText(refused), *f.sends)
		}
	})
}

//libtmux:real-tmux
func TestCallerPaneBatchAsksOnceBeforeAnythingRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	batch := func(f inputRequestFixture) map[string]any {
		return map[string]any{"operations": []map[string]any{
			{"pane_id": f.other.ID().String(), "keys": []string{"C-l"}},
			{"pane_id": f.caller.ID().String(), "keys": []string{"C-l"}},
			{"pane_id": f.caller.ID().String(), "keys": []string{"C-u"}},
		}}
	}

	t.Run("accept asks once, before the first operation", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, true)
		key, _ := onlyRequest(t, f.call(t, client, "send_keys_batch", batch(f), nil))
		if *f.sends != 0 {
			t.Fatalf("operations ran before the person answered: %d sends", *f.sends)
		}
		done := f.call(t, client, "send_keys_batch", batch(f), sdk.InputResponseMap{
			key: &sdk.ElicitResult{Action: "accept"},
		})
		if done.IsError || done.NeedsInput() || *f.sends != 3 {
			t.Fatalf("accepted batch = (error %t, needs input %t, sends %d), want all 3 run once",
				done.IsError, done.NeedsInput(), *f.sends)
		}
	})

	t.Run("decline runs only the operations that do not reach the caller", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, true)
		key, _ := onlyRequest(t, f.call(t, client, "send_keys_batch", batch(f), nil))
		declined := f.call(t, client, "send_keys_batch", batch(f), sdk.InputResponseMap{
			key: &sdk.ElicitResult{Action: "decline"},
		})
		rows := structuredRows(t, declined, "results")
		if declined.NeedsInput() || *f.sends != 1 || len(rows) != 2 ||
			!strings.Contains(fmt.Sprint(rows[1]["error"]), "the person declined") {
			t.Fatalf("declined batch = (needs input %t, sends %d, rows %v)",
				declined.NeedsInput(), *f.sends, rows)
		}
	})

	t.Run("a client without elicitation is refused per operation", func(t *testing.T) {
		f := newInputRequestFixture(ctx, t)
		client := f.client(t, false)
		refused := f.call(t, client, "send_keys_batch", batch(f), nil)
		rows := structuredRows(t, refused, "results")
		if refused.NeedsInput() || *f.sends != 1 || len(rows) != 2 ||
			!strings.Contains(fmt.Sprint(rows[1]["error"]), "cannot be asked") {
			t.Fatalf("unaskable batch = (needs input %t, sends %d, rows %v)",
				refused.NeedsInput(), *f.sends, rows)
		}
	})
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
	source, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatalf("ResolveActivePane() error = %v", err)
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
