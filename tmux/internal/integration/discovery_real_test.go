//go:build linux

package integration

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestDiscoveryMultipleRootsDiagnosticsAndBounds(t *testing.T) {
	ctx := t.Context()
	first := tmuxtest.NewServer(ctx, t)
	second := tmuxtest.NewServer(ctx, t)
	firstRoot := filepath.Dir(first.SocketPath())
	secondRoot := filepath.Dir(second.SocketPath())
	if firstRoot == secondRoot {
		t.Fatal("fixture must use distinct roots")
	}
	stalePath := filepath.Join(firstRoot, "stale")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: stalePath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	hungPath := filepath.Join(firstRoot, "unresponsive")
	hung, err := net.ListenUnix("unix", &net.UnixAddr{Name: hungPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := hung.Close(); err != nil {
			t.Error(err)
		}
	})
	alias := filepath.Join(secondRoot, "alias")
	if err := os.Symlink(first.SocketPath(), alias); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		candidate, err := first.WithSocketPath(stalePath)
		if err != nil {
			t.Error(err)
			return
		}
		owner, err := candidate.Adopt(cleanup, tmux.OwnershipOptions{})
		if err == nil {
			if err := owner.Close(); err != nil {
				t.Error(err)
				return
			}
		} else if !errors.Is(err, tmux.ErrNoServer) {
			t.Error(err)
			return
		}
		for _, path := range []string{alias, stalePath} {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
		}
	})
	missing := filepath.Join(firstRoot, "missing-root")
	denied := t.TempDir()
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(denied, 0o700); err != nil {
			t.Error(err)
		}
	})
	before, err := os.ReadDir(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := first.Discover(ctx, tmux.DiscoveryOptions{
		Roots: []string{firstRoot, secondRoot, firstRoot, missing, denied}, FollowSocketSymlinks: true,
		ProbeTimeout: 500 * time.Millisecond,
	})
	if err != nil || result.Truncated || len(result.Servers) != 2 {
		t.Fatalf("discovery = %#v, %v", result, err)
	}
	found := map[string]bool{}
	for _, s := range result.Servers {
		found[s.SocketPath()] = true
	}
	if !found[first.SocketPath()] || !found[second.SocketPath()] {
		t.Fatalf("missed daemon: %#v; diagnostics: %#v", found, result.Diagnostics)
	}
	staleSeen, hungSeen, rootSeen, aliasSeen := false, false, false, false
	deniedSeen := os.Geteuid() == 0
	for _, diagnostic := range result.Diagnostics {
		switch diagnostic.Path {
		case stalePath:
			staleSeen = diagnostic.Kind == "probe" && errors.Is(diagnostic.Err, tmux.ErrNoServer)
		case hungPath:
			hungSeen = diagnostic.Kind == "probe" && diagnostic.Err != nil
		case missing:
			rootSeen = diagnostic.Kind == "root" && errors.Is(diagnostic.Err, os.ErrNotExist)
		case alias:
			aliasSeen = diagnostic.Kind == "duplicate" && diagnostic.DuplicateOf == first.SocketPath()
		case denied:
			deniedSeen = diagnostic.Kind == "root" && errors.Is(diagnostic.Err, os.ErrPermission)
		}
	}
	if !staleSeen || !hungSeen || !rootSeen || !aliasSeen || !deniedSeen {
		t.Fatalf("diagnostics missing: %#v", result.Diagnostics)
	}
	after, err := os.ReadDir(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("discovery created directory entries: %d -> %d", len(before), len(after))
	}
	staleServer, err := first.WithSocketPath(stalePath)
	if err != nil {
		t.Fatal(err)
	}
	if alive, err := staleServer.IsAlive(ctx); err != nil || alive {
		t.Fatalf("discovery started stale daemon: %t %v", alive, err)
	}
	for _, options := range []tmux.DiscoveryOptions{
		{Roots: []string{firstRoot, secondRoot}, MaxEntries: 1},
		{Roots: []string{firstRoot, secondRoot}, MaxProbes: 1},
	} {
		limited, err := first.Discover(ctx, options)
		if err != nil || !limited.Truncated {
			t.Fatalf("bound ignored: %#v %v", limited, err)
		}
	}
	symlinks, err := first.Discover(ctx, tmux.DiscoveryOptions{Roots: []string{secondRoot}})
	if err != nil {
		t.Fatal(err)
	}
	skipped := false
	for _, d := range symlinks.Diagnostics {
		if d.Path == alias && d.Kind == "symlink" {
			skipped = true
		}
	}
	if !skipped {
		t.Fatal("entry symlink followed by default")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	partial, err := first.Discover(canceled, tmux.DiscoveryOptions{Roots: []string{firstRoot}})
	if !partial.Truncated || !errors.Is(err, context.Canceled) || partial.Probes != 0 {
		t.Fatalf("canceled scan = %#v %v", partial, err)
	}
}

func TestDiscoveryConfiguredRoots(t *testing.T) {
	ctx := t.Context()
	fixture := tmuxtest.NewServer(ctx, t)
	// The selected endpoint parent is a configured root even for explicit -S.
	configured := configuredDiscoveryFixture(t, fixture)
	result, err := configured.Discover(ctx, tmux.DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range result.Servers {
		if s.Equal(configured) {
			found = true
		}
	}
	if !found {
		t.Fatalf("configured endpoint missing: %#v", result)
	}
	selection, err := configured.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(selection.NamedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(selection.NamedDirectory, "selected")
	if err := os.Symlink(fixture.SocketPath(), alias); err != nil {
		t.Fatal(err)
	}
	named, err := configured.WithSocketPath(alias)
	if err != nil {
		t.Fatal(err)
	}
	result, err = named.Discover(ctx, tmux.DiscoveryOptions{FollowSocketSymlinks: true, MaxProbes: 1})
	if err != nil || result.Truncated || len(result.Servers) != 1 || result.Probes != 1 {
		t.Fatalf("one configured root consumed the probe budget twice: %#v, %v", result, err)
	}
}

func TestDiscoveryConfiguredRootPreservesComponents(t *testing.T) {
	ctx := t.Context()
	fixture := tmuxtest.NewServer(ctx, t)
	configured := configuredDiscoveryFixture(t, fixture)
	root := filepath.Dir(fixture.SocketPath())
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(child); err != nil {
			t.Error(err)
		}
	})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		parent string
		found  bool
	}{
		{name: "symlink-dotdot", parent: link + "/../", found: true},
		{name: "missing-dotdot", parent: root + "/missing/../"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := test.parent + filepath.Base(fixture.SocketPath())
			selected, err := configured.WithSocketPath(path)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := selected.SocketSelection()
			configuredSelection, _ := configured.SocketSelection()
			if err != nil || selection.Path != path || selection.NamedDirectory != configuredSelection.NamedDirectory {
				t.Fatalf("unexpected planned discovery roots: %#v, %v", selection, err)
			}
			result, err := selected.Discover(ctx, tmux.DiscoveryOptions{ProbeTimeout: 500 * time.Millisecond})
			if err != nil || result.Truncated {
				t.Fatalf("discover configured parent: %#v, %v", result, err)
			}
			if test.found {
				if len(result.Servers) != 1 || result.Servers[0].SocketPath() != path {
					t.Fatalf("configured path lost filesystem semantics: %#v", result)
				}
				return
			}
			if len(result.Servers) != 0 {
				t.Fatalf("discovery scanned the cleaned alternate parent: %#v", result)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Path == test.parent[:len(test.parent)-1] && diagnostic.Kind == "root" && errors.Is(diagnostic.Err, os.ErrNotExist) {
					return
				}
			}
			t.Fatalf("missing uncleaned parent diagnostic: %#v", result.Diagnostics)
		})
	}
}

func configuredDiscoveryFixture(t *testing.T, fixture tmux.Server) tmux.Server {
	t.Helper()
	namedRoot := t.TempDir()
	configured, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: fixture.SocketPath(), ConfigFile: fixture.ConfigFile(),
		ProcessEnvironment: append(fixture.ProcessEnvironment(), "TMUX_TMPDIR="+namedRoot),
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := configured.SocketSelection()
	if err != nil || selection.Path != fixture.SocketPath() || selection.NamedDirectory != filepath.Join(namedRoot, "tmux-"+strconv.Itoa(os.Getuid())) {
		t.Fatalf("unexpected planned discovery roots: %#v, %v", selection, err)
	}
	return configured
}
