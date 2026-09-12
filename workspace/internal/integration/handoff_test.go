//go:build linux

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/workspace/internal/cli"
	"golang.org/x/term"
)

type handoffProcessConfig struct {
	Args                []string
	Result, Cancel, Raw string
}

type handoffProcessResult struct {
	Code       int
	Restored   bool
	Diagnostic string
}

func TestHumanLoadTerminal(t *testing.T) {
	for _, mode := range []string{"no-controlling-terminal", "detach", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, server, _ := handoffServer(t)
			dir := t.TempDir()
			marker := filepath.Join(dir, "script-ran")
			script := write(t, dir, "before.sh", "printf ran > '"+marker+"'\n")
			path := handoffDocument(t, "/bin/sh "+script)
			config := handoffProcessConfig{
				Args:   []string{"load", path, "-y", "--no-progress", "-S", server.SocketPath()},
				Result: filepath.Join(dir, "result"), Cancel: filepath.Join(dir, "cancel"), Raw: filepath.Join(dir, "raw"),
			}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			environment := []string{}
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "TMUX" && key != "TMUX_PANE" && key != "TERM" {
					environment = append(environment, entry)
				}
			}
			environment = append(environment, "TMUX=", "TERM=xterm-256color", "GO_HANDOFF_CONFIG="+string(encoded), "GO_HANDOFF_MODE="+mode)
			process := tmuxtest.StartPTYProcess(ctx, t, os.Args[0], []string{"-test.run=^TestHumanLoadProcess$"}, environment)
			handled := false
			var data []byte
			handoffWait(ctx, t, func() bool {
				data, err = os.ReadFile(config.Result)
				if err == nil {
					return true
				}
				select {
				case <-process.Done():
					t.Fatalf("terminal helper exited without result: %v %q", process.Wait(ctx), process.Output())
				default:
				}
				clients, err := server.Clients(ctx)
				if err != nil || len(clients) != 1 || handled {
					return false
				}
				if mode == "cancel" {
					if _, err := os.Stat(config.Raw); err != nil {
						return false
					}
					if err := os.WriteFile(config.Cancel, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					result, err := server.Cmd(ctx, "detach-client", "-t", clients[0].Name().String())
					if err != nil || result.ExitCode != 0 {
						t.Fatalf("detach terminal helper: %+v %v", result, err)
					}
				}
				handled = true
				return false
			})
			if err := process.Wait(ctx); err != nil {
				t.Fatalf("terminal helper: %v %q", err, process.Output())
			}
			var result handoffProcessResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("terminal result: %s %v", data, err)
			}
			snapshot, err := server.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, markerErr := os.Stat(marker)
			if mode == "no-controlling-terminal" {
				if result.Code == 0 || handled || !os.IsNotExist(markerErr) || len(snapshot.Sessions()) != 1 {
					t.Fatalf("missing controlling tty reached effects: %+v attached=%v marker=%v sessions=%d", result, handled, markerErr, len(snapshot.Sessions()))
				}
				return
			}
			wantCode := 0
			if mode == "cancel" {
				wantCode = 130
			}
			if result.Code != wantCode || !handled || !result.Restored || len(snapshot.Clients()) != 0 || len(snapshot.Sessions()) != 2 || markerErr != nil || (mode == "cancel" && !strings.Contains(result.Diagnostic, "handoff-loaded")) {
				t.Fatalf("terminal handoff: %+v attached=%v clients=%d sessions=%d marker=%v", result, handled, len(snapshot.Clients()), len(snapshot.Sessions()), markerErr)
			}
		})
	}
}

func TestHumanLoadProcess(t *testing.T) {
	mode := os.Getenv("GO_HANDOFF_MODE")
	if mode == "" {
		return
	}
	if mode == "no-controlling-terminal" {
		command := exec.Command(os.Args[0], "-test.run=^TestHumanLoadProcess$")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GO_HANDOFF_MODE=") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "GO_HANDOFF_MODE=run")
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	var config handoffProcessConfig
	if err := json.Unmarshal([]byte(os.Getenv("GO_HANDOFF_CONFIG")), &config); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), before) }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		for {
			current, err := term.GetState(int(os.Stdin.Fd()))
			if err == nil && !reflect.DeepEqual(current, before) {
				_ = os.WriteFile(config.Raw, nil, 0o600)
			}
			if _, err := os.Stat(config.Cancel); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	var diagnostic bytes.Buffer
	code := cli.Run(ctx, config.Args, os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &diagnostic))
	cancel()
	<-joined
	after, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(handoffProcessResult{code, reflect.DeepEqual(before, after), diagnostic.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Result, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func handoffServer(t *testing.T) (context.Context, tmux.Server, tmux.Pane) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "original"},
	})
	snapshot, err := server.Snapshot(ctx)
	if err != nil || len(snapshot.Panes()) != 1 {
		t.Fatalf("initial snapshot: %v %v", snapshot.Panes(), err)
	}
	return ctx, server, snapshot.Panes()[0]
}

