package workspace_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/workspace"
)

func TestWorkspaceChoosesAnEligibleTransport(t *testing.T) {
	old := os.Getenv("LIBTMUX_OPTIONAL_OLD_TMUX")
	if old == "" {
		t.Skip("set LIBTMUX_OPTIONAL_OLD_TMUX to exact tmux3.3a")
	}
	modern, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, binary         string
		existingOld, control bool
	}{
		{name: "cold older executable", binary: old},
		{name: "older daemon newer client", binary: modern, existingOld: true},
		{name: "qualified modern", binary: modern, control: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "control-attempt")
			proxy := filepath.Join(t.TempDir(), "tmux")
			if err := os.WriteFile(proxy, []byte(`#!/bin/sh
for argument do
    if [ "$argument" = -C ]; then printf 'control\n' >> "$WORKSPACE_OPTIONAL_RECORD"; fi
done
exec "$WORKSPACE_OPTIONAL_BINARY" "$@"
`), 0o700); err != nil {
				t.Fatal(err)
			}
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
				Binary: proxy, FixedShell: true,
				ProcessEnvironment: append(os.Environ(), "WORKSPACE_OPTIONAL_RECORD="+record, "WORKSPACE_OPTIONAL_BINARY="+test.binary),
			})
			if test.existingOld {
				command := exec.Command(old, "-f", os.DevNull, "-S", server.SocketPath(), "new-session", "-d", "-s", "keeper", "sleep 300")
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("old fixture: %v %s", err, output)
				}
			}
			if test.control {
				tmuxtest.RequireVersion(t.Context(), t, server, tmux.MinimumConnectionVersion)
			}
			created, err := workspace.Build(t.Context(), server, workspace.Workspace{
				SessionName: "built",
				Windows: []workspace.Window{
					{Name: "one", Layout: "tiled", Panes: []workspace.Pane{{Shell: "sleep 300"}, {Shell: "sleep 300"}}},
					{Name: "two", Panes: []workspace.Pane{{Shell: "sleep 300"}}},
				},
			})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			windows, err := created.SearchWindows(t.Context(), nil)
			if err != nil || len(windows) != 2 {
				t.Fatalf("built windows: %d %v", len(windows), err)
			}
			panes, err := created.SearchPanes(t.Context(), nil)
			if err != nil || len(panes) != 3 {
				t.Fatalf("built panes: %d %v", len(panes), err)
			}
			data, readErr := os.ReadFile(record)
			if test.control {
				if readErr != nil || len(data) == 0 {
					t.Fatalf("qualified build did not acquire control: %q %v", data, readErr)
				}
			} else if !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("older build acquired control: %q %v", data, readErr)
			}
		})
	}
}
