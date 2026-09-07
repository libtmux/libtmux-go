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

func withoutCallerEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func setPaneInputCallerEnvironment(t *testing.T, server tmux.Server, pane tmux.Pane) {
	t.Helper()
	pid, present := pane.Formats().Raw("pid")
	if !present {
		t.Fatal("pane snapshot has no server pid")
	}
	t.Setenv("TMUX", server.SocketPath()+","+pid+","+
		strings.TrimPrefix(pane.SessionID().String(), "$"))
	t.Setenv("TMUX_PANE", pane.ID().String())
}

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
		Control: strict("0"), SessionID: strict("$1"), WindowID: strict("@1"),
		WindowIndex: strict("0"), PaneID: strict("%2"), Zoomed: strict("0"),
	}
	panes := []paneInputPlacement{
		{SessionID: "$1", WindowID: "@1", WindowIndex: 0, PaneID: "%2"},
		{SessionID: "$1", WindowID: "@1", WindowIndex: 0, PaneID: "%3"},
		{SessionID: "$1", WindowID: "@2", WindowIndex: 1, PaneID: "%4"},
	}

	attended, err := attendedPaneIDs([]clientAttentionSnapshotRow{valid}, panes, "@1")
	if err != nil || !attended["%2"] || !attended["%3"] {
		t.Fatalf("visible window attendance = (%v, %v)", attended, err)
	}
	zoomed := valid
	zoomed.Zoomed = strict("1")
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{zoomed}, panes, "@1")
	if err != nil || !attended["%2"] || attended["%3"] {
		t.Fatalf("zoomed attendance = (%v, %v)", attended, err)
	}
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{{
		Control: strict("1"),
	}}, panes, "@1")
	if err != nil || len(attended) != 0 {
		t.Fatalf("control client attendance = (%v, %v)", attended, err)
	}
	otherWindow := valid
	otherWindow.WindowID = strict("@2")
	otherWindow.WindowIndex = strict("1")
	otherWindow.PaneID = strict("%4")
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{otherWindow}, panes, "@1")
	if err != nil || len(attended) != 0 {
		t.Fatalf("other-window attendance = (%v, %v)", attended, err)
	}
	linked := valid
	linked.SessionID = strict("$2")
	linked.WindowIndex = strict("7")
	linkedPanes := append(slices.Clone(panes),
		paneInputPlacement{SessionID: "$2", WindowID: "@1", WindowIndex: 7, PaneID: "%2"},
		paneInputPlacement{SessionID: "$2", WindowID: "@1", WindowIndex: 7, PaneID: "%3"},
	)
	attended, err = attendedPaneIDs([]clientAttentionSnapshotRow{linked}, linkedPanes, "@1")
	if err != nil || !attended["%2"] || !attended["%3"] {
		t.Fatalf("linked-window attendance = (%v, %v)", attended, err)
	}

	for _, test := range []struct {
		name string
		row  clientAttentionSnapshotRow
	}{
		{name: "missing control", row: clientAttentionSnapshotRow{}},
		{name: "malformed control", row: clientAttentionSnapshotRow{Control: strict("no")}},
		{name: "missing session", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.SessionID = rawPaneFormat{} })},
		{name: "noncanonical session", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.SessionID = strict("$01") })},
		{name: "missing window", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.WindowID = rawPaneFormat{} })},
		{name: "noncanonical window", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.WindowID = strict("@01") })},
		{name: "missing window index", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.WindowIndex = rawPaneFormat{} })},
		{name: "noncanonical window index", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.WindowIndex = strict("01") })},
		{name: "missing pane", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.PaneID = rawPaneFormat{} })},
		{name: "noncanonical pane", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.PaneID = strict("%02") })},
		{name: "missing zoom", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.Zoomed = rawPaneFormat{} })},
		{name: "malformed zoom", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.Zoomed = strict("2") })},
		{name: "zoomed unknown pane", row: mutateClientAttention(zoomed, func(row *clientAttentionSnapshotRow) { row.PaneID = strict("%9") })},
		{name: "session mismatch", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.SessionID = strict("$2") })},
		{name: "window mismatch", row: mutateClientAttention(valid, func(row *clientAttentionSnapshotRow) { row.WindowID = strict("@2") })},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := attendedPaneIDs(
				[]clientAttentionSnapshotRow{test.row}, panes, "@1",
			); err == nil {
				t.Fatal("attendedPaneIDs() accepted an incomplete or malformed client row")
			}
		})
	}
}

