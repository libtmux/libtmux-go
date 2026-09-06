package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
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

func TestAttendedPaneInputMembershipFailsClosed(t *testing.T) {
	strict := func(value string) rawPaneFormat {
		return rawPaneFormat{Value: value, Present: true}
	}
	valid := clientAttentionSnapshotRow{
		Control: strict("0"), PaneID: strict("%2"), Zoomed: strict("0"),
	}
	window := map[string]struct{}{"%2": {}, "%3": {}}

	attended, err := attendedPaneIDs([]clientAttentionSnapshotRow{valid}, window)
	if err != nil || !attended["%2"] || !attended["%3"] {
		t.Fatalf("visible window attendance = (%v, %v)", attended, err)
	}
	zoomed := valid
	zoomed.Zoomed = strict("1")
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{zoomed}, window)
	if err != nil || !attended["%2"] || attended["%3"] {
		t.Fatalf("zoomed attendance = (%v, %v)", attended, err)
	}
	control := valid
	control.Control = strict("1")
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{control}, window)
	if err != nil || len(attended) != 0 {
		t.Fatalf("control client attendance = (%v, %v)", attended, err)
	}

	for _, test := range []struct {
		name string
		row  clientAttentionSnapshotRow
	}{
		{name: "missing control", row: clientAttentionSnapshotRow{PaneID: strict("%2"), Zoomed: strict("0")}},
		{name: "malformed control", row: clientAttentionSnapshotRow{Control: strict("no"), PaneID: strict("%2"), Zoomed: strict("0")}},
		{name: "missing pane", row: clientAttentionSnapshotRow{Control: strict("0"), Zoomed: strict("0")}},
		{name: "noncanonical pane", row: clientAttentionSnapshotRow{Control: strict("0"), PaneID: strict("%02"), Zoomed: strict("0")}},
		{name: "missing zoom", row: clientAttentionSnapshotRow{Control: strict("0"), PaneID: strict("%2")}},
		{name: "malformed zoom", row: clientAttentionSnapshotRow{Control: strict("0"), PaneID: strict("%2"), Zoomed: strict("2")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := attendedPaneIDs([]clientAttentionSnapshotRow{test.row}, window); err == nil {
				t.Fatal("attendedPaneIDs() accepted an incomplete or malformed client row")
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
			InputOff:     rawPaneFormat{Value: "0", Present: true},
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
			name: "source on deduplicates stable pane ids", source: "%2",
			rows: []paneInputSnapshotRow{safe("%2", "1"), safe("%2", "1")},
			want: []string{"%2"},
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
			name: "included input-off peer refuses", source: "%2",
			rows: func() []paneInputSnapshotRow {
				peer := safe("%3", "1")
				peer.InputOff.Value = "1"
				return []paneInputSnapshotRow{safe("%2", "1"), peer}
			}(), errText: "input disabled",
		},
		{
			name: "included missing input flag refuses", source: "%2",
			rows: func() []paneInputSnapshotRow {
				peer := safe("%3", "1")
				peer.InputOff = rawPaneFormat{}
				return []paneInputSnapshotRow{safe("%2", "1"), peer}
			}(), errText: "pane_input_off",
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
	if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatal(err)
	}
	_, pasted, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "blocked", Enter: true,
	})
	if err == nil || setBufferCalls != 0 || pasted.Bytes != 0 {
		t.Fatalf("modal paste preflight = (%+v, %v, setBuffer=%d)", pasted, err, setBufferCalls)
	}
	if err := panes[0].CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := panes[0].Select(ctx, tmux.PaneSelectRequest{Input: tmux.PaneInputDisable}); err != nil {
		t.Fatal(err)
	}
	_, pasted, err = instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "disabled",
	})
	if err == nil || setBufferCalls != 0 || pasted.Bytes != 0 {
		t.Fatalf("input-off paste preflight = (%+v, %v, setBuffer=%d)", pasted, err, setBufferCalls)
	}
	if _, err := panes[0].Select(ctx, tmux.PaneSelectRequest{Input: tmux.PaneInputEnable}); err != nil {
		t.Fatal(err)
	}

	runRoot := t.TempDir()
	t.Setenv("TMPDIR", runRoot)
	if err := panes[0].SetOption(ctx, "synchronize-panes", "off", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	sendCalls := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
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

	cat := "exec /bin/cat"
	if _, err := panes[0].Respawn(ctx, tmux.RespawnRequest{
		Command: &cat, Kill: true,
	}); err != nil {
		t.Fatal(err)
	}
	instance.runtime.deps.beforeRunDispatch = func(context.Context) error { return nil }
	_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "POSIX-compatible pane shell") || sendCalls != 0 ||
		!slices.Equal(ran.ResolvedPaneIDs, []string{panes[0].ID().String()}) {
		t.Fatalf("unknown foreground run refusal = (%+v, %v, sends=%d)", ran, err, sendCalls)
	}

	fixedShell := "exec env ENV= PS1=" + shellQuote(tmuxtest.ShellPrompt) + " /bin/sh -i"
	respawned, err := panes[0].Respawn(ctx, tmux.RespawnRequest{
		Command: &fixedShell, Kill: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, respawned)
	defaults := defaultMCPDependencies()
	var sequences []tmux.SendKeySequenceRequest
	instance.runtime.deps.sendKeySequence = func(
		dispatchCtx context.Context,
		pane tmux.Pane,
		request tmux.SendKeySequenceRequest,
	) error {
		request.Keys = slices.Clone(request.Keys)
		sequences = append(sequences, request)
		return defaults.sendKeySequence(dispatchCtx, pane, request)
	}
	_, ran, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
		SuppressHistory: true,
	})
	if err != nil || ran.ExitStatus == nil || *ran.ExitStatus != 0 ||
		len(sequences) != 1 || len(sequences[0].Keys) != 2 || sequences[0].Keys[1] != "Enter" ||
		!strings.HasPrefix(sequences[0].Keys[0], " . ") {
		t.Fatalf("run dispatch = (%+v, %v, sequences=%#v)", ran, err, sequences)
	}
}

