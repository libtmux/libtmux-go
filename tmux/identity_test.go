package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.__repr__
// libtmux:parity libtmux.session.Session
// libtmux:parity libtmux.session.Session.__enter__
// libtmux:parity libtmux.session.Session.__eq__
// libtmux:parity libtmux.session.Session.__eq__#parameter-branch:other:02a3a438c196
// libtmux:parity libtmux.session.Session.__repr__
// libtmux:parity libtmux.session.Session.id
// libtmux:parity libtmux.session.Session.server
// libtmux:parity libtmux.client.Client
// libtmux:parity libtmux.pane.Pane
// libtmux:parity libtmux.pane.Pane.__enter__
// libtmux:parity libtmux.pane.Pane.__eq__
// libtmux:parity libtmux.pane.Pane.__eq__#parameter-branch:other:13f7731ff992
// libtmux:parity libtmux.pane.Pane.__repr__
// libtmux:parity libtmux.window.Window
// libtmux:parity libtmux.window.Window.__enter__
// libtmux:parity libtmux.window.Window.__eq__
// libtmux:parity libtmux.window.Window.__eq__#parameter-branch:other:5fca9cbeda28
// libtmux:parity libtmux.window.Window.__repr__
func TestModelIdentityAndRepresentations(t *testing.T) {
	t.Parallel()

	snapshot := linkedSnapshot(t)
	alpha, err := snapshot.SessionByID(SessionID("$0"))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := snapshot.SessionByID(SessionID("$1"))
	if err != nil {
		t.Fatal(err)
	}
	if !alpha.Equal(alpha) || alpha.Equal(beta) {
		t.Fatalf("session equality = (self %t, other %t)", alpha.Equal(alpha), alpha.Equal(beta))
	}
	if got := alpha.String(); got != "Session($0 alpha)" {
		t.Fatalf("Session.String() = %q", got)
	}

	linked := snapshot.WindowsByID(WindowID("@0"))
	if !linked[0].Equal(linked[1]) {
		t.Fatal("linked views of one window are not equal by stable ID")
	}
	if got := linked[0].String(); got != "Window(@0 0:shared, Session($0 alpha))" {
		t.Fatalf("Window.String() = %q", got)
	}
	panes := snapshot.PanesByID(PaneID("%0"))
	if !panes[0].Equal(panes[1]) {
		t.Fatal("linked views of one pane are not equal by stable ID")
	}
	if got := panes[0].String(); got != "Pane(%0 Window(@0 0:shared, Session($0 alpha)))" {
		t.Fatalf("Pane.String() = %q", got)
	}
	client, err := snapshot.ClientByName(ClientName("/dev/pts/9"))
	if err != nil {
		t.Fatal(err)
	}
	if got := client.String(); !client.Equal(client) || got != "Client(/dev/pts/9)" {
		t.Fatalf("client identity/string = (%t, %q)", client.Equal(client), got)
	}
}

// libtmux:parity libtmux.session.Session.name
// libtmux:parity libtmux.window.Window.id
// libtmux:parity libtmux.window.Window.name
// libtmux:parity libtmux.window.Window.index
// libtmux:parity libtmux.window.Window.height
// libtmux:parity libtmux.window.Window.width
// libtmux:parity libtmux.pane.Pane.id
// libtmux:parity libtmux.pane.Pane.index
// libtmux:parity libtmux.pane.Pane.height
// libtmux:parity libtmux.pane.Pane.width
// libtmux:parity libtmux.pane.Pane.title
func TestPythonAliasPropertiesUseCanonicalModelValues(t *testing.T) {
	t.Parallel()

	session := Session{
		sessionID: "$7",
		formats:   formatValues{values: map[string]string{"session_name": "work"}},
	}
	window := Window{
		windowID:    "@8",
		windowIndex: 3,
		formats: formatValues{values: map[string]string{
			"window_name":   "editor",
			"window_height": "24",
			"window_width":  "80",
		}},
	}
	pane := Pane{
		paneID:    "%9",
		paneIndex: 2,
		formats: formatValues{values: map[string]string{
			"pane_height": "12",
			"pane_width":  "40",
			"pane_title":  "shell",
		}},
	}
	sessionName, sessionNameOK := session.Name()
	windowName, windowNameOK := window.Name()
	windowHeight, windowHeightOK := window.Height()
	windowWidth, windowWidthOK := window.Width()
	paneHeight, paneHeightOK := pane.Height()
	paneWidth, paneWidthOK := pane.Width()
	paneTitle, paneTitleOK := pane.Title()
	values := []struct {
		model string
		name  string
		got   string
		ok    bool
		want  string
	}{
		{model: "Session", name: "name", got: sessionName, ok: sessionNameOK, want: "work"},
		{model: "Window", name: "id", got: window.ID().String(), ok: true, want: "@8"},
		{model: "Window", name: "name", got: windowName, ok: windowNameOK, want: "editor"},
		{model: "Window", name: "index", got: strconv.Itoa(window.Index()), ok: true, want: "3"},
		{model: "Window", name: "height", got: strconv.Itoa(windowHeight), ok: windowHeightOK, want: "24"},
		{model: "Window", name: "width", got: strconv.Itoa(windowWidth), ok: windowWidthOK, want: "80"},
		{model: "Pane", name: "id", got: pane.ID().String(), ok: true, want: "%9"},
		{model: "Pane", name: "index", got: strconv.Itoa(pane.Index()), ok: true, want: "2"},
		{model: "Pane", name: "height", got: strconv.Itoa(paneHeight), ok: paneHeightOK, want: "12"},
		{model: "Pane", name: "width", got: strconv.Itoa(paneWidth), ok: paneWidthOK, want: "40"},
		{model: "Pane", name: "title", got: paneTitle, ok: paneTitleOK, want: "shell"},
	}
	for _, value := range values {
		if !value.ok || value.got != value.want {
			t.Errorf("%s %s canonical value = (%q, %t), want (%q, true)",
				value.model, value.name, value.got, value.ok, value.want)
		}
	}
}

