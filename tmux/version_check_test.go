package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestMinimumVersionCheckHonorsPatchLetters(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{"3.2a", false},
		{"3.3", false},
		{"3.3-rc2", false},
		{"3.3rc2", false},
		{"next-3.3", false},
		{"next-3.3a", false},
		{"3.3-master", false},
		{"3.3a-rc2", false},
		{"3.3a", true},
		{"3.3b", true},
		{"3.4", true},
		{"3.10", true},
		{"3.3a-master", false},
		{"master", false},
		{"3.4garbage", false},
		{"9.9alpha", false},
		{"3.7a-unknown", false},
		{"next-3.9nonsense", false},
		{"next-3.3a-master", false},
		{"3.3a-dev", false},
		{"3.3b-rc2", false},
		{"3.3.1", true},
		{"3.8-rc2", true},
		{"next-3.8", true},
		{"4.0", true},
	} {
		t.Run(test.raw, func(t *testing.T) {
			version := mustParseVersion(t, test.raw)
			if got := version.meetsMinimum(minimumSupportedVersion); got != test.want {
				t.Fatalf("%s meets core minimum = %t, want %t", version, got, test.want)
			}
		})
	}
	if !mustParseVersion(t, "3.3").AtLeast(mustParseVersion(t, "3.3a")) {
		t.Fatal("feature-level comparison no longer ignores release letters")
	}
	for minor, want := range map[int]bool{3: false, 4: false, 7: false} {
		version := Version{raw: "openbsd-7.9", major: 3, minor: minor}
		if version.meetsMinimum(minimumSupportedVersion) != want {
			t.Fatalf("OpenBSD capability 3.%d meets core minimum = %t", minor, !want)
		}
	}
}

func TestCoreVersionCheckRejectsSyntheticOpenBSDQualification(t *testing.T) {
	for _, daemon := range []string{"", "openbsd-7.8"} {
		t.Run("daemon="+daemon, func(t *testing.T) {
			runner := &versionRunner{daemon: daemon, binary: "openbsd-7.8"}
			server := rawVersionServer(t, runner)
			server.state.shared.version.value = Version{raw: "openbsd-7.8", major: 3, minor: 7}
			server.state.shared.version.valid = true
			_, err := server.Cmd(t.Context(), "set-buffer", "unreached")
			wantProbes := 0
			if daemon == "" {
				wantProbes = 1
			}
			if !errors.Is(err, ErrVersionTooLow) || runner.effects != 0 || runner.binaryProbes != wantProbes {
				t.Fatalf("synthetic capability authorized dispatch: %v, effects=%d, binary probes=%d", err, runner.effects, runner.binaryProbes)
			}
		})
	}
}

func TestCoreVersionCheckRejectsOldDaemonBeforeDispatch(t *testing.T) {
	for _, version := range []string{"3.2a", "3.3", "3.3-rc2", "master", "3.4garbage", "9.9alpha"} {
		for name, call := range map[string]func(Server) error{
			"raw read": func(server Server) error {
				_, err := server.Cmd(t.Context(), "display-message", "-p", "unreached")
				return err
			},
			"raw command list": func(server Server) error {
				_, err := server.Cmd(t.Context(), "set-option", "-g", "status", "off", ";", "set-buffer", "secret")
				return err
			},
			"typed write": func(server Server) error {
				return server.SetBuffer(t.Context(), SetBufferRequest{Data: "secret"})
			},
			"plan": func(server Server) error {
				var plan Plan
				plan.SetBuffer("named", "secret")
				_, err := plan.Run(t.Context(), server)
				return err
			},
			"control client": func(server Server) error {
				_, err := server.OpenControl(t.Context(), Session{server: server, sessionID: "$1"})
				return err
			},
			"connection": func(server Server) error {
				_, err := (Session{server: server, sessionID: "$1"}).OpenControl(t.Context(), ConnectionOptions{})
				return err
			},
			"new session connection": func(server Server) error {
				_, _, err := server.NewSessionConnection(t.Context(), NewSessionRequest{}, ConnectionOptions{})
				return err
			},
			"notification stream": func(server Server) error {
				_, err := server.OpenNotifications(t.Context(), Session{server: server, sessionID: "$1"}, NotificationOptions{})
				return err
			},
		} {
			t.Run(version+"/"+name, func(t *testing.T) {
				runner := &versionRunner{daemon: version, binary: "3.8"}
				server := rawVersionServer(t, runner)
				err := call(server)
				var refused *VersionTooLowError
				if !errors.As(err, &refused) || !errors.Is(err, ErrVersionTooLow) ||
					refused.Current.String() != version || refused.Minimum.String() != "3.3a" {
					t.Fatalf("operation error = %#v, want daemon %s below 3.3a", err, version)
				}
				if runner.effects != 0 || runner.binaryProbes != 0 {
					t.Fatalf("effects=%d binary probes=%d; newer client must not authorize daemon", runner.effects, runner.binaryProbes)
				}
			})
		}
	}
}