//libtmux:real-tmux
func TestRunCommandReservationIsSharedAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	firstInstance := mustInternalMCPServer(t, target)
	secondInstance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	entered := make(chan struct{})
	release := make(chan struct{})
	firstInstance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-barrierCtx.Done():
			return barrierCtx.Err()
		}
	}
	secondSetup := atomic.Int32{}
	secondInstance.runtime.deps.beforeRunDispatch = func(context.Context) error {
		secondSetup.Add(1)
		return errors.New("second instance reached setup")
	}
	first := make(chan error, 1)
	go func() {
		_, _, err := firstInstance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
		})
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, _, err := secondInstance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "earlier run_shell_command") ||
		secondSetup.Load() != 0 {
		t.Fatalf("cross-instance run = (%v, setup calls %d)", err, secondSetup.Load())
	}
	close(release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatalf("first run: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

//libtmux:real-tmux
func TestTimedOutRunKeepsThePaneReservedUntilCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

	_, timedOut, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "sleep 2", TimeoutSeconds: 1,
	})
	if err != nil || !timedOut.TimedOut {
		t.Fatalf("timed run = (%+v, %v)", timedOut, err)
	}
	_, _, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "earlier run_shell_command") {
		t.Fatalf("run during timed-out command = %v, want active-run refusal", err)
	}

	tmuxtest.WaitForShellReady(ctx, t, panes[0])
	requireRunCommandAvailable(ctx, t, instance, target, panes[0])
}

//libtmux:real-tmux
func TestDispatchedRunRetainsSetupUntilCompletion(t *testing.T) {
	for _, test := range []struct {
		name      string
		afterSend func(context.CancelFunc) error
		wantError error
	}{
		{name: "caller cancellation", afterSend: func(cancel context.CancelFunc) error {
			cancel()
			return nil
		}, wantError: context.Canceled},
		{name: "ambiguous delivery", afterSend: func(context.CancelFunc) error {
			return fmt.Errorf("injected ambiguous dispatch: %w", tmux.ErrOutcomeUnknown)
		}, wantError: tmux.ErrOutcomeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			target, _, panes := threePaneInputFixture(ctx, t)
			instance := mustInternalMCPServer(t, target)
			runCtx, cancelRun := context.WithCancel(ctx)
			defaults := defaultMCPDependencies()
			instance.runtime.deps.sendKeySequence = func(
				dispatchCtx context.Context,
				pane tmux.Pane,
				request tmux.SendKeySequenceRequest,
			) error {
				if err := defaults.sendKeySequence(dispatchCtx, pane, request); err != nil {
					return err
				}
				return test.afterSend(cancelRun)
			}
			marker := filepath.Join(t.TempDir(), "finished")
			callCtx := withAcquiredServer(runCtx, &runtimeAcquisition{server: target})
			_, _, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
				PaneID:  panes[0].ID().String(),
				Command: "sleep 1; printf done > " + shellQuote(marker), TimeoutSeconds: 5,
			})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("dispatch result = %v, want %v", err, test.wantError)
			}

			instance.runtime.deps.sendKeySequence = defaults.sendKeySequence
			freshCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
			_, _, err = instance.tools.runCommand(freshCtx, nil, runCommandInput{
				PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 1,
			})
			if err == nil || !strings.Contains(err.Error(), "earlier run_shell_command") {
				t.Fatalf("run after uncertain return = %v, want active-run refusal", err)
			}
			for {
				contents, readErr := os.ReadFile(marker)
				if readErr == nil {
					if string(contents) != "done" {
						t.Fatalf("marker = %q", contents)
					}
					break
				}
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
				select {
				case <-time.After(25 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			requireRunCommandAvailable(ctx, t, instance, target, panes[0])
		})
	}
}

