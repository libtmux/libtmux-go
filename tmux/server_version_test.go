package tmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.common.get_version
// libtmux:parity libtmux.common.get_version#parameter-branch:tmux_bin:f9311ed1c2fc
// libtmux:parity libtmux.common.get_version_str
func TestServerVersionCachesSuccessfulProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0},
	}}}
	server := Server{state: &serverState{
		shared:  &serverShared{},
		options: ServerOptions{SocketPath: "/ignored/socket", ConfigFile: "/ignored/config"},
		runner:  runner,
	}}

	for range 2 {
		version, err := server.Version(context.Background())
		if err != nil {
			t.Fatalf("Version() error = %v", err)
		}
		if got := version.String(); got != "3.7b" {
			t.Fatalf("Version().String() = %q, want 3.7b", got)
		}
	}
	if runner.callCount() != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.callCount())
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, []string{"-V"}) {
		t.Fatalf("version probe requests = %#v, want binary-only -V", requests)
	}
}

func TestServerVersionDoesNotCacheFailure(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{err: context.DeadlineExceeded},
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7a"}, ExitCode: 0}},
	}}
	server := serverWithRunner(runner)

	if _, err := server.Version(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Version() error = %v, want context deadline", err)
	}
	version, err := server.Version(context.Background())
	if err != nil || version.String() != "3.7a" {
		t.Fatalf("second Version() = (%q, %v), want (3.7a, nil)", version, err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("version probe calls = %d, want 2", runner.callCount())
	}
}

func TestServerRefreshVersionInvalidatesCachedValueOnFailure(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7a"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"probe failed"}, ExitCode: 1}},
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
	}}
	server := serverWithRunner(runner)

	if _, err := server.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RefreshVersion(context.Background()); !errors.Is(err, ErrVersionQuery) {
		t.Fatalf("RefreshVersion() error = %v, want ErrVersionQuery", err)
	}
	version, err := server.Version(context.Background())
	if err != nil || version.String() != "3.7b" {
		t.Fatalf("Version() after failed refresh = (%q, %v), want (3.7b, nil)", version, err)
	}
	if runner.callCount() != 3 {
		t.Fatalf("version probe calls = %d, want 3", runner.callCount())
	}
}

func TestServerVersionWaiterHonorsContext(t *testing.T) {
	t.Parallel()

	runner := &blockingVersionRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	server := serverWithRunner(runner)
	firstDone := make(chan error, 1)
	go func() {
		_, err := server.Version(context.Background())
		firstDone <- err
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first version probe did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := server.Version(ctx); !errors.Is(err, context.DeadlineExceeded) {
		close(runner.release)
		t.Fatalf("waiting Version() error = %v, want context deadline", err)
	}
	close(runner.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Version() error = %v", err)
	}
	if calls := runner.calls.Load(); calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", calls)
	}
}

func TestServerVersionWaiterRetriesAfterOwnerCancellation(t *testing.T) {
	t.Parallel()

	runner := &cancelThenSucceedVersionRunner{started: make(chan struct{})}
	server := serverWithRunner(runner)
	ownerContext, cancelOwner := context.WithCancel(context.Background())
	ownerDone := make(chan error, 1)
	go func() {
		_, err := server.Version(ownerContext)
		ownerDone <- err
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("version owner did not start")
	}
	waiterContext := t.Context()
	observedWaiterContext := &doneObservedContext{
		Context:  waiterContext,
		observed: make(chan struct{}),
	}
	waiterDone := make(chan versionResponse, 1)
	go func() {
		version, err := server.Version(observedWaiterContext)
		waiterDone <- versionResponse{version: version, err: err}
	}()
	select {
	case <-observedWaiterContext.observed:
	case <-time.After(time.Second):
		t.Fatal("version waiter did not observe the in-flight probe")
	}
	cancelOwner()

	if err := <-ownerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner Version() error = %v, want context canceled", err)
	}
	response := <-waiterDone
	if response.err != nil || response.version.String() != "3.7b" {
		t.Fatalf("waiter Version() = (%q, %v), want (3.7b, nil)", response.version, response.err)
	}
	if calls := runner.calls.Load(); calls != 2 {
		t.Fatalf("version probe calls = %d, want 2", calls)
	}
}