// A daemon before 3.2 has no run-shell -C, and a 3.0 daemon will not talk to a
// newer client at all; a client before 3.2 rejects -N. All three are an
// unsupported tmux, not a transport or parse failure.
func TestCoreVersionCheckMapsPre32ProbeFailuresToTheRefusal(t *testing.T) {
	// A daemon that cannot answer never reports a release; a client that
	// rejects -N is the executable, which does.
	for name, test := range map[string]struct {
		binary, installed string
		stderr            []string
	}{
		"3.1 daemon": {"3.8", "older than 3.2", []string{"usage: run-shell [-b] [-t target-pane] shell-command"}},
		"3.0 daemon": {"3.8", "older than 3.2", []string{"server version is too old for client"}},
		"3.1 client": {"3.1c", "3.1c", []string{"tmux: unknown option -- N", "usage: tmux [-2CluvV] [-c shell-command] [-f file] [-L socket-name]", "            [-S socket-path] [command [flags]]"}},
		"3.0 client": {"3.0a", "3.0a", []string{"tmux: unknown option -- N", "usage: tmux [-2CluvV] [-c shell-command] [-f file] [-L socket-name]"}},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &versionRunner{binary: test.binary, probeResult: &tmuxcmd.Result{ExitCode: 1, Stderr: test.stderr}}
			_, err := rawVersionServer(t, runner).Cmd(t.Context(), "set-buffer", "secret")
			var refused *VersionTooLowError
			if !errors.As(err, &refused) || !errors.Is(err, ErrVersionTooLow) ||
				refused.Minimum.String() != "3.3a" || refused.Current.AtLeast(mustParseVersion(t, "3.2")) {
				t.Fatalf("error = %#v, want a refusal of a tmux older than 3.2", err)
			}
			if !errors.Is(err, ErrVersionCheckFailed) {
				t.Fatalf("error = %v, want it marked as a failed check that sent nothing", err)
			}
			if !strings.Contains(err.Error(), "installed "+test.installed+",") || runner.effects != 0 {
				t.Fatalf("error = %q, effects=%d, want installed %s", err, runner.effects, test.installed)
			}
		})
	}
}

// The mapping is by the known shapes only: any other probe failure keeps
// its own error instead of being blamed on the version.
func TestCoreVersionCheckLeavesOtherProbeFailuresUnmapped(t *testing.T) {
	for name, result := range map[string]tmuxcmd.Result{
		"other run-shell failure":  {ExitCode: 1, Stderr: []string{"run-shell: something else broke"}},
		"unknown option elsewhere": {ExitCode: 1, Stderr: []string{"tmux: unknown option -- Z", "usage: tmux"}},
		"usage with exit 0":        {ExitCode: 0, Stderr: []string{"usage: run-shell [-b]"}},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &versionRunner{binary: "3.8", probeResult: &result}
			_, err := rawVersionServer(t, runner).Cmd(t.Context(), "set-buffer", "secret")
			if err == nil || errors.Is(err, ErrVersionTooLow) || runner.effects != 0 {
				t.Fatalf("error = %v, effects=%d, want an unmapped probe failure", err, runner.effects)
			}
			if !errors.Is(err, ErrVersionCheckFailed) {
				t.Fatalf("error = %v, want it marked as a failed check that sent nothing", err)
			}
		})
	}
}

