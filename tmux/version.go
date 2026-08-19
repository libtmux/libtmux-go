package tmux

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MinimumSupportedVersion is the oldest tmux feature level supported by this package.
	MinimumSupportedVersion = "3.2a"
	// MaximumTestedVersion is the newest numbered tmux feature level covered by
	// this package's tests. That feature level is tested against tmux 3.7b.
	MaximumTestedVersion = "3.7"

	latestTestedMajor = 3
	latestTestedMinor = 7
)

// ErrInvalidVersion classifies [VersionError] through errors.Is.
var (
	// ErrInvalidVersion identifies malformed tmux version tokens. It is matched by
	// errors.Is for VersionError.
	ErrInvalidVersion = errors.New("tmux: invalid version")

	versionPattern = regexp.MustCompile(
		`^(?:(next)-)?([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?` +
			`(?:([a-z][a-z0-9]*(?:[.-][a-z0-9]+)*)|(-[a-z0-9]+(?:[.-][a-z0-9]+)*))?$`,
	)
	openBSDVersionPattern    = regexp.MustCompile(`^openbsd-[0-9]+(?:\.[0-9]+)?$`)
	legacyVersionPartPattern = regexp.MustCompile(`[0-9]+|[a-z]+|[.-]`)
)

// VersionError reports a tmux version token that cannot be parsed. It matches
// [ErrInvalidVersion] through errors.Is; callers can recover Token with errors.As.
type VersionError struct {
	// Token is the rejected tmux version text.
	Token string
}

// Error implements error.
func (e *VersionError) Error() string {
	return fmt.Sprintf("%v %q", ErrInvalidVersion, e.Token)
}

// Unwrap makes VersionError compatible with ErrInvalidVersion.
func (e *VersionError) Unwrap() error {
	return ErrInvalidVersion
}

// Version is a parsed tmux version that preserves its original token. Obtain a
// value with [ParseVersion], [Server.Version], or [Snapshot.Version]. Its zero
// value has no feature level and is useful only as an absent version. Parsed
// OpenBSD base-system tokens also have no feature level until [Server.Version]
// probes the configured binary's commands.
type Version struct {
	raw         string
	major       int
	minor       int
	patch       int
	development int
}

// ParseVersion parses a raw tmux version token. It returns a [VersionError]
// matching [ErrInvalidVersion] when the token cannot be parsed.
func ParseVersion(raw string) (Version, error) {
	if openBSDVersionPattern.MatchString(raw) {
		return Version{raw: raw}, nil
	}
	if raw == "master" {
		return Version{
			raw:         raw,
			major:       latestTestedMajor,
			minor:       latestTestedMinor,
			development: 1,
		}, nil
	}

	matches := versionPattern.FindStringSubmatch(raw)
	if matches == nil {
		return Version{}, &VersionError{Token: raw}
	}

	major, err := parseVersionNumber(raw, matches[2])
	if err != nil {
		return Version{}, err
	}
	minor, err := parseVersionNumber(raw, matches[3])
	if err != nil {
		return Version{}, err
	}
	patch, err := parseVersionNumber(raw, matches[4])
	if err != nil {
		return Version{}, err
	}

	qualifier := matches[5]
	if qualifier == "" {
		qualifier = matches[6]
	}
	development := 0
	if strings.Contains(qualifier, "master") {
		if qualifier != "master" && !strings.HasSuffix(qualifier, "-master") {
			return Version{}, &VersionError{Token: raw}
		}
		development = 1
	}
	return Version{
		raw:         raw,
		major:       major,
		minor:       minor,
		patch:       patch,
		development: development,
	}, nil
}

func parseVersionNumber(raw, value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, &VersionError{Token: raw}
	}
	return number, nil
}

// String returns the original tmux version token.
func (v Version) String() string {
	return v.raw
}

// Major returns the capability-level major component. Development tokens use
// the latest tested feature core. Unprobed OpenBSD base-system tokens return
// zero.
func (v Version) Major() int {
	return v.major
}

// Minor returns the capability-level minor component, or zero when absent.
// Development tokens use the latest tested feature core. Unprobed OpenBSD
// base-system tokens return zero.
func (v Version) Minor() int {
	return v.minor
}

// Patch returns the capability-level patch component, or zero when absent.
func (v Version) Patch() int {
	return v.patch
}

// Compare compares numeric feature levels and returns -1, 0, or 1.
// Release qualifiers other than master do not change the feature level.
func (v Version) Compare(other Version) int {
	left := [...]int{v.major, v.minor, v.patch, v.development}
	right := [...]int{other.major, other.minor, other.patch, other.development}
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// CompareRelease compares raw release suffixes using libtmux's legacy order.
// Use Compare for feature gates, where point-release suffixes intentionally
// share one capability level.
func (v Version) CompareRelease(other Version) int {
	left := legacyVersionKey(v.raw)
	right := legacyVersionKey(other.raw)
	for index := range min(len(left), len(right)) {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func legacyVersionKey(raw string) []string {
	parts := legacyVersionPartPattern.FindAllString(strings.ToLower(raw), -1)
	key := make([]string, 0, len(parts)+1)
	for _, part := range append(parts, "final") {
		switch part {
		case ".":
			continue
		case "pre", "preview", "rc":
			part = "c"
		case "dev":
			part = "@"
		case "-":
			part = "final-"
		}

		if part[0] >= '0' && part[0] <= '9' {
			if padding := 8 - len(part); padding > 0 {
				part = strings.Repeat("0", padding) + part
			}
		} else {
			part = "*" + part
			if part < "*final" {
				for len(key) > 0 && key[len(key)-1] == "*final-" {
					key = key[:len(key)-1]
				}
			}
			for len(key) > 0 && key[len(key)-1] == "00000000" {
				key = key[:len(key)-1]
			}
		}
		key = append(key, part)
	}
	return key
}

// AtLeast reports whether v provides the same or a newer feature level.
func (v Version) AtLeast(minimum Version) bool {
	return v.Compare(minimum) >= 0
}