func mutateClientAttention(
	row clientAttentionSnapshotRow,
	mutate func(*clientAttentionSnapshotRow),
) clientAttentionSnapshotRow {
	mutate(&row)
	return row
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

func TestPaneInputReservationsUseFullDaemonGenerations(t *testing.T) {
	coordinator := paneInputCoordinator{}
	first := []paneInputIdentity{
		{endpointID: 1, serverPID: 101, serverStartTime: 456, paneID: "%1"},
		{endpointID: 1, serverPID: 101, serverStartTime: 456, paneID: "%2"},
	}
	lease, err := coordinator.acquire(first, paneInputReservationRun, "run_shell_command")
	if err != nil {
		t.Fatal(err)
	}
	if !coordinator.owns(lease, first) {
		t.Fatal("coordinator does not recognize its full-cohort owner")
	}
	if _, err := coordinator.acquire(first[1:], paneInputReservationInput, "send_keys"); err == nil {
		t.Fatal("overlapping input acquired an active run pane")
	}
	for _, distinct := range []paneInputIdentity{
		{endpointID: 2, serverPID: 101, serverStartTime: 456, paneID: "%1"},
		{endpointID: 1, serverPID: 102, serverStartTime: 456, paneID: "%1"},
		{endpointID: 1, serverPID: 101, serverStartTime: 457, paneID: "%1"},
		{endpointID: 1, serverPID: 101, serverStartTime: 456, paneID: "%3"},
	} {
		other, err := coordinator.acquire(
			[]paneInputIdentity{distinct}, paneInputReservationInput, "send_keys",
		)
		if err != nil {
			t.Fatalf("distinct identity %#v conflicted: %v", distinct, err)
		}
		coordinator.release(other)
	}
	changed := slices.Clone(first)
	changed[0].serverStartTime++
	if coordinator.owns(lease, changed) {
		t.Fatal("lease covered a changed daemon generation")
	}
	coordinator.release(lease)
	if _, err := coordinator.acquire(first[1:], paneInputReservationInput, "send_keys"); err != nil {
		t.Fatalf("released pane remained reserved: %v", err)
	}
}

//libtmux:real-tmux
func TestPaneInputReservationsCanonicalizeSocketAliases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	base := mustInternalMCPServer(t, target)
	baseCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	basePreflight, err := base.tools.preflightPaneInput(
		baseCtx, panes[0].ID().String(), "", paneInputTargetOnly, "paste_text",
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		link func(string, string) error
	}{
		{name: "symlink", link: os.Symlink},
		{name: "hard link", link: os.Link},
	} {
		t.Run(test.name, func(t *testing.T) {
			alias := filepath.Join(t.TempDir(), "tmux.sock")
			if err := test.link(target.SocketPath(), alias); err != nil {
				t.Fatal(err)
			}
			aliasTarget, err := target.WithSocketPath(alias)
			if err != nil {
				t.Fatal(err)
			}
			aliased := mustInternalMCPServer(t, aliasTarget)
			aliasCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: aliasTarget})
			aliasPreflight, err := aliased.tools.preflightPaneInput(
				aliasCtx, panes[0].ID().String(), "", paneInputTargetOnly, "paste_text",
			)
			if err != nil {
				t.Fatalf("aliased socket preflight: %v", err)
			}

			coordinator := paneInputCoordinator{}
			lease, err := coordinator.acquire(
				basePreflight.Identities(), paneInputReservationInput, "send_keys",
			)
			if err != nil {
				t.Fatal(err)
			}
			defer coordinator.release(lease)
			if _, err := coordinator.acquire(
				aliasPreflight.Identities(), paneInputReservationInput, "paste_text",
			); err == nil {
				t.Fatal("physical socket alias acquired an active pane")
			}
		})
	}
}

func TestCommandRunDisappearanceIsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "pane absent", err: fmt.Errorf("lookup: %w", tmux.ErrSnapshotNotFound), want: true},
		{name: "daemon unreachable", err: fmt.Errorf("probe: %w", tmux.ErrNoServer)},
		{name: "daemon replaced", err: fmt.Errorf("probe: %w", tmux.ErrDaemonReplaced), want: true},
		{name: "ambiguous outcome", err: tmux.ErrOutcomeUnknown},
		{name: "cancelled probe", err: context.Canceled},
		{name: "transport failure", err: errors.New("injected transport failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := commandRunDisappeared(test.err); got != test.want {
				t.Fatalf("commandRunDisappeared(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

//libtmux:real-tmux
func TestCommandRunPresenceTreatsDeadPaneAsEnded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	_, _, panes := threePaneInputFixture(ctx, t)
	pane := panes[0]
	if err := pane.SetRemainOnExit(ctx, tmux.RemainOnExitOn); err != nil {
		t.Fatal(err)
	}
	command := "exit 7"
	if _, err := pane.Respawn(ctx, tmux.RespawnRequest{Command: &command, Kill: true}); err != nil {
		t.Fatal(err)
	}
	for {
		refreshed, err := pane.Refresh(ctx)
		if err != nil {
			t.Fatal(err)
		}
		dead, present := refreshed.Formats().PaneDead()
		if present && dead {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	present, err := commandRunPresent(ctx, pane, paneInputIdentity{
		endpointID: 1, serverPID: 1, serverStartTime: 1, paneID: pane.ID().String(),
	})
	if err != nil || present {
		t.Fatalf("dead pane presence = (%t, %v), want ended", present, err)
	}
}

func TestRetainedRunRequiresAuthenticatedCompletion(t *testing.T) {
	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	cancelRuntime()
	directory := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	closedAt := filepath.Join(directory, "closed")
	statusAt := filepath.Join(directory, "status")
	if err := os.WriteFile(closedAt, []byte("0 0 0 80 24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusAt, []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := paneInputIdentity{
		endpointID: 999_001, serverPID: 999_002,
		serverStartTime: 999_003, paneID: "%999",
	}
	lease, err := processPaneInputs.acquire(
		[]paneInputIdentity{identity}, paneInputReservationRun, "run_shell_command",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { processPaneInputs.release(lease) })
	registry := &tools{runtime: &tmuxRuntime{ctx: runtimeCtx}}
	registry.reapCommandRun(commandRun{
		directory: directory, closedAt: closedAt, statusAt: statusAt,
		identity: identity, lease: lease,
	})

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		unexpected, acquireErr := processPaneInputs.acquire(
			[]paneInputIdentity{identity}, paneInputReservationInput, "send_keys",
		)
		if acquireErr == nil {
			processPaneInputs.release(unexpected)
			t.Fatal("instance cancellation or invalid status released retained run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(statusAt, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		available, acquireErr := processPaneInputs.acquire(
			[]paneInputIdentity{identity}, paneInputReservationInput, "send_keys",
		)
		if acquireErr == nil {
			processPaneInputs.release(available)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("valid completion did not release retained run")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

//libtmux:real-tmux
func TestPaneInputPreflightUsesOneFreshSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
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
	if got.Identity.endpoint != resolvePath(target.SocketPath()) ||
		got.Identity.endpointID == 0 || got.Identity.serverPID == 0 ||
		got.Identity.serverStartTime == 0 ||
		got.Caller.state != paneInputCallerDetached ||
		!slices.Equal(got.Identities(), []paneInputIdentity{
			{
				endpointID: got.Identity.endpointID, serverPID: got.Identity.serverPID,
				serverStartTime: got.Identity.serverStartTime, paneID: want[0],
			},
			{
				endpointID: got.Identity.endpointID, serverPID: got.Identity.serverPID,
				serverStartTime: got.Identity.serverStartTime, paneID: want[1],
			},
		}) {
		t.Fatalf("configured preflight identity = %+v", got)
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
func TestPaneInputPreflightAuthenticatesLinkedTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, window, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	want := paneIDs(panes)

	initial, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputConfigured, "send_keys",
	)
	if err != nil {
		t.Fatal(err)
	}
	linkedSession, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "linked"})
	if err != nil {
		t.Fatal(err)
	}
	linkedIndex := 7
	if err := window.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: linkedSession.ID(), TargetIndex: &linkedIndex, Detach: true,
	}); err != nil {
		t.Fatal(err)
	}

	linked, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputConfigured, "send_keys",
	)
	if err != nil {
		t.Fatalf("linked pane preflight = %v", err)
	}
	if linked.Source.ID() != panes[0].ID() ||
		!slices.Equal(linked.ConfiguredIDs, want) {
		t.Fatalf("linked pane preflight = %+v", linked)
	}
	if samePaneInputPreflight(initial, linked) {
		t.Fatal("linked topology change preserved the pane input signature")
	}
}

//libtmux:real-tmux
func TestPaneInputSourceComesFromGuardSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	if _, err := panes[0].Select(ctx, tmux.PaneSelectRequest{}); err != nil {
		t.Fatal(err)
	}
	instance := mustInternalMCPServer(t, target)
	defaults := defaultMCPDependencies()
	instance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		if _, err := panes[1].Select(snapshotCtx, tmux.PaneSelectRequest{}); err != nil {
			return tmux.Snapshot{}, err
		}
		return defaults.snapshot(snapshotCtx, server)
	}
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	preflight, err := instance.tools.preflightPaneInput(
		callCtx, "", "work", paneInputConfigured, "send_keys",
	)
	if err != nil || preflight.Source.ID() != panes[1].ID() {
		t.Fatalf("guard-snapshot source = (%s, %v), want %s",
			preflight.Source.ID(), err, panes[1].ID())
	}
}

