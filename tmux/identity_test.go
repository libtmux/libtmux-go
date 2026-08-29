package tmux

import (
	"strconv"
	"testing"
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