func requireRunCommandAvailable(
	ctx context.Context,
	t *testing.T,
	instance *Instance,
	target tmux.Server,
	pane tmux.Pane,
) {
	t.Helper()
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	for {
		_, output, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
			PaneID: pane.ID().String(), Command: "true", TimeoutSeconds: 1,
		})
		if err == nil {
			if output.ExitStatus == nil || *output.ExitStatus != 0 {
				t.Fatalf("run after completion = %+v", output)
			}
			return
		}
		if !strings.Contains(err.Error(), "earlier run_shell_command") {
			t.Fatalf("run after completion = %v", err)
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

//libtmux:real-tmux
func TestRunCommandRepeatsCallerProtectionAtDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	runRoot := t.TempDir()
	t.Setenv("TMPDIR", runRoot)
	instance.tools.caller = callerIdentity{inside: false}
	instance.tools.callerCached = true
	instance.runtime.deps.beforeRunDispatch = func(context.Context) error {
		instance.tools.callerMutex.Lock()
		instance.tools.caller = callerIdentity{
			paneID: panes[0].ID().String(), socket: resolvePath(target.SocketPath()), inside: true,
		}
		instance.tools.callerMutex.Unlock()
		return nil
	}
	var dispatches atomic.Int32
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		dispatches.Add(1)
		return nil
	}
	client := connectInputTestClient(ctx, t, instance, nil)
	result := callInputTool(ctx, t, client, "run_shell_command", map[string]any{
		"pane_id": panes[0].ID().String(), "command": "true", "timeout": 5,
	})
	if !result.IsError || dispatches.Load() != 0 ||
		!strings.Contains(callToolResultText(result), "cannot be asked") {
		t.Fatalf("caller transition = (error %t, dispatches %d, text %q)",
			result.IsError, dispatches.Load(), callToolResultText(result))
	}
	if entries, err := os.ReadDir(runRoot); err != nil || len(entries) != 0 {
		t.Fatalf("caller-refused setup residue = (%v, %v)", entryNames(entries), err)
	}
}

//libtmux:real-tmux
func TestPasteEnterUsesOneTargetOnlyBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	staged := ""
	instance.runtime.deps.setBuffer = func(
		bufferCtx context.Context,
		server tmux.Server,
		request tmux.SetBufferRequest,
	) error {
		staged = request.Data
		return defaults.setBuffer(bufferCtx, server, request)
	}
	separateSends := 0
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		separateSends++
		return errors.New("separate Enter must not be sent")
	}

	result, output, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: ":", Enter: true,
	})
	if err != nil || result != nil || staged != ":\n" || separateSends != 0 ||
		output.Bytes != 1 {
		t.Fatalf(
			"paste = (result %#v, output %+v, error %v, staged %q, sends %d)",
			result, output, err, staged, separateSends,
		)
	}
}

//libtmux:real-tmux
func TestPasteRechecksTargetAfterBufferSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	var bufferName *string
	instance.runtime.deps.setBuffer = func(
		bufferCtx context.Context,
		server tmux.Server,
		request tmux.SetBufferRequest,
	) error {
		bufferName = request.Name
		if err := defaults.setBuffer(bufferCtx, server, request); err != nil {
			return err
		}
		return panes[0].CopyMode(bufferCtx, tmux.CopyModeRequest{})
	}
	t.Cleanup(func() {
		_ = panes[0].CopyMode(context.Background(), tmux.CopyModeRequest{Cancel: true})
	})

	_, output, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "must-not-paste",
	})
	if err == nil || output.Bytes != 0 {
		t.Fatalf("paste after mode transition = (%+v, %v), want refusal", output, err)
	}
	if bufferName == nil {
		t.Fatal("paste did not stage its private buffer before the transition")
	}
	if _, err := target.ShowBuffer(ctx, bufferName); err == nil {
		t.Fatalf("refused paste left buffer %q behind", *bufferName)
	}
}