func TestParseQueriedVersionUsesExactOpenBSDFallback(t *testing.T) {
	t.Parallel()

	result := CommandResult{
		Stderr:   []string{"tmux: unknown option -- V"},
		ExitCode: 1,
	}
	version, err := parseQueriedVersion(result, "openbsd")
	if err != nil {
		t.Fatalf("parseQueriedVersion() error = %v", err)
	}
	if got, want := version.String(), "openbsd"; got != want {
		t.Fatalf("parseQueriedVersion().String() = %q, want %q", got, want)
	}
	if version.Major() != 0 || version.Minor() != 0 || version.Patch() != 0 {
		t.Fatalf("unprobed OpenBSD fallback has feature level %#v", version)
	}

	result.Stderr[0] += " "
	if _, err := parseQueriedVersion(result, "openbsd"); !errors.Is(err, ErrVersionQuery) {
		t.Fatalf("non-exact OpenBSD fallback error = %v, want ErrVersionQuery", err)
	}
}

func TestParseQueriedVersionAcceptsModernOpenBSDToken(t *testing.T) {
	t.Parallel()

	result := CommandResult{Stdout: []string{"tmux openbsd-7.8"}, ExitCode: 0}
	version, err := parseQueriedVersion(result, "openbsd")
	if err != nil {
		t.Fatalf("parseQueriedVersion() error = %v", err)
	}
	if got := version.String(); got != "openbsd-7.8" {
		t.Fatalf("parseQueriedVersion().String() = %q, want openbsd-7.8", got)
	}
	if version.Major() != 0 || version.Minor() != 0 || version.Patch() != 0 {
		t.Fatalf("unprobed OpenBSD token has feature level %#v", version)
	}
}

func TestOpenBSDCapabilityVersionUsesContiguousCommandSupport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "no minimum capability"},
		{
			name:  "3.2a display popup",
			lines: []string{"display-popup (popup) [-CE]"},
			want:  "3.2a",
		},
		{
			name: "3.3 background confirmation",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-by] command",
			},
			want: "3.3",
		},
		{
			name: "3.4 confirmation key",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-by] [-c confirm-key] command",
			},
			want: "3.4",
		},
		{
			name: "3.5 copy mode clear selection",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-by] [-c confirm-key] command",
				"copy-mode [-deHMqSu] [-t target-pane]",
			},
			want: "3.5",
		},
		{
			name: "3.6 capture pane mouse selection",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-by] [-c confirm-key] command",
				"copy-mode [-deHMqSu] [-t target-pane]",
				"capture-pane (capturep) [-aCeFHJLMNpPqT]",
			},
			want: "3.6",
		},
		{
			name: "3.7 formatted key listing",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-by] [-c confirm-key] command",
				"copy-mode [-deHMqSu] [-t target-pane]",
				"capture-pane (capturep) [-aCeFHJLMNpPqT]",
				"list-keys (lsk) [-1aNr] [-F format]",
			},
			want: "3.7",
		},
		{
			name: "later feature does not bridge gap",
			lines: []string{
				"display-popup (popup) [-CE]",
				"confirm-before (confirm) [-cy] command",
				"list-keys (lsk) [-F format]",
			},
			want: "3.2a",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			version := openBSDCapabilityVersion("openbsd-7.8", test.lines)
			if version.String() != "openbsd-7.8" {
				t.Fatalf("String() = %q, want openbsd-7.8", version)
			}
			if test.want == "" {
				if version.Major() != 0 || version.Minor() != 0 || version.Patch() != 0 {
					t.Fatalf("capability version = %#v, want zero feature level", version)
				}
				return
			}
			want := mustParseVersion(t, test.want)
			if version.Compare(want) != 0 {
				t.Fatalf("capability version = %#v, want %s feature level", version, want)
			}
		})
	}
}

func TestServerVersionProbesOpenBSDCapabilities(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux openbsd-7.8"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{
			"display-popup (popup) [-CE]",
			"confirm-before (confirm) [-by] [-c confirm-key] command",
			"copy-mode [-deHMqSu] [-t target-pane]",
		}, ExitCode: 0}},
	}}
	server := Server{state: &serverState{
		shared: &serverShared{},
		options: ServerOptions{
			Binary:             "configured-tmux",
			SocketPath:         "/ignored/socket",
			ConfigFile:         "/ignored/config",
			ProcessEnvironment: []string{"TEST_ENV=value"},
		},
		runner: runner,
	}}

	version, err := server.queryVersionForOS(context.Background(), "openbsd")
	if err != nil {
		t.Fatalf("queryVersionForOS() error = %v", err)
	}
	want := mustParseVersion(t, "3.5")
	if version.String() != "openbsd-7.8" || version.Compare(want) != 0 {
		t.Fatalf("queryVersionForOS() = %#v, want raw openbsd-7.8 at %s", version, want)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("version probe requests = %d, want 2", len(requests))
	}
	if requests[0].Binary != "configured-tmux" ||
		!slices.Equal(requests[0].Arguments, []string{"-V"}) {
		t.Fatalf("version token request = %#v", requests[0])
	}
	capabilityRequest := requests[1]
	if capabilityRequest.Binary != "configured-tmux" ||
		!slices.Equal(capabilityRequest.Environment, []string{"TEST_ENV=value"}) ||
		len(capabilityRequest.Arguments) != 5 ||
		capabilityRequest.Arguments[0] != "-f/dev/null" ||
		!strings.HasPrefix(capabilityRequest.Arguments[1], "-Llibtmux-capability-") ||
		!slices.Equal(capabilityRequest.Arguments[2:], []string{"start-server", ";", "list-commands"}) {
		t.Fatalf("capability request = %#v", capabilityRequest)
	}
}