//libtmux:real-tmux
func TestConfirmCallerInputPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target, window, panes := threePaneInputFixture(ctx, t)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	serverPID, ok := panes[1].Formats().Raw("pid")
	if !ok {
		t.Fatal("pane snapshot has no server pid")
	}
	t.Setenv("TMUX", target.SocketPath()+","+serverPID+","+
		strings.TrimPrefix(panes[1].SessionID().String(), "$"))
	t.Setenv("TMUX_PANE", panes[1].ID().String())
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	preflight, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputConfigured, "send_keys",
	)
	if err != nil || preflight.Caller.state != paneInputCallerSelected {
		t.Fatalf("selected caller preflight = (%+v, %v)", preflight, err)
	}
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("TMUX_PANE"); err != nil {
		t.Fatal(err)
	}

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
	changedGeneration := preflight
	changedGeneration.Caller.serverStartTime++
	if err := instance.tools.confirmCallerInputPreflight(
		ctx, request, changedGeneration, "sending keys",
	); err != nil || prompts != 2 {
		t.Fatalf("changed-generation confirmation = (%v, %d prompts)", err, prompts)
	}

	outside := paneInputPreflight{
		Panes: []tmux.Pane{panes[0]}, ConfiguredIDs: []string{panes[0].ID().String()},
		Caller: preflight.Caller,
	}
	if err := instance.tools.confirmCallerInputPreflight(
		ctx, request, outside, "sending keys",
	); err != nil || prompts != 2 {
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
	// The refusal keys off the foreground command's basename, not off any real
	// shell, so name a binary that exists everywhere `fish` rather than
	// depending on fish being installed. `cat` also keeps the pane alive and
	// reading keys, which is the state this case is about.
	fishDir := t.TempDir()
	if err := os.Symlink("/bin/cat", filepath.Join(fishDir, "fish")); err != nil {
		t.Fatal(err)
	}
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		fish := "exec " + filepath.Join(fishDir, "fish")
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
	var sendCalls atomic.Int32
	secondInstance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		sendCalls.Add(1)
		return nil
	}
	_, _, sendErr := secondInstance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if sendErr == nil || !strings.Contains(sendErr.Error(), "earlier run_shell_command") ||
		sendCalls.Load() != 0 {
		t.Fatalf("send during run = (%v, dispatches %d)", sendErr, sendCalls.Load())
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
func TestPaneInputRefusesPlacementTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, window, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	var snapshots, dispatches atomic.Int32
	instance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		if snapshots.Add(1) == 2 {
			index := 7
			if _, err := window.Move(snapshotCtx, tmux.MoveWindowRequest{
				TargetIndex: &index,
			}); err != nil {
				return tmux.Snapshot{}, err
			}
		}
		return defaults.snapshot(snapshotCtx, server)
	}
	instance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		dispatches.Add(1)
		return nil
	}

	_, _, err := instance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if err == nil || dispatches.Load() != 0 {
		t.Fatalf("placement transition = (%v, dispatches %d), want refusal",
			err, dispatches.Load())
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
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	runRoot := t.TempDir()
	t.Setenv("TMPDIR", runRoot)
	serverPID, ok := panes[0].Formats().Raw("pid")
	if !ok {
		t.Fatal("pane snapshot has no server pid")
	}
	callerTMUX := target.SocketPath() + "," + serverPID + "," +
		strings.TrimPrefix(panes[0].SessionID().String(), "$")
	instance.runtime.deps.beforeRunDispatch = func(context.Context) error {
		t.Setenv("TMUX", callerTMUX)
		t.Setenv("TMUX_PANE", panes[0].ID().String())
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
		!strings.Contains(callToolResultText(result), "caller changed") {
		t.Fatalf("caller transition = (error %t, dispatches %d, text %q)",
			result.IsError, dispatches.Load(), callToolResultText(result))
	}
	if entries, err := os.ReadDir(runRoot); err != nil || len(entries) != 0 {
		t.Fatalf("caller-refused setup residue = (%v, %v)", entryNames(entries), err)
	}
}

//libtmux:real-tmux
func TestRunCommandRefusesShellIdentityTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		command := "exec env PS1=" + shellQuote(tmuxtest.ShellPrompt) +
			" /bin/bash --noprofile --norc -i"
		pane, err := panes[0].Respawn(barrierCtx, tmux.RespawnRequest{
			Command: &command, Kill: true,
		})
		if err != nil {
			return err
		}
		tmuxtest.WaitForShellReady(barrierCtx, t, pane)
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

	_, _, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "state or placement changed") ||
		dispatches.Load() != 0 {
		t.Fatalf("shell transition = (%v, dispatches %d), want refusal",
			err, dispatches.Load())
	}
}

