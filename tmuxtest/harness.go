// Package tmuxtest provides isolated tmux servers and supporting processes for
// tests that exercise a real tmux daemon.
//
// # Setup and lifecycle
//
// Call [Main] exactly once from the package's TestMain before calling
// [NewServer] or [NewServerWithOptions]. Main creates short temporary roots,
// temporarily redirects TMPDIR and GOTMPDIR, and cleans every registered server
// after the test package returns.
//
//	func TestMain(m *testing.M) {
//		os.Exit(tmuxtest.Main(m))
//	}
//
//
//	func TestWindow(t *testing.T) {
//		ctx := context.Background()
//		server := tmuxtest.NewServer(ctx, t)
//		session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{})
//		window := tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
//		_ = window
//	}
//
// The helpers register testing cleanup; setup failures call [testing.TB.Fatal].
//
// # Isolation and compatibility
//
// Each server has an explicit short socket path, a harness-owned configuration
// file, and a process environment with tmux targeting variables removed. tmux
// 3.2a or newer must be available. The server harness supports its Unix targets;
// [StartPTYProcess] additionally requires Linux. Cleanup is owned by the
// creating test, bounded, and retried during the package lifecycle if needed.
// Resource cleanup targets stable tmux IDs, so a renamed session or moved window
// remains owned by its creating test.
//
// # Context and failures
//
// [NewServer] and [NewControlMode] contexts bound startup only; they do not own
// an already-returned [ControlMode]'s lifetime. [StartPTYProcess] is the
// exception because its start context owns the child process. [NewSession] and
// [NewWindow] use their caller-owned contexts directly for resource creation;
// their cleanup uses fresh bounded contexts. Inputs to [ServerOptions],
// [NewSession], and [NewWindow] are copied before use. Nil and empty inputs can
// differ: a nil server environment inherits a snapshot, an empty environment
// stays empty, and a nil window name requests a generated name while an empty
// non-nil name is explicit. A failed test reports one package-relative go test
// command for its top-level test and whether every harness-owned socket was
// cleaned. The diagnostic excludes commands, command output, paths, options,
// and environment values.
//
// # Control clients and subprocesses
//
// [NewControlMode] attaches a tmux control client with one output reader and a
// disk-backed spool. [StartPTYProcess] starts a child with a private controlling
// terminal and a memory-backed transcript. Call [ControlMode.Close] or
// [PTYProcess.Close] to release these owned resources.
//
// # Concurrency
//
// [WaitFor] calls its condition serially. [ControlMode.Read] has one reader,
// and [PTYProcess.Write] serializes writers. Close operations on [ControlMode]
// and [PTYProcess] are safe to call concurrently and more than once.
package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tmux "github.com/libtmux/libtmux-go"
)

// ServerOptions configures a harness-owned [tmux.Server]. All inputs are copied
// before filesystem or process operations, so callers retain ownership.
type ServerOptions struct {
	// Binary is the tmux executable. An empty value resolves tmux from PATH.
	Binary string
	// Config is appended to the mandatory isolation configuration in the
	// harness-owned config file; it cannot replace that file.
	Config []byte
	// ProcessEnvironment supplies the server environment. Nil snapshots the
	// current process environment, while a non-nil empty slice remains empty.
	// Tmux targeting variables are removed in either case.
	ProcessEnvironment []string
	// InitialSession starts the daemon with this copied request. Nil returns a
	// lazy bare server; a later tmux command may start it.
	InitialSession *tmux.NewSessionRequest
}

const (
	harnessIsolationConfig = ""
	maxSocketPathBytes     = 103
	cleanupTimeout         = 3 * time.Second
	perTestCleanupTries    = 2
)

var (
	errInvalidHarnessState  = errors.New("tmuxtest: invalid harness state")
	errHarnessProcessExited = errors.New("tmuxtest: process exited")
	errHarnessOperation     = errors.New("tmuxtest: operation error")
)

type harnessOperationError struct {
	operation      string
	status         string
	classification error
}

func (e *harnessOperationError) Error() string {
	return fmt.Sprintf("tmuxtest: %s failed: %s", e.operation, e.status)
}

func (e *harnessOperationError) Unwrap() error { return e.classification }

