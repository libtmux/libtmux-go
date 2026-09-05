package mcp

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStrictPaneInputFormatsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   rawPaneFormat
		value bool
		ok    bool
	}{
		{name: "zero", raw: rawPaneFormat{Value: "0", Present: true}, ok: true},
		{name: "one", raw: rawPaneFormat{Value: "1", Present: true}, value: true, ok: true},
		{name: "missing", raw: rawPaneFormat{}},
		{name: "empty", raw: rawPaneFormat{Present: true}},
		{name: "negative", raw: rawPaneFormat{Value: "-1", Present: true}},
		{name: "two", raw: rawPaneFormat{Value: "2", Present: true}},
		{name: "whitespace", raw: rawPaneFormat{Value: " 0", Present: true}},
		{name: "word", raw: rawPaneFormat{Value: "off", Present: true}},
	} {
		t.Run("flag_"+test.name, func(t *testing.T) {
			got, err := parseStrictPaneFlag("pane_synchronized", test.raw)
			if (err == nil) != test.ok || got != test.value {
				t.Fatalf("parseStrictPaneFlag() = (%v, %v), want (%v, ok=%v)", got, err, test.value, test.ok)
			}
		})
	}

	for _, test := range []struct {
		name string
		raw  rawPaneFormat
		ok   bool
	}{
		{name: "zero", raw: rawPaneFormat{Value: "0", Present: true}, ok: true},
		{name: "double_zero", raw: rawPaneFormat{Value: "00", Present: true}},
		{name: "positive_zero", raw: rawPaneFormat{Value: "+0", Present: true}},
		{name: "one", raw: rawPaneFormat{Value: "1", Present: true}},
		{name: "large_positive", raw: rawPaneFormat{Value: "12", Present: true}},
		{name: "missing", raw: rawPaneFormat{}},
		{name: "negative", raw: rawPaneFormat{Value: "-1", Present: true}},
		{name: "word", raw: rawPaneFormat{Value: "copy-mode", Present: true}},
	} {
		t.Run("mode_"+test.name, func(t *testing.T) {
			err := requireSafePaneMode(test.raw)
			if (err == nil) != test.ok {
				t.Fatalf("requireSafePaneMode() error = %v, want ok=%v", err, test.ok)
			}
		})
	}
}

func TestConfiguredPaneInputMembership(t *testing.T) {
	safe := func(id, synchronized string) paneInputSnapshotRow {
		return paneInputSnapshotRow{
			PaneID:       id,
			Synchronized: rawPaneFormat{Value: synchronized, Present: true},
			Dead:         rawPaneFormat{Value: "0", Present: true},
			InMode:       rawPaneFormat{Value: "0", Present: true},
		}
	}
	broken := safe("%9", "broken")
	broken.Dead = rawPaneFormat{Value: "1", Present: true}
	broken.InMode = rawPaneFormat{Value: "2", Present: true}

	tests := []struct {
		name    string
		source  string
		rows    []paneInputSnapshotRow
		want    []string
		errText string
	}{
		{
			name: "source off ignores broken peers", source: "%2",
			rows: []paneInputSnapshotRow{broken, safe("%2", "0")}, want: []string{"%2"},
		},
		{
			name: "source on returns sorted on peers", source: "%2",
			rows: []paneInputSnapshotRow{safe("%10", "1"), safe("%2", "1"), safe("%1", "0")},
			want: []string{"%10", "%2"},
		},
		{
			name: "source on rejects missing peer sync", source: "%2",
			rows: []paneInputSnapshotRow{safe("%2", "1"), {PaneID: "%3"}}, errText: "pane_synchronized",
		},
		{
			name: "source on rejects malformed peer sync", source: "%2",
			rows: []paneInputSnapshotRow{safe("%2", "1"), safe("%3", "wat")}, errText: "pane_synchronized",
		},
		{
			name: "included modal peer refuses", source: "%2",
			rows: func() []paneInputSnapshotRow {
				peer := safe("%3", "1")
				peer.InMode.Value = "1"
				return []paneInputSnapshotRow{safe("%2", "1"), peer}
			}(), errText: "mode",
		},
		{
			name: "included dead peer refuses", source: "%2",
			rows: func() []paneInputSnapshotRow {
				peer := safe("%3", "1")
				peer.Dead.Value = "1"
				return []paneInputSnapshotRow{safe("%2", "1"), peer}
			}(), errText: "no process",
		},
		{
			name: "off modal dead peer is irrelevant", source: "%2",
			rows: func() []paneInputSnapshotRow {
				peer := safe("%3", "0")
				peer.Dead.Value = "1"
				peer.InMode.Value = "1"
				return []paneInputSnapshotRow{safe("%2", "1"), peer}
			}(), want: []string{"%2"},
		},
		{
			name: "missing source refuses", source: "%8",
			rows: []paneInputSnapshotRow{safe("%2", "0")}, errText: "fresh pane snapshot",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := configuredPaneInputMembership(test.source, test.rows)
			if test.errText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errText) {
					t.Fatalf("configuredPaneInputMembership() error = %v, want %q", err, test.errText)
				}
				if got == nil || len(got) != 0 {
					t.Fatalf("configuredPaneInputMembership() ids = %#v, want nonnil empty", got)
				}
				return
			}
			if err != nil || !slices.Equal(got, test.want) {
				t.Fatalf("configuredPaneInputMembership() = (%v, %v), want (%v, nil)", got, err, test.want)
			}
		})
	}
}

