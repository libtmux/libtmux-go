package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// equivalenceFormats are materialized tmux formats compared across transports.
// They span every scope and every value kind the decoder handles, so a version
// whose control-mode rendering differs from its process output fails here
// rather than in a caller's data.
var equivalenceFormats = []string{
	"session_id", "session_name", "session_windows", "session_attached",
	"window_id", "window_name", "window_index", "window_width", "window_height",
	"window_active", "window_layout", "window_flags", "window_panes",
	"pane_id", "pane_index", "pane_width", "pane_height", "pane_active",
	"pane_current_command", "pane_title", "pane_pid", "pane_tty", "pane_dead",
}

//libtmux:real-tmux
func TestControlEngineMaterializesTheSameStateAsProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatal(err)
	}

	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	viaProcess, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	viaControl, err := connected.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() over the control engine: %v", err)
	}

	if got, want := len(viaControl.Panes()), len(viaProcess.Panes()); got != want {
		t.Fatalf("panes over the control engine = %d, over processes = %d", got, want)
	}

	compared := 0
	controlPanes := viaControl.Panes()
	for index, processPane := range viaProcess.Panes() {
		controlPane := controlPanes[index]
		for _, format := range equivalenceFormats {
			processValue, processOK := processPane.Formats().Raw(format)
			controlValue, controlOK := controlPane.Formats().Raw(format)
			if processOK != controlOK || processValue != controlValue {
				t.Errorf(
					"pane %d format %s: process = (%q, %t), control engine = (%q, %t)",
					index, format, processValue, processOK, controlValue, controlOK,
				)
				continue
			}
			compared++
		}
	}
	if compared == 0 {
		t.Fatal("no format was compared across transports")
	}
	t.Logf("compared %d materialized format values across both transports", compared)
}

// Subprocess escaping and control-mode quoting must store identical literals.
//
//libtmux:real-tmux
func TestTransportsAgreeOnLiteralValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	for _, value := range []string{
		"plain",
		"trailing;",
		"mid;dle",
		";leading",
		"with space",
		"it's",
		"tab\there",
	} {
		t.Run(value, func(t *testing.T) {
			store := func(handle tmux.Server, name string) string {
				t.Helper()
				if err := handle.SetEnvironment(
					ctx, name, value, tmux.SetEnvironmentOptions{},
				); err != nil {
					t.Fatalf("SetEnvironment(%q) error = %v", value, err)
				}
				stored, ok, err := handle.GetEnvironment(ctx, name)
				if err != nil || !ok {
					t.Fatalf("GetEnvironment() = (%#v, %t, %v)", stored, ok, err)
				}
				return stored.Value
			}
			viaProcess := store(server, "LIBTMUX_EQUIVALENCE_PROCESS")
			viaControl := store(connected, "LIBTMUX_EQUIVALENCE_CONTROL")

			if viaProcess != value {
				t.Errorf("process transport stored %q, want %q", viaProcess, value)
			}
			if viaControl != viaProcess {
				t.Errorf(
					"control engine stored %q, process stored %q",
					viaControl, viaProcess,
				)
			}
		})
	}
}

// Control mode emits one reply frame per command in a list. Every frame must be
// drained or the next request receives a stale reply.
//
//libtmux:real-tmux
func TestTransportsAgreeOnCommandLists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	for _, transport := range []struct {
		name   string
		handle tmux.Server
		suffix string
	}{
		{name: "process", handle: server, suffix: "_process"},
		{name: "control", handle: connected, suffix: "_control"},
	} {
		t.Run(transport.name, func(t *testing.T) {
			first, second := "@list_first"+transport.suffix, "@list_second"+transport.suffix
			result, err := transport.handle.Cmd(ctx,
				"set-option", "-g", first, "one",
				";",
				"set-option", "-g", second, "two",
			)
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("command list = (%#v, %v)", result, err)
			}

			scope := transport.handle.GlobalSessionScope()
			for name, want := range map[string]string{first: "one", second: "two"} {
				got, ok, err := scope.RawOption(ctx, name)
				if err != nil || !ok || got != want {
					t.Errorf("RawOption(%q) = (%q, %t, %v), want %q", name, got, ok, err, want)
				}
			}

			// Whatever the list did, the connection still answers the next
			// command with that command's own reply.
			echoed, err := transport.handle.Cmd(ctx, "display-message", "-p", "still-aligned")
			if err != nil || len(echoed.Stdout) == 0 || echoed.Stdout[0] != "still-aligned" {
				t.Fatalf("command after a list = (%#v, %v)", echoed, err)
			}
		})
	}
}

// tmux drops commands after the first list failure and emits no control reply
// for them. The reader must not wait for frames that will never arrive.
//
//libtmux:real-tmux
func TestCommandListStopsAtFirstFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	for _, transport := range []struct {
		name   string
		handle tmux.Server
		suffix string
	}{
		{name: "process", handle: server, suffix: "_process"},
		{name: "control", handle: connected, suffix: "_control"},
	} {
		t.Run(transport.name, func(t *testing.T) {
			before, after := "@abort_before"+transport.suffix, "@abort_after"+transport.suffix
			result, err := transport.handle.Cmd(ctx,
				"set-option", "-g", before, "ran",
				";",
				"send-keys", "-t", "%999", "unreachable",
				";",
				"set-option", "-g", after, "ran",
			)
			if err != nil {
				t.Fatalf("failed command list returned a transport error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Errorf("failed command list reported exit 0: %#v", result)
			}

			scope := transport.handle.GlobalSessionScope()
			if _, ok, err := scope.RawOption(ctx, before); err != nil || !ok {
				t.Errorf("command before the failure did not run: (%t, %v)", ok, err)
			}
			if _, ok, err := scope.RawOption(ctx, after); err != nil || ok {
				t.Errorf("command after the failure ran: (%t, %v)", ok, err)
			}

			echoed, err := transport.handle.Cmd(ctx, "display-message", "-p", "still-aligned")
			if err != nil || len(echoed.Stdout) == 0 || echoed.Stdout[0] != "still-aligned" {
				t.Fatalf("command after a failed list = (%#v, %v)", echoed, err)
			}
		})
	}
}

// TestControlEngineReadsOptionsIdentically compares the option surface, whose
// values cross the transport as command output rather than as format rows.
//
//libtmux:real-tmux
func TestControlEngineReadsOptionsIdentically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%d, %v), want at least one", len(sessions), err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	connected := server.WithEngine(client.Engine())

	for _, name := range []string{"base-index", "status", "history-limit", "bell-action"} {
		processValue, processOK, processErr := sessions[0].RawOption(ctx, name)
		controlSession, err := connected.Session(ctx, sessions[0].ID())
		if err != nil {
			t.Fatalf("look up session over the control engine: %v", err)
		}
		controlValue, controlOK, controlErr := controlSession.RawOption(ctx, name)

		got := fmt.Sprintf("%q %t %v", controlValue, controlOK, controlErr)
		want := fmt.Sprintf("%q %t %v", processValue, processOK, processErr)
		if got != want {
			t.Errorf("option %s: control engine = %s, process = %s", name, got, want)
		}
	}
}
