// Package exampletest captures output from executable examples and resolves
// the tmux server an example test runs against, including the one the
// documentation arena lends.
package exampletest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

// Environment variables carrying the documentation arena's activation
// contract. LIBTMUX_ARENA_DESCRIPTOR non-empty is the only activation signal;
// once active, LIBTMUX_ARENA_ARTIFACT must equal the caller's own artifact id
// and LIBTMUX_SOCKET_PATH / LIBTMUX_TMUX_BIN must be present, or resolution
// fails without touching any other server.
const (
	ArenaDescriptorVariable = "LIBTMUX_ARENA_DESCRIPTOR"
	ArenaArtifactVariable   = "LIBTMUX_ARENA_ARTIFACT"
	ArenaSocketVariable     = "LIBTMUX_SOCKET_PATH"
	ArenaBinaryVariable     = "LIBTMUX_TMUX_BIN"
)

// ArenaServer is the tmux server resolved for one example test, bound to the
// artifact id its evidence must carry.
type ArenaServer struct {
	Server   tmux.Server
	artifact string
	active   bool
}

// Active reports whether the documentation arena lent this server, rather
// than the example creating its own.
func (a ArenaServer) Active() bool { return a.active }

// ResolveArenaServer resolves the tmux server for one example test: the
// server the documentation arena lent, when the environment activates
// artifact's contract, otherwise newOwned's result.
func ResolveArenaServer(
	artifact string,
	lookup func(string) (string, bool),
	newOwned func() tmux.Server,
) (ArenaServer, error) {
	descriptor, _ := lookup(ArenaDescriptorVariable)
	if descriptor == "" {
		return ArenaServer{Server: newOwned(), artifact: artifact}, nil
	}
	reportedArtifact, _ := lookup(ArenaArtifactVariable)
	socketPath, _ := lookup(ArenaSocketVariable)
	binary, _ := lookup(ArenaBinaryVariable)
	if reportedArtifact == "" || socketPath == "" || binary == "" {
		return ArenaServer{}, errors.New("arena contract is incomplete")
	}
	if reportedArtifact != artifact {
		return ArenaServer{}, fmt.Errorf("arena artifact %q does not match %q", reportedArtifact, artifact)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: binary, SocketPath: socketPath})
	if err != nil {
		return ArenaServer{}, fmt.Errorf("configure arena tmux server: %w", err)
	}
	return ArenaServer{Server: server, artifact: artifact, active: true}, nil
}

// EmitEvidence prints the one line the arena's contract requires after a
// successful run against a's server: LIBTMUX_ARENA_EVIDENCE={json}, schema 1,
// carrying the artifact, the tmux global option @libtmux_arena_challenge, the
// server's pid, and its socket path, having verified the reported socket path
// equals the one the arena lent. Call it only when a.Active().
func (a ArenaServer) EmitEvidence(ctx context.Context) error {
	evidence, err := a.evidence(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Printf("LIBTMUX_ARENA_EVIDENCE=%s\n", evidence)
	return err
}

func (a ArenaServer) evidence(ctx context.Context) ([]byte, error) {
	serverPID, err := arenaServerPID(ctx, a.Server)
	if err != nil {
		return nil, err
	}
	actualSocketPath, err := arenaServerSocketPath(ctx, a.Server)
	if err != nil {
		return nil, err
	}
	if wantSocketPath := a.Server.SocketPath(); actualSocketPath != wantSocketPath {
		return nil, fmt.Errorf("arena socket path %q does not match requested %q", actualSocketPath, wantSocketPath)
	}
	challenge, present, err := a.Server.GlobalSessionScope().RawOption(ctx, "@libtmux_arena_challenge")
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
		Artifact:   a.artifact,
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

// RequireTmux skips a real-tmux example below its stated feature floor.
func RequireTmux(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	minimum string,
) {
	t.Helper()
	want, err := tmux.ParseVersion(minimum)
	if err != nil {
		t.Fatalf("parse required tmux version %q: %v", minimum, err)
	}
	got, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("query tmux version: %v", err)
	}
	if !got.AtLeast(want) {
		t.Skipf("example requires tmux %s or newer; installed %s", want, got)
	}
}

// Output returns work's stdout and fails with partial output when work fails.
func Output(t *testing.T, work func() error) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("exampletest: create pipe: %v", err)
	}
	os.Stdout = writer

	// Drain concurrently so output larger than the pipe buffer cannot block.
	var printed bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&printed, reader)
	}()

	workErr := work()

	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("exampletest: close writer: %v", err)
	}
	<-drained
	if err := reader.Close(); err != nil {
		t.Fatalf("exampletest: close reader: %v", err)
	}

	if workErr != nil {
		t.Fatalf("run() error = %v; printed so far:\n%s", workErr, printed.String())
	}
	return printed.String()
}
