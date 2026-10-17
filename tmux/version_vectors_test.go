package tmux

import (
	"errors"
	"testing"
)

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
			if err := requireSupportedVersion(Version{raw: raw}); err == nil {
				t.Fatalf("requireSupportedVersion(%q) accepted a token tmux never reports", raw)
			}
		})
	}

	// Each chain is strictly ascending, which also fixes how 3.10 and a patch
	// letter compare.
	for _, chain := range [][]string{
		{"next-3.7", "3.7-rc", "3.7-rc3", "3.7", "3.7a"},
		{"next-3.4", "3.4-rc", "3.4"},
		{"3.9", "3.10"},
	} {
		for index := 1; index < len(chain); index++ {
			low, high := Version{raw: chain[index-1]}, Version{raw: chain[index]}
			if !high.meetsMinimum(low) || low.meetsMinimum(high) {
				t.Errorf("%s must rank strictly below %s", low, high)
			}
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
