package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListServersMarksAnAbsentImplicitTarget(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		socketName string
		wantName   string
	}{
		{name: "named", socketName: "application", wantName: "application"},
		{name: "default", wantName: "default"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(
				root,
				"tmux-"+strconv.Itoa(os.Getuid()),
			)
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			targetPath := filepath.Join(directory, testCase.wantName)
			leaveDeadUnixSocket(t, targetPath)
			target := mustTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
				SocketName:         testCase.socketName,
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
			}))
			session, ctx := connectTarget(t, target)

			var listed struct {
				Servers []struct {
					SocketPath string `json:"socketPath"`
					IsTarget   bool   `json:"isTarget"`
				} `json:"servers"`
				SearchedIn string `json:"searchedIn"`
			}
			result := call(
				ctx,
				t,
				session,
				"list_servers",
				map[string]any{"includeDead": true},
				&listed,
			)
			if result.IsError {
				t.Fatalf("list_servers failed: %s", resultText(result))
			}
			if listed.SearchedIn != directory || len(listed.Servers) != 1 ||
				listed.Servers[0].SocketPath != targetPath ||
				!listed.Servers[0].IsTarget {
				t.Fatalf(
					"list_servers = %#v in %q, want dead target %q in %q",
					listed.Servers,
					listed.SearchedIn,
					targetPath,
					directory,
				)
			}
		})
	}
}

func TestListServersUsesTargetsFrozenSocketDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	ambientRoot := t.TempDir()
	frozenRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", ambientRoot)
	directory := filepath.Join(frozenRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(directory, "sibling")
	leaveDeadUnixSocket(t, sibling)
	notASocket := filepath.Join(directory, "tmux.conf")
	if err := os.WriteFile(notASocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ambientDirectory := filepath.Join(ambientRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(ambientDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := filepath.Join(ambientDirectory, "ambient")
	leaveDeadUnixSocket(t, ambient)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	targetPath := filepath.Join(t.TempDir(), "explicit-target.sock")
	frozenEnvironment := []string{"PATH=", "TMUX_TMPDIR=" + frozenRoot}

	options := executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         targetPath,
		ProcessEnvironment: frozenEnvironment,
	})
	if options.Binary != executable {
		t.Fatalf("fixture executable = %q, want %q", options.Binary, executable)
	}
	target := mustTmuxServer(t, options)

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := mustMCPServer(t, target)
	serverSession, err := instance.Connect(ctx, assumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "frozen-binding"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	var listed struct {
		Servers []struct {
			SocketPath string `json:"socketPath"`
			IsTarget   bool   `json:"isTarget"`
		} `json:"servers"`
		Total      int    `json:"total"`
		SearchedIn string `json:"searchedIn"`
	}
	result := call(
		ctx,
		t,
		session,
		"list_servers",
		map[string]any{"includeDead": true},
		&listed,
	)
	if result.IsError {
		t.Fatalf("list_servers failed: %#v", result.Content)
	}
	if listed.SearchedIn != directory || listed.Total != 2 {
		t.Fatalf(
			"discovery = (%q, total %d), want frozen directory %q with sibling and explicit target",
			listed.SearchedIn,
			listed.Total,
			directory,
		)
	}
	foundTarget := false
	for _, server := range listed.Servers {
		if server.SocketPath == ambient {
			t.Fatalf("list_servers scanned ambient socket %q", ambient)
		}
		if server.SocketPath == targetPath && server.IsTarget {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("servers = %#v, want explicit target outside scan directory", listed.Servers)
	}
}

//libtmux:real-tmux
func TestListServersUsesTheBoundLaneForItsTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	base := tmuxtest.NewServer(ctx, t)
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.Symlink(base.Executable(), proxy); err != nil {
		t.Fatal(err)
	}
	target := mustTmuxServer(t, tmux.ServerOptions{
		Binary:             proxy,
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: base.ProcessEnvironment(),
	})
	session, sessionCtx := connectTarget(t, target)
	var serverInfo struct{}
	call(sessionCtx, t, session, "get_server_info", map[string]any{}, &serverInfo)
	if err := os.Remove(proxy); err != nil {
		t.Fatal(err)
	}

	callCtx, endCall := context.WithTimeout(sessionCtx, time.Second)
	defer endCall()
	result, err := session.CallTool(callCtx, &sdk.CallToolParams{
		Name: "list_servers", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_servers depended on the blocked process lane: %v", err)
	}
	if result.IsError {
		t.Fatalf("list_servers failed: %s", resultText(result))
	}
}

func leaveDeadUnixSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func connectTarget(
	t *testing.T,
	target tmux.Server,
) (*sdk.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := mustMCPServer(t, target)
	t.Cleanup(func() { _ = instance.Close() })
	serverSession, err := instance.Connect(ctx, assumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "list-servers-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, ctx
}

//libtmux:real-tmux
func TestListPanesReportsAnEmptyServerAsNoPanes(t *testing.T) {
	session, _, ctx := connect(t)

	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	result := call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("list_panes on an unstarted server failed: %#v", result.Content)
	}
	if len(listed.Panes) != 0 {
		t.Fatalf("list_panes = %d panes, want 0", len(listed.Panes))
	}
}

//libtmux:real-tmux
func TestAnAbsentServerIsNotAnEmptyOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target := mustTmuxServer(t, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "absent-server"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	for _, listing := range []string{"list_panes", "list_windows", "list_sessions"} {
		var reported struct {
			Total      int    `json:"total"`
			ServerNote string `json:"serverNote"`
		}
		result := call(ctx, t, session, listing, map[string]any{}, &reported)
		if result.IsError {
			t.Fatalf("%s: %s", listing, resultText(result))
		}
		if reported.Total != 0 {
			t.Errorf("%s found %d on a socket with no server", listing, reported.Total)
		}
		if !strings.Contains(reported.ServerNote, "no tmux server is running") {
			t.Errorf("%s said nothing about the absent server: %q",
				listing, reported.ServerNote)
		}
	}

	// A read has no empty list to hand back, so it fails -- but with the same
	// sentence, rather than the tmux command that failed and the socket file
	// that is not there.
	for _, uri := range []string{
		"tmux://panes/0", "tmux://panes/0/content", "tmux://windows/0",
		"tmux://windows/0/panes", "tmux://sessions/anything",
	} {
		_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
		if err == nil {
			t.Errorf("%s was read on a socket with no server", uri)
			continue
		}
		if !strings.Contains(err.Error(), "no tmux server is running") {
			t.Errorf("%s says %q, want it to name the absent server", uri, err)
		}
	}

	// The same question asked directly, and the field a client iterates.
	var info struct {
		Alive           bool  `json:"alive"`
		AttachedClients []any `json:"attachedClients"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if info.Alive {
		t.Error("get_server_info called an absent server alive")
	}
	if info.AttachedClients == nil {
		t.Error("attachedClients came back null, not an empty array")
	}
}

//libtmux:real-tmux
func TestOrientationToolsDescribeTheServer(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: oriented\nwindows:\n" +
			"  - window_name: first\n    panes:\n      - {}\n      - {}\n" +
			"  - window_name: second\n    panes:\n      - {}\n",
	}, nil)

	var windows struct {
		Windows []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Panes  int    `json:"panes"`
			Active bool   `json:"active"`
		} `json:"windows"`
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &windows)
	if len(windows.Windows) != 2 {
		t.Fatalf("listed %d windows, want 2", len(windows.Windows))
	}
	byName := map[string]int{}
	for _, window := range windows.Windows {
		byName[window.Name] = window.Panes
	}
	if byName["first"] != 2 || byName["second"] != 1 {
		t.Errorf("pane counts = %v, want first 2 and second 1", byName)
	}

	var sessions struct {
		Sessions []struct {
			Name    string `json:"name"`
			Windows int    `json:"windows"`
		} `json:"sessions"`
	}
	call(ctx, t, session, "list_sessions", map[string]any{}, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].Windows != 2 {
		t.Fatalf("sessions = %+v, want one holding two windows", sessions.Sessions)
	}

	// Selecting the window that is not current makes it so.
	var inactive string
	for _, window := range windows.Windows {
		if !window.Active {
			inactive = window.ID
		}
	}
	if inactive == "" {
		t.Fatal("both windows reported as active")
	}
	if result := call(ctx, t, session, "select_window", map[string]any{
		"windowId": inactive,
	}, nil); result.IsError {
		t.Fatalf("select_window: %#v", result.Content)
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &windows)
	for _, window := range windows.Windows {
		if window.ID == inactive && !window.Active {
			t.Error("the selected window is not current")
		}
	}
}

//libtmux:real-tmux
func TestSnapshotAndSearchFindPanesByWhatTheyShow(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: findable\nwindows:\n  - panes:\n" +
			"      - shell: sh -c 'printf \"NEEDLE-9f2c\\n\"; sleep 120'\n" +
			"      - shell: sh -c 'printf \"other output\\n\"; sleep 120'\n",
	}, nil)
	panes := paneIDs(ctx, t, session)
	if len(panes) != 2 {
		t.Fatalf("built %d panes, want 2", len(panes))
	}

	var found struct {
		Panes []struct {
			Pane struct {
				ID string `json:"id"`
			} `json:"pane"`
			Matches []struct {
				Text string `json:"text"`
			} `json:"matches"`
		} `json:"panes"`
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		call(ctx, t, session, "search_panes", map[string]any{"text": "NEEDLE-9f2c"}, &found)
		if len(found.Panes) == 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(found.Panes) != 1 {
		t.Fatalf("search found %d panes, want exactly the one showing it", len(found.Panes))
	}

	var shot struct {
		Lines []string `json:"lines"`
		Pane  struct {
			ID       string `json:"id"`
			Geometry struct {
				Width int `json:"width"`
			} `json:"geometry"`
		} `json:"pane"`
		Dead bool `json:"dead"`
	}
	if len(found.Panes[0].Matches) == 0 {
		t.Error("search reported a matching pane without the line that matched")
	}
	result := call(ctx, t, session, "snapshot_pane", map[string]any{
		"paneId": found.Panes[0].Pane.ID,
	}, &shot)
	if result.IsError {
		t.Fatalf("snapshot_pane: %#v", result.Content)
	}
	if shot.Pane.ID != found.Panes[0].Pane.ID {
		t.Errorf("snapshot describes %q, want %q", shot.Pane.ID, found.Panes[0].Pane.ID)
	}
	if !strings.Contains(strings.Join(shot.Lines, "\n"), "NEEDLE-9f2c") {
		t.Error("the snapshot does not include the pane's contents")
	}
	if shot.Pane.Geometry.Width <= 0 || shot.Dead {
		t.Errorf("snapshot state looks wrong: width=%d dead=%v",
			shot.Pane.Geometry.Width, shot.Dead)
	}
}

//libtmux:real-tmux
func TestAListWithNoMatchesIsStillAList(t *testing.T) {
	session, _, ctx := connect(t)

	for _, listing := range []struct {
		tool       string
		arguments  map[string]any
		collection string
	}{
		{"list_panes", map[string]any{"command": "no-such-command-xyz"}, "panes"},
		{"list_sessions", map[string]any{"name": "no-such-session-xyz"}, "sessions"},
		{"list_windows", map[string]any{"name": "no-such-window-xyz"}, "windows"},
	} {
		t.Run(listing.tool, func(t *testing.T) {
			result := call(ctx, t, session, listing.tool, listing.arguments, nil)
			if result.IsError {
				t.Fatalf("%s failed: %#v", listing.tool, result.Content)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var reply map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &reply); err != nil {
				t.Fatal(err)
			}
			found, present := reply[listing.collection]
			if !present {
				t.Fatalf("%s left out %q entirely: %s",
					listing.tool, listing.collection, encoded)
			}
			if string(found) != "[]" {
				t.Errorf("%s reported %s = %s, want []",
					listing.tool, listing.collection, found)
			}
		})
	}
}

//libtmux:real-tmux
func TestListServersLeavesOutSocketsNothingIsListeningOn(t *testing.T) {
	session, _, ctx := connect(t)

	var listed struct {
		Servers []struct {
			Name     string `json:"name"`
			Alive    bool   `json:"alive"`
			IsTarget bool   `json:"isTarget"`
		} `json:"servers"`
		Total   int `json:"total"`
		Skipped int `json:"skipped"`
	}
	result := call(ctx, t, session, "list_servers", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("list_servers failed: %#v", result.Content)
	}
	for _, found := range listed.Servers {
		if !found.Alive && !found.IsTarget {
			t.Errorf("socket %q has no server and is not the target, so it should "+
				"have been left out", found.Name)
		}
	}
	if listed.Total < len(listed.Servers) {
		t.Errorf("total = %d with %d reported, want total to count them all",
			listed.Total, len(listed.Servers))
	}

	// A cap is reported rather than applied quietly, so a caller can tell a
	// machine with one server from a reply that stopped after one.
	var capped struct {
		Servers []struct{} `json:"servers"`
		Skipped int        `json:"skipped"`
	}
	call(ctx, t, session, "list_servers", map[string]any{"maxServers": 1}, &capped)
	if len(capped.Servers) > 1 {
		t.Errorf("maxServers 1 returned %d servers", len(capped.Servers))
	}
}

//libtmux:real-tmux
func TestServerInfoDoesNotInventAHealthyEmptyServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	unreachable := mustTmuxServer(t, executableFixtureOptions(t, fixtureUnavailable, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	}))
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := mustMCPServer(t, unreachable).Connect(
		ctx, assumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "unreachable"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	var reported struct {
		Alive    bool   `json:"alive"`
		Sessions int    `json:"sessions"`
		Socket   string `json:"socketPath"`
	}
	result := call(ctx, t, session, "get_server_info", map[string]any{}, &reported)
	if !result.IsError {
		t.Fatalf("a tmux that cannot be run was reported as alive=%t with %d sessions "+
			"and socket %q, rather than as an error",
			reported.Alive, reported.Sessions, reported.Socket)
	}
}

//libtmux:real-tmux
func TestAFilteredListingSaysHowManyItLeftOut(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: counted\nwindows:\n"+
		"  - panes:\n      - {}\n  - panes:\n      - {}\n")
	if result := call(ctx, t, session, "create_window", map[string]any{
		"sessionName": "counted", "name": "singled-out",
	}, nil); result.IsError {
		t.Fatalf("create_window: %#v", result.Content)
	}

	var listed struct {
		Windows []struct {
			Name string `json:"name"`
		} `json:"windows"`
		Total   int `json:"total"`
		Skipped int `json:"skipped"`
	}
	call(ctx, t, session, "list_windows", map[string]any{"name": "singled-out"}, &listed)
	if len(listed.Windows) != 1 {
		t.Fatalf("the filter did not select one window: %#v", listed.Windows)
	}
	if listed.Total <= len(listed.Windows) {
		t.Fatalf("nothing was filtered out, so this proves nothing: total %d",
			listed.Total)
	}
	if listed.Total != len(listed.Windows)+listed.Skipped {
		t.Errorf("total %d does not reconcile: %d listed, %d skipped",
			listed.Total, len(listed.Windows), listed.Skipped)
	}

	// An unfiltered listing left nothing out, and says so by omitting the
	// field rather than by a zero a caller has to tell apart from a filter
	// that happened to exclude none. A pointer is what distinguishes the two,
	// because an absent key leaves a plain int at whatever it already held.
	var unfiltered struct {
		Windows []struct{} `json:"windows"`
		Total   int        `json:"total"`
		Skipped *int       `json:"skipped"`
	}
	call(ctx, t, session, "list_windows", map[string]any{}, &unfiltered)
	if unfiltered.Skipped != nil {
		t.Errorf("an unfiltered listing reported %d skipped", *unfiltered.Skipped)
	}
	if unfiltered.Total != len(unfiltered.Windows) {
		t.Errorf("an unfiltered listing dropped %d of %d",
			unfiltered.Total-len(unfiltered.Windows), unfiltered.Total)
	}
}
