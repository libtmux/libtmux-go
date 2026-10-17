package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Version-probe sentinels classify [VersionQueryError] and
// [VersionTooLowError] through errors.Is.
var (
	// ErrVersionQuery identifies a tmux version probe that produced no usable
	// version. It is matched by errors.Is for VersionQueryError.
	ErrVersionQuery = errors.New("tmux: version query failed")
	// ErrVersionTooLow identifies a tmux version below a required feature level.
	// It is matched by errors.Is for VersionTooLowError.
	ErrVersionTooLow = errors.New("tmux: version too low")
)

// VersionQueryError reports an unsuccessful tmux version probe. It matches
// [ErrVersionQuery] through errors.Is; callers can recover its fields with
// errors.As. Library-created errors retain only the exit code; command
// arguments and output are omitted, although callers may construct values with
// other contents.
type VersionQueryError struct {
	// Result contains the library-created error's exit code and no diagnostics.
	Result CommandResult
	// Reason describes a malformed successful probe result, when applicable.
	Reason string

	commandFailure bool
}

// Error implements error.
func (e *VersionQueryError) Error() string {
	if e.Reason == "" {
		return ErrVersionQuery.Error()
	}
	return fmt.Sprintf("%v: %s", ErrVersionQuery, e.Reason)
}

// Unwrap makes VersionQueryError compatible with ErrVersionQuery.
func (e *VersionQueryError) Unwrap() error {
	return ErrVersionQuery
}

func (e *VersionQueryError) failedCommand() bool {
	return e.commandFailure || e.Result.ExitCode != 0 || len(e.Result.Stderr) != 0
}

func newVersionQueryError(result CommandResult, reason string) *VersionQueryError {
	commandFailure := result.ExitCode != 0 || len(result.Stderr) != 0
	if reason == "" && result.ExitCode != 0 {
		reason = fmt.Sprintf("tmux -V exited %d", result.ExitCode)
	} else if reason == "" && len(result.Stderr) != 0 {
		reason = "tmux -V returned stderr"
	}
	return &VersionQueryError{
		Result:         CommandResult{ExitCode: result.ExitCode},
		Reason:         reason,
		commandFailure: commandFailure,
	}
}

// VersionTooLowError reports the installed and required tmux feature levels.
// It matches [ErrVersionTooLow] through errors.Is; callers can recover its
// fields with errors.As.
//
// A version that names no release, such as a bare "master" or a distributor's
// suffix, is refused the same way, and its message says so instead of
// comparing it with the floor.
//
// Subcommand and Feature are set when one optional capability within a command
// was refused rather than the command itself, which is what
// [UnsupportedPolicy] governs.
type VersionTooLowError struct {
	// Current is the installed tmux feature level.
	Current Version
	// Minimum is the requested minimum feature level.
	Minimum Version
	// Subcommand names the tmux subcommand whose optional capability was
	// refused. Empty when the command itself is below its floor.
	Subcommand string
	// Feature names the refused optional capability. Empty when the command
	// itself is below its floor.
	Feature string

	unrecognized bool
}

// versionRemedy says what to do about a refused tmux.
const versionRemedy = "upgrade tmux and restart its server, or pin the previous libtmux release"

// Error implements error.
func (e *VersionTooLowError) Error() string {
	message := e.Reason()
	if e.Feature == "" && e.Minimum.raw == MinimumSupportedVersion {
		message += "; " + versionRemedy
	}
	return message
}

// Reason describes the refusal without saying what to do about it, for a
// caller that words its own remedy.
func (e *VersionTooLowError) Reason() string {
	if e.Feature != "" {
		return fmt.Sprintf(
			"%v: %s: %s requires %s or newer, installed %s",
			ErrVersionTooLow,
			e.Subcommand,
			e.Feature,
			e.Minimum,
			e.Current,
		)
	}
	if e.unrecognized {
		return fmt.Sprintf(
			"tmux: version unrecognized: %q names no tmux release, require %s or newer",
			e.Current.String(),
			e.Minimum,
		)
	}
	return fmt.Sprintf(
		"%v: installed %s, require %s or newer",
		ErrVersionTooLow,
		e.Current,
		e.Minimum,
	)
}