//libtmux:real-tmux
func TestPaneInputPreflightUsesOneFreshSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := panes[2].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputConfigured, "send_keys",
	)
	want := []string{panes[0].ID().String(), panes[1].ID().String()}
	slices.Sort(want)
	if err != nil || !slices.Equal(got.ConfiguredIDs, want) || len(got.Panes) != 2 ||
		got.Source.ID() != panes[0].ID() {
		t.Fatalf("configured preflight = (%+v, %v), want fresh %v", got, err, want)
	}
	if raw, ok := got.Source.Formats().Raw("pane_synchronized"); !ok || raw != "1" {
		t.Fatalf("fresh source pane_synchronized = (%q, %t), want (1, true)", raw, ok)
	}

	targetOnly, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputTargetOnly, "paste_text",
	)
	if err != nil || !slices.Equal(targetOnly.ConfiguredIDs, []string{panes[0].ID().String()}) ||
		len(targetOnly.Panes) != 1 {
		t.Fatalf("target-only preflight = (%+v, %v)", targetOnly, err)
	}

	if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatal(err)
	}
	failed, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputConfigured, "send_keys",
	)
	if err == nil || failed.ConfiguredIDs == nil || len(failed.ConfiguredIDs) != 0 {
		t.Fatalf("failed preflight = (%+v, %v), want nonnil empty ids and refusal", failed, err)
	}
}

//libtmux:real-tmux
func TestConfirmCallerInputPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	t.Setenv("TMUX", target.SocketPath()+",0,0")
	t.Setenv("TMUX_PANE", panes[1].ID().String())
	instance := mustInternalMCPServer(t, target)

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	prompts := 0
	client := sdk.NewClient(&sdk.Implementation{Name: "caller-preflight", Version: "1"}, &sdk.ClientOptions{
		ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			prompts++
			return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"remember": true}}, nil
		},
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	request := &sdk.CallToolRequest{Session: serverSession.sdk}
	preflight := paneInputPreflight{
		Panes: []tmux.Pane{panes[0], panes[1], panes[2]},
		ConfiguredIDs: []string{
			panes[0].ID().String(), panes[1].ID().String(), panes[2].ID().String(),
		},
	}
	if err := instance.tools.confirmCallerInputPreflight(
		ctx, request, preflight, "sending keys",
	); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("caller prompts = %d, want one configured-member prompt", prompts)
	}
	if err := instance.tools.confirmCallerInputPreflight(
		ctx, request, preflight, "sending keys",
	); err != nil || prompts != 1 {
		t.Fatalf("remembered caller confirmation = (%v, %d prompts)", err, prompts)
	}

	outside := paneInputPreflight{
		Panes: []tmux.Pane{panes[0]}, ConfiguredIDs: []string{panes[0].ID().String()},
	}
	if err := instance.tools.confirmCallerInputPreflight(
		ctx, request, outside, "sending keys",
	); err != nil || prompts != 1 {
		t.Fatalf("outside-caller confirmation = (%v, %d prompts)", err, prompts)
	}
}

