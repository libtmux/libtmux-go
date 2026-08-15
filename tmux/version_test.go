package tmux

import (
	"errors"
	"testing"
)

// libtmux:parity libtmux._compat.LegacyVersion
// libtmux:parity libtmux._compat.LegacyVersion.__init__
// libtmux:parity libtmux._compat.LegacyVersion.__repr__
// libtmux:parity libtmux._compat.LegacyVersion.__str__
func TestParseVersionPreservesTokenAndComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		raw                 string
		major, minor, patch int
	}{
		{name: "major", raw: "1", major: 1},
		{name: "minor", raw: "3.2a", major: 3, minor: 2},
		{name: "patch", raw: "3.10.2", major: 3, minor: 10, patch: 2},
		{name: "release candidate", raw: "3.3-rc2", major: 3, minor: 3},
		{name: "next", raw: "next-3.8", major: 3, minor: 8},
		{name: "master", raw: "master", major: 3, minor: 7},
		{name: "versioned master", raw: "3.6a-master", major: 3, minor: 6},
		{name: "OpenBSD base", raw: "openbsd-7.8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			version, err := ParseVersion(test.raw)
			if err != nil {
				t.Fatalf("ParseVersion(%q) error = %v", test.raw, err)
			}
			if got := version.String(); got != test.raw {
				t.Errorf("String() = %q, want %q", got, test.raw)
			}
			if got := version.Major(); got != test.major {
				t.Errorf("Major() = %d, want %d", got, test.major)
			}
			if got := version.Minor(); got != test.minor {
				t.Errorf("Minor() = %d, want %d", got, test.minor)
			}
			if got := version.Patch(); got != test.patch {
				t.Errorf("Patch() = %d, want %d", got, test.patch)
			}
		})
	}
}

// libtmux:parity libtmux._compat.LegacyVersion.__hash__
func TestVersionIsComparableValue(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7b")
	versions := map[Version]bool{version: true}
	if !versions[version] {
		t.Fatal("Version cannot be used as a comparable map key")
	}
}

func TestOpenBSDBaseVersionDoesNotAssumeCapabilityLevel(t *testing.T) {
	t.Parallel()

	openBSD := mustParseVersion(t, "openbsd-7.8")
	minimum := mustParseVersion(t, MinimumSupportedVersion)
	if openBSD.String() != "openbsd-7.8" {
		t.Fatalf("String() = %q, want raw OpenBSD token", openBSD)
	}
	if openBSD.AtLeast(minimum) {
		t.Fatalf("unprobed OpenBSD token reports capability at least %s", minimum)
	}
}

func TestParseVersionRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"", "tmux 3.7", "next-", "3.x", "3..7", "3.7.", "3.6-master-extra",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			_, err := ParseVersion(raw)
			if !errors.Is(err, ErrInvalidVersion) {
				t.Fatalf("ParseVersion(%q) error = %v, want ErrInvalidVersion", raw, err)
			}
			var versionError *VersionError
			if !errors.As(err, &versionError) || versionError.Token != raw {
				t.Fatalf("ParseVersion(%q) error = %#v, want VersionError with original token", raw, err)
			}
		})
	}
}