// Unwrap makes VersionTooLowError compatible with ErrVersionTooLow.
func (e *VersionTooLowError) Unwrap() error {
	return ErrVersionTooLow
}

type versionCache struct {
	mu       sync.Mutex
	value    Version
	valid    bool
	inFlight chan struct{}
}

type versionTransportError struct {
	err error
}

type openBSDCapability struct {
	major   int
	minor   int
	command string
	flag    byte
}

var (
	openBSDCapabilityProbeSequence atomic.Uint64
	openBSDCapabilityLadder        = [...]openBSDCapability{
		{major: 3, minor: 2, command: "display-popup"},
		{major: 3, minor: 3, command: "confirm-before", flag: 'b'},
		{major: 3, minor: 4, command: "confirm-before", flag: 'c'},
		{major: 3, minor: 5, command: "copy-mode", flag: 'd'},
		{major: 3, minor: 6, command: "capture-pane", flag: 'M'},
		{major: 3, minor: 7, command: "list-keys", flag: 'F'},
	}
)

func (e *versionTransportError) Error() string { return e.err.Error() }

func (e *versionTransportError) Unwrap() error { return e.err }

// Version returns a materialized handle's daemon version or an unbound handle's
// cached executable version. Failed probes are not cached. A waiting caller can
// abandon an in-flight shared probe when ctx ends without canceling its owner.
func (s Server) Version(ctx context.Context) (Version, error) {
	return s.loadVersion(ctx, false)
}

// RefreshVersion returns a materialized handle's daemon version or invalidates
// an unbound handle's executable cache and probes again. Canceling ctx stops this
// call's wait; it does not establish whether the executable received a probe.
func (s Server) RefreshVersion(ctx context.Context) (Version, error) {
	return s.loadVersion(ctx, true)
}

// RequireVersion returns a [VersionTooLowError] matching [ErrVersionTooLow]
// when the selected daemon, or an executable creating it, is older than minimum.
func (s Server) RequireVersion(ctx context.Context, minimum Version) error {
	checked, err := s.checkCore(ctx, false)
	if err != nil {
		return err
	}
	current, err := checked.Version(ctx)
	if err != nil {
		return err
	}
	if !current.meetsMinimum(minimum) {
		return refuseVersion(current, minimum)
	}
	return nil
}

func (s Server) loadVersion(ctx context.Context, refresh bool) (Version, error) {
	state, err := s.stateForUse()
	if err != nil {
		return Version{}, err
	}
	if s.daemon != nil {
		return s.daemon.version, nil
	}
	if s.startupVersion != nil {
		return *s.startupVersion, nil
	}
	cache := &state.shared.version
	for {
		cache.mu.Lock()
		if !refresh && cache.valid {
			version := cache.value
			cache.mu.Unlock()
			return version, nil
		}
		if cache.inFlight != nil {
			done := cache.inFlight
			cache.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return Version{}, ctx.Err()
			}
		}
		if refresh {
			cache.valid = false
		}
		done := make(chan struct{})
		cache.inFlight = done
		cache.mu.Unlock()

		version, err := s.queryVersion(ctx)

		cache.mu.Lock()
		if err == nil {
			cache.value = version
			cache.valid = true
		}
		cache.inFlight = nil
		close(done)
		cache.mu.Unlock()
		return version, err
	}
}

func (s Server) queryVersion(ctx context.Context) (Version, error) {
	return s.queryVersionForOS(ctx, runtime.GOOS)
}

func (s Server) queryVersionForOS(ctx context.Context, goos string) (Version, error) {
	version, err := s.queryExecutableVersion(ctx, goos)
	if err != nil || !isOpenBSDVersionToken(version.String()) {
		return version, err
	}
	return s.probeOpenBSDCapabilities(ctx, version.String())
}