//libtmux:real-tmux
func TestConfiguredMembershipResultPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	want := paneIDs(panes)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}

	sendFailure := errors.New("injected send sequence failure")
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		return sendFailure
	}
	_, sent, err := instance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if !errors.Is(err, sendFailure) || sent.Sent != 0 || sent.ResolvedPaneIDs == nil ||
		!slices.Equal(sent.ResolvedPaneIDs, want) {
		t.Fatalf("classified send failure = (%+v, %v), want sent=0 ids=%v", sent, err, want)
	}

	setBufferCalls := 0
	instance.runtime.deps.setBuffer = func(
		context.Context,
		tmux.Server,
		tmux.SetBufferRequest,
	) error {
		setBufferCalls++
		return nil
	}
	if err := panes[1].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatal(err)
	}
	_, pasted, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "blocked", Enter: true,
	})
	if err == nil || setBufferCalls != 0 || pasted.EnterPaneIDs == nil || len(pasted.EnterPaneIDs) != 0 {
		t.Fatalf("modal paste preflight = (%+v, %v, setBuffer=%d)", pasted, err, setBufferCalls)
	}
	if err := panes[1].CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatal(err)
	}
	defaults := defaultMCPDependencies()
	instance.runtime.deps.setBuffer = defaults.setBuffer
	enterFailure := errors.New("injected Enter failure")
	instance.runtime.deps.sendKeys = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeysRequest,
	) error {
		return enterFailure
	}
	result, pasted, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "partial-paste", Enter: true,
	})
	if err != nil || result == nil || !result.IsError || pasted.Bytes != len("partial-paste") ||
		!slices.Equal(pasted.EnterPaneIDs, want) {
		t.Fatalf("partial Enter failure = (%#v, %+v, %v), want ids=%v", result, pasted, err, want)
	}

	runRoot := t.TempDir()
	t.Setenv("TMPDIR", runRoot)
	if err := panes[0].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	sendCalls := 0
	instance.runtime.deps.sendKeys = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeysRequest,
	) error {
		sendCalls++
		return nil
	}
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		return panes[0].UnsetOption(
			barrierCtx, "synchronize-panes", tmux.UnsetOptionOptions{},
		)
	}
	_, ran, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || sendCalls != 0 || !slices.Equal(ran.ResolvedPaneIDs, want) {
		t.Fatalf("second classified run refusal = (%+v, %v, sends=%d), want ids=%v", ran, err, sendCalls, want)
	}
	if entries, readErr := os.ReadDir(runRoot); readErr != nil || len(entries) != 0 {
		t.Fatalf("run setup cleanup = (%v, %v)", entryNames(entries), readErr)
	}

	if err := panes[0].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	barrierFailure := errors.New("injected run dispatch barrier failure")
	instance.runtime.deps.beforeRunDispatch = func(context.Context) error { return barrierFailure }
	_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if !errors.Is(err, barrierFailure) || sendCalls != 0 ||
		!slices.Equal(ran.ResolvedPaneIDs, []string{panes[0].ID().String()}) {
		t.Fatalf("run barrier failure = (%+v, %v, sends=%d)", ran, err, sendCalls)
	}
	if entries, readErr := os.ReadDir(runRoot); readErr != nil || len(entries) != 0 {
		t.Fatalf("barrier setup cleanup = (%v, %v)", entryNames(entries), readErr)
	}

	if err := panes[0].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		if err := panes[0].UnsetOption(
			barrierCtx, "synchronize-panes", tmux.UnsetOptionOptions{},
		); err != nil {
			return err
		}
		return panes[1].CopyMode(barrierCtx, tmux.CopyModeRequest{})
	}
	_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || sendCalls != 0 ||
		!slices.Equal(ran.ResolvedPaneIDs, []string{panes[0].ID().String()}) {
		t.Fatalf("second modal run refusal = (%+v, %v, sends=%d)", ran, err, sendCalls)
	}
	if err := panes[1].CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatal(err)
	}

	if err := panes[0].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		fish := "exec /usr/bin/fish"
		_, respawnErr := panes[0].Respawn(barrierCtx, tmux.RespawnRequest{
			Command: &fish, Kill: true,
		})
		return respawnErr
	}
	_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "fish") || sendCalls != 0 ||
		!slices.Equal(ran.ResolvedPaneIDs, []string{panes[0].ID().String()}) {
		t.Fatalf("second shell run refusal = (%+v, %v, sends=%d)", ran, err, sendCalls)
	}
}
