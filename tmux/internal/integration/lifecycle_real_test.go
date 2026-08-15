//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.server.Server.has_session
// libtmux:parity libtmux.server.Server.has_session#parameter-branch:exact:17992787d42b
// libtmux:parity libtmux.session.Session.rename_session
//
//libtmux:real-tmux
func TestLifecycleOperationsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	assertHasSession(ctx, t, server, tmux.HasSessionRequest{Target: "work"}, true)
	assertHasSession(ctx, t, server, tmux.HasSessionRequest{Target: "wo*", Pattern: true}, true)
	assertHasSession(ctx, t, server, tmux.HasSessionRequest{Target: "wo*"}, false)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name:       "phase5-created",
		WindowName: "initial",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	session, err = session.Rename(ctx, "phase5-renamed")
	if err != nil {
		t.Fatalf("Session.Rename() error = %v", err)
	}
	if name, _ := session.Name(); name != "phase5-renamed" {
		t.Fatalf("renamed session name = %q, want phase5-renamed", name)
	}

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: newWindowName("second")})
	if err != nil {
		t.Fatalf("Session.NewWindow() error = %v", err)
	}
	window, err = window.Rename(ctx, "renamed-window")
	if err != nil {
		t.Fatalf("Window.Rename() error = %v", err)
	}

	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{Direction: tmux.PaneDirectionRight})
	if err != nil {
		t.Fatalf("Window.SplitPane() error = %v", err)
	}
	window, err = window.Select(ctx)
	if err != nil {
		t.Fatalf("Window.Select() error = %v", err)
	}
	if active, _ := window.Active(); !active {
		t.Fatal("selected window active = false, want true")
	}
	pane, err = pane.Select(ctx, tmux.PaneSelectRequest{})
	if err != nil {
		t.Fatalf("Pane.Select() error = %v", err)
	}
	if active, _ := pane.Active(); !active {
		t.Fatal("selected pane active = false, want true")
	}

	if err := pane.Kill(ctx); err != nil {
		t.Fatalf("Pane.Kill() error = %v", err)
	}
	if _, err := server.Pane(ctx, pane.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("lookup killed pane error = %v, want ErrSnapshotNotFound", err)
	}
	if err := window.Kill(ctx); err != nil {
		t.Fatalf("Window.Kill() error = %v", err)
	}
	if _, err := server.Window(ctx, window.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("lookup killed window error = %v, want ErrSnapshotNotFound", err)
	}
	if err := session.Kill(ctx); err != nil {
		t.Fatalf("Session.Kill() error = %v", err)
	}
	if _, err := server.Session(ctx, session.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("lookup killed session error = %v, want ErrSnapshotNotFound", err)
	}
	if err := server.Kill(ctx); err != nil {
		t.Fatalf("Server.Kill() error = %v", err)
	}
	if err := server.Kill(ctx); err != nil {
		t.Fatalf("second Server.Kill() error = %v", err)
	}
	alive, err := server.IsAlive(ctx)
	if err != nil {
		t.Fatalf("IsAlive() after Kill() error = %v", err)
	}
	if alive {
		t.Fatal("IsAlive() after Kill() = true, want false")
	}
}

