package tmux

import (
	"context"
	"errors"
	"strings"
)

var minimumSupportedVersion = Version{raw: MinimumSupportedVersion, major: 3, minor: 3}

// meetsMinimum preserves feature comparisons while honoring a minimum release's
// patch letter. An unqualified or prerelease token cannot prove that patch.
func (v Version) meetsMinimum(minimum Version) bool {
	if v.raw == "master" || isOpenBSDVersionToken(v.raw) {
		return false
	}
	if v.major != minimum.major || v.minor != minimum.minor || v.patch != minimum.patch {
		return v.Compare(minimum) > 0
	}
	if v.development < minimum.development {
		return false
	}
	required := versionPattern.FindStringSubmatch(minimum.raw)
	if len(required) <= 5 || len(required[5]) != 1 {
		return true
	}
	current := versionPattern.FindStringSubmatch(v.raw)
	if len(current) <= 5 {
		return false
	}
	patch := strings.TrimSuffix(current[5], "-master")
	return len(patch) == 1 && patch >= required[5]
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
