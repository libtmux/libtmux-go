package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
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

// TestControlEngineMaterializesTheSameStateAsProcesses is the transport
// equivalence gate.
//
// The control engine is only sound if a reply frame and a tmux process's stdout
// decode to the same values. That was verified when the engine landed on one
// tmux version, while the module supports 3.2a and newer. This test runs in CI
// against every supported version, so a release that renders a reply
// differently fails the build instead of silently returning different data on a
// connected handle.
//
//libtmux:real-tmux
func TestControlEngineMaterializesTheSameStateAsProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

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
	for index, processPane := range viaProcess.Panes() {
		controlPane := viaControl.Panes()[index]
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

// TestTransportsAgreeOnLiteralValues compares what tmux ends up storing when a
// typed operation carries an awkward value.
//
// The two transports protect a value in opposite ways. A tmux process hands its
// argv to tmux's outer command parser, so a value ending in a semicolon is
// escaped on the way there. A control connection quotes every argument instead,
// where that escape is consumed by nothing and lands in the stored value. This
// is the gate on that: the escape belongs to one transport, and every value
// below round-trips identically through either.
//
//libtmux:real-tmux
func TestTransportsAgreeOnLiteralValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

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

// TestTransportsAgreeOnCommandLists is the gate on [tmux.Server.Cmd] submitting
// several commands in one call.
//
// tmux answers a command list with one reply frame per command, so a control
// connection has more than one reply to read for a single request. Reading only
// the first leaves the rest queued, and the next command then receives an
// earlier command's reply -- a desync that persists for the life of the
// connection. The trailing reads here are what catch that: they matter as much
// as the list itself.
//
//libtmux:real-tmux
func TestTransportsAgreeOnCommandLists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

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

// TestCommandListStopsAtFirstFailure gates the abort semantics a chained plan
// depends on, on both transports.
//
// tmux runs a command list until one command fails, drops the rest, and reports
// the failure. A control connection additionally sends no frame at all for a
// dropped command, so a reader that expects one reply per command in the list
// waits for a frame that never arrives. The read after the failed list is what
// proves it did not.
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
	server := tmuxtest.NewServer(ctx, t).WithStrictErrors()

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