func handoffClient(ctx context.Context, t *testing.T, server tmux.Server, repeat bool) tmux.Client {
	t.Helper()
	before, err := server.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" && key != "TMUX_PANE" && key != "TERM" {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "TERM=xterm-256color")
	binary := server.Executable()
	args := []string{"-S", server.SocketPath(), "attach-session", "-t", "original"}
	if repeat {
		args = append([]string{"-c", `"$@"; exec "$@"`, "repeat-client", binary}, args...)
		binary = "/bin/sh"
	}
	process := tmuxtest.StartPTYProcess(ctx, t, binary, args, environment)
	var selected tmux.Client
	handoffWait(ctx, t, func() bool {
		select {
		case <-process.Done():
			t.Fatalf("fixture client exited: %v %q", process.Wait(ctx), process.Output())
		default:
		}
		clients, err := server.Clients(ctx)
		if err != nil || len(clients) != len(before)+1 {
			return false
		}
		for _, client := range clients {
			found := false
			for _, old := range before {
				found = found || old.Name() == client.Name()
			}
			if !found {
				selected = client
				return true
			}
		}
		return false
	})
	return selected
}

func handoffInput(t *testing.T, pane tmux.Pane) *os.File {
	t.Helper()
	tty, ok := pane.TTY()
	if !ok {
		t.Fatal("fixture pane has no tty")
	}
	input, err := os.OpenFile(tty, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	return input
}

func handoffDocument(t *testing.T, script string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_name": "handoff-loaded", "before_script": script,
		"windows": []any{map[string]any{"panes": []any{nil}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return write(t, t.TempDir(), "workspace.json", string(data))
}

func TestHumanLoadPreflight(t *testing.T) {
	for _, mode := range []string{"endpoint", "stale-pid", "wrong-terminal", "no-client", "ambiguous", "detached", "append", "choice-detached", "choice-append"} {
		t.Run(mode, func(t *testing.T) {
			ctx, server, pane := handoffServer(t)
			input := handoffInput(t, pane)
			t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
			t.Setenv("TMUX_PANE", pane.ID().String())
			if mode != "no-client" {
				handoffClient(ctx, t, server, false)
			}
			selected := server
			flags := []string{"-y"}
			var reader io.Reader = input
			control := false
			switch mode {
			case "endpoint":
				_, selected, _ = handoffServer(t)
			case "stale-pid":
				t.Setenv("TMUX", server.SocketPath()+",1,0")
			case "wrong-terminal":
				_, _, other := handoffServer(t)
				reader = handoffInput(t, other)
			case "ambiguous":
				handoffClient(ctx, t, server, false)
			case "detached", "append", "choice-detached", "choice-append":
				control = true
				reader = strings.NewReader("n\n")
				flags = []string{"-d"}
				switch mode {
				case "append":
					flags = []string{"-d", "--append"}
				case "choice-detached":
					flags = nil
				case "choice-append":
					flags = nil
					reader = strings.NewReader("a\n")
				}
			}
			marker := filepath.Join(t.TempDir(), "script")
			script := write(t, t.TempDir(), "before.sh", "printf ran > '"+marker+"'\n")
			path := handoffDocument(t, "/bin/sh "+script)
			args := append([]string{"load", path, "--no-progress", "-S", selected.SocketPath()}, flags...)
			var out, diagnostic bytes.Buffer
			code := cli.Run(ctx, args, reader, &out, &diagnostic)
			snapshot, err := selected.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, markerErr := os.Stat(marker)
			if control {
				if code != 0 || markerErr != nil || len(snapshot.Windows()) != 2 {
					t.Fatalf("mode precedence: code=%d marker=%v windows=%d stderr=%q", code, markerErr, len(snapshot.Windows()), diagnostic.String())
				}
			} else if code == 0 || !os.IsNotExist(markerErr) || len(snapshot.Sessions()) != 1 {
				t.Fatalf("invalid handoff reached effects: code=%d marker=%v sessions=%d stdout=%q stderr=%q", code, markerErr, len(snapshot.Sessions()), out.String(), diagnostic.String())
			}
		})
	}
}

func TestHumanLoadRetainsClientGeneration(t *testing.T) {
	ctx, server, pane := handoffServer(t)
	selected := handoffClient(ctx, t, server, true)
	pid, _ := selected.ProcessPID()
	input := handoffInput(t, pane)
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", pane.ID().String())
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	script := write(t, dir, "before.sh", "printf ready > '"+started+"'\nwhile [ ! -f '"+release+"' ]; do sleep .01; done\n")
	path := handoffDocument(t, "/bin/sh "+script)
	var out, diagnostic bytes.Buffer
	done := make(chan int, 1)
	runCtx, cancel := context.WithCancel(ctx)
	joined := make(chan struct{})
	defer func() { cancel(); <-joined }()
	go func() {
		defer close(joined)
		done <- cli.Run(runCtx, []string{"load", path, "-y", "--no-progress", "-S", server.SocketPath()}, input, &out, &diagnostic)
	}()
	handoffWait(ctx, t, func() bool { _, err := os.Stat(started); return err == nil })
	result, err := server.Cmd(ctx, "detach-client", "-t", selected.Name().String())
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("detach original: %+v %v", result, err)
	}
	handoffWait(ctx, t, func() bool {
		live, err := server.Client(ctx, selected.Name())
		currentPID, ok := live.ProcessPID()
		return err == nil && ok && currentPID != pid
	})
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	select {
	case code = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	live, err := snapshot.ClientByName(selected.Name())
	if err != nil {
		t.Fatal(err)
	}
	attached, ok := live.AttachedSession()
	name, _ := attached.Name()
	if code == 0 || !ok || name != "original" || len(snapshot.Sessions()) != 2 || !strings.Contains(diagnostic.String(), "handoff-loaded") {
		t.Fatalf("replaced client handoff: code=%d attached=%q sessions=%d stdout=%q stderr=%q", code, name, len(snapshot.Sessions()), out.String(), diagnostic.String())
	}
}

type handoffFlushFailure struct{ bytes.Buffer }

func (*handoffFlushFailure) Flush() error { return errors.New("handoff flush rejected") }

func TestHumanLoadFlushBeforeHandoff(t *testing.T) {
	for _, prompt := range []bool{false, true} {
		t.Run(fmt.Sprint("prompt-", prompt), func(t *testing.T) {
			ctx, server, pane := handoffServer(t)
			selected := handoffClient(ctx, t, server, false)
			t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
			t.Setenv("TMUX_PANE", pane.ID().String())
			path := handoffDocument(t, "/bin/true")
			var rejected handoffFlushFailure
			var other bytes.Buffer
			var out, diagnostic io.Writer = &rejected, &other
			var input io.Reader = handoffInput(t, pane)
			args := []string{"load", path, "--no-progress", "-S", server.SocketPath()}
			if prompt {
				out, diagnostic = &other, &rejected
				input = strings.NewReader("n\n")
			} else {
				args = append(args, "-y")
			}
			code := cli.Run(ctx, args, input, out, diagnostic)
			snapshot, err := server.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			live, err := snapshot.ClientByName(selected.Name())
			if err != nil {
				t.Fatal(err)
			}
			attached, ok := live.AttachedSession()
			name, _ := attached.Name()
			wantSessions := 2
			if prompt {
				wantSessions = 1
			}
			if code == 0 || !ok || name != "original" || len(snapshot.Sessions()) != wantSessions || !strings.Contains(rejected.String()+other.String(), "handoff flush rejected") {
				t.Fatalf("flush boundary: code=%d attached=%q sessions=%d output=%q %q", code, name, len(snapshot.Sessions()), rejected.String(), other.String())
			}
		})
	}
}

func TestHumanLoadClientProjection(t *testing.T) {
	for _, independent := range []bool{false, true} {
		name := "ordinary"
		if independent {
			name = "independent-pane"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
				FixedShell:     true,
				InitialSession: &tmux.NewSessionRequest{Name: "original"},
			})
			before, err := server.Snapshot(ctx)
			if err != nil || len(before.Panes()) != 1 {
				t.Fatalf("initial snapshot: %v %v", before.Panes(), err)
			}
			first := before.Panes()[0]
			result, err := server.Cmd(ctx, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", first.ID().String())
			if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
				t.Fatalf("split fixture: %+v %v", result, err)
			}
			second, err := server.Pane(ctx, tmux.PaneID(result.Stdout[0]))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-S", server.SocketPath(), "-f", server.ConfigFile(), "attach-session", "-t", "original"}
			if independent {
				args = append(args, "-f", "active-pane")
			}
			environment := []string{}
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "TMUX" && key != "TMUX_PANE" && key != "TERM" {
					environment = append(environment, entry)
				}
			}
			environment = append(environment, "TERM=xterm-256color")
			client := tmuxtest.StartPTYProcess(ctx, t, server.Executable(), args, environment)
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("terminal transcript: %q", client.Output())
				}
			})
			handoffWait(ctx, t, func() bool {
				select {
				case <-client.Done():
					t.Fatalf("client exited before registration: %v; %q", client.Wait(ctx), client.Output())
				default:
				}
				clients, err := server.Clients(ctx)
				return err == nil && len(clients) == 1
			})
			dir := t.TempDir()
			focusPath := filepath.Join(dir, "focus")
			input := "\x02oprintf '%s' \"$TMUX_PANE\" > '" + focusPath + "'\n"
			if _, err := client.Write(ctx, []byte(input)); err != nil {
				t.Fatal(err)
			}
			handoffWait(ctx, t, func() bool {
				data, err := os.ReadFile(focusPath)
				return err == nil && string(data) == second.ID().String()
			})
			snapshot, err := server.Snapshot(ctx)
			if err != nil || len(snapshot.Clients()) != 1 {
				t.Fatalf("attached snapshot: %v %v", snapshot.Clients(), err)
			}
			selected := snapshot.Clients()[0]
			projected, ok := selected.Formats().PaneID()
			wantProjection := second.ID()
			if independent {
				wantProjection = first.ID()
			}
			if !ok || projected != wantProjection {
				t.Fatalf("client projection = %s, actual input pane = %s, want projection %s", projected, second.ID(), wantProjection)
			}
			t.Logf("input pane=%s list-clients pane=%s independent=%v", second.ID(), projected, independent)
			invoking := second
			if independent {
				invoking = first
			}
			tty, ok := invoking.TTY()
			if !ok {
				t.Fatal("fixture pane has no tty")
			}
			inputTTY, err := os.OpenFile(tty, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = inputTTY.Close() }()
			t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
			t.Setenv("TMUX_PANE", invoking.ID().String())
			marker := filepath.Join(dir, "script-ran")
			script := write(t, dir, "before.sh", "#!/bin/sh\nprintf ran > '"+marker+"'\n")
			document, err := json.Marshal(map[string]any{
				"session_name": "handoff-loaded", "before_script": "/bin/sh " + script,
				"windows": []any{map[string]any{"panes": []any{nil}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			path := write(t, dir, "workspace.json", string(document))
			var out, diagnostic bytes.Buffer
			code := cli.Run(ctx, []string{"load", path, "-y", "--no-progress", "-S", server.SocketPath()}, inputTTY, &out, &diagnostic)
			after, err := server.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, markerErr := os.Stat(marker)
			if independent {
				if code == 0 || !strings.Contains(diagnostic.String(), "active-pane") || !strings.Contains(diagnostic.String(), "-d") || !os.IsNotExist(markerErr) || len(after.Sessions()) != 1 {
					t.Fatalf("independent focus was not refused before effects: code=%d sessions=%d marker=%v stdout=%q stderr=%q", code, len(after.Sessions()), markerErr, out.String(), diagnostic.String())
				}
				return
			}
			if code != 0 || markerErr != nil || len(after.Sessions()) != 2 {
				t.Fatalf("ordinary handoff: code=%d sessions=%d marker=%v stdout=%q stderr=%q", code, len(after.Sessions()), markerErr, out.String(), diagnostic.String())
			}
			live, err := after.ClientByName(selected.Name())
			if err != nil {
				t.Fatal(err)
			}
			attached, ok := live.AttachedSession()
			attachedName, _ := attached.Name()
			if !ok || attachedName != "handoff-loaded" {
				t.Fatalf("selected client attached to %q, want handoff-loaded", attachedName)
			}
		})
	}
}

func handoffWait(ctx context.Context, t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
