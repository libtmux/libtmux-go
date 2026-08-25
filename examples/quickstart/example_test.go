package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

const (
	arenaDescriptorVariable = "LIBTMUX_ARENA_DESCRIPTOR"
	arenaArtifactVariable   = "LIBTMUX_ARENA_ARTIFACT"
	arenaSocketVariable     = "LIBTMUX_SOCKET_PATH"
	arenaBinaryVariable     = "LIBTMUX_TMUX_BIN"
	arenaArtifact           = "go-quickstart"
)

type arenaEndpoint struct {
	binary     string
	socketPath string
}

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

// TestQuickstart runs the example itself against a real tmux. The harness gives
// it a server on a socket path of its own and takes it away afterwards, so this
// reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
func TestQuickstart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server, arena, err := quickstartServer(ctx, t, os.LookupEnv)
	if err != nil {
		t.Fatalf("select arena endpoint: %v", err)
	}
	printed := exampletest.Output(t, func() error {
		return run(ctx, server)
	})

	// The example is only finished when the pane it built has echoed back what
	// was sent to it, which is what proves the keys arrived and the capture read
	// them rather than the pane merely existing.
	if want := "libtmux ready"; !strings.Contains(printed, want) {
		t.Fatalf("printed %q, want it to contain %q", printed, want)
	}
	if arena {
		if err := emitArenaEvidence(ctx, server, server.SocketPath()); err != nil {
			t.Fatalf("emit arena evidence: %v", err)
		}
	}
}

func quickstartServer(
	ctx context.Context,
	t testing.TB,
	lookup func(string) (string, bool),
) (tmux.Server, bool, error) {
	t.Helper()
	endpoint, active, err := arenaEndpointFromLookup(lookup)
	if err != nil {
		return tmux.Server{}, false, err
	}
	if active {
		return tmux.NewServer(tmux.ServerOptions{
			Binary:     endpoint.binary,
			SocketPath: endpoint.socketPath,
		}), true, nil
	}
	return tmuxtest.NewServer(ctx, t), false, nil
}

func arenaEndpointFromLookup(lookup func(string) (string, bool)) (arenaEndpoint, bool, error) {
	descriptor, _ := lookup(arenaDescriptorVariable)
	if descriptor == "" {
		return arenaEndpoint{}, false, nil
	}
	artifact, _ := lookup(arenaArtifactVariable)
	socketPath, _ := lookup(arenaSocketVariable)
	binary, _ := lookup(arenaBinaryVariable)
	if artifact == "" || socketPath == "" || binary == "" {
		return arenaEndpoint{}, false, errors.New("arena contract is incomplete")
	}
	if artifact != arenaArtifact {
		return arenaEndpoint{}, false, fmt.Errorf("arena artifact %q does not match %q", artifact, arenaArtifact)
	}
	return arenaEndpoint{binary: binary, socketPath: socketPath}, true, nil
}

func emitArenaEvidence(ctx context.Context, server tmux.Server, socketPath string) error {
	evidence, err := arenaEvidence(ctx, server, socketPath)
	if err != nil {
		return err
	}
	_, err = fmt.Printf("LIBTMUX_ARENA_EVIDENCE=%s\n", evidence)
	return err
}

func arenaEvidence(ctx context.Context, server tmux.Server, socketPath string) ([]byte, error) {
	serverPID, err := arenaServerPID(ctx, server)
	if err != nil {
		return nil, err
	}
	actualSocketPath, err := arenaServerSocketPath(ctx, server)
	if err != nil {
		return nil, err
	}
	if actualSocketPath != socketPath {
		return nil, fmt.Errorf("arena socket path %q does not match requested %q", actualSocketPath, socketPath)
	}
	challenge, present, err := server.GlobalSessionScope().RawOption(
		ctx,
		"@libtmux_arena_challenge",
	)
	if err != nil {
		return nil, fmt.Errorf("read arena challenge: %w", err)
	}
	if !present || challenge == "" {
		return nil, errors.New("arena challenge is empty")
	}
	return json.Marshal(struct {
		Artifact   string `json:"artifact"`
		Challenge  string `json:"challenge"`
		Schema     int    `json:"schema"`
		ServerPID  int    `json:"server_pid"`
		SocketPath string `json:"socket_path"`
	}{
		Artifact:   arenaArtifact,
		Challenge:  challenge,
		Schema:     1,
		ServerPID:  serverPID,
		SocketPath: actualSocketPath,
	})
}

func arenaServerPID(ctx context.Context, server tmux.Server) (int, error) {
	value, err := arenaDisplayMessage(ctx, server, "#{pid}")
	if err != nil {
		return 0, fmt.Errorf("read arena server pid: %w", err)
	}
	serverPID, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse arena server pid %q: %w", value, err)
	}
	return serverPID, nil
}

func arenaServerSocketPath(ctx context.Context, server tmux.Server) (string, error) {
	value, err := arenaDisplayMessage(ctx, server, "#{socket_path}")
	if err != nil {
		return "", fmt.Errorf("read arena server socket path: %w", err)
	}
	return value, nil
}

func arenaDisplayMessage(ctx context.Context, server tmux.Server, format string) (string, error) {
	result, err := server.Cmd(ctx, "display-message", "-p", format)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 || len(result.Stdout) != 1 {
		return "", fmt.Errorf("exit %d, stdout %q", result.ExitCode, result.Stdout)
	}
	return result.Stdout[0], nil
}