//libtmux:real-tmux
func TestRunCommandRefusesAnIdenticalShellReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	// The replacement runs the same program the fixture started, so
	// pane_current_command is unchanged across the two checkpoints and only the
	// pane's process tells the shells apart.
	instance.runtime.deps.beforeRunDispatch = func(barrierCtx context.Context) error {
		command := "ENV= PS1=" + shellQuote(tmuxtest.ShellPrompt) + " /bin/sh -i"
		pane, err := panes[0].Respawn(barrierCtx, tmux.RespawnRequest{
			Command: &command, Kill: true,
		})
		if err != nil {
			return err
		}
		tmuxtest.WaitForShellReady(barrierCtx, t, pane)
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

	_, _, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "state or placement changed") ||
		dispatches.Load() != 0 {
		t.Fatalf("identical shell replacement = (%v, dispatches %d), want refusal",
			err, dispatches.Load())
	}
}

//libtmux:real-tmux
func TestSendReservationIsSharedAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	firstInstance := mustInternalMCPServer(t, target)
	secondInstance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	entered := make(chan struct{})
	release := make(chan struct{})
	firstInstance.runtime.deps.sendKeySequence = func(
		dispatchCtx context.Context,
		_ tmux.Pane,
		_ tmux.SendKeySequenceRequest,
	) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-dispatchCtx.Done():
			return dispatchCtx.Err()
		}
	}
	first := make(chan error, 1)
	go func() {
		_, _, err := firstInstance.tools.sendKeysBatch(
			callCtx, nil,
			sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
			"send_keys",
		)
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var secondDispatches atomic.Int32
	secondInstance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		secondDispatches.Add(1)
		return nil
	}
	_, _, err := secondInstance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if err == nil || !strings.Contains(err.Error(), "pane-input dispatch") ||
		secondDispatches.Load() != 0 {
		t.Fatalf("overlapping send = (%v, dispatches %d)", err, secondDispatches.Load())
	}
	close(release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

//libtmux:real-tmux
func TestSendReservationCoversFinalPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	firstInstance := mustInternalMCPServer(t, target)
	secondInstance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	var firstDispatches, secondDispatches atomic.Int32
	firstInstance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		firstDispatches.Add(1)
		return nil
	}
	secondInstance.runtime.deps.sendKeySequence = func(
		context.Context,
		tmux.Pane,
		tmux.SendKeySequenceRequest,
	) error {
		secondDispatches.Add(1)
		return nil
	}
	var snapshots atomic.Int32
	var competingErr error
	firstInstance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		snapshot, err := defaults.snapshot(snapshotCtx, server)
		if err == nil && snapshots.Add(1) == 2 {
			_, _, competingErr = secondInstance.tools.sendKeysBatch(
				callCtx, nil,
				sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
				"send_keys",
			)
		}
		return snapshot, err
	}

	_, _, err := firstInstance.tools.sendKeysBatch(
		callCtx, nil,
		sendKeysBatchInput{PaneID: panes[0].ID().String(), Keys: []string{"C-l"}},
		"send_keys",
	)
	if err != nil || competingErr == nil ||
		!strings.Contains(competingErr.Error(), "pane-input dispatch") ||
		firstDispatches.Load() != 1 || secondDispatches.Load() != 0 {
		t.Fatalf("send gap = (first %v/%d, competing %v/%d)",
			err, firstDispatches.Load(), competingErr, secondDispatches.Load())
	}
}

