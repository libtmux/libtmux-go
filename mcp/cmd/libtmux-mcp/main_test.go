package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// How this process exits matters to whatever started it. An agent CLI that
// supervises the server reads a nonzero exit as a crash and says so, and the
// ordinary way a stdio server ends is that its client stopped talking to it.

func TestAClientHangingUpIsNotAFailure(t *testing.T) {
	t.Parallel()
	// The shape the SDK actually produces: the shutdown wrapped, and the read
	// error formatted into the message rather than wrapped, which is why the
	// io.EOF underneath is invisible to errors.Is.
	//nolint:errorlint // reproducing the SDK's own non-wrapping format is the
	// point: it is why the io.EOF underneath cannot be seen with errors.Is.
	closing := fmt.Errorf("%w: %v", &jsonrpc.Error{
		Code:    codeServerClosing,
		Message: "server is closing",
	}, io.EOF)

	for _, test := range []struct {
		name string
		err  error
	}{
		{"the SDK's shutdown", closing},
		{"a bare EOF", io.EOF},
		{"a wrapped EOF", fmt.Errorf("reading: %w", io.EOF)},
		{"a cancelled context", context.Canceled},
		{"a closed file", os.ErrClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !isClientHangup(test.err) {
				t.Errorf("isClientHangup(%v) = false, want true", test.err)
			}
		})
	}
}

func TestARealFailureIsStillAFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{"a tmux that is not there", errors.New("resolve tmux: no such file")},
		{"another JSON-RPC error", &jsonrpc.Error{Code: -32603, Message: "internal error"}},
		{"a deadline", context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if isClientHangup(test.err) {
				t.Errorf("isClientHangup(%v) = true, want false", test.err)
			}
		})
	}
}

// firstSentence keeps a fifty-tool listing a listing rather than the manual.
func TestFirstSentenceStopsAtTheFirstSentence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ in, want string }{
		{"Read what one pane holds. And then some more.", "Read what one pane holds."},
		{"No full stop here", "No full stop here"},
		{"Ends with one.", "Ends with one."},
		{"", ""},
	} {
		if got := firstSentence(test.in); got != test.want {
			t.Errorf("firstSentence(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestResolveTargetPinsOneSocketAndConfiguration(t *testing.T) {
	clearTargetEnvironment(t)
	name, path, config, origin, minimal, err := resolveTarget("", "")
	if err != nil || name != "libtmux-mcp" || path != "" || config != "" ||
		origin != "default dedicated socket" || !minimal {
		t.Fatalf("default target = (%q, %q, %q, %q, %t, %v)", name, path, config, origin, minimal, err)
	}

	t.Run("explicit named socket", func(t *testing.T) {
		clearTargetEnvironment(t)
		t.Setenv("LIBTMUX_SOCKET", "named")
		name, path, config, origin, minimal, err := resolveTarget("", "")
		if err != nil || name != "named" || path != "" || config != "" ||
			origin != "LIBTMUX_SOCKET" || minimal {
			t.Fatalf("target = (%q, %q, %q, %q, %t, %v)", name, path, config, origin, minimal, err)
		}
	})

	t.Run("explicit path and config", func(t *testing.T) {
		clearTargetEnvironment(t)
		t.Setenv("LIBTMUX_SOCKET_PATH", "/env/socket")
		t.Setenv("LIBTMUX_TMUX_CONFIG", "/env/tmux.conf")
		name, path, config, origin, minimal, err := resolveTarget("", "")
		if err != nil || name != "" || path != "/env/socket" ||
			config != "/env/tmux.conf" || origin != "LIBTMUX_SOCKET_PATH" || minimal {
			t.Fatalf("target = (%q, %q, %q, %q, %t, %v)", name, path, config, origin, minimal, err)
		}
	})

	for _, test := range []struct {
		name, flagName, flagPath, environmentName, environmentPath, config string
	}{
		{name: "both flags", flagName: "named", flagPath: "/socket"},
		{name: "both environment selectors", environmentName: "named", environmentPath: "/socket"},
		{name: "relative path", environmentPath: "relative"},
		{name: "relative config", config: "relative"},
		{name: "empty named selector", environmentName: " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearTargetEnvironment(t)
			if test.environmentName != "" {
				t.Setenv("LIBTMUX_SOCKET", test.environmentName)
			}
			if test.environmentPath != "" {
				t.Setenv("LIBTMUX_SOCKET_PATH", test.environmentPath)
			}
			if test.config != "" {
				t.Setenv("LIBTMUX_TMUX_CONFIG", test.config)
			}
			if _, _, _, _, _, err := resolveTarget(test.flagName, test.flagPath); err == nil {
				t.Fatal("invalid target was accepted")
			}
		})
	}
}

func TestResolveTargetRejectsEmptyConfig(t *testing.T) {
	clearTargetEnvironment(t)
	t.Setenv("LIBTMUX_TMUX_CONFIG", "")
	if _, _, _, _, _, err := resolveTarget("", ""); err == nil {
		t.Fatal("empty config path was accepted")
	}
}

func clearTargetEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"LIBTMUX_SOCKET", "LIBTMUX_SOCKET_PATH", "LIBTMUX_TMUX_CONFIG"} {
		value := os.Getenv(name)
		t.Setenv(name, value)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheSocketAndBinaryComeFromTheEnvironmentToo(t *testing.T) {
	t.Run("the binary comes from the environment", func(t *testing.T) {
		t.Setenv("LIBTMUX_TMUX_BIN", "/opt/tmux/bin/tmux")
		if got := binaryFrom(""); got != "/opt/tmux/bin/tmux" {
			t.Errorf("binaryFrom() = %q, want the path from the environment", got)
		}
	})
}
