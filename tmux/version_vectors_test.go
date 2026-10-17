package tmux

import (
	"errors"
	"strings"
	"testing"
)

// versionChain holds every ordering form in ascending order, with the feature
// level Compare gives it: a release and its patch letters share one, which
// versionKey still orders. Both rankings are iterated over it: versionKey,
// behind the floor and RequireVersion, and rank, behind AtLeast and Compare.
var versionChain = []struct {
	raw   string
	level int
}{
	{"next-3.4", 0},
	{"3.4-rc", 1},
	{"3.4-rc2", 2},
	{"3.4", 3},
	{"3.4a", 3},
	{"next-3.7", 4},
	{"3.7-rc", 5},
	{"3.7-rc3", 6},
	{"3.7", 7},
	{"3.7a", 7},
	{"3.7d", 7},
	{"next-3.8", 8},
	{"3.8-rc", 9},
	{"3.8-rc2", 10},
	{"3.8", 11},
	{"3.9", 12},
	{"3.10", 13},
	{"4.0", 14},
}

func TestBothRankingsOrderTheSharedVersionChain(t *testing.T) {
	t.Parallel()

	for low := range versionChain {
		for high := low + 1; high < len(versionChain); high++ {
			lower, upper := versionChain[low].raw, versionChain[high].raw
			if !(Version{raw: upper}).meetsMinimum(Version{raw: lower}) ||
				(Version{raw: lower}).meetsMinimum(Version{raw: upper}) {
				t.Errorf("versionKey: %s must rank strictly below %s", lower, upper)
			}
			left, right := mustParseVersion(t, lower), mustParseVersion(t, upper)
			want := -1
			if versionChain[low].level == versionChain[high].level {
				want = 0
			}
			if got := left.Compare(right); got != want {
				t.Errorf("rank: %s.Compare(%s) = %d, want %d", lower, upper, got, want)
			}
			if got := right.Compare(left); got != -want {
				t.Errorf("rank: %s.Compare(%s) = %d, want %d", upper, lower, got, -want)
			}
			if !right.AtLeast(left) || (left.AtLeast(right) != (want == 0)) {
				t.Errorf("rank: AtLeast disagrees on %s and %s", lower, upper)
			}
		}
	}
}

// The vectors are every form tmux has reported through #{version}; the
// ordering is next-X.Y < X.Y-rc < X.Y-rcN < X.Y < X.Ya.
func TestVersionCheckGrammarAndOrdering(t *testing.T) {
	t.Parallel()

	supported := []string{
		"3.3a", "3.4", "3.7d", "3.8", "3.10", "4.0", "3.4-rc", "3.8-rc",
		"3.8-rc2", "next-3.4", "next-3.9",
	}
	belowFloor := []string{"3.3", "3.2a", "3.3-rc", "3.2-rc5", "next-3.3"}
	unparseable := []string{
		"master", "openbsd-7.6", "", "3", "garbage", "3.3a garbage",
		"3.3a-master", "3.3a-dev", "next-3.3a", "next-3.9-master",
	}
	for _, raw := range supported {
		t.Run("supported "+raw, func(t *testing.T) {
			t.Parallel()
			if err := requireSupportedVersion(Version{raw: raw}); err != nil {
				t.Fatalf("requireSupportedVersion(%q) = %v, want supported", raw, err)
			}
		})
	}
	for _, raw := range belowFloor {
		t.Run("below floor "+raw, func(t *testing.T) {
			t.Parallel()
			var tooLow *VersionTooLowError
			if err := requireSupportedVersion(Version{raw: raw}); !errors.As(err, &tooLow) {
				t.Fatalf("requireSupportedVersion(%q) = %v, want VersionTooLowError", raw, err)
			}
		})
	}
	for _, raw := range unparseable {
		t.Run("refuse "+raw, func(t *testing.T) {
			t.Parallel()
			var tooLow *VersionTooLowError
			err := requireSupportedVersion(Version{raw: raw})
			if !errors.As(err, &tooLow) || !errors.Is(err, ErrVersionTooLow) {
				t.Fatalf("requireSupportedVersion(%q) = %v, want VersionTooLowError", raw, err)
			}
			if !tooLow.unrecognized || !strings.Contains(err.Error(), "unrecognized") {
				t.Fatalf("requireSupportedVersion(%q) = %v, want it flagged and worded as unrecognized", raw, err)
			}
		})
	}
	for _, raw := range belowFloor {
		if err := requireSupportedVersion(Version{raw: raw}); err != nil && strings.Contains(err.Error(), "unrecognized") {
			t.Errorf("requireSupportedVersion(%q) = %v, want too low, not unrecognized", raw, err)
		}
	}

	// A feature gate at 3.7 follows the same order.
	gate := Version{raw: "3.7"}
	for raw, want := range map[string]bool{
		"next-3.7": false, "3.7-rc2": false, "3.7": true, "3.7a": true,
		"3.8-rc2": true, "next-3.8": true,
	} {
		if got := (Version{raw: raw}).meetsMinimum(gate); got != want {
			t.Errorf("%s meets 3.7 = %t, want %t", raw, got, want)
		}
	}
}

// Feature gates rank as the floor does: next-X.Y < X.Y-rc < X.Y-rcN < X.Y,
// with a patch letter sharing its release's level. A gate opens on a candidate
// only when it names that candidate. The oracle is the library's own parser.
func TestFeatureGatesRankCandidatesAndDevelopmentBuildsBelowTheirRelease(t *testing.T) {
	gate := mustParseVersion(t, "3.4")
	for raw, want := range map[string]bool{
		"next-3.4": false, "3.4-rc": false, "3.4-rc2": false, "3.4": true,
		"3.4a": true, "next-3.5": true, "3.5-rc": true,
	} {
		version := mustParseVersion(t, raw)
		if got := version.AtLeast(gate); got != want {
			t.Errorf("%s.AtLeast(3.4) = %t, want %t", raw, got, want)
		}
		if got := version.meetsMinimum(gate); got != want {
			t.Errorf("%s meets 3.4 = %t, want %t", raw, got, want)
		}
	}
	// A gate that names a candidate opens from it on, and not before.
	candidate := mustParseVersion(t, "3.8-rc2")
	for raw, want := range map[string]bool{
		"3.7c": false, "next-3.8": false, "3.8-rc": false, "3.8-rc2": true,
		"3.8-rc3": true, "3.8": true, "next-3.9": true,
	} {
		if got := mustParseVersion(t, raw).AtLeast(candidate); got != want {
			t.Errorf("%s.AtLeast(3.8-rc2) = %t, want %t", raw, got, want)
		}
	}
}
