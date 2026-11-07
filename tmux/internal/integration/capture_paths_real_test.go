package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// capturePathCases are the paths a tmux command list could misread. Each is
// relative to the server's working directory (the test's), so a lone ";" can
// be spelled at all.
var capturePathCases = []string{
	";",
	"ends-with;",
	"inside\\;escape",
	"with space",
	"-leading-dash",
	"new\nline",
	"a;b",
	"semi ; spaced",
}

func scratchBuffersOn(ctx context.Context, t *testing.T, server tmux.Server) []string {
	t.Helper()
	names, err := server.ListBuffers(ctx, tmux.ListBuffersRequest{Format: new("#{buffer_name}")})
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, name := range names {
		if strings.HasPrefix(name, "libtmux-go-") {
			found = append(found, name)
		}
	}
	return found
}

// A path is either written exactly as named or refused before dispatch with a
// request error. It never runs some other tmux command, and no scratch buffer
// outlives the call, over a subprocess and over a Connection lane alike.
//
//libtmux:real-tmux
func TestCaptureToFilePathsOverBothTransports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	t.Chdir(directory)
	server := tmuxtest.NewServer(ctx, t)
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v)", len(sessions), err)
	}
	_, connection, err := server.NewSessionConnection(
		ctx, tmux.NewSessionRequest{Name: "paths"}, tmux.ConnectionOptions{Lanes: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	bound, err := connection.Session().SearchPanes(ctx, nil)
	if err != nil || len(bound) == 0 {
		t.Fatalf("connection panes = (%v, %v)", bound, err)
	}
	loose, err := sessions[0].SearchPanes(ctx, nil)
	if err != nil || len(loose) == 0 {
		t.Fatalf("panes = (%v, %v)", loose, err)
	}
	before := func() []string {
		result, err := server.Cmd(ctx, "list-sessions", "-F", "#{session_name}")
		if err != nil {
			t.Fatal(err)
		}
		return result.Stdout
	}
	sessionsBefore := before()

	for _, transport := range []struct {
		name string
		pane tmux.Pane
	}{{"subprocess", loose[0]}, {"connection", bound[0]}} {
		for _, path := range capturePathCases {
			t.Run(transport.name+"/"+strings.NewReplacer("\n", "NL", "/", "_").Replace(path), func(t *testing.T) {
				_ = os.Remove(path)
				lines, err := transport.pane.CaptureToFile(ctx, path, tmux.CapturePaneRequest{})
				var request *tmux.CaptureRequestError
				switch {
				case err == nil:
					if _, statErr := os.Stat(filepath.Join(directory, path)); statErr != nil {
						t.Fatalf("capture reported success but %q is absent: %v (lines %q)", path, statErr, lines)
					}
					entries, _ := os.ReadDir(directory)
					if len(entries) != 1 {
						t.Fatalf("directory holds %d entries after capturing to %q, want exactly that one", len(entries), path)
					}
				case errors.As(err, &request) || errors.Is(err, tmux.ErrInvalidCaptureRequest):
					t.Logf("refused before dispatch: %v", err)
				default:
					t.Fatalf("CaptureToFile(%q) = %v, want success or a request error", path, err)
				}
				if left := scratchBuffersOn(ctx, t, server); len(left) != 0 {
					t.Fatalf("scratch buffers left: %q", left)
				}
				if after := before(); strings.Join(after, "|") != strings.Join(sessionsBefore, "|") {
					t.Fatalf("sessions changed from %q to %q: a different command ran", sessionsBefore, after)
				}
				_ = os.Remove(path)
			})
		}
	}
}