func TestArenaEndpointFromLookup(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		environment map[string]string
		want        arenaEndpoint
		active      bool
		wantErr     bool
	}{
		{name: "ordinary", environment: nil},
		{
			name: "aliases alone",
			environment: map[string]string{
				arenaSocketVariable: "/tmp/arena.sock",
				arenaBinaryVariable: "/usr/bin/tmux",
			},
		},
		{
			name: "empty descriptor",
			environment: map[string]string{
				arenaDescriptorVariable: "",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "/tmp/arena.sock",
				arenaBinaryVariable:     "/usr/bin/tmux",
			},
		},
		{
			name: "descriptor only",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
			},
			wantErr: true,
		},
		{
			name: "empty artifact",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   "",
				arenaSocketVariable:     "/tmp/arena.sock",
				arenaBinaryVariable:     "/usr/bin/tmux",
			},
			wantErr: true,
		},
		{
			name: "empty socket path",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "",
				arenaBinaryVariable:     "/usr/bin/tmux",
			},
			wantErr: true,
		},
		{
			name: "empty binary",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "/tmp/arena.sock",
				arenaBinaryVariable:     "",
			},
			wantErr: true,
		},
		{
			name: "partial endpoint",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "/tmp/arena.sock",
			},
			wantErr: true,
		},
		{
			name: "wrong artifact",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   "other-example",
				arenaSocketVariable:     "/tmp/arena.sock",
				arenaBinaryVariable:     "/usr/bin/tmux",
			},
			wantErr: true,
		},
		{
			name: "complete contract",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "/tmp/arena.sock",
				arenaBinaryVariable:     "/usr/bin/tmux",
			},
			want: arenaEndpoint{
				binary:     "/usr/bin/tmux",
				socketPath: "/tmp/arena.sock",
			},
			active: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, present := testCase.environment[name]
				return value, present
			}
			got, active, err := arenaEndpointFromLookup(lookup)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("arenaEndpointFromLookup() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("arenaEndpointFromLookup() error = %v", err)
			}
			if active != testCase.active {
				t.Fatalf("arenaEndpointFromLookup() active = %v, want %v", active, testCase.active)
			}
			if got != testCase.want {
				t.Fatalf("arenaEndpointFromLookup() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestQuickstartArenaEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	external := tmuxtest.NewServer(ctx, t)
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("resolve tmux binary: %v", err)
	}
	probeDirectory := t.TempDir()
	markerPath := filepath.Join(probeDirectory, "invocations")
	wrapperPath := filepath.Join(probeDirectory, "tmux-arena-wrapper")
	const wrapper = `#!/bin/sh
printf '%s\n' "$0" >> "$LIBTMUX_ARENA_BINARY_MARKER"
exec "$LIBTMUX_ARENA_REAL_TMUX" "$@"
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o700); err != nil {
		t.Fatalf("write tmux executable wrapper: %v", err)
	}
	challenge := "quickstart-arena"
	if _, err := external.Cmd(
		ctx,
		"set-option",
		"-g",
		"@libtmux_arena_challenge",
		challenge,
	); err != nil {
		t.Fatalf("set arena challenge: %v", err)
	}
	t.Setenv(arenaDescriptorVariable, "arena")
	t.Setenv(arenaArtifactVariable, arenaArtifact)
	t.Setenv(arenaSocketVariable, external.SocketPath())
	t.Setenv(arenaBinaryVariable, wrapperPath)
	t.Setenv("LIBTMUX_ARENA_BINARY_MARKER", markerPath)
	t.Setenv("LIBTMUX_ARENA_REAL_TMUX", binary)

	server, active, err := quickstartServer(ctx, t, os.LookupEnv)
	if err != nil {
		t.Fatalf("select arena endpoint: %v", err)
	}
	if !active {
		t.Fatal("select arena endpoint: inactive")
	}
	printed := exampletest.Output(t, func() error {
		return run(ctx, server)
	})
	if want := "libtmux ready"; !strings.Contains(printed, want) {
		t.Fatalf("printed %q, want it to contain %q", printed, want)
	}
	invocations, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read tmux executable invocations: %v", err)
	}
	for _, invocation := range strings.Split(strings.TrimSpace(string(invocations)), "\n") {
		if invocation != wrapperPath {
			t.Fatalf("tmux executable invocation = %q, want %q", invocation, wrapperPath)
		}
	}

	printedEvidence := exampletest.Output(t, func() error {
		return emitArenaEvidence(ctx, server, external.SocketPath())
	})
	const evidencePrefix = "LIBTMUX_ARENA_EVIDENCE="
	if !strings.HasPrefix(printedEvidence, evidencePrefix) {
		t.Fatalf("printed evidence %q, want prefix %q", printedEvidence, evidencePrefix)
	}
	var got struct {
		Artifact   string `json:"artifact"`
		Challenge  string `json:"challenge"`
		Schema     int    `json:"schema"`
		ServerPID  int    `json:"server_pid"`
		SocketPath string `json:"socket_path"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(
		strings.TrimPrefix(printedEvidence, evidencePrefix),
	)), &got); err != nil {
		t.Fatalf("decode arena evidence: %v", err)
	}
	if got.Artifact != arenaArtifact || got.Challenge != challenge || got.Schema != 1 ||
		got.ServerPID <= 0 || got.SocketPath != external.SocketPath() {
		t.Fatalf("arena evidence = %#v", got)
	}
	if alive, err := external.IsAlive(ctx); err != nil || !alive {
		t.Fatalf("external server alive = %v, error = %v", alive, err)
	}
}