// A version that names no release is not "too low": the message says it was
// not recognized and keeps the remedy, while a real older release still reads
// as installed and required.
func TestCoreVersionRefusalWordsUnreadableAndOlderVersionsApart(t *testing.T) {
	for _, version := range []string{"master", "openbsd-7.8", "3.3a-dev", "3.4garbage"} {
		t.Run(version, func(t *testing.T) {
			err := requireSupportedVersion(mustParseVersion(t, version))
			text := err.Error()
			if !errors.Is(err, ErrVersionTooLow) || !strings.Contains(text, "unrecognized") ||
				!strings.Contains(text, strconv.Quote(version)) ||
				strings.Contains(text, "installed") || strings.Contains(text, "too low") {
				t.Fatalf("error = %q, want an unrecognized-version refusal", text)
			}
			if !strings.Contains(text, "restart") {
				t.Fatalf("error = %q, want the remedy", text)
			}
		})
	}
	for _, version := range []string{"3.2a", "3.3", "3.3-rc2"} {
		text := requireSupportedVersion(mustParseVersion(t, version)).Error()
		if !strings.Contains(text, "installed "+version+", require 3.3a or newer") ||
			strings.Contains(text, "unrecognized") ||
			!strings.Contains(text, "upgrade tmux and restart its server, or pin the previous libtmux release") {
			t.Fatalf("error = %q, want installed %s with the remedy", text, version)
		}
	}
	// An optional capability names the feature and carries no pin advice.
	feature := (&VersionTooLowError{
		Current: mustParseVersion(t, "3.4"), Minimum: mustParseVersion(t, "3.6"),
		Subcommand: "display-popup", Feature: "no_keys",
	}).Error()
	if strings.Contains(feature, "pin the previous") {
		t.Fatalf("feature refusal = %q, want no floor remedy", feature)
	}
}

// A probe is read-only, so a failure while it runs means the request was never
// sent. A plan must leave its operations skipped, not indeterminate.
func TestCoreVersionCheckProbeFailureLeavesPlanOperationsSkipped(t *testing.T) {
	for name, probeErr := range map[string]error{
		"cancelled after the process started": &tmuxcmd.OutcomeUnknownError{Err: context.Canceled},
		"read failure":                        &tmuxcmd.OutcomeUnknownError{Err: errors.New("read tmux output: broken pipe")},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &versionRunner{binary: "3.8", probeErr: probeErr}
			var plan Plan
			plan.SetBuffer("named", "secret")
			result, err := plan.Run(t.Context(), rawVersionServer(t, runner))
			if err == nil || errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("error = %v, want a failure that is not outcome-unknown", err)
			}
			if len(result.Ops) != 1 || result.Ops[0].Status != OpSkipped || runner.effects != 0 {
				t.Fatalf("ops = %#v, effects=%d, want one skipped operation", result.Ops, runner.effects)
			}
		})
	}
}

func TestCoreVersionCheckExecutableDiagnosticsDoNotAuthorizeDaemon(t *testing.T) {
	for _, binary := range []string{"3.2a", "3.8", "master"} {
		t.Run(binary, func(t *testing.T) {
			runner := &versionRunner{daemon: "3.2a", binary: binary}
			server := rawVersionServer(t, runner)
			for _, flag := range []string{"-V", "-h"} {
				if result, err := server.Cmd(t.Context(), flag); err != nil || result.ExitCode != 0 {
					t.Fatalf("executable diagnostic %s = %v, %v", flag, result, err)
				}
			}
			version, err := server.Version(t.Context())
			if err != nil || version.String() != binary {
				t.Fatalf("executable version = %s, %v", version, err)
			}
			if runner.effects != 0 {
				t.Fatalf("executable diagnostics reached daemon effects: %d", runner.effects)
			}
			_, err = server.Cmd(t.Context(), "set-buffer", "unreached")
			if !errors.Is(err, ErrVersionTooLow) || runner.effects != 0 {
				t.Fatalf("diagnostics authorized unsupported daemon: %v, effects=%d", err, runner.effects)
			}
		})
	}
}

