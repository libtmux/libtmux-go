package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func optionalOldTmux(t *testing.T) string {
	return optionalFeatureTmux(t, "LIBTMUX_OPTIONAL_OLD_TMUX", "3.3a")
}

func optionalFeatureTmux(t *testing.T, variable, version string) string {
	t.Helper()
	binary := os.Getenv(variable)
	if binary == "" {
		binary, _ = exec.LookPath("tmux")
	}
	output, err := exec.CommandContext(t.Context(), binary, "-V").Output()
	if err != nil || strings.TrimSpace(string(output)) != "tmux "+version {
		if os.Getenv(variable) == "" {
			t.Skipf("set %s to exact tmux %s", variable, version)
		}
		t.Fatalf("optional fixture: %q %v", output, err)
	}
	return binary
}

func TestOptionalExact37BreakPaneKeepsItsNamingContract(t *testing.T) {
	binary := optionalFeatureTmux(t, "LIBTMUX_BREAK_PANE_TMUX", "3.7")
	for _, count := range []int{1, 2} {
		t.Run(string(rune('0'+count))+" panes", func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "s")
			versionNativeCommand(t, binary, socket, "new-session", "-d", "-s", "keeper", "-n", "original", "sleep 300")
			t.Cleanup(func() { _ = exec.Command(binary, "-S", socket, "kill-server").Run() })
			if count == 2 {
				versionNativeCommand(t, binary, socket, "split-window", "-d", "-t", "keeper:0", "sleep 300")
			}
			server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary, SocketPath: socket, ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			sessions, err := server.Sessions(t.Context())
			if err != nil || len(sessions) != 1 {
				t.Fatalf("sessions: %v %v", sessions, err)
			}
			pane, err := sessions[0].ResolveActivePane(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			before := versionNativeCommand(t, binary, socket, "list-windows", "-F", "#{window_id}:#{window_name}:#{window_panes}")
			for _, arguments := range [][]string{
				{"break-pane", "-d", "-s", pane.ID().String()},
				{"breakp", "-d", "-n", "renamed", "-s", pane.ID().String()},
				{"br", "-d", "-n", "renamed", "-s", pane.ID().String()},
			} {
				if _, err := server.Cmd(t.Context(), arguments...); !errors.Is(err, tmux.ErrVersionTooLow) {
					t.Fatalf("raw exact3.7 break = %v", err)
				}
			}
			if _, err := pane.BreakPane(t.Context(), tmux.BreakPaneRequest{}); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("unnamed exact3.7 break = %v", err)
			}
			if after := versionNativeCommand(t, binary, socket, "list-windows", "-F", "#{window_id}:#{window_name}:#{window_panes}"); after != before {
				t.Fatalf("refusal changed topology: %q => %q", before, after)
			}
			broken, err := pane.BreakPane(t.Context(), tmux.BreakPaneRequest{Name: "repaired"})
			if err != nil {
				t.Fatalf("explicit exact3.7 repair: %v", err)
			}
			if name, _ := broken.Name(); name != "repaired" {
				t.Fatalf("repaired name = %q", name)
			}
		})
	}
}

