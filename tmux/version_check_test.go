package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
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
		{"3.3a-master", true},
		{"master", false},
		{"3.4garbage", false},
		{"9.9alpha", false},
		{"3.7a-unknown", false},
		{"next-3.9nonsense", false},
		{"next-3.3a-master", false},
		{"3.3a-dev", true},
		{"3.3b-rc2", true},
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

func rawAdmissionServer(t *testing.T, runner commandRunner) Server {
	t.Helper()
	server, err := NewServer(ServerOptions{Binary: "/bin/false"})
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
	if len(arguments) == 3 && arguments[0] == "display-message" && arguments[1] == "-p" &&
		strings.Contains(arguments[2], "#{q:version}") {
		if r.malformed {
			return tmuxcmd.Result{RawStdout: []byte("malformed")}, nil
		}
		if r.daemon == "" {
			return tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/absent"}}, nil
		}
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(coreAdmissionIdentityFields(), map[string]string{
			"version": r.daemon, "pid": "123", "start_time": "456", "socket_path": "/tmp/admission.sock",
		})}, nil
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
