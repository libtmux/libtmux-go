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

func TestResolveSocketPrefersAFlagOverTheEnvironment(t *testing.T) {
	for _, testCase := range []struct {
		name, flagName, flagPath, environment, want, origin string
	}{
		{"nothing at all", "", "", "", "", "tmux's default"},
		{"the variable alone", "", "", "named", "named", "LIBTMUX_SOCKET"},
		{"a name beats it", "flagged", "", "named", "flagged", "-socket-name"},
		{"a path beats it", "", "/tmp/s", "named", "", "-socket-path"},
		{"blank is not a name", "", "", "   ", "", "tmux's default"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("LIBTMUX_SOCKET", testCase.environment)
			resolved, origin := resolveSocket(testCase.flagName, testCase.flagPath)
			if resolved != testCase.want {
				t.Errorf("socket = %q, want %q", resolved, testCase.want)
			}
			if origin != testCase.origin {
				t.Errorf("origin = %q, want %q", origin, testCase.origin)
			}
		})
	}
}

func TestTheSocketAndBinaryComeFromTheEnvironmentToo(t *testing.T) {
	t.Run("a path in the environment is taken as a path", func(t *testing.T) {
		t.Setenv("LIBTMUX_SOCKET_PATH", "/somewhere/else/tmux.sock")
		if got := socketPathFrom(""); got != "/somewhere/else/tmux.sock" {
			t.Errorf("socketPathFrom() = %q, want the path from the environment", got)
		}
	})
	t.Run("the flag wins over the environment", func(t *testing.T) {
		t.Setenv("LIBTMUX_SOCKET_PATH", "/from/the/environment.sock")
		if got := socketPathFrom("/from/the/flag.sock"); got != "/from/the/flag.sock" {
			t.Errorf("socketPathFrom() = %q, want the flag to win", got)
		}
	})
	t.Run("the binary comes from the environment", func(t *testing.T) {
		t.Setenv("LIBTMUX_TMUX_BIN", "/opt/tmux/bin/tmux")
		if got := binaryFrom(""); got != "/opt/tmux/bin/tmux" {
			t.Errorf("binaryFrom() = %q, want the path from the environment", got)
		}
	})
	t.Run("doctor names the path variable as the origin", func(t *testing.T) {
		t.Setenv("LIBTMUX_SOCKET_PATH", "/somewhere/else/tmux.sock")
		if _, origin := resolveSocket("", ""); origin != "LIBTMUX_SOCKET_PATH" {
			t.Errorf("origin = %q, want %q", origin, "LIBTMUX_SOCKET_PATH")
		}
	})
}