func TestSnapshotIdentityUsesProbedOpenBSDCapabilities(t *testing.T) {
	t.Parallel()

	formatVersion := mustParseVersion(t, "3.7")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(
				snapshotIdentityFields(),
				snapshotRowValues(formatVersion, map[string]string{
					"version": "openbsd-7.8",
				}),
			),
			ExitCode: 0,
		}},
		{result: tmuxcmd.Result{Stdout: []string{"tmux openbsd-7.8"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{
			"display-popup (popup) [-CE]",
			"confirm-before (confirm) [-by] [-c confirm-key] command",
			"copy-mode [-deHMqSu] [-t target-pane]",
		}, ExitCode: 0}},
	}}
	server := serverWithRunner(runner)

	identity, err := server.probeSnapshotIdentity(context.Background())
	if err != nil {
		t.Fatalf("probeSnapshotIdentity() = (%#v, %v)", identity, err)
	}
	want := mustParseVersion(t, "3.5")
	if identity.version.String() != "openbsd-7.8" || identity.version.Compare(want) != 0 {
		t.Fatalf("identity version = %#v, want raw openbsd-7.8 at %s", identity.version, want)
	}
	if calls := runner.callCount(); calls != 3 {
		t.Fatalf("identity and version probe calls = %d, want 3", calls)
	}
}

// libtmux:parity libtmux.common.TMUX_MIN_VERSION
// libtmux:parity libtmux.common.has_minimum_version
// libtmux:parity libtmux.common.has_minimum_version#parameter-branch:raises:3ae9164dc384
// libtmux:parity libtmux.common.has_minimum_version#version-branch:tmux_bin:aecdd0e3d066
// libtmux:parity libtmux.exc.VersionTooLow
func TestServerRequireVersionReturnsConcreteTooLowError(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.1"}, ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	minimum := mustParseVersion(t, MinimumSupportedVersion)

	err := server.RequireVersion(context.Background(), minimum)
	if !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("RequireVersion() error = %v, want ErrVersionTooLow", err)
	}
	var tooLow *VersionTooLowError
	if !errors.As(err, &tooLow) || tooLow.Current.String() != "3.1" || tooLow.Minimum != minimum {
		t.Fatalf("RequireVersion() error = %#v, want concrete current and minimum", err)
	}
}

func TestVersionQueryErrorRetainsOnlyExitCode(t *testing.T) {
	t.Parallel()

	const secret = "private-version-output"
	source := tmuxcmd.Result{
		Command:  []string{"tmux", "-V", secret},
		Stdout:   []string{secret},
		Stderr:   []string{secret},
		ExitCode: 1,
	}
	runner := &versionQueueRunner{responses: []versionResponse{{result: source}}}
	server := serverWithRunner(runner)

	_, err := server.Version(context.Background())
	if !errors.Is(err, ErrVersionQuery) {
		t.Fatalf("Version() error = %v, want ErrVersionQuery", err)
	}
	var queryError *VersionQueryError
	if !errors.As(err, &queryError) || queryError.Result.ExitCode != 1 ||
		queryError.Result.Command != nil || queryError.Result.Stdout != nil ||
		queryError.Result.Stderr != nil {
		t.Fatalf("Version() error = %#v, want VersionQueryError with result", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("VersionQueryError.Error() retained private output: %q", err)
	}
	encoded, marshalErr := json.Marshal(queryError)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("VersionQueryError JSON retained private output: %s", encoded)
	}
}

