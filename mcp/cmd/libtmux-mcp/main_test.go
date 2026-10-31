package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

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

// targetFor builds the server the flags and environment select, without
// starting tmux.
func targetFor(t *testing.T, flagName, flagPath string) (tmux.Server, string, error) {
	t.Helper()
	name, path, config, err := resolveTarget(flagName, flagPath)
	if err != nil {
		return tmux.Server{}, "", err
	}
	server, err := tmux.NewServer(tmux.ServerOptions{SocketName: name, SocketPath: path, ConfigFile: config})
	if err != nil {
		t.Fatal(err)
	}
	return server, config, nil
}

func TestResolveTargetSelectsTheSocketByFlagThenVariableThenTmux(t *testing.T) {
	for _, test := range []struct {
		name               string
		flagName, flagPath string
		environmentName    string
		environmentPath    string
		wantString         string
		wantSource         tmux.SocketSource
		wantAbsolutePath   bool
	}{
		{
			name:       "nothing selects tmux's own choice, with no dedicated socket",
			wantString: "Server(default)", wantSource: tmux.SocketSourceTmux,
		},
		{
			name: "the name variable", environmentName: "named",
			wantString: "Server(socket_name=named)", wantSource: tmux.SocketSourceEnvironmentName,
		},
		{
			name: "the path variable", environmentPath: "/env/socket",
			wantString: "Server(socket_path=/env/socket)", wantSource: tmux.SocketSourceEnvironmentPath,
		},
		{
			name: "both variables, so the path wins without an error", environmentName: "named", environmentPath: "/env/socket",
			wantString: "Server(socket_path=/env/socket)", wantSource: tmux.SocketSourceEnvironmentPath,
		},
		{
			name: "an empty path variable leaves the name variable in force", environmentName: "named", environmentPath: " ",
			wantString: "Server(socket_name=named)", wantSource: tmux.SocketSourceEnvironmentName,
		},
		{
			name: "a blank name variable selects nothing", environmentName: " ",
			wantString: "Server(default)", wantSource: tmux.SocketSourceTmux,
		},
		{
			name: "a relative path variable is left to tmux", environmentPath: "relative/socket",
			wantString: "Server(socket_path=relative/socket)", wantSource: tmux.SocketSourceEnvironmentPath,
			wantAbsolutePath: true,
		},
		{
			name: "the name flag beats both variables", flagName: "flagged", environmentName: "named", environmentPath: "/env/socket",
			wantString: "Server(socket_name=flagged)", wantSource: tmux.SocketSourceExplicit,
		},
		{
			name: "the path flag beats both variables", flagPath: "/flag/socket", environmentName: "named", environmentPath: "/env/socket",
			wantString: "Server(socket_path=/flag/socket)", wantSource: tmux.SocketSourceExplicit,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearTargetEnvironment(t)
			if test.environmentName != "" {
				t.Setenv("LIBTMUX_SOCKET", test.environmentName)
			}
			if test.environmentPath != "" {
				t.Setenv("LIBTMUX_SOCKET_PATH", test.environmentPath)
			}
			server, _, err := targetFor(t, test.flagName, test.flagPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := server.String(); got != test.wantString {
				t.Errorf("target = %s, want %s", got, test.wantString)
			}
			selection, err := server.SocketSelection()
			if err != nil || selection.Source != test.wantSource {
				t.Errorf("SocketSelection() = (%#v, %v), want source %q", selection, err, test.wantSource)
			}
			if test.wantAbsolutePath && !filepath.IsAbs(selection.Path) {
				t.Errorf("selected path %q is not absolute", selection.Path)
			}
		})
	}
}

func TestResolveTargetPinsTheConfiguration(t *testing.T) {
	clearTargetEnvironment(t)
	t.Setenv("LIBTMUX_TMUX_CONFIG", "/env/tmux.conf")
	_, config, err := targetFor(t, "", "")
	if err != nil || config != "/env/tmux.conf" {
		t.Fatalf("config = (%q, %v), want the variable's path", config, err)
	}
}

func TestResolveTargetRefusesWhatCannotBePinned(t *testing.T) {
	for _, test := range []struct {
		name               string
		flagName, flagPath string
		config             string
	}{
		{name: "both flags", flagName: "named", flagPath: "/socket"},
		{name: "relative path flag", flagPath: "relative"},
		{name: "relative config", config: "relative"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearTargetEnvironment(t)
			if test.config != "" {
				t.Setenv("LIBTMUX_TMUX_CONFIG", test.config)
			}
			if _, _, _, err := resolveTarget(test.flagName, test.flagPath); err == nil {
				t.Fatal("invalid target was accepted")
			}
		})
	}
}

func TestResolveTargetRejectsEmptyConfig(t *testing.T) {
	clearTargetEnvironment(t)
	t.Setenv("LIBTMUX_TMUX_CONFIG", "")
	if _, _, _, err := resolveTarget("", ""); err == nil {
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