//libtmux:real-tmux
func TestRunCommandUsesExactlyTwoFullPreflights(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	var snapshots atomic.Int32
	instance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		snapshots.Add(1)
		return defaults.snapshot(snapshotCtx, server)
	}
	instance.runtime.deps.beforeRunDispatch = func(context.Context) error {
		if got := snapshots.Load(); got != 1 {
			return fmt.Errorf("setup saw %d full checks, want 1", got)
		}
		return nil
	}
	instance.runtime.deps.sendKeySequence = func(
		dispatchCtx context.Context,
		pane tmux.Pane,
		request tmux.SendKeySequenceRequest,
	) error {
		if got := snapshots.Load(); got != 2 {
			return fmt.Errorf("dispatch saw %d full checks, want 2", got)
		}
		return defaults.sendKeySequence(dispatchCtx, pane, request)
	}

	_, output, err := instance.tools.runCommand(callCtx, nil, runCommandInput{
		PaneID: panes[0].ID().String(), Command: "true", TimeoutSeconds: 5,
	})
	if err != nil || output.ExitStatus == nil || *output.ExitStatus != 0 ||
		snapshots.Load() != 2 {
		t.Fatalf("two-check run = (%+v, %v, snapshots %d)", output, err, snapshots.Load())
	}
}

//libtmux:real-tmux
func TestUncertainRunReleasesOnAuthenticatedDisappearance(t *testing.T) {
	for _, test := range []struct {
		name      string
		disappear func(context.Context, tmux.Server, tmux.Pane) error
	}{
		{name: "pane", disappear: func(ctx context.Context, _ tmux.Server, pane tmux.Pane) error {
			return pane.Kill(ctx)
		}},
		{name: "daemon generation", disappear: func(ctx context.Context, server tmux.Server, _ tmux.Pane) error {
			return server.Kill(ctx)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			withoutCallerEnvironment(t)
			target, _, panes := threePaneInputFixture(ctx, t)
			instance := mustInternalMCPServer(t, target)
			callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
			preflight, err := instance.tools.preflightPaneInput(
				callCtx, panes[0].ID().String(), "", paneInputConfigured, "run_shell_command",
			)
			if err != nil {
				t.Fatal(err)
			}
			defaults := defaultMCPDependencies()
			instance.runtime.deps.sendKeySequence = func(
				dispatchCtx context.Context,
				pane tmux.Pane,
				request tmux.SendKeySequenceRequest,
			) error {
				if err := defaults.sendKeySequence(dispatchCtx, pane, request); err != nil {
					return err
				}
				return tmux.ErrOutcomeUnknown
			}
			_, _, err = instance.tools.runCommand(callCtx, nil, runCommandInput{
				PaneID: panes[0].ID().String(), Command: "sleep 30", TimeoutSeconds: 5,
			})
			if !errors.Is(err, tmux.ErrOutcomeUnknown) {
				t.Fatalf("ambiguous dispatch = %v", err)
			}
			if err := test.disappear(ctx, target, panes[0]); err != nil {
				t.Fatal(err)
			}
			for {
				lease, acquireErr := processPaneInputs.acquire(
					preflight.Identities(), paneInputReservationRun, "run_shell_command",
				)
				if acquireErr == nil {
					processPaneInputs.release(lease)
					break
				}
				if !strings.Contains(acquireErr.Error(), "earlier run_shell_command") {
					t.Fatal(acquireErr)
				}
				select {
				case <-time.After(50 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}

//libtmux:real-tmux
func TestPasteEnterUsesOneTargetOnlyBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, window, panes := threePaneInputFixture(ctx, t)
	if err := window.SetOption(ctx, "synchronize-panes", "on", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	var buffers, pastes atomic.Int32
	var staged string
	instance.runtime.deps.setBuffer = func(
		bufferCtx context.Context,
		server tmux.Server,
		request tmux.SetBufferRequest,
	) error {
		buffers.Add(1)
		staged = request.Data
		return defaults.setBuffer(bufferCtx, server, request)
	}
	instance.runtime.deps.pasteBuffer = func(
		pasteCtx context.Context,
		pane tmux.Pane,
		request tmux.PasteBufferRequest,
	) error {
		pastes.Add(1)
		return defaults.pasteBuffer(pasteCtx, pane, request)
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

	for index, test := range []struct {
		name       string
		text       string
		wantStaged string
		wantBytes  int
	}{
		{name: "text", text: ":", wantStaged: ":\n", wantBytes: 1},
		{name: "empty", wantStaged: "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, output, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
				PaneID: panes[0].ID().String(), Text: test.text, Enter: true,
			})
			wantCalls := int32(index + 1)
			if err != nil || result != nil || staged != test.wantStaged ||
				separateSends != 0 || output.Bytes != test.wantBytes ||
				buffers.Load() != wantCalls || pastes.Load() != wantCalls {
				t.Fatalf(
					"paste = (result %#v, output %+v, error %v, staged %q, buffers %d, pastes %d, sends %d)",
					result, output, err, staged, buffers.Load(), pastes.Load(), separateSends,
				)
			}
		})
	}
}

//libtmux:real-tmux
func TestEmptyPasteIsGuardedAndBufferFree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	var buffers atomic.Int32
	instance.runtime.deps.setBuffer = func(
		context.Context,
		tmux.Server,
		tmux.SetBufferRequest,
	) error {
		buffers.Add(1)
		return nil
	}

	_, output, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(),
	})
	if err != nil || output.PaneID != panes[0].ID().String() || output.Bytes != 0 ||
		buffers.Load() != 0 {
		t.Fatalf("empty paste = (%+v, %v, buffers %d)", output, err, buffers.Load())
	}

	preflight, err := instance.tools.preflightPaneInput(
		callCtx, panes[0].ID().String(), "", paneInputTargetOnly, "run_shell_command",
	)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := processPaneInputs.acquire(
		preflight.Identities(), paneInputReservationRun, "run_shell_command",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer processPaneInputs.release(lease)
	_, _, err = instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(),
	})
	if err == nil || !strings.Contains(err.Error(), "earlier run_shell_command") ||
		buffers.Load() != 0 {
		t.Fatalf("empty paste during run = (%v, buffers %d)", err, buffers.Load())
	}
}

