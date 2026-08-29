//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.display_popup
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:border_lines:d9be2600ac4e
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:border_style:4d3b7eb6c12c
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_existing:b874c97fbb98
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_any_key:512abb2889cb
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_exit,close_on_success:7fe7f6d507f5
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_exit:8ae43bb440be
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_success:ba40729359dc
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:command:581de879aee9
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:height:584748e889a5
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:no_border:f3385c3cc820
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:no_keys:5ad750b56292
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:start_directory:d91549582997
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:style:2fb8c408bf6c
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:title:a849ce4d4991
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:width:c4a3db243018
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:y:0cf048966732
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:1cded5d69f99:2
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:2
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:3
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:4
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:5
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:6
// libtmux:parity libtmux.pane.Pane.display_popup#warning:02db17883dc3
// libtmux:parity libtmux.pane.Pane.display_popup#warning:04e24fc19aae
// libtmux:parity libtmux.pane.Pane.display_popup#warning:435d237695c4
// libtmux:parity libtmux.pane.Pane.display_popup#warning:7203e98175ed
// libtmux:parity libtmux.pane.Pane.display_popup#warning:8ae1ebc29718
// libtmux:parity libtmux.pane.Pane.display_popup#warning:b5ab51d893a0
// libtmux:parity libtmux.pane.Pane.display_popup#warning:c0c571ede24a
// libtmux:parity libtmux.pane.Pane.display_popup#warning:dd8c2a6933f5
//
//libtmux:real-tmux
func TestDisplayPopupVersionedFieldsAgainstRealTmux(t *testing.T) {
	base := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 3)
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: base.ProcessEnvironment(),
		Unsupported:        tmux.DegradeUnsupported,
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}

	directory := t.TempDir()
	marker := filepath.Join(directory, "popup-marker")
	command := "printf '%s' \"${POPUP_VALUE-unset}\" > " + strconv.Quote(marker)
	client := control.ClientName()
	title := "go-popup"
	if err := panes[0].DisplayPopup(ctx, tmux.DisplayPopupRequest{
		Command:        &command,
		CloseOnSuccess: true,
		TargetClient:   client,
		StartDirectory: &directory,
		Title:          &title,
		Environment:    map[string]string{"POPUP_VALUE": "set"},
		NoKeys:         true,
	}); err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version33 := mustPaneModeVersion(t, "3.3")
	version36 := mustPaneModeVersion(t, "3.6")
	wantMarker := "unset"
	wantFeatures := []string{"title", "environment", "no_keys"}
	if version.AtLeast(version33) {
		wantMarker = "set"
		wantFeatures = []string{"no_keys"}
	}
	if version.AtLeast(version36) {
		wantFeatures = nil
	}
	if got := waitForProcessFile(ctx, t, marker); got != wantMarker {
		t.Fatalf("popup marker = %q, want %q on tmux %s", got, wantMarker, version)
	}
	features := make([]string, len(warnings))
	for index, warning := range warnings {
		features[index] = warning.Feature
	}
	if !slices.Equal(features, wantFeatures) {
		t.Fatalf("DisplayPopup warning features = %#v, want %#v", features, wantFeatures)
	}
}