// queryExecutableVersion reads only the current executable's report. The version check
// never shares a diagnostic cache or starts a capability-probe daemon.
func (s Server) queryExecutableVersion(ctx context.Context, goos string) (Version, error) {
	state, err := s.stateForUse()
	if err != nil {
		return Version{}, &versionTransportError{err: err}
	}
	result, err := s.runExactArgv(ctx, []string{"-V"})
	commandResult := cloneCommandResult(CommandResult{
		Command:  result.Command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	})
	if err != nil {
		return Version{}, &versionTransportError{err: err}
	}
	// Prefix matching needs the original bytes; the public text decoder
	// escapes invalid UTF-8 in an otherwise valid executable basename.
	if len(result.RawStdout) != 0 {
		commandResult.Stdout = strings.Split(strings.TrimRight(string(result.RawStdout), "\n"), "\n")
	}
	// tmux 3.3a reports argv[0]'s basename; newer releases print "tmux".
	// Accept that exact configured name without loosening the version token.
	if len(commandResult.Stdout) == 1 {
		name := filepath.Base(state.config.executable)
		if token, ok := strings.CutPrefix(commandResult.Stdout[0], name+" "); ok {
			commandResult.Stdout[0] = "tmux " + token
		}
	}
	return parseQueriedVersion(commandResult, goos)
}

func parseQueriedVersion(result CommandResult, goos string) (Version, error) {
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		if goos == "openbsd" && len(result.Stderr) > 0 &&
			result.Stderr[0] == "tmux: unknown option -- V" {
			return Version{raw: "openbsd"}, nil
		}
		return Version{}, newVersionQueryError(result, "")
	}
	if len(result.Stdout) != 1 {
		return Version{}, newVersionQueryError(
			result, fmt.Sprintf("tmux -V returned %d lines", len(result.Stdout)),
		)
	}
	token, found := strings.CutPrefix(result.Stdout[0], "tmux ")
	if !found || token == "" {
		return Version{}, newVersionQueryError(result, "unexpected tmux -V output")
	}
	version, err := ParseVersion(token)
	if err != nil {
		return Version{}, newVersionQueryError(result, "tmux -V returned a malformed version token")
	}
	return version, nil
}

func (s Server) probeOpenBSDCapabilities(ctx context.Context, raw string) (Version, error) {
	socketName := fmt.Sprintf(
		"libtmux-capability-%d-%d",
		os.Getpid(),
		openBSDCapabilityProbeSequence.Add(1),
	)
	result, err := s.runExactArgv(ctx, []string{
		"-f" + os.DevNull,
		"-L" + socketName,
		"start-server",
		";",
		"list-commands",
	})
	commandResult := cloneCommandResult(CommandResult{
		Command:  result.Command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	})
	if err != nil {
		return Version{}, &versionTransportError{err: err}
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		reason := "tmux capability probe returned stderr"
		if result.ExitCode != 0 {
			reason = fmt.Sprintf("tmux capability probe exited %d", result.ExitCode)
		}
		return Version{}, newVersionQueryError(commandResult, reason)
	}
	if len(result.Stdout) == 0 {
		return Version{}, newVersionQueryError(
			commandResult,
			"tmux capability probe returned no commands",
		)
	}
	return openBSDCapabilityVersion(raw, result.Stdout), nil
}

func openBSDCapabilityVersion(raw string, lines []string) Version {
	version := Version{raw: raw}
	for _, capability := range openBSDCapabilityLadder {
		if !openBSDCommandSupports(lines, capability.command, capability.flag) {
			break
		}
		version.major = capability.major
		version.minor = capability.minor
	}
	return version
}

func isOpenBSDVersionToken(raw string) bool {
	return raw == "openbsd" || openBSDVersionPattern.MatchString(raw)
}

func openBSDCommandSupports(lines []string, command string, flag byte) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != command {
			continue
		}
		if flag == 0 {
			return true
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "[-") {
				continue
			}
			flags := strings.TrimPrefix(field, "[-")
			if end := strings.IndexByte(flags, ']'); end >= 0 {
				flags = flags[:end]
			}
			if strings.IndexByte(flags, flag) >= 0 {
				return true
			}
		}
		return false
	}
	return false
}