func TestServerIdentityUsesSelectedSocketPath(t *testing.T) {
	t.Parallel()

	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	left := serverWithOptionsAndRunner(ServerOptions{
		SocketName:         "work",
		ConfigFile:         "/one",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + leftRoot},
	}, &versionQueueRunner{})
	right := serverWithOptionsAndRunner(ServerOptions{
		SocketName:         "work",
		ConfigFile:         "/two",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + rightRoot},
	}, &versionQueueRunner{})
	sameEndpoint := serverWithOptionsAndRunner(ServerOptions{
		SocketName:         "work",
		ConfigFile:         "/different",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + leftRoot},
	}, &versionQueueRunner{})
	other := serverWithOptionsAndRunner(
		ServerOptions{SocketPath: "/tmp/other.sock"},
		&versionQueueRunner{},
	)
	if left.Equal(right) || !left.Equal(sameEndpoint) || left.Equal(other) {
		t.Fatalf(
			"server equality = (different roots %t, same endpoint %t, other selector %t)",
			left.Equal(right),
			left.Equal(sameEndpoint),
			left.Equal(other),
		)
	}
	if got := left.String(); got != "Server(socket_name=work)" {
		t.Fatalf("named Server.String() = %q", got)
	}
	if got := other.String(); got != "Server(socket_path=/tmp/other.sock)" {
		t.Fatalf("path Server.String() = %q", got)
	}
	if got := (Server{}).String(); got != "Server(invalid)" {
		t.Fatalf("zero Server.String() = %q", got)
	}
	if (Server{}).Equal(Server{}) {
		t.Fatal("zero servers compare equal")
	}
}

func TestServerIdentityAnchorsRelativePathsToFrozenWorkingDirectory(t *testing.T) {
	t.Parallel()

	left := serverWithSocketSelection(
		t,
		t.TempDir(),
		ServerOptions{SocketPath: "relative.sock"},
	)
	right := serverWithSocketSelection(
		t,
		t.TempDir(),
		ServerOptions{SocketPath: "relative.sock"},
	)
	if left.Equal(right) {
		t.Fatal("relative socket paths under different frozen directories compare equal")
	}
}

// libtmux:parity libtmux.server.Server.__enter__
// libtmux:parity libtmux.server.Server.__eq__
// libtmux:parity libtmux.server.Server.__eq__#parameter-branch:other:6cb50a842361
// libtmux:parity libtmux.server.Server.socket_name
// libtmux:parity libtmux.server.Server.socket_path
func TestServerIdentityUsesEffectiveSocketPathSelector(t *testing.T) {
	t.Parallel()

	left := serverWithOptionsAndRunner(
		ServerOptions{SocketPath: "/tmp/shared.sock", SocketName: "left"},
		&versionQueueRunner{},
	)
	right := serverWithOptionsAndRunner(
		ServerOptions{SocketPath: "/tmp/shared.sock", SocketName: "right"},
		&versionQueueRunner{},
	)
	named := serverWithOptionsAndRunner(
		ServerOptions{SocketName: "left"},
		&versionQueueRunner{},
	)

	if !left.Equal(right) {
		t.Fatal("servers with the same effective socket path compare unequal")
	}
	if left.Equal(named) {
		t.Fatal("explicit socket path compares equal to shadowed socket name")
	}
	if got := left.String(); got != "Server(socket_path=/tmp/shared.sock)" {
		t.Fatalf("mixed-selector String() = %q, want socket path", got)
	}
}