// libtmux:parity libtmux.server.Server.new_session
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:attach:58cb72758f2e
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:client_flags,session_name,start_directory,window_command,window_name,x,y:73e7c644693b
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:client_flags:54ec301cbb32
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:detach_others:6edab05acbf1
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:kill_session:84e924322fc0
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:no_size:4872209d09e2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3:2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:ab485de610f3:3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:session_name:e704ec4d7e25
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:start_directory:bef78f09efe5
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:window_command:9439fd3d9eb2
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:window_name:52160acf82b3
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.server.Server.new_session#parameter-branch:y:0cf048966732
//
//libtmux:real-tmux
func TestLifecycleCreationOptionsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	startDirectory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	first, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name:           "phase5-options",
		StartDirectory: startDirectory,
		WindowName:     "base",
		Command:        "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession(options) error = %v", err)
	}
	assertRealPaneLaunch(
		ctx, t, server, requiredRealPaneID(t, first.Formats().PaneID), startDirectory,
	)
	firstID := first.ID()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name:           "phase5-options",
		KillExisting:   true,
		StartDirectory: startDirectory,
		WindowName:     "replacement",
		Command:        "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession(KillExisting) error = %v", err)
	}
	if session.ID() == firstID {
		t.Fatalf("replacement session ID = %s, want a new identity", session.ID())
	}
	if _, err := server.Session(ctx, firstID); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("lookup replaced session error = %v, want ErrSnapshotNotFound", err)
	}

	index := 7
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name:           newWindowName("indexed"),
		Index:          &index,
		StartDirectory: startDirectory,
		Command:        "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewWindow(options) error = %v", err)
	}
	if window.Index() != index {
		t.Fatalf("indexed window index = %d, want %d", window.Index(), index)
	}
	assertRealPaneLaunch(
		ctx, t, server, requiredRealPaneID(t, window.Formats().PaneID), startDirectory,
	)
	if active, _ := window.Active(); active {
		t.Fatal("detached new window active = true, want false")
	}

	selected, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name:    newWindowName("selected"),
		Attach:  true,
		Command: "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewWindow(Attach) error = %v", err)
	}
	if active, _ := selected.Active(); !active {
		t.Fatal("attached new window active = false, want true")
	}

	for _, test := range []struct {
		name      string
		direction tmux.PaneDirection
		edge      func(tmux.Pane) (bool, bool)
		dimension func(tmux.Pane) (int, bool)
	}{
		{name: "above", direction: tmux.PaneDirectionAbove, edge: tmux.Pane.AtTop, dimension: tmux.Pane.Height},
		{name: "below", direction: tmux.PaneDirectionBelow, edge: tmux.Pane.AtBottom, dimension: tmux.Pane.Height},
		{name: "left", direction: tmux.PaneDirectionLeft, edge: tmux.Pane.AtLeft, dimension: tmux.Pane.Width},
		{name: "right", direction: tmux.PaneDirectionRight, edge: tmux.Pane.AtRight, dimension: tmux.Pane.Width},
	} {
		t.Run("split "+test.name, func(t *testing.T) {
			window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
				Name:    newWindowName("split-" + test.name),
				Command: "sleep 30",
			})
			if err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			mustRealCommand(t, server, "resize-window", "-t", window.ID().String(), "-x", "80", "-y", "24")
			size := 6
			pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
				Direction:      test.direction,
				Size:           &size,
				StartDirectory: startDirectory,
				Command:        "sleep 30",
			})
			if err != nil {
				t.Fatalf("SplitPane() error = %v", err)
			}
			if edge, _ := test.edge(pane); !edge {
				t.Fatalf("%s split edge = false, want true", test.name)
			}
			if dimension, _ := test.dimension(pane); dimension != 6 {
				t.Fatalf("%s split dimension = %d, want 6", test.name, dimension)
			}
			if active, _ := pane.Active(); active {
				t.Fatalf("detached %s pane active = true, want false", test.name)
			}
			assertRealPaneLaunch(ctx, t, server, pane.ID(), startDirectory)
		})
	}

	t.Run("split percentage", func(t *testing.T) {
		if version.String() == "3.4" {
			// tmux 3.4 checks the size flag while parsing -p and rejects the
			// valid request. Upstream fixed split-window -p in tmux 3.5.
			t.Skip("split-window -p is broken in tmux 3.4")
		}
		percentageWindow, err := session.NewWindow(ctx, tmux.NewWindowRequest{
			Name:    newWindowName("split-percentage"),
			Attach:  true,
			Command: "sleep 30",
		})
		if err != nil {
			t.Fatalf("NewWindow(percentage) error = %v", err)
		}
		mustRealCommand(t, server, "resize-window", "-t", percentageWindow.ID().String(), "-x", "80", "-y", "24")
		percentage := 25
		percentagePane, err := percentageWindow.SplitPane(ctx, tmux.SplitPaneRequest{
			Attach:     true,
			Direction:  tmux.PaneDirectionBelow,
			Percentage: &percentage,
			Command:    "sleep 30",
		})
		if err != nil {
			t.Fatalf("SplitPane(Percentage) error = %v", err)
		}
		if active, _ := percentagePane.Active(); !active {
			t.Fatal("attached percentage pane active = false, want true")
		}
		if bottom, _ := percentagePane.AtBottom(); !bottom {
			t.Fatal("below percentage pane at bottom = false, want true")
		}
		paneHeight := realFormatInt(t, percentagePane.Height)
		windowHeight := realFormatInt(t, percentagePane.Formats().WindowHeight)
		if scaled := paneHeight * 100; scaled < windowHeight*20 || scaled > windowHeight*30 {
			t.Fatalf("percentage pane height = %d of %d, want approximately 25%%", paneHeight, windowHeight)
		}
	})

	zero := 0
	for _, test := range []struct {
		name    string
		request tmux.SplitPaneRequest
	}{
		{name: "zero size", request: tmux.SplitPaneRequest{Size: &zero, Command: "sleep 30"}},
		{name: "zero percentage", request: tmux.SplitPaneRequest{Percentage: &zero, Command: "sleep 30"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "zero percentage" && version.String() == "3.4" {
				t.Skip("split-window -p is broken in tmux 3.4")
			}
			window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
				Name:    newWindowName("split-" + test.name),
				Command: "sleep 30",
			})
			if err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			mustRealCommand(t, server, "resize-window", "-t", window.ID().String(), "-x", "80", "-y", "24")
			pane, err := window.SplitPane(ctx, test.request)
			if err != nil {
				t.Fatalf("SplitPane() error = %v", err)
			}
			if pane.ID() == "" {
				t.Fatal("SplitPane() returned an empty pane identity")
			}
		})
	}
}