func harnessFailure(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	status := "operation error"
	classification := errHarnessOperation
	switch {
	case errors.Is(cause, context.Canceled):
		status = "canceled"
		classification = context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		status = "deadline exceeded"
		classification = context.DeadlineExceeded
	case errors.Is(cause, exec.ErrNotFound):
		status = "not found"
		classification = exec.ErrNotFound
	case errors.Is(cause, os.ErrNotExist):
		status = "not found"
		classification = os.ErrNotExist
	case errors.Is(cause, os.ErrPermission):
		status = "permission denied"
		classification = os.ErrPermission
	case errors.Is(cause, tmux.ErrCommand):
		status = "command failed"
		classification = tmux.ErrCommand
	case errors.Is(cause, errInvalidHarnessState):
		status = "invalid state"
		classification = errInvalidHarnessState
	default:
		var exitError *exec.ExitError
		if errors.As(cause, &exitError) {
			status = "process exited"
			classification = errHarnessProcessExited
		}
	}
	return &harnessOperationError{
		operation:      operation,
		status:         status,
		classification: classification,
	}
}

type serverRecord struct {
	server        tmux.Server
	tempDir       string
	socketPath    string
	configFile    string
	pid           int
	daemonStopped bool
}

var suite = struct {
	sync.Mutex
	active  bool
	root    string
	records map[string]*serverRecord
}{records: make(map[string]*serverRecord)}

type failureGuidanceState struct {
	sync.Mutex
	allSocketsCleaned bool
}

var failureGuidance = struct {
	sync.Mutex
	states map[testing.TB]*failureGuidanceState
}{states: make(map[testing.TB]*failureGuidanceState)}

// Main runs the one-call package lifecycle for tmuxtest. It prepares short
// temporary paths, temporarily sets TMPDIR and GOTMPDIR, runs m, cleans
// registered servers, restores the environment, and returns the final status.
// TestMain should return its result through [os.Exit].
func Main(m *testing.M) int {
	if !platformSupported() {
		fmt.Fprintf(os.Stderr, "tmuxtest: real tmux tests are unsupported on %s\n", runtime.GOOS)
		return 2
	}
	return runSuite(m.Run)
}

