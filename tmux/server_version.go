package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// VersionQueryError reports an unsuccessful tmux -V probe. It matches
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
}

// Error implements error.
func (e *VersionTooLowError) Error() string {
	if e.Feature == "" {
		return fmt.Sprintf(
			"%v: installed %s, require %s or newer",
			ErrVersionTooLow,
			e.Current,
			e.Minimum,
		)
	}
	return fmt.Sprintf(
		"%v: %s: %s requires %s or newer, installed %s",
		ErrVersionTooLow,
		e.Subcommand,
		e.Feature,
		e.Minimum,
		e.Current,
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

// Version returns the configured tmux binary's cached version. Failed probes
// are not cached. A waiting caller can abandon an in-flight shared probe when
// ctx ends without canceling the caller that owns the probe.
func (s Server) Version(ctx context.Context) (Version, error) {
	return s.loadVersion(ctx, false)
}

// RefreshVersion invalidates the cached version and probes the binary again.
// Canceling ctx stops this call's wait; it does not establish whether a probe
// reached the configured binary.
func (s Server) RefreshVersion(ctx context.Context) (Version, error) {
	return s.loadVersion(ctx, true)
}

// RequireVersion returns a [VersionTooLowError] matching [ErrVersionTooLow]
// when the configured binary is older than minimum.
func (s Server) RequireVersion(ctx context.Context, minimum Version) error {
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}
	if !current.AtLeast(minimum) {
		return &VersionTooLowError{Current: current, Minimum: minimum}
	}
	return nil
}

func (s Server) loadVersion(ctx context.Context, refresh bool) (Version, error) {
	cache := &s.connectionState().coordination().version
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
	version, err := parseQueriedVersion(commandResult, goos)
	if err != nil {
		return Version{}, err
	}
	if !isOpenBSDVersionToken(version.String()) {
		return version, nil
	}
	return s.probeOpenBSDCapabilities(ctx, version.String())
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