func TestOptional34BufferAnd35OutputBoundaries(t *testing.T) {
	for _, version := range []string{"3.4", "3.5"} {
		t.Run(version, func(t *testing.T) {
			variable := "LIBTMUX_BUFFER_GOOD_TMUX"
			if version == "3.5" {
				variable = "LIBTMUX_OUTPUT_GOOD_TMUX"
			}
			binary := optionalFeatureTmux(t, variable, version)
			socket := filepath.Join(t.TempDir(), "s")
			versionNativeCommand(t, binary, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
			t.Cleanup(func() { versionNativeCommand(t, binary, socket, "kill-server") })
			versionNativeCommand(t, binary, socket, "set-buffer", "-b", "sentinel", "original")
			server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary, SocketPath: socket, ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			if err := server.DeleteBuffer(t.Context(), new("absent")); !errors.Is(err, tmux.ErrCommand) {
				t.Fatalf("exact missing buffer delete = %v", err)
			}
			result, err := server.Cmd(t.Context(), "set-buffer", "-b", "absent", "-n", "renamed")
			if err != nil || result.ExitCode == 0 {
				t.Fatalf("exact missing rename: %+v %v", result, err)
			}
			if got := versionNativeCommand(t, binary, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
				t.Fatalf("native missing-target refusal changed sentinel: %q", got)
			}
			versionNativeCommand(t, binary, socket, "set-buffer", "-b", "present", "delete-me")
			if err := server.DeleteBuffer(t.Context(), new("present")); err != nil {
				t.Fatalf("supported named deletion: %v", err)
			}
			if version == "3.4" {
				for _, verb := range []string{"split-window", "splitw", "sp"} {
					_, err := server.Cmd(t.Context(), "set-buffer", "-b", "unexpected", "changed;", verb, "-d", "-p25", "-t", "keeper:0")
					if !errors.Is(err, tmux.ErrVersionTooLow) {
						t.Errorf("3.4 raw percentage split = %v", err)
					}
				}
				if got := versionNativeCommand(t, binary, socket, "list-buffers", "-F", "#{buffer_name}"); got != "sentinel\n" {
					t.Fatalf("3.4 percentage refusal changed prior batch state: %q", got)
				}
			} else {
				result, err := server.Cmd(t.Context(), "split-window", "-d", "-p25", "-t", "keeper:0", "sleep 300")
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("restored 3.5 raw percentage = %v %+v", err, result)
				}
			}
			output, err := server.RunShell(t.Context(), tmux.RunShellRequest{Command: "printf 'captured\\n'"})
			if version == "3.4" {
				if !errors.Is(err, tmux.ErrVersionTooLow) {
					t.Fatalf("3.4 captured output = %q %v", output, err)
				}
			} else if err != nil || strings.Join(output, "\n") != "captured" {
				t.Fatalf("3.5 captured output = %q %v", output, err)
			}
		})
	}
}

func TestOptionalControlRejectsOlderSupportedDaemon(t *testing.T) {
	old := optionalOldTmux(t)
	nativeBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() { versionNativeCommand(t, old, socket, "kill-server") })
	record := filepath.Join(t.TempDir(), "control-attempt")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
for argument do
    if [ "$argument" = run-shell ]; then probe=1; fi
    if [ "$argument" = -C ] && [ -z "$probe" ]; then printf 'unsafe\n' >> "$PIN_RECORD"; fi
done
exec "$PIN_REAL" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: proxy, SocketPath: socket, ConfigFile: os.DevNull,
		ProcessEnvironment: append(os.Environ(), "PIN_RECORD="+record, "PIN_REAL="+nativeBinary),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := server.Sessions(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("core listing: %v %v", sessions, err)
	}
	pane, err := sessions[0].ResolveActivePane(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func(context.Context) error{
		"server attach": func(ctx context.Context) error {
			client, err := server.OpenControl(ctx, sessions[0])
			if client != nil {
				_ = client.Close()
			}
			return err
		},
		"session connection": func(ctx context.Context) error {
			connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{})
			if connection != nil {
				_ = connection.Close()
			}
			return err
		},
		"new session connection": func(ctx context.Context) error {
			_, connection, err := server.NewSessionConnection(ctx, tmux.NewSessionRequest{Name: "forbidden"}, tmux.ConnectionOptions{})
			if connection != nil {
				_ = connection.Close()
			}
			return err
		},
		"pane observation": func(ctx context.Context) error {
			observation, err := pane.OpenObservation(ctx)
			if observation != nil {
				_ = observation.Close()
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := call(ctx); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("optional acquisition error = %v", err)
			}
		})
	}
	if data, err := os.ReadFile(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old daemon saw a control process: %q %v", data, err)
	}
	if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
		t.Fatalf("refusal changed sessions: %q", got)
	}
}

func TestOptionalColdControlRejectsOlderExecutable(t *testing.T) {
	old := optionalOldTmux(t)
	socket := filepath.Join(t.TempDir(), "s")
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: old, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	_, connection, err := server.NewSessionConnection(t.Context(), tmux.NewSessionRequest{Name: "forbidden"}, tmux.ConnectionOptions{})
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("cold optional refusal = %v", err)
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused cold acquisition created socket: %v", err)
	}
}