func runSuite(run func() int) int {
	root, err := os.MkdirTemp(shortTempBase(), "ltg-")
	if err != nil {
		fmt.Fprintln(os.Stderr, harnessFailure("create short temporary root", err))
		return 2
	}
	restoreTMPDIR, err := replaceEnvironment("TMPDIR", root)
	if err != nil {
		fmt.Fprintln(os.Stderr, harnessFailure("set TMPDIR", err))
		_ = os.RemoveAll(root)
		return 2
	}
	restoreGOTMPDIR, err := replaceEnvironment("GOTMPDIR", root)
	if err != nil {
		fmt.Fprintln(os.Stderr, harnessFailure("set GOTMPDIR", err))
		_ = restoreTMPDIR()
		_ = os.RemoveAll(root)
		return 2
	}

	suite.Lock()
	if suite.active {
		suite.Unlock()
		fmt.Fprintln(os.Stderr, "tmuxtest: Main called while another suite is active")
		_ = restoreGOTMPDIR()
		_ = restoreTMPDIR()
		_ = os.RemoveAll(root)
		return 2
	}
	suite.active = true
	suite.root = root
	suite.records = make(map[string]*serverRecord)
	suite.Unlock()

	code := run()
	cleanupErr := cleanupRegisteredServers()
	if cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "tmuxtest: suite cleanup failed")
		if code == 0 {
			code = 1
		}
	}

	suite.Lock()
	suite.active = false
	suite.root = ""
	suite.records = make(map[string]*serverRecord)
	suite.Unlock()
	if err := restoreGOTMPDIR(); err != nil {
		fmt.Fprintln(os.Stderr, harnessFailure("restore GOTMPDIR", err))
		if code == 0 {
			code = 1
		}
	}
	if err := restoreTMPDIR(); err != nil {
		fmt.Fprintln(os.Stderr, harnessFailure("restore TMPDIR", err))
		if code == 0 {
			code = 1
		}
	}
	if cleanupErr == nil {
		if err := os.RemoveAll(root); err != nil {
			fmt.Fprintln(os.Stderr, harnessFailure("remove temporary root", err))
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}

// NewServer starts an isolated [tmux.Server] with a session named "work".
// Startup uses ctx and the harness timeout, whichever ends first. It registers
// bounded cleanup with t; setup failures call [testing.TB.Fatal]. The context
// does not govern later server cleanup or daemon lifetime.
func NewServer(ctx context.Context, t testing.TB) tmux.Server {
	t.Helper()
	initialSession := tmux.NewSessionRequest{Name: "work"}
	server := NewServerWithOptions(ctx, t, ServerOptions{InitialSession: &initialSession})
	return server
}

// NewServerWithOptions prepares an isolated harness-owned [tmux.Server] from
// options. Initial startup uses ctx and the harness timeout, whichever ends
// first. The daemon starts only when [ServerOptions.InitialSession] is non-nil
// or a later tmux command starts it. It registers bounded cleanup with t; setup
// failures call [testing.TB.Fatal]. The context does not govern later cleanup
// or daemon lifetime.
func NewServerWithOptions(
	ctx context.Context,
	t testing.TB,
	options ServerOptions,
) tmux.Server {
	t.Helper()
	server, _ := newServerWithOptions(ctx, t, captureServerOptions(options), true, true)
	return server
}

func newServer(
	ctx context.Context,
	t testing.TB,
	binary string,
	registerCleanup bool,
	exactConfig bool,
) (tmux.Server, *serverRecord) {
	t.Helper()
	initialSession := tmux.NewSessionRequest{Name: "work"}
	return newServerWithOptions(ctx, t, ServerOptions{
		Binary:         binary,
		InitialSession: &initialSession,
	}, registerCleanup, exactConfig)
}

func newServerWithOptions(
	ctx context.Context,
	t testing.TB,
	options ServerOptions,
	registerCleanup bool,
	exactConfig bool,
) (tmux.Server, *serverRecord) {
	t.Helper()
	var guidance *failureGuidanceState
	if registerCleanup {
		guidance = registerFailureGuidance(t)
	}

	binary := options.Binary
	if binary == "" {
		var err error
		binary, err = exec.LookPath("tmux")
		if err != nil {
			t.Fatal(harnessFailure("resolve tmux executable", err))
		}
		binary, err = filepath.Abs(binary)
		if err != nil {
			t.Fatal(harnessFailure("resolve tmux executable path", err))
		}
	}
	root, err := activeSuiteRoot()
	if err != nil {
		t.Fatal(err)
	}
	tempDir, err := os.MkdirTemp(root, "s-")
	if err != nil {
		t.Fatal(harnessFailure("create server directory", err))
	}
	socketPath := filepath.Join(tempDir, "s")
	configFile := ""
	if exactConfig {
		configFile = filepath.Join(tempDir, "tmux.conf")
		config := make([]byte, 0, len(harnessIsolationConfig)+len(options.Config))
		config = append(config, harnessIsolationConfig...)
		config = append(config, options.Config...)
		if err := os.WriteFile(configFile, config, 0o600); err != nil {
			_ = os.RemoveAll(tempDir)
			t.Fatal(harnessFailure("create isolated tmux config", err))
		}
	}
	if len([]byte(socketPath)) > maxSocketPathBytes {
		_ = os.RemoveAll(tempDir)
		t.Fatal(harnessFailure("validate socket path", errInvalidHarnessState))
	}

	processEnvironment := options.ProcessEnvironment
	if processEnvironment == nil {
		processEnvironment = os.Environ()
	}
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:             binary,
		SocketPath:         socketPath,
		ConfigFile:         configFile,
		ProcessEnvironment: scrubTmuxEnvironment(processEnvironment),
	})
	record := &serverRecord{
		server:     server,
		tempDir:    tempDir,
		socketPath: socketPath,
		configFile: configFile,
	}
	registerServer(record)
	if registerCleanup {
		t.Cleanup(func() {
			err := cleanupWithRetries(record, perTestCleanupTries)
			recordSocketCleanup(guidance, err == nil)
			if err != nil {
				t.Errorf(
					"tmuxtest: harness-owned socket cleanup failed after %d attempts",
					perTestCleanupTries,
				)
			}
		})
	}

	if options.InitialSession == nil {
		return server, record
	}
	startupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	_, err = server.NewSession(startupCtx, *options.InitialSession)
	if err != nil {
		t.Fatal(harnessFailure("create initial session", err))
	}
	pidResult := runCommand(startupCtx, server, "display-message", "-p", "#{pid}")
	if err := commandFailure("display-message", pidResult); err != nil {
		t.Fatal(err)
	}
	if len(pidResult.Stdout) != 1 {
		t.Fatal(harnessFailure("read server pid", errInvalidHarnessState))
	}
	pid, err := strconv.Atoi(pidResult.Stdout[0])
	if err != nil {
		t.Fatal(harnessFailure("parse server pid", err))
	}
	record.pid = pid
	return server, record
}

