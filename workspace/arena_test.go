package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/workspace"
)

// workspace is a separate module from examples, and an internal package can
// only be imported from within the directory tree rooted at its parent, so
// this mirrors examples/internal/exampletest's arena activation contract
// rather than depending on it.
const (
	arenaDescriptorVariable = "LIBTMUX_ARENA_DESCRIPTOR"
	arenaArtifactVariable   = "LIBTMUX_ARENA_ARTIFACT"
	arenaSocketVariable     = "LIBTMUX_SOCKET_PATH"
	arenaBinaryVariable     = "LIBTMUX_TMUX_BIN"
	arenaArtifact           = "go-workspace"
)

type arenaEndpoint struct {
	binary     string
	socketPath string
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

func arenaServer(lookup func(string) (string, bool)) (tmux.Server, bool, error) {
	endpoint, active, err := arenaEndpointFromLookup(lookup)
	if err != nil {
		return tmux.Server{}, false, err
	}
	if !active {
		return tmux.Server{}, false, nil
	}
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: endpoint.binary, SocketPath: endpoint.socketPath})
	if err != nil {
		return tmux.Server{}, false, fmt.Errorf("configure arena tmux server: %w", err)
	}
	return server, true, nil
}

func emitArenaEvidence(ctx context.Context, server tmux.Server) error {
	evidence, err := arenaEvidence(ctx, server)
	if err != nil {
		return err
	}
	_, err = fmt.Printf("LIBTMUX_ARENA_EVIDENCE=%s\n", evidence)
	return err
}

func arenaEvidence(ctx context.Context, server tmux.Server) ([]byte, error) {
	pidValue, err := arenaDisplayMessage(ctx, server, "#{pid}")
	if err != nil {
		return nil, fmt.Errorf("read arena server pid: %w", err)
	}
	serverPID, err := strconv.Atoi(pidValue)
	if err != nil {
		return nil, fmt.Errorf("parse arena server pid %q: %w", pidValue, err)
	}
	actualSocketPath, err := arenaDisplayMessage(ctx, server, "#{socket_path}")
	if err != nil {
		return nil, fmt.Errorf("read arena server socket path: %w", err)
	}
	if wantSocketPath := server.SocketPath(); actualSocketPath != wantSocketPath {
		return nil, fmt.Errorf("arena socket path %q does not match requested %q", actualSocketPath, wantSocketPath)
	}
	challenge, present, err := server.GlobalSessionScope().RawOption(ctx, "@libtmux_arena_challenge")
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
		{name: "ordinary"},
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
			name:        "descriptor only",
			environment: map[string]string{arenaDescriptorVariable: "arena"},
			wantErr:     true,
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
			name: "partial endpoint",
			environment: map[string]string{
				arenaDescriptorVariable: "arena",
				arenaArtifactVariable:   arenaArtifact,
				arenaSocketVariable:     "/tmp/arena.sock",
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

// TestWorkspaceArenaEndpoint builds the same workspace the package's Example
// builds, against the documentation arena's lent server when one is active,
// then reports the one evidence line the arena's contract requires.
func TestWorkspaceArenaEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server, active, err := arenaServer(os.LookupEnv)
	if err != nil {
		t.Fatalf("resolve arena server: %v", err)
	}
	if !active {
		server = tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	}

	document := []byte(`
session_name: libtmux-workspace-arena
windows:
  - window_name: work
    panes:
      - shell_command: printf 'ready\n'
`)
	parsed, err := workspace.Parse(document)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = session.Kill(cleanupCtx)
	}()

	name, _ := session.Name()
	if name != "libtmux-workspace-arena" {
		t.Fatalf("session name = %q, want %q", name, "libtmux-workspace-arena")
	}

	if active {
		if err := emitArenaEvidence(ctx, server); err != nil {
			t.Fatalf("emit arena evidence: %v", err)
		}
	}
}