//libtmux:real-tmux
func TestPasteReservationIsSharedAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	firstInstance := mustInternalMCPServer(t, target)
	secondInstance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstInstance.runtime.deps.pasteBuffer = func(
		pasteCtx context.Context,
		pane tmux.Pane,
		request tmux.PasteBufferRequest,
	) error {
		close(entered)
		select {
		case <-release:
			return defaults.pasteBuffer(pasteCtx, pane, request)
		case <-pasteCtx.Done():
			return pasteCtx.Err()
		}
	}
	first := make(chan error, 1)
	go func() {
		_, _, err := firstInstance.tools.pasteText(callCtx, nil, pasteTextInput{
			PaneID: panes[0].ID().String(), Text: "first",
		})
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var secondPastes atomic.Int32
	secondInstance.runtime.deps.pasteBuffer = func(
		context.Context,
		tmux.Pane,
		tmux.PasteBufferRequest,
	) error {
		secondPastes.Add(1)
		return nil
	}
	_, _, err := secondInstance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "second",
	})
	if err == nil || !strings.Contains(err.Error(), "pane-input dispatch") ||
		secondPastes.Load() != 0 {
		t.Fatalf("overlapping paste = (%v, dispatches %d)", err, secondPastes.Load())
	}
	close(release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

//libtmux:real-tmux
func TestPasteReservationCoversFinalPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	firstInstance := mustInternalMCPServer(t, target)
	secondInstance := mustInternalMCPServer(t, target)
	callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	defaults := defaultMCPDependencies()
	firstInstance.runtime.deps.setBuffer = func(
		context.Context,
		tmux.Server,
		tmux.SetBufferRequest,
	) error {
		return nil
	}
	secondInstance.runtime.deps.setBuffer = firstInstance.runtime.deps.setBuffer
	var firstDispatches, secondDispatches atomic.Int32
	firstInstance.runtime.deps.pasteBuffer = func(
		context.Context,
		tmux.Pane,
		tmux.PasteBufferRequest,
	) error {
		firstDispatches.Add(1)
		return nil
	}
	secondInstance.runtime.deps.pasteBuffer = func(
		context.Context,
		tmux.Pane,
		tmux.PasteBufferRequest,
	) error {
		secondDispatches.Add(1)
		return nil
	}
	var snapshots atomic.Int32
	var competingErr error
	firstInstance.runtime.deps.snapshot = func(
		snapshotCtx context.Context,
		server tmux.Server,
	) (tmux.Snapshot, error) {
		snapshot, err := defaults.snapshot(snapshotCtx, server)
		if err == nil && snapshots.Add(1) == 2 {
			_, _, competingErr = secondInstance.tools.pasteText(
				callCtx, nil,
				pasteTextInput{PaneID: panes[0].ID().String(), Text: "second"},
			)
		}
		return snapshot, err
	}

	_, _, err := firstInstance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "first",
	})
	if err != nil || competingErr == nil ||
		!strings.Contains(competingErr.Error(), "pane-input dispatch") ||
		firstDispatches.Load() != 1 || secondDispatches.Load() != 0 {
		t.Fatalf("paste gap = (first %v/%d, competing %v/%d)",
			err, firstDispatches.Load(), competingErr, secondDispatches.Load())
	}
}