//libtmux:real-tmux
func TestDisplayPopupNoKeysAndCloseOnAnyKeyPrecedenceAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if !version.AtLeast(mustPaneModeVersion(t, "3.6")) {
		t.Skip("NoKeys and CloseOnAnyKey require tmux 3.6")
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}

	directory := t.TempDir()
	ready := filepath.Join(directory, "popup-ready")
	activeKey := filepath.Join(directory, "popup-active-key")
	exited := filepath.Join(directory, "popup-exited")
	const exitLock = "go-popup-allow-exit"
	command := strings.Join([]string{
		"stty raw -echo",
		"printf ready > " + strconv.Quote(ready),
		"dd bs=1 count=1 of=" + strconv.Quote(activeKey) + " 2>/dev/null",
		"tmux wait-for " + exitLock,
		"printf exited > " + strconv.Quote(exited),
	}, "; ")
	client := control.ClientName()
	popupDone := make(chan error, 1)
	go func() {
		popupDone <- panes[0].DisplayPopup(ctx, tmux.DisplayPopupRequest{
			Command:      &command,
			CloseOnExit:  true,
			TargetClient: client,
		})
	}()
	if got := waitForProcessFile(ctx, t, ready); got != "ready" {
		t.Fatalf("popup ready marker = %q, want ready", got)
	}
	if err := panes[0].DisplayPopup(ctx, tmux.DisplayPopupRequest{
		TargetClient:  client,
		CloseOnAnyKey: true,
		NoKeys:        true,
	}); err != nil {
		t.Fatalf("DisplayPopup(modify flags) error = %v", err)
	}

	interactiveRealCommand(ctx, t, server, "send-keys", "-K", "-c", client.String(), "x")
	if got := waitForProcessFile(ctx, t, activeKey); got != "x" {
		t.Fatalf("popup active key = %q, want x", got)
	}
	select {
	case err := <-popupDone:
		t.Fatalf("active key dismissed popup: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	interactiveRealCommand(ctx, t, server, "wait-for", "-S", exitLock)
	if got := waitForProcessFile(ctx, t, exited); got != "exited" {
		t.Fatalf("popup exit marker = %q, want exited", got)
	}
	select {
	case err := <-popupDone:
		t.Fatalf("popup closed on job exit after NoKeys reset CloseOnExit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	interactiveRealCommand(ctx, t, server, "send-keys", "-K", "-c", client.String(), "y")
	select {
	case err := <-popupDone:
		if err != nil {
			t.Fatalf("DisplayPopup() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("post-exit key did not dismiss popup: %v", ctx.Err())
	}
}

//libtmux:real-tmux
func TestDisplayPopupUsesExplicitClientAndLinkedPaneContextAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	firstSession := snapshot.Sessions()[0]
	shared := relatedWindows(t, firstSession)[0]
	secondSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "go-popup-linked-target",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: secondSession.ID(),
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	canonical, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	var canonicalSession, receiverSession tmux.Session
	switch canonical.SessionID() {
	case firstSession.ID():
		canonicalSession = firstSession
		receiverSession = secondSession
	case secondSession.ID():
		canonicalSession = secondSession
		receiverSession = firstSession
	default:
		t.Fatalf("canonical session = %s, want one linked session", canonical.SessionID())
	}
	if _, err := canonicalSession.SelectWindow(ctx, tmux.SelectWindowRequest{
		WindowID: shared.ID(),
	}); err != nil {
		t.Fatalf("canonical SelectWindow(shared) error = %v", err)
	}
	firstControl := tmuxtest.NewControlMode(context.Background(), t, server, canonicalSession)
	secondControl := tmuxtest.NewControlMode(context.Background(), t, server, canonicalSession)
	if firstControl.ClientName() == secondControl.ClientName() {
		t.Fatalf(
			"simultaneous client names are both %q, want distinct identities",
			firstControl.ClientName(),
		)
	}

	snapshot = mustRealSnapshot(t, server)
	receiver := exactRealWindow(t, snapshot, receiverSession.ID(), shared.ID())
	receiverPanes := relatedPanes(t, receiver)
	if len(receiverPanes) != 1 {
		t.Fatalf("receiver panes = %#v, want one", receiverPanes)
	}
	receiverPane := receiverPanes[0]
	target := receiverPane.SessionID().String() + ":" + receiverPane.WindowID().String() +
		"." + receiverPane.ID().String()
	defaultResult, defaultErr := server.Cmd(
		ctx,
		"display-message",
		"-p",
		"-t",
		target,
		"#{client_name}",
	)
	requireRealPaneModeCommandSuccess(
		t,
		"display-message default client",
		defaultResult,
		defaultErr,
	)
	if len(defaultResult.Stdout) != 1 {
		t.Fatalf("default client stdout = %#v, want one row", defaultResult.Stdout)
	}
	targetClient := firstControl.ClientName()
	if defaultResult.Stdout[0] == targetClient.String() {
		targetClient = secondControl.ClientName()
	}
	if defaultResult.Stdout[0] == targetClient.String() {
		t.Fatalf("explicit target client %q unexpectedly equals tmux default", targetClient)
	}

	receiverName, ok := receiverSession.Name()
	if !ok {
		t.Fatal("receiver session name is unavailable")
	}
	root := t.TempDir()
	expectedDirectory := filepath.Join(root, receiverName, targetClient.String())
	if err := os.MkdirAll(expectedDirectory, 0o700); err != nil {
		t.Fatalf("create expected popup directory: %v", err)
	}
	directoryTemplate := filepath.Join(root, "#{session_name}", "#{client_name}")
	marker := filepath.Join(root, "popup-context-marker")
	command := "pwd > " + strconv.Quote(marker)
	if err := receiverPane.DisplayPopup(ctx, tmux.DisplayPopupRequest{
		Command:        &command,
		CloseOnSuccess: true,
		TargetClient:   targetClient,
		StartDirectory: &directoryTemplate,
	}); err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}
	if got := strings.TrimSpace(waitForProcessFile(ctx, t, marker)); got != expectedDirectory {
		t.Fatalf("popup expansion context = %q, want %q", got, expectedDirectory)
	}
}

func mustPaneModeVersion(t *testing.T, value string) tmux.Version {
	t.Helper()
	version, err := tmux.ParseVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