func TestVersionQueryErrorRedactsMalformedOutput(t *testing.T) {
	t.Parallel()

	const secret = "private-malformed-version"
	_, err := parseQueriedVersion(CommandResult{
		Command: []string{"tmux", "-V", secret},
		Stdout:  []string{secret},
	}, "linux")
	var queryError *VersionQueryError
	if !errors.As(err, &queryError) {
		t.Fatalf("parseQueriedVersion() error = %#v, want VersionQueryError", err)
	}
	if queryError.Result.Command != nil || queryError.Result.Stdout != nil ||
		queryError.Result.Stderr != nil || strings.Contains(queryError.Reason, secret) ||
		strings.Contains(queryError.Error(), secret) {
		t.Fatalf("VersionQueryError retained private output: %#v", queryError)
	}
	encoded, marshalErr := json.Marshal(queryError)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("VersionQueryError JSON retained private output: %s", encoded)
	}
}

func TestVersionQueryErrorRedactsPrefixedMalformedToken(t *testing.T) {
	t.Parallel()

	const secret = "private-prefixed-version"
	tests := []struct {
		name   string
		invoke func() error
	}{
		{
			name: "parser",
			invoke: func() error {
				_, err := parseQueriedVersion(CommandResult{
					Command: []string{"tmux", "-V", secret},
					Stdout:  []string{"tmux " + secret},
				}, "linux")
				return err
			},
		},
		{
			name: "server",
			invoke: func() error {
				runner := &versionQueueRunner{responses: []versionResponse{{
					result: tmuxcmd.Result{
						Command: []string{"tmux", "-V", secret},
						Stdout:  []string{"tmux " + secret},
					},
				}}}
				_, err := serverWithRunner(runner).Version(context.Background())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.invoke()
			if !errors.Is(err, ErrVersionQuery) || errors.Is(err, ErrInvalidVersion) {
				t.Fatalf("version query error = %v, want only ErrVersionQuery", err)
			}
			var queryError *VersionQueryError
			if !errors.As(err, &queryError) {
				t.Fatalf("version query error = %#v, want *VersionQueryError", err)
			}
			if queryError.Result.Command != nil || queryError.Result.Stdout != nil ||
				queryError.Result.Stderr != nil || strings.Contains(queryError.Reason, secret) {
				t.Fatalf("VersionQueryError retained malformed probe data: %#v", queryError)
			}
			assertErrorGraphRedacts(t, err, secret)
		})
	}
}

func assertErrorGraphRedacts(t *testing.T, root error, secret string) {
	t.Helper()

	stack := []error{root}
	for len(stack) != 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, representation := range []string{current.Error(), fmt.Sprintf("%#v", current)} {
			if strings.Contains(representation, secret) {
				t.Fatalf("error graph retained secret in %q", representation)
			}
		}
		encoded, err := json.Marshal(current)
		if err != nil {
			t.Fatalf("marshal error graph node: %v", err)
		}
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("error graph retained secret in JSON: %s", encoded)
		}
		if multiple, ok := current.(interface{ Unwrap() []error }); ok {
			stack = append(stack, multiple.Unwrap()...)
			continue
		}
		if next := errors.Unwrap(current); next != nil {
			stack = append(stack, next)
		}
	}
}

type versionResponse struct {
	result  tmuxcmd.Result
	version Version
	err     error
}

type versionQueueRunner struct {
	mu        sync.Mutex
	responses []versionResponse
	requests  []tmuxcmd.Request
}

func (r *versionQueueRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		return tmuxcmd.Result{}, errors.New("unexpected version probe")
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response.result, response.err
}

func (r *versionQueueRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *versionQueueRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

type blockingVersionRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

type cancelThenSucceedVersionRunner struct {
	started chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

type doneObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *doneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func (r *cancelThenSucceedVersionRunner) Run(ctx context.Context, _ tmuxcmd.Request) (tmuxcmd.Result, error) {
	if r.calls.Add(1) == 1 {
		r.once.Do(func() { close(r.started) })
		<-ctx.Done()
		return tmuxcmd.Result{ExitCode: -1}, ctx.Err()
	}
	return tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}, nil
}

func (r *blockingVersionRunner) Run(context.Context, tmuxcmd.Request) (tmuxcmd.Result, error) {
	r.calls.Add(1)
	r.once.Do(func() { close(r.started) })
	<-r.release
	return tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}, nil
}

func serverWithRunner(runner commandRunner) Server {
	return Server{state: &serverState{shared: &serverShared{}, runner: runner}}
}

// degradingServerWithRunner is serverWithRunner with the policy that omits a
// capability the running tmux lacks instead of refusing the request. It is what
// the tests covering that path use; the default refuses, and
// TestUnsupportedFeaturesAreRefusedByDefault covers that.
func degradingServerWithRunner(runner commandRunner) Server {
	return Server{state: &serverState{
		shared:  &serverShared{},
		runner:  runner,
		options: ServerOptions{Unsupported: DegradeUnsupported},
	}}
}