func TestCoreVersionCheckGuardsQualifiedDaemon(t *testing.T) {
	for _, version := range []string{"3.3a", "3.4", "3.10"} {
		t.Run(version, func(t *testing.T) {
			runner := &versionRunner{daemon: version, binary: "3.2a"}
			server := rawVersionServer(t, runner)
			if err := server.SetBuffer(t.Context(), SetBufferRequest{Data: "literal"}); err != nil {
				t.Fatal(err)
			}
			if runner.effects != 1 || runner.guarded != 1 || runner.binaryProbes != 0 {
				t.Fatalf("effects=%d guarded=%d binary probes=%d", runner.effects, runner.guarded, runner.binaryProbes)
			}
		})
	}
}

func TestCoreVersionCheckRejectsReplacementAtMutation(t *testing.T) {
	runner := &versionRunner{daemon: "3.3a", replaced: true}
	err := rawVersionServer(t, runner).SetBuffer(t.Context(), SetBufferRequest{Data: "secret"})
	if !errors.Is(err, ErrDaemonReplaced) || runner.effects != 0 || runner.guarded != 1 {
		t.Fatalf("error=%v effects=%d guarded=%d", err, runner.effects, runner.guarded)
	}
}

func TestCoreVersionCheckChecksExecutableOnlyWithoutDaemon(t *testing.T) {
	for _, version := range []string{"3.2a", "3.3", "master", "3.4garbage", "9.9alpha", "3.3a"} {
		t.Run(version, func(t *testing.T) {
			runner := &versionRunner{binary: version}
			_, err := rawVersionServer(t, runner).Cmd(t.Context(), "new-session", "-d", "-s", "created")
			if version == "3.3a" {
				if err != nil || runner.effects != 1 {
					t.Fatalf("new session error=%v effects=%d", err, runner.effects)
				}
			} else if !errors.Is(err, ErrVersionTooLow) || runner.effects != 0 {
				t.Fatalf("old executable error=%v effects=%d", err, runner.effects)
			}
			if runner.binaryProbes != 1 {
				t.Fatalf("binary probes=%d, want one", runner.binaryProbes)
			}
		})
	}
}

func TestCoreVersionCheckFencesDaemonAppearingDuringStartup(t *testing.T) {
	runner := &versionRunner{binary: "3.3a", startupDaemon: "3.2a"}
	_, err := rawVersionServer(t, runner).Cmd(t.Context(), "new-session", "-d", "-s", "never-created")
	if !errors.Is(err, ErrDaemonReplaced) || runner.effects != 0 || runner.guarded != 1 {
		t.Fatalf("error=%v effects=%d guarded=%d", err, runner.effects, runner.guarded)
	}
}

