//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.server.Server.default_option_scope
// libtmux:parity libtmux.session.Session.default_option_scope
// libtmux:parity libtmux._internal.constants.ServerOptions.terminal_features
// libtmux:parity libtmux._internal.constants.TerminalFeatures
// libtmux:parity libtmux.options.CommandAliases
// libtmux:parity libtmux.options.TerminalOverride
// libtmux:parity libtmux.options.TerminalOverrides
// libtmux:parity libtmux.options.explode_complex
//
//libtmux:real-tmux
func TestOptionRuntimePreservesAllRealScopesAndValueStates(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	snapshot := mustRealSnapshot(t, server)
	session, window, pane := onlyRealOptionHierarchy(t, snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.SetBufferLimit(ctx, 73); err != nil {
		t.Fatalf("Server.SetBufferLimit() error = %v", err)
	}
	serverValues, err := server.Options(ctx)
	if err != nil {
		t.Fatalf("Server.Options() error = %v", err)
	}
	if got, ok := serverValues.BufferLimit().Get(); !ok || got != 73 {
		t.Fatalf("BufferLimit().Get() = (%d, %t), want (73, true)", got, ok)
	}
	for _, option := range []struct {
		name  string
		value string
	}{
		{name: "command-alias[40]", value: "libtmux-display=display-message"},
		{name: "terminal-features[40]", value: "libtmux*:clipboard:title"},
		{name: "terminal-overrides[40]", value: "libtmux*:colors=256:Tc"},
	} {
		if err := server.SetOption(ctx, option.name, option.value, tmux.SetOptionOptions{}); err != nil {
			t.Fatalf("Server.SetOption(%s) error = %v", option.name, err)
		}
	}
	serverValues, err = server.Options(ctx)
	if err != nil {
		t.Fatalf("Server.Options(complex) error = %v", err)
	}
	aliasesValue, err := serverValues.CommandAliases()
	if err != nil {
		t.Fatalf("CommandAliases() error = %v", err)
	}
	aliases, ok := aliasesValue.Get()
	if !ok {
		t.Fatal("CommandAliases() reported absent")
	}
	if command, found := aliases.Lookup("libtmux-display"); !found || command != "display-message" {
		t.Fatalf("CommandAliases().Lookup() = (%q, %t)", command, found)
	}
	featuresValue, err := serverValues.ParsedTerminalFeatures()
	if err != nil {
		t.Fatalf("ParsedTerminalFeatures() error = %v", err)
	}
	features, ok := featuresValue.Get()
	if !ok {
		t.Fatal("ParsedTerminalFeatures() reported absent")
	}
	if list, found := features.Lookup("libtmux*"); !found || !slices.Equal(list, []string{"clipboard", "title"}) {
		t.Fatalf("ParsedTerminalFeatures().Lookup() = (%#v, %t)", list, found)
	}
	overridesValue, err := serverValues.ParsedTerminalOverrides()
	if err != nil {
		t.Fatalf("ParsedTerminalOverrides() error = %v", err)
	}
	overrides, ok := overridesValue.Get()
	if !ok {
		t.Fatal("ParsedTerminalOverrides() reported absent")
	}
	capabilities, found := overrides.Lookup("libtmux*")
	if !found {
		t.Fatal("ParsedTerminalOverrides().Lookup() reported absent")
	}
	colors, found := capabilities.Lookup("colors")
	if !found {
		t.Fatal("terminal override colors reported absent")
	}
	integer, integerOK := colors.Integer()
	if !integerOK || integer.Int64() != 256 {
		t.Fatalf("terminal override colors = (%v, %t)", integer, integerOK)
	}
	if tc, found := capabilities.Lookup("Tc"); !found || !tc.IsFlag() {
		t.Fatalf("terminal override Tc = (%#v, %t)", tc, found)
	}

	globalSessionScope := server.GlobalSessionScope()
	if err := globalSessionScope.SetStatusLeft(ctx, "global-left"); err != nil {
		t.Fatalf("GlobalSessionScope.SetStatusLeft() error = %v", err)
	}
	if got, ok, err := globalSessionScope.RawOption(ctx, "status-left"); err != nil || !ok || got != "global-left" {
		t.Fatalf("GlobalSessionScope.RawOption() = (%q, %t, %v)", got, ok, err)
	}
	globalSession, err := globalSessionScope.Options(ctx)
	if err != nil {
		t.Fatalf("GlobalSessionScope.Options() error = %v", err)
	}
	if got, ok := globalSession.StatusLeft().Get(); !ok || got != "global-left" {
		t.Fatalf("global StatusLeft().Get() = (%q, %t)", got, ok)
	}
	inheritedSession, err := session.Options(ctx)
	if err != nil {
		t.Fatalf("Session.Options(inherited) error = %v", err)
	}
	if got, ok := inheritedSession.StatusLeft().Get(); !ok || got != "global-left" {
		t.Fatalf("inherited StatusLeft().Get() = (%q, %t)", got, ok)
	}
	if origin, ok := inheritedSession.StatusLeft().Origin(); !ok || origin != tmux.OptionOriginInherited {
		t.Fatalf("inherited StatusLeft().Origin() = (%v, %t)", origin, ok)
	}
	if err := session.SetStatusLeft(ctx, ""); err != nil {
		t.Fatalf("Session.SetStatusLeft(empty) error = %v", err)
	}
	if got, ok, err := session.RawOption(ctx, "status-left"); err != nil || !ok || got != "" {
		t.Fatalf("Session.RawOption(empty) = (%q, %t, %v)", got, ok, err)
	}
	localSession, err := session.Options(ctx)
	if err != nil {
		t.Fatalf("Session.Options(local) error = %v", err)
	}
	if got, ok := localSession.StatusLeft().Get(); !ok || got != "" {
		t.Fatalf("local StatusLeft().Get() = (%q, %t), want present empty", got, ok)
	}
	if origin, ok := localSession.StatusLeft().Origin(); !ok || origin != tmux.OptionOriginLocal {
		t.Fatalf("local StatusLeft().Origin() = (%v, %t)", origin, ok)
	}

	if err := session.SetOption(ctx, "update-environment[2]", "", tmux.SetOptionOptions{}); err != nil {
		t.Fatalf("set empty array entry error = %v", err)
	}
	if err := session.SetOption(ctx, "update-environment[7]", "A B", tmux.SetOptionOptions{}); err != nil {
		t.Fatalf("set sparse array entry error = %v", err)
	}
	if got, ok, err := session.RawOption(ctx, "update-environment[2]"); err != nil || !ok || got != "" {
		t.Fatalf("RawOption(empty index) = (%q, %t, %v)", got, ok, err)
	}
	arrayValues, err := session.Options(ctx)
	if err != nil {
		t.Fatalf("Session.Options(array) error = %v", err)
	}
	array, ok := arrayValues.UpdateEnvironment().Get()
	if !ok || !slices.Contains(array.Indices(), 2) || !slices.Contains(array.Indices(), 7) {
		t.Fatalf("UpdateEnvironment() = (%#v, %t), want sparse indices 2 and 7", array, ok)
	}
	if got, ok := array.Get(2); !ok || got != "" {
		t.Fatalf("UpdateEnvironment()[2] = (%q, %t), want present empty", got, ok)
	}
	if got, ok := array.Get(7); !ok || got != "A B" {
		t.Fatalf("UpdateEnvironment()[7] = (%q, %t), want A B", got, ok)
	}

	globalWindowScope := server.GlobalWindowScope()
	if err := globalWindowScope.SetWindowStyle(ctx, "fg=red"); err != nil {
		t.Fatalf("GlobalWindowScope.SetWindowStyle() error = %v", err)
	}
	if got, ok, err := globalWindowScope.RawOption(ctx, "window-style"); err != nil || !ok || got != "fg=red" {
		t.Fatalf("GlobalWindowScope.RawOption() = (%q, %t, %v)", got, ok, err)
	}
	globalWindow, err := globalWindowScope.Options(ctx)
	if err != nil {
		t.Fatalf("GlobalWindowScope.Options() error = %v", err)
	}
	if got, ok := globalWindow.WindowStyle().Get(); !ok || got != "fg=red" {
		t.Fatalf("global WindowStyle().Get() = (%q, %t)", got, ok)
	}
	windowValues, err := window.Options(ctx)
	if err != nil {
		t.Fatalf("Window.Options() error = %v", err)
	}
	if got, ok := windowValues.WindowStyle().Get(); !ok || got != "fg=red" {
		t.Fatalf("WindowStyle().Get() = (%q, %t)", got, ok)
	}
	if err := window.SetWindowStyle(ctx, "fg=green"); err != nil {
		t.Fatalf("Window.SetWindowStyle() error = %v", err)
	}
	if err := window.UnsetOption(ctx, "window-style", tmux.UnsetOptionOptions{}); err != nil {
		t.Fatalf("Window.UnsetOption() error = %v", err)
	}
	if err := pane.SetWindowStyle(ctx, "bg=blue"); err != nil {
		t.Fatalf("Pane.SetWindowStyle() error = %v", err)
	}
	paneValues, err := pane.Options(ctx)
	if err != nil {
		t.Fatalf("Pane.Options() error = %v", err)
	}
	if got, ok := paneValues.WindowStyle().Get(); !ok || got != "bg=blue" {
		t.Fatalf("Pane WindowStyle().Get() = (%q, %t)", got, ok)
	}
	if err := window.UnsetOption(ctx, "window-style", tmux.UnsetOptionOptions{UnsetPanes: true}); err != nil {
		t.Fatalf("Window.UnsetOption(UnsetPanes) error = %v", err)
	}
	afterUnset, err := pane.Options(ctx)
	if err != nil {
		t.Fatalf("Pane.Options(after UnsetPanes) error = %v", err)
	}
	if got, ok := afterUnset.WindowStyle().Get(); !ok || got != "fg=red" {
		t.Fatalf("Pane WindowStyle() after UnsetPanes = (%q, %t), want inherited fg=red", got, ok)
	}

	for _, target := range []struct {
		name string
		set  func(string, string) error
		raw  func(string) (string, bool, error)
	}{
		{name: "server", set: func(name, value string) error { return server.SetOption(ctx, name, value, tmux.SetOptionOptions{}) }, raw: func(name string) (string, bool, error) { return server.RawOption(ctx, name) }},
		{name: "session", set: func(name, value string) error { return session.SetOption(ctx, name, value, tmux.SetOptionOptions{}) }, raw: func(name string) (string, bool, error) { return session.RawOption(ctx, name) }},
		{name: "window", set: func(name, value string) error { return window.SetOption(ctx, name, value, tmux.SetOptionOptions{}) }, raw: func(name string) (string, bool, error) { return window.RawOption(ctx, name) }},
		{name: "pane", set: func(name, value string) error { return pane.SetOption(ctx, name, value, tmux.SetOptionOptions{}) }, raw: func(name string) (string, bool, error) { return pane.RawOption(ctx, name) }},
	} {
		name := "@libtmux-" + target.name
		if err := target.set(name, "custom value"); err != nil {
			t.Fatalf("%s custom SetOption() error = %v", target.name, err)
		}
		if got, ok, err := target.raw(name); err != nil || !ok || got != "custom value" {
			t.Fatalf("%s custom RawOption() = (%q, %t, %v)", target.name, got, ok, err)
		}
	}

	if err := session.SetOption(ctx, "@append", "one", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendOption(ctx, "@append", " two", tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := session.RawOption(ctx, "@append"); err != nil || !ok || got != "one two" {
		t.Fatalf("appended RawOption() = (%q, %t, %v)", got, ok, err)
	}
	if _, ok, err := session.RawOption(ctx, "@missing"); err != nil || ok {
		t.Fatalf("missing RawOption() = (%t, %v), want absent", ok, err)
	}
	if err := session.SetOption(ctx, "definitely-not-an-option", "value", tmux.SetOptionOptions{}); !errors.Is(err, tmux.ErrInvalidOption) {
		t.Fatalf("unknown-name SetOption() error = %v, want ErrInvalidOption", err)
	}
}

// libtmux:parity libtmux.options.OptionsMixin.set_option
//
//libtmux:real-tmux
func TestTypedArrayOptionReplacementPreservesRealState(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	snapshot := mustRealSnapshot(t, server)
	session, window, pane := onlyRealOptionHierarchy(t, snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	globalStatus := mustSparseStrings(t,
		tmux.SparseEntry[string]{Index: 0, Value: "global"},
		tmux.SparseEntry[string]{Index: 4, Value: "global tail"},
	)
	if result, err := server.GlobalSessionScope().SetStatusFormat(ctx, globalStatus); err != nil ||
		!result.Replaced || !slices.Equal(result.AppliedIndices, []int{0, 4}) {
		t.Fatalf("GlobalSessionScope.SetStatusFormat() = (%#v, %v)", result, err)
	}
	result, err := session.SetStatusFormat(ctx, tmux.SparseArray[string]{})
	if err != nil || !result.Replaced || result.AppliedIndices == nil || len(result.AppliedIndices) != 0 {
		t.Fatalf("Session.SetStatusFormat(empty) = (%#v, %v)", result, err)
	}
	localSessionValues, err := session.Options(ctx)
	if err != nil {
		t.Fatal(err)
	}
	emptyStatus, ok := localSessionValues.StatusFormat().Get()
	if !ok || emptyStatus.Len() != 0 {
		t.Fatalf("local StatusFormat() = (%#v, %t), want present empty", emptyStatus, ok)
	}
	if origin, ok := localSessionValues.StatusFormat().Origin(); !ok || origin != tmux.OptionOriginLocal {
		t.Fatalf("local StatusFormat().Origin() = (%v, %t)", origin, ok)
	}
	if err := session.UnsetOption(ctx, "status-format", tmux.UnsetOptionOptions{}); err != nil {
		t.Fatalf("Session.UnsetOption(status-format) error = %v", err)
	}
	inheritedValues, err := session.Options(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inheritedStatus, ok := inheritedValues.StatusFormat().Get()
	if !ok || !slices.Equal(inheritedStatus.Indices(), []int{0, 4}) {
		t.Fatalf("inherited StatusFormat() = (%#v, %t)", inheritedStatus, ok)
	}
	if origin, ok := inheritedValues.StatusFormat().Origin(); !ok || origin != tmux.OptionOriginInherited {
		t.Fatalf("inherited StatusFormat().Origin() = (%v, %t)", origin, ok)
	}

	dense := mustSparseStrings(t,
		tmux.SparseEntry[string]{Index: 0, Value: "ZERO"},
		tmux.SparseEntry[string]{Index: 1, Value: "ONE"},
		tmux.SparseEntry[string]{Index: 2, Value: "TWO"},
	)
	if _, err := session.SetUpdateEnvironment(ctx, dense); err != nil {
		t.Fatalf("Session.SetUpdateEnvironment(dense) error = %v", err)
	}
	sparse := mustSparseStrings(t,
		tmux.SparseEntry[string]{Index: 2, Value: ""},
		tmux.SparseEntry[string]{Index: 7, Value: "A B"},
	)
	result, err = session.SetUpdateEnvironment(ctx, sparse)
	if err != nil || !result.Replaced || !slices.Equal(result.AppliedIndices, []int{2, 7}) {
		t.Fatalf("Session.SetUpdateEnvironment(sparse) = (%#v, %v)", result, err)
	}
	updatedValues, err := session.Options(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := updatedValues.UpdateEnvironment().Get()
	if !ok || !slices.Equal(updated.Indices(), []int{2, 7}) {
		t.Fatalf("UpdateEnvironment() = (%#v, %t), want only indices 2 and 7", updated, ok)
	}
	if got, ok := updated.Get(2); !ok || got != "" {
		t.Fatalf("UpdateEnvironment()[2] = (%q, %t), want present empty", got, ok)
	}
	if _, ok := updated.Get(0); ok {
		t.Fatal("dense-to-sparse replacement retained old index 0")
	}

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paneColoursFloor, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	colours := mustSparseStrings(t, tmux.SparseEntry[string]{Index: 2, Value: "red"})
	if !version.AtLeast(paneColoursFloor) {
		result, err = window.SetPaneColours(ctx, colours)
		if !errors.Is(err, tmux.ErrInvalidOption) || result.Replaced || result.AppliedIndices != nil {
			t.Fatalf("Window.SetPaneColours(before 3.3) = (%#v, %v)", result, err)
		}
	} else {
		if _, err := server.GlobalWindowScope().SetPaneColours(ctx, colours); err != nil {
			t.Fatalf("GlobalWindowScope.SetPaneColours() error = %v", err)
		}
		paneColours := mustSparseStrings(t, tmux.SparseEntry[string]{Index: 5, Value: "blue"})
		if _, err := pane.SetPaneColours(ctx, paneColours); err != nil {
			t.Fatalf("Pane.SetPaneColours() error = %v", err)
		}
		paneValues, err := pane.Options(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := paneValues.PaneColours().Get()
		if !ok || !slices.Equal(got.Indices(), []int{5}) {
			t.Fatalf("PaneColours() = (%#v, %t), want local index 5", got, ok)
		}
	}

	codepointFloor, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	result, err = server.SetCodepointWidths(ctx, tmux.SparseArray[string]{})
	if version.AtLeast(codepointFloor) {
		if err != nil || !result.Replaced || result.AppliedIndices == nil {
			t.Fatalf("Server.SetCodepointWidths(empty) = (%#v, %v)", result, err)
		}
	} else if !errors.Is(err, tmux.ErrInvalidOption) || result.Replaced || result.AppliedIndices != nil {
		t.Fatalf("Server.SetCodepointWidths(before 3.6) = (%#v, %v)", result, err)
	}
}

// libtmux:parity libtmux.options.OptionsMixin.set_option
//
//libtmux:real-tmux
func TestTypedArrayOptionReplacementReportsRealPartialFailure(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.Join(t.TempDir(), "tmux-option-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"for argument in \"$@\"; do\n" +
		"  if [ \"$argument\" = 'command-alias[2]' ]; then\n" +
		"    echo 'injected indexed failure' >&2\n" +
		"    exit 73\n" +
		"  fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_OPTION_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_OPTION_REAL_TMUX", realBinary)
	proxyServer := tmux.NewServer(tmux.ServerOptions{
		Binary:             proxyPath,
		SocketPath:         server.SocketPath(),
		ConfigFile:         server.ConfigFile(),
		ProcessEnvironment: os.Environ(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	values := mustSparseStrings(t,
		tmux.SparseEntry[string]{Index: 0, Value: "first=display-message"},
		tmux.SparseEntry[string]{Index: 2, Value: "second=display-message"},
		tmux.SparseEntry[string]{Index: 4, Value: "third=display-message"},
	)

	result, err := proxyServer.SetCommandAlias(ctx, values)
	if !errors.Is(err, tmux.ErrOption) || !result.Replaced ||
		!slices.Equal(result.AppliedIndices, []int{0}) {
		t.Fatalf("SetCommandAlias(proxy failure) = (%#v, %v)", result, err)
	}
	if got, ok, err := server.RawOption(ctx, "command-alias[0]"); err != nil || !ok || got != "first=display-message" {
		t.Fatalf("confirmed command-alias[0] = (%q, %t, %v)", got, ok, err)
	}
	if _, ok, err := server.RawOption(ctx, "command-alias[2]"); err != nil || ok {
		t.Fatalf("failed command-alias[2] presence = (%t, %v), want absent", ok, err)
	}
	if _, ok, err := server.RawOption(ctx, "command-alias[4]"); err != nil || ok {
		t.Fatalf("unattempted command-alias[4] presence = (%t, %v), want absent", ok, err)
	}
}

func mustSparseStrings(t *testing.T, entries ...tmux.SparseEntry[string]) tmux.SparseArray[string] {
	t.Helper()
	values, err := tmux.NewSparseArray(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

// libtmux:parity libtmux.server.Server.default_hook_scope
// libtmux:parity libtmux.session.Session.default_hook_scope
//
//libtmux:real-tmux
func TestHookRuntimePreservesAllRealScopesAndPartialBulkSemantics(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	snapshot := mustRealSnapshot(t, server)
	session, window, pane := onlyRealOptionHierarchy(t, snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Server.Version() error = %v", err)
	}
	emptyHookMinimum, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	emptyHookValue := "display-message sparse-global"
	localSparseValue := "display-message sparse-local"
	if version.AtLeast(emptyHookMinimum) {
		emptyHookValue = ""
		localSparseValue = ""
	}

	globalHooks, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "display-message global"},
		tmux.SparseEntry[string]{Index: 5, Value: emptyHookValue},
	)
	if err != nil {
		t.Fatal(err)
	}
	globalSessionScope := server.GlobalSessionScope()
	result, err := globalSessionScope.SetHooks(ctx, "session-renamed", globalHooks, tmux.SetHooksOptions{ClearExisting: true})
	if err != nil {
		t.Fatalf("GlobalSessionScope.SetHooks() error = %v", err)
	}
	if !result.Cleared || !slices.Equal(result.AppliedIndices, []int{0, 5}) {
		t.Fatalf("GlobalSessionScope.SetHooks() result = %#v", result)
	}
	serverValues, err := globalSessionScope.Hooks(ctx)
	if err != nil {
		t.Fatalf("GlobalSessionScope.Hooks() error = %v", err)
	}
	hooks, ok := serverValues.SessionRenamed().Get()
	if !ok || !slices.Equal(hooks.Indices(), []int{0, 5}) {
		t.Fatalf("Server SessionRenamed() = (%#v, %t)", hooks, ok)
	}
	if got, ok := hooks.Get(5); !ok || got != emptyHookValue {
		t.Fatalf("Server SessionRenamed()[5] = (%q, %t), want %q", got, ok, emptyHookValue)
	}
	inherited, err := session.Hooks(ctx)
	if err != nil {
		t.Fatalf("Session.Hooks(inherited) error = %v", err)
	}
	if origin, ok := inherited.SessionRenamed().Origin(); !ok || origin != tmux.OptionOriginInherited {
		t.Fatalf("inherited SessionRenamed().Origin() = (%v, %t)", origin, ok)
	}

	localHooks, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 2, Value: localSparseValue},
		tmux.SparseEntry[string]{Index: 7, Value: "set-option -t work @hook-fired yes"},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err = session.SetHooks(ctx, "session-renamed", localHooks, tmux.SetHooksOptions{ClearExisting: true})
	if err != nil {
		t.Fatalf("Session.SetHooks() error = %v", err)
	}
	if !result.Cleared || !slices.Equal(result.AppliedIndices, []int{2, 7}) {
		t.Fatalf("Session.SetHooks() result = %#v", result)
	}
	if got, ok, err := session.RawHook(ctx, "session-renamed[2]"); err != nil || !ok || got != localSparseValue {
		t.Fatalf("RawHook(sparse index) = (%q, %t, %v), want %q", got, ok, err, localSparseValue)
	}
	if got, ok, err := session.RawHook(ctx, "session-renamed[3]"); err != nil || ok || got != "" {
		t.Fatalf("RawHook(missing index) = (%q, %t, %v), want absent", got, ok, err)
	}
	local, err := session.Hooks(ctx)
	if err != nil {
		t.Fatalf("Session.Hooks(local) error = %v", err)
	}
	hooks, ok = local.SessionRenamed().Get()
	if !ok || !slices.Equal(hooks.Indices(), []int{2, 7}) {
		t.Fatalf("local SessionRenamed() = (%#v, %t)", hooks, ok)
	}
	if got, ok := hooks.Get(2); !ok || got != localSparseValue {
		t.Fatalf("SessionRenamed()[2] = (%q, %t), want %q", got, ok, localSparseValue)
	}
	if err := session.RunHook(ctx, "session-renamed"); err != nil {
		t.Fatalf("Session.RunHook() error = %v", err)
	}
	if got, ok, err := session.RawOption(ctx, "@hook-fired"); err != nil || !ok || got != "yes" {
		t.Fatalf("RunHook side effect = (%q, %t, %v)", got, ok, err)
	}

	globalWindowScope := server.GlobalWindowScope()
	if err := globalWindowScope.SetHook(
		ctx,
		"pane-died[0]",
		"display-message global-window",
	); err != nil {
		t.Fatalf("GlobalWindowScope.SetHook() error = %v", err)
	}
	if got, ok, err := globalWindowScope.RawHook(ctx, "pane-died[0]"); err != nil || !ok || got != "display-message global-window" {
		t.Fatalf("GlobalWindowScope.RawHook() = (%q, %t, %v)", got, ok, err)
	}
	globalWindow, err := globalWindowScope.Hooks(ctx)
	if err != nil {
		t.Fatalf("GlobalWindowScope.Hooks() error = %v", err)
	}
	if value, ok := globalWindow.PaneDied().Get(); !ok || !slices.Equal(value.Indices(), []int{0}) {
		t.Fatalf("global PaneDied() = (%#v, %t)", value, ok)
	}
	windowValues, err := window.Hooks(ctx)
	if err != nil {
		t.Fatalf("Window.Hooks() error = %v", err)
	}
	if origin, ok := windowValues.PaneDied().Origin(); !ok || origin != tmux.OptionOriginInherited {
		t.Fatalf("Window PaneDied().Origin() = (%v, %t)", origin, ok)
	}
	if err := window.SetHook(ctx, "pane-died[3]", "display-message window"); err != nil {
		t.Fatalf("Window.SetHook() error = %v", err)
	}
	if err := pane.SetHook(ctx, "pane-died[4]", "display-message pane"); err != nil {
		t.Fatalf("Pane.SetHook() error = %v", err)
	}
	if _, err := window.Hooks(ctx); err != nil {
		t.Fatalf("Window.Hooks(local) error = %v", err)
	}
	paneValues, err := pane.Hooks(ctx)
	if err != nil {
		t.Fatalf("Pane.Hooks() error = %v", err)
	}
	if value, ok := paneValues.PaneDied().Get(); !ok || !slices.Contains(value.Indices(), 4) {
		t.Fatalf("Pane PaneDied() = (%#v, %t)", value, ok)
	}
	if err := pane.AppendHook(ctx, "pane-died", "display-message appended"); err != nil {
		t.Fatalf("Pane.AppendHook() error = %v", err)
	}
	if _, ok, err := pane.RawHook(ctx, "pane-died[0]"); err != nil || !ok {
		t.Fatalf("Pane.RawHook(appended) = (%t, %v), want present", ok, err)
	}
	if err := pane.UnsetHook(ctx, "pane-died"); err != nil {
		t.Fatalf("Pane.UnsetHook() error = %v", err)
	}
	if err := session.SetHook(ctx, "definitely-not-a-hook", "display-message value"); !errors.Is(err, tmux.ErrInvalidOption) {
		t.Fatalf("unknown-name SetHook() error = %v, want ErrInvalidOption", err)
	}
}

//libtmux:real-tmux
func TestSetHooksRejectsOverflowWithoutClearingRealHook(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("an index above MaxInt32 is not representable by int")
	}
	server := tmuxtest.NewServer(context.Background(), t)
	snapshot := mustRealSnapshot(t, server)
	session, _, _ := onlyRealOptionHierarchy(t, snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const existing = "display-message preserved-hook"
	if err := session.SetHook(ctx, "session-renamed[0]", existing); err != nil {
		t.Fatalf("Session.SetHook() error = %v", err)
	}
	const secret = "private-overflow-hook"
	values, err := tmux.NewSparseArray(tmux.SparseEntry[string]{
		Index: int(int64(math.MaxInt32) + 1),
		Value: secret,
	})
	if err != nil {
		t.Fatalf("NewSparseArray() error = %v", err)
	}
	result, err := session.SetHooks(
		ctx,
		"session-renamed",
		values,
		tmux.SetHooksOptions{ClearExisting: true},
	)
	if !errors.Is(err, tmux.ErrInvalidSparseIndex) {
		t.Fatalf("Session.SetHooks() error = %v, want ErrInvalidSparseIndex", err)
	}
	if result.Cleared || result.AppliedIndices != nil {
		t.Fatalf("Session.SetHooks() result = %#v, want zero progress", result)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Session.SetHooks() error retained hook value: %v", err)
	}
	got, ok, err := session.RawHook(ctx, "session-renamed[0]")
	if err != nil || !ok || got != existing {
		t.Fatalf("Session.RawHook() = (%q, %t, %v), want preserved hook", got, ok, err)
	}
}

func onlyRealOptionHierarchy(t *testing.T, snapshot tmux.Snapshot) (tmux.Session, tmux.Window, tmux.Pane) {
	t.Helper()
	sessions := snapshot.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("snapshot sessions = %d, want 1", len(sessions))
	}
	windows := relatedWindows(t, sessions[0])
	if len(windows) != 1 {
		t.Fatalf("session windows = %d, want 1", len(windows))
	}
	panes := relatedPanes(t, windows[0])
	if len(panes) != 1 {
		t.Fatalf("window panes = %d, want 1", len(panes))
	}
	return sessions[0], windows[0], panes[0]
}

// TestOptionTargetSentinelSeparatesAMissingWindowFromABadName covers the pair
// against real tmux. Both exit 1 and neither renders tmux's message, so the
// sentinel is the only thing that tells a caller which mistake it made.
//
//libtmux:real-tmux
func TestOptionTargetSentinelSeparatesAMissingWindowFromABadName(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "targets"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatalf("create window: %v", err)
	}
	if err := window.Kill(ctx); err != nil {
		t.Fatalf("kill window: %v", err)
	}

	err = window.SetOption(ctx, "remain-on-exit", "on", tmux.SetOptionOptions{})
	if !errors.Is(err, tmux.ErrOptionTarget) {
		t.Errorf("setting an option on a killed window gave %v, want ErrOptionTarget", err)
	}
	if errors.Is(err, tmux.ErrUnknownOption) {
		t.Errorf("a missing target must not report an unknown option: %v", err)
	}

	live, err := session.NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		t.Fatalf("create second window: %v", err)
	}
	err = live.SetOption(ctx, "no-such-option-name", "x", tmux.SetOptionOptions{})
	if errors.Is(err, tmux.ErrOptionTarget) {
		t.Errorf("an unknown name must not report a missing target: %v", err)
	}
	if !errors.Is(err, tmux.ErrOption) {
		t.Errorf("got %v, want an option error", err)
	}
}