//libtmux:real-tmux
func TestNewSessionScrubsAmbientTMUXAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.Join(t.TempDir(), "tmux-lifecycle-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"if [ \"${TMUX+x}\" = x ]; then\n" +
		"  echo 'TMUX leaked into session lifecycle' >&2\n" +
		"  exit 73\n" +
		"fi\n" +
		"exec \"$LIBTMUX_LIFECYCLE_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_LIFECYCLE_REAL_TMUX", realBinary)
	t.Setenv("TMUX", "/tmp/foreign,424242,7")
	ambient := tmux.NewServer(tmux.ServerOptions{
		Binary:     proxyPath,
		SocketPath: server.SocketPath(),
		ConfigFile: server.ConfigFile(),
	}).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := ambient.NewSession(ctx, tmux.NewSessionRequest{
		Name:    "ambient-clean",
		Command: "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession() with ambient TMUX error = %v", err)
	}
	if name, _ := session.Name(); name != "ambient-clean" {
		t.Fatalf("new session name = %q, want ambient-clean", name)
	}
}

func assertRealPaneLaunch(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	paneID tmux.PaneID,
	wantPath string,
) {
	t.Helper()
	pane, err := server.Pane(ctx, paneID)
	if err != nil {
		t.Fatalf("Server.Pane(%s) error = %v", paneID, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var path, command string
	var pathOK, commandOK bool
	err = tmuxtest.WaitFor(waitCtx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		pane, err = pane.Refresh(ctx)
		if err != nil {
			return false, err
		}
		path, pathOK = pane.CurrentPath()
		command, commandOK = pane.StartCommand()
		command = normalizeRealPaneStartCommand(command)
		return pathOK && path == wantPath && commandOK && command == "sleep 30", nil
	})
	if err != nil {
		t.Fatalf(
			"wait for pane launch: %v; current path = (%q, %t), start command = (%q, %t)",
			err,
			path,
			pathOK,
			command,
			commandOK,
		)
	}
}

func normalizeRealPaneStartCommand(command string) string {
	if unquoted, err := strconv.Unquote(command); err == nil {
		return unquoted
	}
	return strings.Trim(command, "'\"")
}

func requiredRealPaneID(
	t *testing.T,
	value func() (tmux.PaneID, bool),
) tmux.PaneID {
	t.Helper()
	paneID, ok := value()
	if !ok {
		t.Fatal("created object has no materialized pane ID")
	}
	return paneID
}

func newWindowName(value string) *string { return &value }

func realFormatInt(t *testing.T, accessor func() (int, bool)) int {
	t.Helper()
	value, ok := accessor()
	if !ok {
		t.Fatal("numeric format value is unavailable")
	}
	return value
}

func assertHasSession(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	request tmux.HasSessionRequest,
	want bool,
) {
	t.Helper()
	got, err := server.HasSession(ctx, request)
	if err != nil {
		t.Fatalf("HasSession(%q) error = %v", request.Target, err)
	}
	if got != want {
		t.Fatalf("HasSession(%q) = %t, want %t", request.Target, got, want)
	}
}

// TestStartKeepsAnEmptyServerOnlyThroughTheConfigFile pins the escape hatch
// Start documents. tmux reads exit-empty as the server starts, and a later
// command cannot turn it off because that command needs the very server the
// setting exists to keep alive.
//
//libtmux:real-tmux
func TestStartKeepsAnEmptyServerOnlyThroughTheConfigFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	configuration := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(
		configuration, []byte("set -s exit-empty off\n"), 0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		ConfigFile: configuration,
	}).WithStrictErrors()
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	alive, err := server.IsAlive(ctx)
	if err != nil {
		t.Fatalf("is alive: %v", err)
	}
	if !alive {
		t.Fatal("an empty server with exit-empty off in its config did not survive Start")
	}
}
