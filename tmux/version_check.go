package tmux

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var (
	minimumSupportedVersion = Version{raw: MinimumSupportedVersion, major: 3, minor: 3}
	admissionReleasePattern = regexp.MustCompile(
		`^(next-)?[0-9]+\.[0-9]+(?:\.[0-9]+)?` +
			`([a-z]?)(?:-(master|dev|rc[0-9]+))?$`,
	)
)

// meetsMinimum keeps diagnostic parsing separate from qualified admission and
// compares patch letters before a development suffix on the same numeric core.
func (v Version) meetsMinimum(minimum Version) bool {
	current := admissionReleasePattern.FindStringSubmatch(v.raw)
	if current == nil {
		return false
	}
	if v.major != minimum.major || v.minor != minimum.minor || v.patch != minimum.patch {
		return v.Compare(minimum) > 0
	}
	required := admissionReleasePattern.FindStringSubmatch(minimum.raw)
	if required == nil {
		return false
	}
	if current[2] != required[2] {
		return current[2] > required[2]
	}
	return v.development >= minimum.development && current[1] == "" &&
		!strings.HasPrefix(current[3], "rc")
}

func requireSupportedVersion(current Version) error {
	if !current.meetsMinimum(minimumSupportedVersion) {
		return &VersionTooLowError{Current: current, Minimum: minimumSupportedVersion}
	}
	return nil
}

// checkCore binds a live daemon to the same identity guard as materialized
// records. An absent daemon is checked by the executable that can create it.
func (s Server) checkCore(ctx context.Context, absentCleanup bool) (Server, error) {
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
	version, err := s.Version(ctx)
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
