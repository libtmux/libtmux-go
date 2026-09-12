package exampletest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestResolveArenaServerLookup(t *testing.T) {
	t.Parallel()
	const artifact = "go-example"

	// Not a fixed path: tmux is /usr/bin on the Linux runners and elsewhere on
	// macOS, and resolving the executable is part of what is under test, so a
	// path that does not exist fails the complete-contract case for the wrong
	// reason.
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux is not on PATH: %v", err)
	}

	for _, testCase := range []struct {
		name        string
		environment map[string]string
		wantActive  bool
		wantErr     bool
	}{
		{name: "ordinary"},
		{
			name: "aliases alone",
			environment: map[string]string{
				ArenaSocketVariable: "/tmp/arena.sock",
				ArenaBinaryVariable: tmuxBinary,
			},
		},
		{
			name: "empty descriptor",
			environment: map[string]string{
				ArenaDescriptorVariable: "",
				ArenaArtifactVariable:   artifact,
				ArenaSocketVariable:     "/tmp/arena.sock",
				ArenaBinaryVariable:     tmuxBinary,
			},
		},
		{
			name:        "descriptor only",
			environment: map[string]string{ArenaDescriptorVariable: "arena"},
			wantErr:     true,
		},
		{
			name: "empty artifact",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   "",
				ArenaSocketVariable:     "/tmp/arena.sock",
				ArenaBinaryVariable:     tmuxBinary,
			},
			wantErr: true,
		},
		{
			name: "empty socket path",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   artifact,
				ArenaSocketVariable:     "",
				ArenaBinaryVariable:     tmuxBinary,
			},
			wantErr: true,
		},
		{
			name: "empty binary",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   artifact,
				ArenaSocketVariable:     "/tmp/arena.sock",
				ArenaBinaryVariable:     "",
			},
			wantErr: true,
		},
		{
			name: "partial endpoint",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   artifact,
				ArenaSocketVariable:     "/tmp/arena.sock",
			},
			wantErr: true,
		},
		{
			name: "wrong artifact",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   "other-artifact",
				ArenaSocketVariable:     "/tmp/arena.sock",
				ArenaBinaryVariable:     tmuxBinary,
			},
			wantErr: true,
		},
		{
			name: "complete contract",
			environment: map[string]string{
				ArenaDescriptorVariable: "arena",
				ArenaArtifactVariable:   artifact,
				ArenaSocketVariable:     "/tmp/arena.sock",
				ArenaBinaryVariable:     tmuxBinary,
			},
			wantActive: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				value, present := testCase.environment[name]
				return value, present
			}
			ownedCalls := 0
			resolved, err := ResolveArenaServer(artifact, lookup, func() tmux.Server {
				ownedCalls++
				return tmux.Server{}
			})
			if testCase.wantErr {
				if err == nil {
					t.Fatal("ResolveArenaServer() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveArenaServer() error = %v", err)
			}
			if resolved.Active() != testCase.wantActive {
				t.Fatalf("ResolveArenaServer() active = %v, want %v", resolved.Active(), testCase.wantActive)
			}
			switch {
			case testCase.wantActive && ownedCalls != 0:
				t.Fatalf("ResolveArenaServer() called newOwned %d times, want 0 when active", ownedCalls)
			case testCase.wantActive && resolved.Server.SocketPath() != "/tmp/arena.sock":
				t.Fatalf("ResolveArenaServer() socket path = %q, want %q",
					resolved.Server.SocketPath(), "/tmp/arena.sock")
			case !testCase.wantActive && ownedCalls != 1:
				t.Fatalf("ResolveArenaServer() called newOwned %d times, want 1 when inactive", ownedCalls)
			}
		})
	}
}

// TestResolveArenaServerWrapperBinary proves the mechanism every example
// reuses: the resolved server dispatches through the arena's named binary,
// and evidence reports the arena's own challenge, pid, and socket path.
func TestResolveArenaServerWrapperBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const artifact = "go-example"
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
	challenge := "exampletest-arena"
	if _, err := external.Cmd(ctx, "set-option", "-g", "@libtmux_arena_challenge", challenge); err != nil {
		t.Fatalf("set arena challenge: %v", err)
	}
	t.Setenv("LIBTMUX_ARENA_BINARY_MARKER", markerPath)
	t.Setenv("LIBTMUX_ARENA_REAL_TMUX", binary)

	environment := map[string]string{
		ArenaDescriptorVariable: "arena",
		ArenaArtifactVariable:   artifact,
		ArenaSocketVariable:     external.SocketPath(),
		ArenaBinaryVariable:     wrapperPath,
	}
	lookup := func(name string) (string, bool) {
		value, present := environment[name]
		return value, present
	}

	resolved, err := ResolveArenaServer(artifact, lookup, func() tmux.Server {
		t.Fatal("ResolveArenaServer() called newOwned while the arena is active")
		return tmux.Server{}
	})
	if err != nil {
		t.Fatalf("ResolveArenaServer() error = %v", err)
	}
	if !resolved.Active() {
		t.Fatal("ResolveArenaServer() active = false, want true")
	}

	if _, err := resolved.Server.Cmd(ctx, "display-message", "-p", "ok"); err != nil {
		t.Fatalf("dispatch through resolved server: %v", err)
	}
	invocations, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read tmux executable invocations: %v", err)
	}
	for invocation := range strings.SplitSeq(strings.TrimSpace(string(invocations)), "\n") {
		if invocation != wrapperPath {
			t.Fatalf("tmux executable invocation = %q, want %q", invocation, wrapperPath)
		}
	}

	printedEvidence := Output(t, func() error { return resolved.EmitEvidence(ctx) })
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
	if got.Artifact != artifact || got.Challenge != challenge || got.Schema != 1 ||
		got.ServerPID <= 0 || got.SocketPath != external.SocketPath() {
		t.Fatalf("arena evidence = %#v", got)
	}
	if alive, err := external.IsAlive(ctx); err != nil || !alive {
		t.Fatalf("external server alive = %v, error = %v", alive, err)
	}
}

// TestResolveArenaServerRejectsMissingChallenge proves EmitEvidence bites when
// the lent server carries no arena challenge, rather than reporting evidence
// no verifier issued.
func TestResolveArenaServerRejectsMissingChallenge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	external := tmuxtest.NewServer(ctx, t)
	resolved := ArenaServer{Server: external, artifact: "go-example", active: true}

	if err := resolved.EmitEvidence(ctx); err == nil {
		t.Fatal("EmitEvidence() error = nil, want a missing challenge")
	}
}