//libtmux:real-tmux
func TestPasteRechecksTargetAfterBufferSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
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

//libtmux:real-tmux
func TestPasteRefusesPlacementTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, window, panes := threePaneInputFixture(ctx, t)
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
		index := 7
		_, err := window.Move(bufferCtx, tmux.MoveWindowRequest{TargetIndex: &index})
		return err
	}

	_, output, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "must-not-paste",
	})
	if err == nil || output.Bytes != 0 {
		t.Fatalf("paste after placement transition = (%+v, %v), want refusal", output, err)
	}
	if bufferName == nil {
		t.Fatal("paste did not stage its private buffer before the transition")
	}
	if _, err := target.ShowBuffer(ctx, bufferName); err == nil {
		t.Fatalf("refused paste left buffer %q behind", *bufferName)
	}
}

//libtmux:real-tmux
func TestPasteCleansBufferAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	target, _, panes := threePaneInputFixture(ctx, t)
	instance := mustInternalMCPServer(t, target)
	baseCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
	callCtx, cancelCall := context.WithCancel(baseCtx)
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
		cancelCall()
		return nil
	}

	_, _, err := instance.tools.pasteText(callCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "must-be-cleaned",
	})
	if !errors.Is(err, context.Canceled) || bufferName == nil {
		t.Fatalf("cancelled paste = (%v, buffer %v)", err, bufferName)
	}
	if _, err := target.ShowBuffer(ctx, bufferName); err == nil {
		t.Fatalf("cancelled paste left buffer %q behind", *bufferName)
	}

	instance.runtime.deps.setBuffer = func(
		bufferCtx context.Context,
		server tmux.Server,
		request tmux.SetBufferRequest,
	) error {
		bufferName = request.Name
		if err := defaults.setBuffer(bufferCtx, server, request); err != nil {
			return err
		}
		return tmux.ErrOutcomeUnknown
	}
	_, _, err = instance.tools.pasteText(baseCtx, nil, pasteTextInput{
		PaneID: panes[0].ID().String(), Text: "unknown-set-buffer-outcome",
	})
	if !errors.Is(err, tmux.ErrOutcomeUnknown) || bufferName == nil {
		t.Fatalf("ambiguous buffer setup = (%v, buffer %v)", err, bufferName)
	}
	if _, err := target.ShowBuffer(ctx, bufferName); err == nil {
		t.Fatalf("ambiguous buffer setup left buffer %q behind", *bufferName)
	}
}
