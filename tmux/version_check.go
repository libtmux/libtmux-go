package tmux

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

var (
	minimumSupportedVersion = Version{raw: MinimumSupportedVersion, major: 3, minor: 3}

	// versionReleasePattern is the grammar tmux has reported through
	// #{version}: a release with at most one patch letter, a release
	// candidate (bare or numbered) of an unpatched release, or the development
	// build next-X.Y. Suffixes such as -master or -dev never came from tmux.
	versionReleasePattern = regexp.MustCompile(
		`^(?:(next)-([0-9]+)\.([0-9]+)|([0-9]+)\.([0-9]+)(?:\.([0-9]+))?` +
			`(?:([a-z])|-rc([0-9]*))?)$`,
	)
)

// Release stages order next-X.Y below X.Y-rc below X.Y, which a patch letter
// then follows.
const (
	stageNext = iota
	stageCandidate
	stageRelease
)

// versionKey orders qualified tokens: major, minor, patch, stage, then the
// candidate number or patch letter. ok is false outside the grammar.
func versionKey(raw string) (key [6]int, ok bool) {
	match := versionReleasePattern.FindStringSubmatch(raw)
	if match == nil {
		return key, false
	}
	number := func(text string) int {
		value, err := strconv.Atoi(text)
		if err != nil {
			return 0
		}
		return value
	}
	if match[1] == "next" {
		return [6]int{number(match[2]), number(match[3]), 0, stageNext, 0, 0}, true
	}
	key = [6]int{number(match[4]), number(match[5]), number(match[6]), stageRelease, 0, 0}
	switch {
	case match[7] != "":
		key[4] = int(match[7][0]-'a') + 1
	case strings.Contains(raw, "-rc"):
		key[3] = stageCandidate
		key[4] = number(match[8])
	}
	return key, true
}

// meetsMinimum orders tokens by the grammar above, so a candidate or
// development build ranks below the release it names. A token outside the
// grammar never meets a minimum.
func (v Version) meetsMinimum(minimum Version) bool {
	current, ok := versionKey(v.raw)
	if !ok {
		return false
	}
	required, ok := versionKey(minimum.raw)
	if !ok {
		return false
	}
	return slices.Compare(current[:], required[:]) >= 0
}

func requireSupportedVersion(current Version) error {
	if !current.meetsMinimum(minimumSupportedVersion) {
		return refuseVersion(current, minimumSupportedVersion)
	}
	return nil
}

// refuseVersion builds the refusal for a version below minimum, marking one
// outside the release grammar as unrecognized rather than older.
func refuseVersion(current, minimum Version) *VersionTooLowError {
	_, recognized := versionKey(current.raw)
	return &VersionTooLowError{Current: current, Minimum: minimum, unrecognized: !recognized}
}

// checkCore binds a live daemon to the same identity guard as materialized
// records. An absent daemon is checked by the executable that can create it.
func (s Server) checkCore(ctx context.Context, absentCleanup bool) (Server, error) {
	checked, err := s.checkCoreVersion(ctx, absentCleanup)
	return checked, notSent(err)
}

// ErrVersionCheckFailed identifies a failure of the check that precedes every
// ordinary command, a refusal included: an ErrVersionTooLow from Cmd matches
// it too. The check is a read-only probe, so a command that fails with it was
// never sent, whatever else the error matches.
var ErrVersionCheckFailed = errors.New("tmux: version check failed")

type versionCheckError struct{ err error }

func (e *versionCheckError) Error() string { return e.err.Error() }

func (e *versionCheckError) Unwrap() error { return e.err }

// Is matches [ErrVersionCheckFailed].
func (e *versionCheckError) Is(target error) bool { return target == ErrVersionCheckFailed }

// notSent marks a check failure as one that sent nothing and removes the
// outcome-unknown classification from it: the check is a read-only probe, so
// however it ended, the caller's request was never sent.
func notSent(err error) error {
	if err == nil {
		return nil
	}
	cause, stripped := err, false
	if unknown, ok := errors.AsType[*outcomeUnknownError](cause); ok {
		cause, stripped = unknown.cause, true
	}
	if unknown, ok := errors.AsType[*tmuxcmd.OutcomeUnknownError](cause); ok {
		cause, stripped = unknown.Err, true
	}
	if stripped {
		err = &commandTransportError{err: cause}
	}
	return &versionCheckError{err: err}
}

func (s Server) checkCoreVersion(ctx context.Context, absentCleanup bool) (Server, error) {
	if err := ctx.Err(); err != nil {
		return Server{}, err
	}
	if s.daemon != nil {
		return s, requireSupportedVersion(s.daemon.version)
	}
	// Connections are checked before their creating control process starts and
	// keep addressing that exact daemon for their entire lifetime.
	if s.connection != nil {
		return s, nil
	}
	identity, err := s.probeServerIdentity(ctx, coreVersionIdentityFields())
	if err == nil {
		if err := requireSupportedVersion(identity.version); err != nil {
			return Server{}, err
		}
		return s.withDaemon(identity), nil
	}
	if !errors.Is(err, ErrNoServer) {
		return Server{}, err
	}
	if absentCleanup {
		return s, nil
	}
	version, err := s.queryExecutableVersion(ctx, runtime.GOOS)
	if err != nil {
		return Server{}, err
	}
	if err := requireSupportedVersion(version); err != nil {
		return Server{}, err
	}
	s.startupVersion = &version
	return s, nil
}

func coreVersionIdentityFields() []formatField {
	return []formatField{
		{name: "pid"},
		{name: "start_time"},
		{name: "socket_path"},
		{name: "version"},
	}
}