func TestOptionalColdBootstrapKeepsItsFeatureFloor(t *testing.T) {
	old := optionalOldTmux(t)
	nativeBinary := optionalControlBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	count := filepath.Join(t.TempDir(), "probes")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
case "$*" in
    *'#{q:pid}|#{q:start_time}|#{q:socket_path}|#{q:version}='*)
        count=0
        if [ -f "$PIN_COUNT" ]; then read -r count < "$PIN_COUNT"; fi
        count=$((count + 1))
        printf '%s\n' "$count" > "$PIN_COUNT"
        if [ "$count" -eq 3 ]; then
            "$PIN_OLD" -f /dev/null -S "$PIN_ORIGINAL" new-session -d -s keeper 'sleep 300' || exit 92
        fi
        ;;
esac
exec "$PIN_REAL" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: proxy, SocketPath: socket, ConfigFile: os.DevNull,
		ProcessEnvironment: append(os.Environ(), "PIN_COUNT="+count, "PIN_OLD="+old, "PIN_ORIGINAL="+socket, "PIN_REAL="+nativeBinary),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(old, "-S", socket, "kill-server").Run() })
	_, connection, err := server.NewSessionConnection(t.Context(), tmux.NewSessionRequest{Name: "forbidden"}, tmux.ConnectionOptions{})
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("cold race refusal = %v", err)
	}
	if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
		t.Fatalf("optional bootstrap created on an older daemon: %q", got)
	}
}

func TestPinnedControlSocketPreventsReplacement(t *testing.T) {
	old := optionalOldTmux(t)
	nativeBinary := optionalControlBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, nativeBinary, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() { versionNativeCommand(t, nativeBinary, socket, "kill-server") })
	record := filepath.Join(t.TempDir(), "control-endpoint")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
control=no
endpoint=
for argument do
    if [ "$argument" = run-shell ]; then probe=1; fi
    if [ "$argument" = -C ] && [ -z "$probe" ]; then control=yes; fi
    case "$argument" in -S*) endpoint=${argument#-S} ;; esac
done
if [ "$control" = yes ]; then
    if [ "$endpoint" = "$PIN_ORIGINAL" ] || ! [ "$endpoint" -ef "$PIN_ORIGINAL" ]; then
        printf 'unsafe\n' > "$PIN_RECORD"
    else
        printf 'pinned\n' > "$PIN_RECORD"
    fi
    "$PIN_REAL" -S "$PIN_ORIGINAL" kill-server || exit 91
    "$PIN_OLD" -f /dev/null -S "$PIN_ORIGINAL" new-session -d -s keeper 'sleep 300' || exit 92
fi
exec "$PIN_REAL" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: proxy, SocketPath: socket, ConfigFile: os.DevNull,
		ProcessEnvironment: append(os.Environ(), "PIN_RECORD="+record, "PIN_REAL="+nativeBinary, "PIN_OLD="+old, "PIN_ORIGINAL="+socket),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := server.Sessions(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := server.OpenControl(ctx, sessions[0])
	if client != nil {
		_ = client.Close()
	}
	if err == nil {
		t.Fatal("replacement acquired control")
	}
	if data, err := os.ReadFile(record); err != nil || string(data) != "pinned\n" {
		t.Fatalf("control launch did not use the same socket inode: %q %v", data, err)
	}
	if got := versionNativeCommand(t, old, socket, "list-clients", "-F", "#{client_control_mode}"); got != "" {
		t.Fatalf("replacement received control client: %q", got)
	}
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(socket), ".lc-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("control carrier leaked: %v %v", entries, err)
	}
}