func registerFailureGuidance(t testing.TB) *failureGuidanceState {
	failureGuidance.Lock()
	defer failureGuidance.Unlock()
	if state, ok := failureGuidance.states[t]; ok {
		return state
	}
	state := &failureGuidanceState{allSocketsCleaned: true}
	failureGuidance.states[t] = state
	t.Cleanup(func() {
		failureGuidance.Lock()
		delete(failureGuidance.states, t)
		failureGuidance.Unlock()
		if !t.Failed() {
			return
		}
		state.Lock()
		cleaned := state.allSocketsCleaned
		state.Unlock()
		name := sanitizedTopLevelTestName(t.Name())
		t.Logf("tmuxtest: reproduce: go test -run '^%s$' .", name)
		if cleaned {
			t.Log("tmuxtest: harness-owned socket cleaned: yes")
			return
		}
		t.Log("tmuxtest: harness-owned socket cleaned: no")
	})
	return state
}

func recordSocketCleanup(state *failureGuidanceState, cleaned bool) {
	if cleaned {
		return
	}
	state.Lock()
	state.allSocketsCleaned = false
	state.Unlock()
}

func sanitizedTopLevelTestName(name string) string {
	name, _, _ = strings.Cut(name, "/")
	var sanitized strings.Builder
	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			sanitized.WriteRune(character)
		} else {
			sanitized.WriteByte('_')
		}
		if sanitized.Len() == 128 {
			break
		}
	}
	if sanitized.Len() == 0 {
		return "Test"
	}
	return sanitized.String()
}

func captureServerOptions(options ServerOptions) ServerOptions {
	options.Config = slices.Clone(options.Config)
	options.ProcessEnvironment = slices.Clone(options.ProcessEnvironment)
	if options.InitialSession != nil {
		initialSession := captureNewSessionRequest(*options.InitialSession)
		options.InitialSession = &initialSession
	}
	return options
}

func activeSuiteRoot() (string, error) {
	suite.Lock()
	defer suite.Unlock()
	if !suite.active {
		return "", errors.New("tmuxtest: NewServer requires tmuxtest.Main in TestMain")
	}
	return suite.root, nil
}

func runCommand(ctx context.Context, server tmux.Server, args ...string) tmux.CommandResult {
	result, err := server.Cmd(ctx, args...)
	if err != nil {
		return tmux.CommandResult{Command: result.Command, Stderr: []string{err.Error()}, ExitCode: -1}
	}
	return result
}

func runCleanupCommand(server tmux.Server, args ...string) tmux.CommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	return runCommand(ctx, server, args...)
}

func commandFailure(operation string, result tmux.CommandResult) error {
	if result.ExitCode == 0 {
		return nil
	}
	cause := tmux.ErrCommand
	if result.ExitCode == -1 && len(result.Stderr) == 1 {
		switch result.Stderr[0] {
		case context.Canceled.Error():
			cause = context.Canceled
		case context.DeadlineExceeded.Error():
			cause = context.DeadlineExceeded
		}
	}
	return harnessFailure(operation, cause)
}

func cleanupServer(record *serverRecord) error {
	if record.daemonStopped {
		return removeServerArtifacts(record)
	}
	currentPID, unavailable, err := probeServerPID(record)
	if err != nil {
		return err
	}
	if unavailable {
		record.daemonStopped = true
		return removeServerArtifacts(record)
	}
	record.pid = currentPID

	result := runCleanupCommand(record.server, "kill-server")
	if !waitForProcessDeath(currentPID, time.Now().Add(cleanupTimeout)) {
		return errors.Join(
			commandFailure("kill-server", result),
			harnessFailure("stop tmux server", errInvalidHarnessState),
		)
	}

	replacementPID, unavailable, err := probeServerPID(record)
	if err != nil {
		return errors.Join(commandFailure("kill-server", result), err)
	}
	if !unavailable {
		record.pid = replacementPID
		return harnessFailure("verify tmux server stopped", errInvalidHarnessState)
	}
	record.daemonStopped = true
	return removeServerArtifacts(record)
}