func TestCoreVersionCheckAllowsAbsentDaemonCleanup(t *testing.T) {
	runner := &versionRunner{binary: "3.2a"}
	if err := rawVersionServer(t, runner).Kill(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.binaryProbes != 0 || runner.effects != 0 || runner.cleanupCommands != 0 {
		t.Fatalf("binary probes=%d effects=%d cleanup commands=%d", runner.binaryProbes, runner.effects, runner.cleanupCommands)
	}
}

func TestCoreVersionCheckRejectsMalformedProbeWithoutEffects(t *testing.T) {
	runner := &versionRunner{malformed: true, binary: "3.8"}
	_, err := rawVersionServer(t, runner).Cmd(t.Context(), "set-buffer", "secret")
	if err == nil || runner.effects != 0 || runner.binaryProbes != 0 {
		t.Fatalf("error=%v effects=%d binary probes=%d", err, runner.effects, runner.binaryProbes)
	}
}

func TestCoreVersionCheckCanceledContextDoesNotProbe(t *testing.T) {
	runner := &versionRunner{daemon: "3.3a"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := rawVersionServer(t, runner).Cmd(ctx, "set-buffer", "secret")
	if !errors.Is(err, context.Canceled) || runner.requests != 0 {
		t.Fatalf("error=%v requests=%d", err, runner.requests)
	}
}

// The identity probe is run-shell -C, which carries no after-hook, and -N so a
// vanished daemon is never restarted by the probing client.
func TestCoreVersionCheckProbeIsHookFreeAndNeverStartsADaemon(t *testing.T) {
	t.Parallel()

	runner := &probeRecorder{}
	server := rawVersionServer(t, runner)
	// The absent daemon then asks for the executable's version, which this
	// recorder refuses; only the first command matters here.
	_, _ = server.Cmd(t.Context(), "list-sessions")
	if len(runner.requests) == 0 {
		t.Fatal("no command reached the executor")
	}
	probe := runner.requests[0]
	index := slices.Index(probe, "run-shell")
	if index < 0 || !slices.Contains(probe[:index], "-N") ||
		len(probe) != index+3 || probe[index+1] != "-C" ||
		!strings.HasPrefix(probe[index+2], identityProbeMarkerPrefix) {
		t.Fatalf("first command = %#v, want -N run-shell -C <marker>...", probe)
	}
	for _, argument := range probe {
		if argument == "display-message" {
			t.Fatalf("identity probe %#v uses display-message, which fires user hooks", probe)
		}
	}
}

type probeRecorder struct{ requests [][]string }

func (r *probeRecorder) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.requests = append(r.requests, slices.Clone(request.Arguments))
	return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/absent"}}, nil
}

func rawVersionServer(t *testing.T, runner commandRunner) Server {
	t.Helper()
	server, err := NewServer(ServerOptions{Binary: "false"})
	if err != nil {
		t.Fatal(err)
	}
	server.state.executor = runner
	return server
}

type versionRunner struct {
	daemon, binary  string
	startupDaemon   string
	replaced        bool
	malformed       bool
	probeResult     *tmuxcmd.Result
	probeErr        error
	requests        int
	binaryProbes    int
	guarded         int
	effects         int
	cleanupCommands int
}

func (r *versionRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.requests++
	arguments := exactArgvSubcommand(request.Arguments)
	if slices.Equal(arguments, []string{"kill-server"}) {
		r.cleanupCommands++
	}
	if slices.Equal(arguments, []string{"-h"}) {
		return tmuxcmd.Result{Stdout: []string{"usage: tmux"}}, nil
	}
	if slices.Equal(arguments, []string{"-V"}) {
		r.binaryProbes++
		return tmuxcmd.Result{Stdout: []string{"tmux " + r.binary}}, nil
	}
	if slices.Equal(arguments, identityProbeArgumentsFor(coreVersionIdentityFields())) {
		if r.probeErr != nil {
			return tmuxcmd.Result{ExitCode: -1}, r.probeErr
		}
		if r.probeResult != nil {
			return *r.probeResult, nil
		}
		if r.malformed {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"unknown command: malformed"}}, nil
		}
		if r.daemon == "" {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/absent"}}, nil
		}
		return identityProbeResult(coreVersionIdentityFields(), map[string]string{
			"version": r.daemon, "pid": "123", "start_time": "456", "socket_path": "/tmp/version.sock",
		}), nil
	}
	if len(arguments) >= 7 && arguments[0] == "if-shell" && arguments[1] == "-F" {
		r.guarded++
		if r.replaced || (r.startupDaemon != "" &&
			arguments[2] != "#{==:#{version},"+r.startupDaemon+"}") {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"unknown command: " + arguments[4]}}, nil
		}
	}
	if len(arguments) == 1 && (arguments[0] == "kill-server" || arguments[0] == "list-sessions") && r.daemon == "" {
		return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/absent"}}, nil
	}
	r.effects++
	return tmuxcmd.Result{Stdout: []string{"$1"}}, nil
}