// libtmux:parity libtmux.common.has_gt_version
// libtmux:parity libtmux.common.has_gte_version
// libtmux:parity libtmux.common.has_lt_version
// libtmux:parity libtmux.common.has_lte_version
// libtmux:parity libtmux.common.has_version
func TestVersionCompareUsesNumericCoreAndDevelopmentOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		left, right string
		want        int
	}{
		{name: "missing components are zero", left: "1", right: "1.0.0", want: 0},
		{name: "release letters share feature level", left: "3.7b", right: "3.7", want: 0},
		{name: "numeric comparison", left: "3.10", right: "3.9", want: 1},
		{name: "older", left: "3.2a", right: "3.3", want: -1},
		{name: "master follows tested release", left: "master", right: "3.7", want: 1},
		{name: "master precedes next numeric release", left: "master", right: "next-3.8", want: -1},
		{name: "versioned master follows its core", left: "3.6a-master", right: "3.6", want: 1},
		{name: "versioned master precedes next core", left: "3.6a-master", right: "3.7", want: -1},
		{name: "non-master qualifier shares feature level", left: "3.3-rc2", right: "3.3", want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			left := mustParseVersion(t, test.left)
			right := mustParseVersion(t, test.right)
			if got := left.Compare(right); got != test.want {
				t.Fatalf("ParseVersion(%q).Compare(%q) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
}

// libtmux:parity libtmux.common.TMUX_MAX_VERSION
func TestMaximumTestedVersionMatchesMasterNumericCore(t *testing.T) {
	t.Parallel()

	maximum := mustParseVersion(t, MaximumTestedVersion)
	master := mustParseVersion(t, "master")
	if master.Major() != maximum.Major() ||
		master.Minor() != maximum.Minor() ||
		master.Patch() != maximum.Patch() {
		t.Fatalf(
			"master numeric core = %d.%d.%d, want MaximumTestedVersion core %d.%d.%d",
			master.Major(),
			master.Minor(),
			master.Patch(),
			maximum.Major(),
			maximum.Minor(),
			maximum.Patch(),
		)
	}
}

// libtmux:parity libtmux._compat.LegacyCmpKey
// libtmux:parity libtmux._compat.LegacyVersion.__eq__
// libtmux:parity libtmux._compat.LegacyVersion.__eq__#parameter-branch:other:823699310e32
// libtmux:parity libtmux._compat.LegacyVersion.__eq__#parameter-branch:other:92d07012c4d3
// libtmux:parity libtmux._compat.LegacyVersion.__lt__
// libtmux:parity libtmux._compat.LegacyVersion.__lt__#parameter-branch:other:823699310e32
// libtmux:parity libtmux._compat.LegacyVersion.__lt__#parameter-branch:other:92d07012c4d3
// libtmux:parity libtmux._compat.LegacyVersion._key
// libtmux:parity libtmux._compat.LooseVersion
// libtmux:parity libtmux._compat._legacy_cmpkey
// libtmux:parity libtmux._compat._legacy_version_component_re
// libtmux:parity libtmux._compat._legacy_version_replacement_map
// libtmux:parity libtmux._compat._parse_version_parts
// libtmux:parity libtmux._vendor._structures#version-ordering-sentinels
// libtmux:parity libtmux._vendor.version#tmux-version-semantics
func TestVersionCompareReleasePreservesLegacySuffixOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		left, right string
		want        int
	}{
		{name: "alpha before beta", left: "3.7a", right: "3.7b", want: -1},
		{name: "beta before final", left: "3.7b", right: "3.7", want: -1},
		{name: "release candidate before final", left: "3.3-rc2", right: "3.3", want: -1},
		{name: "development before release candidate", left: "3.3-dev", right: "3.3-rc2", want: -1},
		{name: "operating system build after final", left: "3.7-openbsd", right: "3.7", want: 1},
		{name: "missing zero components compare equal", left: "1", right: "1.0.0", want: 0},
		{name: "final after alpha", left: "1", right: "1.0.0a", want: 1},
		{name: "final after beta", left: "1", right: "1.0.0b", want: 1},
		{name: "final before patch suffix", left: "1", right: "1.0.0p1", want: -1},
		{name: "final before operating system suffix", left: "1", right: "1.0.0-openbsd", want: -1},
		{name: "release candidate after beta", left: "1.0.0c", right: "1.0.0b", want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			left := mustParseVersion(t, test.left)
			right := mustParseVersion(t, test.right)
			if got := left.CompareRelease(right); got != test.want {
				t.Fatalf("ParseVersion(%q).CompareRelease(%q) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
}

func mustParseVersion(t *testing.T, raw string) Version {
	t.Helper()

	version, err := ParseVersion(raw)
	if err != nil {
		t.Fatalf("ParseVersion(%q) error = %v", raw, err)
	}
	return version
}
