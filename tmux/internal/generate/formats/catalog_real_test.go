//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestFormatCatalogCoversRealTmuxInventory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	directory, err := os.MkdirTemp("", "ltg-formats")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "tmux.sock")
	config := filepath.Join(directory, "tmux.conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		command := exec.CommandContext(cleanupCtx, "tmux", "-S", socket, "kill-server")
		output, _ := command.CombinedOutput()
		if err := cleanupCtx.Err(); err != nil {
			t.Errorf("kill isolated tmux server: %v: %s", err, strings.TrimSpace(string(output)))
		}
	})
	runCatalogTmux(ctx, t, socket, "-f", config, "new-session", "-d", "-s", "catalog")

	version := strings.TrimSpace(runCatalogTmux(
		ctx,
		t,
		socket,
		"display-message",
		"-p",
		"#{version}",
	))
	current, err := parseCatalogVersion(version)
	if err != nil {
		t.Fatalf("parse server format version %q: %v", version, err)
	}

	spec, err := readFormatSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	fields := make(map[string]fieldSpec, len(spec.Fields))
	for _, field := range spec.Fields {
		fields[field.Name] = field
	}

	attachCatalogClient(ctx, t, socket)

	inventory := runCatalogTmux(ctx, t, socket, "display-message", "-p", "-a")
	seen := make(map[string]struct{})
	missing := make([]string, 0)
	incompatible := make([]string, 0)
	for lineNumber, line := range strings.Split(strings.TrimSuffix(inventory, "\n"), "\n") {
		name, _, found := strings.Cut(line, "=")
		if !found || name == "" {
			t.Fatalf("display-message -a line %d is malformed: %q", lineNumber+1, line)
		}
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("display-message -a contains duplicate field %q", name)
		}
		seen[name] = struct{}{}

		field, found := fields[name]
		if !found {
			missing = append(missing, name)
			continue
		}
		minimum, err := parseCatalogVersion(field.Since)
		if err != nil {
			t.Fatalf("parse catalog version for %q: %v", name, err)
		}
		if !catalogVersionAtLeast(current, minimum) {
			incompatible = append(
				incompatible,
				fmt.Sprintf("%s requires %s on %s", name, field.Since, version),
			)
		}
	}
	slices.Sort(missing)
	slices.Sort(incompatible)
	if len(missing) != 0 || len(incompatible) != 0 {
		t.Fatalf(
			"format catalog does not cover tmux %s inventory: missing=%q incompatible=%q",
			version,
			missing,
			incompatible,
		)
	}
}

// attachCatalogClient keeps client-scoped variables in tmux's format inventory.
// Control mode avoids a terminal; open stdin keeps the client attached.
func attachCatalogClient(ctx context.Context, t *testing.T, socket string) {
	t.Helper()

	client := exec.CommandContext(ctx, "tmux", "-S", socket, "-C",
		"attach-session", "-t", "catalog")
	held, err := client.StdinPipe()
	if err != nil {
		t.Fatalf("hold the control client's stdin: %v", err)
	}
	client.Stdout, client.Stderr = io.Discard, io.Discard
	if err := client.Start(); err != nil {
		t.Fatalf("attach a control client: %v", err)
	}
	t.Cleanup(func() {
		_ = held.Close()
		_ = client.Process.Kill()
		_, _ = client.Process.Wait()
	})

	// The inventory is only complete once tmux has registered the client.
	deadline := time.Now().Add(5 * time.Second)
	for {
		listed := runCatalogTmux(ctx, t, socket, "list-clients", "-F", "#{client_name}")
		if strings.TrimSpace(listed) != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no control client registered, so the inventory would be partial")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runCatalogTmux(
	ctx context.Context,
	t *testing.T,
	socket string,
	arguments ...string,
) string {
	t.Helper()

	commandArguments := append([]string{"-S", socket}, arguments...)
	command := exec.CommandContext(ctx, "tmux", commandArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"tmux %q failed: %v: %s",
			arguments,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return string(output)
}

func parseCatalogVersion(raw string) (tmux.Version, error) {
	return tmux.ParseVersion(raw)
}

func catalogVersionAtLeast(current, minimum tmux.Version) bool {
	return current.AtLeast(minimum)
}

func TestParseCatalogVersionAcceptsServerTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		minimum string
		want    bool
	}{
		{name: "numbered release below floor", current: "3.5", minimum: "3.6"},
		{name: "development build", current: "master", minimum: "3.7", want: true},
		{name: "unprobed OpenBSD base system", current: "openbsd-7.8", minimum: "3.7"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			current, err := parseCatalogVersion(test.current)
			if err != nil {
				t.Fatalf("parseCatalogVersion(%q) error = %v", test.current, err)
			}
			minimum, err := parseCatalogVersion(test.minimum)
			if err != nil {
				t.Fatalf("parseCatalogVersion(%q) error = %v", test.minimum, err)
			}
			if got := catalogVersionAtLeast(current, minimum); got != test.want {
				t.Fatalf("catalogVersionAtLeast(%q, %q) = %t, want %t", test.current, test.minimum, got, test.want)
			}
		})
	}
}