// generationRunner is a daemon whose generation a test can advance or end.
type generationRunner struct {
	mu        sync.Mutex
	pid       int
	gone      bool
	probes    int
	commands  int
	refusedBy int
}

func (r *generationRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	arguments := exactArgvSubcommand(request.Arguments)
	switch {
	case slices.Equal(arguments, []string{"-V"}):
		return tmuxcmd.Result{Stdout: []string{"tmux 3.7"}}, nil
	case slices.Equal(arguments, identityProbeArgumentsFor(coreVersionIdentityFields())):
		r.probes++
		if r.gone {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/gen"}}, nil
		}
		return identityProbeResult(coreVersionIdentityFields(), map[string]string{
			"version": "3.7", "pid": strconv.Itoa(r.pid), "start_time": "1", "socket_path": "/tmp/gen",
		}), nil
	case len(arguments) >= 7 && arguments[0] == "if-shell":
		if r.gone {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/gen"}}, nil
		}
		if !strings.Contains(arguments[2], "#{==:#{pid},"+strconv.Itoa(r.pid)+"}") {
			r.refusedBy++
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"unknown command: " + arguments[4]}}, nil
		}
		r.commands++
		return tmuxcmd.Result{Stdout: []string{"ok"}}, nil
	}
	r.commands++
	return tmuxcmd.Result{Stdout: []string{"ok"}}, nil
}

func TestVersionCheckIsCachedPerEndpointAndTheFenceInvalidatesIt(t *testing.T) {
	t.Parallel()

	runner := &generationRunner{pid: 100}
	server := rawVersionServer(t, runner)
	ctx := t.Context()
	for range 3 {
		if result, err := server.Cmd(ctx, "list-sessions"); err != nil || result.ExitCode != 0 {
			t.Fatalf("Cmd() = (%+v, %v)", result, err)
		}
	}
	if runner.probes != 1 || runner.commands != 3 {
		t.Fatalf("probes = %d commands = %d, want one probe for three fenced commands",
			runner.probes, runner.commands)
	}

	// A replacement daemon refuses the cached fence before any effect; the
	// handle probes again and the command runs once on the new generation.
	runner.mu.Lock()
	runner.pid = 200
	runner.mu.Unlock()
	if result, err := server.Cmd(ctx, "list-sessions"); err != nil || result.ExitCode != 0 {
		t.Fatalf("Cmd() after replacement = (%+v, %v)", result, err)
	}
	if runner.refusedBy != 1 || runner.probes != 2 || runner.commands != 4 {
		t.Fatalf("refused = %d probes = %d commands = %d, want one refusal, one new probe, one effect",
			runner.refusedBy, runner.probes, runner.commands)
	}

	// A vanished daemon answers the -N client with "no server running"; the
	// record is dropped, so the next probe sees it absent instead of looping.
	runner.mu.Lock()
	runner.gone = true
	runner.mu.Unlock()
	result, _ := server.Cmd(ctx, "list-sessions")
	if result.ExitCode == 0 && len(result.Stdout) != 0 {
		t.Fatalf("Cmd() against a vanished daemon succeeded: %+v", result)
	}
	if runner.probes != 3 {
		t.Fatalf("probes = %d, want the stale record dropped and one fresh probe", runner.probes)
	}
}