func probeServerPID(record *serverRecord) (pid int, unavailable bool, err error) {
	result := runCleanupCommand(record.server, "display-message", "-p", "#{pid}")
	if result.ExitCode != 0 {
		if serverUnavailable(result) && (record.pid <= 0 || !processAlive(record.pid)) {
			return 0, true, nil
		}
		if serverUnavailable(result) {
			return 0, false, harnessFailure(
				"probe tmux server",
				errInvalidHarnessState,
			)
		}
		return 0, false, commandFailure("display-message", result)
	}
	if len(result.Stderr) != 0 {
		return 0, false, harnessFailure("probe tmux server", tmux.ErrCommand)
	}
	if len(result.Stdout) != 1 {
		return 0, false, harnessFailure("read server pid", errInvalidHarnessState)
	}
	pid, err = strconv.Atoi(result.Stdout[0])
	if err != nil {
		return 0, false, harnessFailure("parse server pid", err)
	}
	if pid <= 0 {
		return 0, false, harnessFailure("validate server pid", errInvalidHarnessState)
	}
	return pid, false, nil
}

func serverUnavailable(result tmux.CommandResult) bool {
	if result.ExitCode == 0 || len(result.Stdout) != 0 {
		return false
	}
	for _, line := range result.Stderr {
		if strings.HasPrefix(line, "no server running on ") {
			return true
		}
		if strings.HasPrefix(line, "error connecting to ") &&
			(strings.Contains(line, "No such file or directory") ||
				strings.Contains(line, "Connection refused")) {
			return true
		}
	}
	return false
}

func removeServerArtifacts(record *serverRecord) error {
	var failures []error
	if err := os.Remove(record.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, harnessFailure("remove tmux socket", err))
	}
	if record.configFile != "" {
		if err := os.Remove(record.configFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, harnessFailure("remove tmux config", err))
		}
	}
	if record.tempDir != "" {
		if filepath.Dir(record.socketPath) != record.tempDir ||
			(record.configFile != "" && filepath.Dir(record.configFile) != record.tempDir) {
			failures = append(failures, harnessFailure("validate server directory", errInvalidHarnessState))
		} else if err := os.Remove(record.tempDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, harnessFailure("remove server directory", err))
		}
	}
	return errors.Join(failures...)
}

func cleanupAndUnregister(record *serverRecord) error {
	if err := cleanupServer(record); err != nil {
		return err
	}
	unregisterServer(record.socketPath)
	return nil
}

func cleanupWithRetries(record *serverRecord, attempts int) error {
	if attempts < 1 {
		return errors.New("tmuxtest: cleanup requires at least one attempt")
	}
	var failures []error
	for range attempts {
		if err := cleanupAndUnregister(record); err != nil {
			failures = append(failures, harnessFailure("cleanup tmux server", err))
			continue
		}
		return nil
	}
	return errors.Join(failures...)
}

func waitForProcessDeath(pid int, deadline time.Time) bool {
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return !processAlive(pid)
}

func registerServer(record *serverRecord) {
	suite.Lock()
	defer suite.Unlock()
	suite.records[record.socketPath] = record
}

func unregisterServer(socketPath string) {
	suite.Lock()
	defer suite.Unlock()
	delete(suite.records, socketPath)
}

func cleanupRegisteredServers() error {
	suite.Lock()
	records := make([]*serverRecord, 0, len(suite.records))
	for _, record := range suite.records {
		records = append(records, record)
	}
	suite.Unlock()

	var failures []error
	for _, record := range records {
		if err := cleanupAndUnregister(record); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func scrubTmuxEnvironment(environment []string) []string {
	cleaned := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR":
			continue
		default:
			cleaned = append(cleaned, entry)
		}
	}
	return cleaned
}

func replaceEnvironment(key, value string) (func() error, error) {
	previous, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		return nil, err
	}
	return func() error {
		if existed {
			return os.Setenv(key, previous)
		}
		return os.Unsetenv(key)
	}, nil
}