func TestOptionalRefusalsLeaveOlderCoreStateIntact(t *testing.T) {
	old := optionalOldTmux(t)
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() { versionNativeCommand(t, old, socket, "kill-server") })
	versionNativeCommand(t, old, socket, "set-buffer", "-b", "sentinel", "original")
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := server.Sessions(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("core sessions: %v %v", sessions, err)
	}
	pane, err := sessions[0].ResolveActivePane(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	output := filepath.Join(directory, "capture")
	jobMarker := filepath.Join(directory, "job-marker")
	for name, call := range map[string]func() error{
		"typed named delete":      func() error { return server.DeleteBuffer(t.Context(), new("absent")) },
		"raw named delete":        func() error { _, err := server.Cmd(t.Context(), "delete-buffer", "-b", "absent"); return err },
		"raw abbreviated delete":  func() error { _, err := server.Cmd(t.Context(), "del", "-b", "absent"); return err },
		"raw joined alias delete": func() error { _, err := server.Cmd(t.Context(), "deleteb", "-babsent"); return err },
		"raw named rename": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-b", "absent", "-n", "renamed")
			return err
		},
		"raw rename with flag shaped target": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-t", "-n", "-b", "absent", "-n", "renamed")
			return err
		},
		"raw abbreviated rename":  func() error { _, err := server.Cmd(t.Context(), "set-b", "-b", "absent", "-n", "renamed"); return err },
		"raw joined alias rename": func() error { _, err := server.Cmd(t.Context(), "setb", "-babsent", "-nrenamed"); return err },
		"batch preflight": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-b", "unexpected", "changed", ";", "delete-buffer", "-b", "absent")
			return err
		},
		"suffix batch preflight": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-b", "unexpected", "changed;", "delete-buffer", "-b", "absent")
			return err
		},
		"verb suffix batch preflight": func() error {
			_, err := server.Cmd(t.Context(), "list-sessions;", "delete-buffer", "-b", "absent")
			return err
		},
		"plan preflight": func() error {
			var plan tmux.Plan
			plan.SetBuffer("unexpected", "changed")
			plan.DeleteBuffer("absent")
			_, err := plan.Run(t.Context(), server)
			return err
		},
		"capture staging": func() error { _, err := pane.CaptureToFile(t.Context(), output, tmux.CapturePaneRequest{}); return err },
		"captured shell output": func() error {
			_, err := server.RunShell(t.Context(), tmux.RunShellRequest{Command: "touch " + jobMarker})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("optional refusal = %v", err)
			}
			if got := versionNativeCommand(t, old, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
				t.Fatalf("refusal changed unrelated top buffer: %q", got)
			}
			if got := versionNativeCommand(t, old, socket, "list-buffers", "-F", "#{buffer_name}"); got != "sentinel\n" {
				t.Fatalf("refusal staged or renamed a buffer: %q", got)
			}
		})
	}
	for _, path := range []string{output, jobMarker} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refusal wrote %s: %v", filepath.Base(path), err)
		}
	}
	// Native named paste-and-delete resolves an exact buffer even on 3.3a.
	if err := server.SetBuffer(t.Context(), tmux.SetBufferRequest{Name: new("paste-once"), Data: "pasted"}); err != nil {
		t.Fatal(err)
	}
	if err := pane.PasteBuffer(t.Context(), tmux.PasteBufferRequest{BufferName: new("paste-once"), DeleteAfter: true}); err != nil {
		t.Fatalf("safe named paste-and-delete = %v", err)
	}
	if names := versionNativeCommand(t, old, socket, "list-buffers", "-F", "#{buffer_name}"); names != "sentinel\n" {
		t.Fatalf("named paste-and-delete changed the wrong buffer: %q", names)
	}
	if err := pane.PasteBuffer(t.Context(), tmux.PasteBufferRequest{BufferName: new("absent"), DeleteAfter: true}); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("missing named paste-and-delete = %v", err)
	}
	if got := versionNativeCommand(t, old, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
		t.Fatalf("missing named paste-and-delete changed sentinel: %q", got)
	}
	// Pane-targeted jobs do not promise client stdout capture.
	jobOutput, err := server.RunShell(t.Context(), tmux.RunShellRequest{
		Command: "printf pane-output > " + jobMarker, TargetPane: pane.ID(),
	})
	if err != nil || len(jobOutput) != 0 {
		t.Fatalf("older pane-targeted job = %q %v", jobOutput, err)
	}
	if contents, err := os.ReadFile(jobMarker); err != nil || string(contents) != "pane-output" {
		t.Fatalf("older pane-targeted job did not run: %q %v", contents, err)
	}
	if err := server.DeleteBuffer(t.Context(), nil); err != nil {
		t.Fatalf("ordinary top-buffer deletion refused: %v", err)
	}
}

func optionalControlBinary(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary, SocketPath: filepath.Join(t.TempDir(), "s"), ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion(tmux.MinimumConnectionVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RequireVersion(t.Context(), minimum); err != nil {
		if errors.Is(err, tmux.ErrVersionTooLow) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	return binary
}