// A handoff hands the terminal to a client, so a daemon that vanished after
// the check must not be restarted by it: every attach and switch carries -N,
// and the attach one is the only request that streams.
func TestCoreVersionCheckHandoffCommandsCarryNoStartServer(t *testing.T) {
	for name, call := range map[string]func(Session) error{
		"attach-session": func(session Session) error {
			return session.Attach(t.Context(), AttachSessionOptions{})
		},
		"switch-client": func(session Session) error { return session.SwitchClient(t.Context()) },
		"plan switch-client": func(session Session) error {
			var plan Plan
			plan.SwitchClient(session.Ref(), "/dev/pts/1")
			_, err := plan.Run(t.Context(), session.server)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &handoffRecorder{}
			session := Session{server: rawVersionServer(t, recorder), sessionID: "$1"}
			if err := call(session); err != nil {
				t.Fatal(err)
			}
			subcommand := strings.Split(name, " ")[len(strings.Split(name, " "))-1]
			var handoffs int
			for _, request := range recorder.requests {
				index := slices.Index(request.Arguments, subcommand)
				if index < 0 {
					continue
				}
				handoffs++
				if !slices.Contains(request.Arguments[:index], "-N") {
					t.Fatalf("%s argv = %#v, want -N before the command", subcommand, request.Arguments)
				}
			}
			if handoffs != 1 {
				t.Fatalf("%s requests = %d in %#v, want one", subcommand, handoffs, recorder.requests)
			}
		})
	}
}

type handoffRecorder struct{ requests []tmuxcmd.Request }

func (r *handoffRecorder) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	arguments := exactArgvSubcommand(request.Arguments)
	switch {
	case slices.Equal(arguments, []string{"-V"}):
		return tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}, nil
	case slices.Equal(arguments, identityProbeArgumentsFor(coreVersionIdentityFields())):
		return identityProbeResult(coreVersionIdentityFields(), map[string]string{
			"version": "3.8", "pid": "1", "start_time": "2", "socket_path": "/tmp/handoff.sock",
		}), nil
	}
	r.requests = append(r.requests, request)
	return tmuxcmd.Result{}, nil
}

func TestNeverReachedServerNamesOnlyAnswersThatRanNothing(t *testing.T) {
	for _, stderr := range [][]string{
		{"no server running on /tmp/s"},
		{"server version is too old for client"},
		{"error connecting to /tmp/s (No such file or directory)"},
	} {
		if !neverReachedServer(stderr) {
			t.Fatalf("neverReachedServer(%q) = false, want true", stderr)
		}
	}
	for _, stderr := range [][]string{
		{"server exited unexpectedly"},
		{"error connecting to /tmp/s (Permission denied)"},
		{"can't find session: x"},
	} {
		if neverReachedServer(stderr) {
			t.Fatalf("neverReachedServer(%q) = true, want false", stderr)
		}
	}
}

// A snapshot of a daemon below the floor, or one that names no release, is the
// version refusal with its remedy, worded like every other floor refusal.
func TestSnapshotRefusesAnUnsupportedDaemonWithTheRemedy(t *testing.T) {
	for _, daemon := range []string{"3.2a", "master"} {
		t.Run(daemon, func(t *testing.T) {
			runner := &snapshotProbeRunner{daemon: daemon}
			_, err := rawVersionServer(t, runner).Snapshot(t.Context())
			var refused *VersionTooLowError
			if !errors.As(err, &refused) || !errors.Is(err, ErrVersionTooLow) {
				t.Fatalf("Snapshot() error = %#v, want the version refusal", err)
			}
			want := "installed " + daemon
			if daemon == "master" {
				want = "unrecognized"
			}
			if !strings.Contains(err.Error(), want) ||
				!strings.Contains(err.Error(), "upgrade tmux and restart its server") {
				t.Fatalf("Snapshot() error = %q, want %q and the remedy", err, want)
			}
		})
	}
}

type snapshotProbeRunner struct{ daemon string }

func (r *snapshotProbeRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	if slices.Equal(exactArgvSubcommand(request.Arguments), identityProbeArgumentsFor(snapshotIdentityFields())) {
		return identityProbeResult(snapshotIdentityFields(), map[string]string{
			"version": r.daemon, "pid": "123", "start_time": "456", "socket_path": "/tmp/snapshot.sock",
		}), nil
	}
	return tmuxcmd.Result{}, errors.New("the refusal must precede every listing")
}