func TestModelEqualityIncludesDaemonProvenance(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	materialize := func(pid string) Snapshot {
		snapshot, err := newSnapshot(Server{}, version, snapshotRecords{
			sessions: []formatValues{snapshotValues(
				t, version, "pid", pid, "session_id", "$0")},
			windows: []formatValues{snapshotValues(
				t, version, "pid", pid,
				"session_id", "$0", "window_id", "@0", "window_index", "0")},
			panes: []formatValues{snapshotValues(
				t, version, "pid", pid,
				"session_id", "$0", "window_id", "@0", "window_index", "0",
				"pane_id", "%0", "pane_index", "0")},
			clients: []formatValues{snapshotValues(
				t, version, "pid", pid, "client_name", "/dev/pts/9")},
		})
		if err != nil {
			t.Fatalf("newSnapshot() error = %v", err)
		}
		return snapshot
	}
	first := materialize("123")
	replacement := materialize("999")
	if first.Sessions()[0].Equal(replacement.Sessions()[0]) ||
		first.Windows()[0].Equal(replacement.Windows()[0]) ||
		first.Panes()[0].Equal(replacement.Panes()[0]) ||
		first.Clients()[0].Equal(replacement.Clients()[0]) {
		t.Fatal("equal tmux identifiers from different daemons compare equal")
	}
}

type daemonReplacementRunner struct {
	requests []tmuxcmd.Request
}

func (r *daemonReplacementRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.requests = append(r.requests, request)
	if index := slices.Index(request.Arguments, "if-shell"); index >= 0 {
		failure := request.Arguments[len(request.Arguments)-1]
		return tmuxcmd.Result{
			ExitCode: 1,
			Stderr:   []string{"unknown command: " + failure},
		}, nil
	}
	return tmuxcmd.Result{ExitCode: 0}, nil
}

func TestMaterializedCommandAtomicallyRejectsAReplacement(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &daemonReplacementRunner{}
	server := serverWithRunner(runner)
	snapshot, err := newSnapshot(server, version, snapshotRecords{
		sessions: []formatValues{snapshotValues(
			t, version,
			"socket_path", server.SocketPath(),
			"session_id", "$0",
		)},
	})
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	_, err = snapshot.Sessions()[0].Cmd(
		context.Background(), "display-message", "-p", "must-not-reach-replacement")
	if !errors.Is(err, ErrDaemonReplaced) {
		t.Fatalf("Cmd() error = %v, want ErrDaemonReplaced", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runner calls = %d, want one guarded command", len(runner.requests))
	}
}

func TestConnectionBoundCommandSkipsDaemonGuard(t *testing.T) {
	t.Parallel()

	server := serverWithRunner(&versionQueueRunner{})
	server = server.withDaemon(snapshotServerIdentity{
		version:    mustParseVersion(t, "3.7"),
		pid:        "123",
		startTime:  "456",
		socketPath: server.SocketPath(),
	})
	server.connection = &Connection{}
	arguments := []string{"display-message", "-p", "#{pane_id}"}

	guarded, guard, err := server.guardCommand(arguments, false)
	if err != nil {
		t.Fatal(err)
	}
	if guard != nil || !slices.Equal(guarded, arguments) {
		t.Fatalf("connection command = (%q, %#v), want original arguments without a guard", guarded, guard)
	}
}

func TestDaemonGuardEscapesFormatSeparators(t *testing.T) {
	t.Parallel()

	const value = "/tmp/a,b#c:d{e}"
	if got, want := escapeFormatLiteral(value), "/tmp/a#,b##c#:d#{e#}"; got != want {
		t.Fatalf("escapeFormatLiteral() = %q, want %q", got, want)
	}
}

func TestStreamingDaemonGuardClassifiesAReplacement(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 1}},
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(
				snapshotIdentityFields(),
				snapshotRowValues(version, map[string]string{"pid": "999"}),
			),
			ExitCode: 0,
		}},
	}}
	server := serverWithRunner(runner)
	server = server.withDaemon(snapshotServerIdentity{
		version:    version,
		pid:        "123",
		startTime:  "456",
		socketPath: server.SocketPath(),
	})

	result, err := server.runCommand(
		context.Background(),
		commandProcess,
		[]string{"attach-session", "-t", "$0"},
		&tmuxcmd.Stdio{},
		false,
	)
	if !errors.Is(err, ErrDaemonReplaced) || result.ExitCode != -1 {
		t.Fatalf("streaming guarded command = (%#v, %v), want replacement refusal", result, err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 || requests[0].Stdio == nil || requests[1].Stdio != nil {
		t.Fatalf("runner requests = %#v, want streamed command then captured identity probe", requests)
	}
}
