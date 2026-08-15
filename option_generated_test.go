package tmux

import "testing"

func TestGeneratedOptionMetadataCountsAndVersions(t *testing.T) {
	t.Parallel()

	if generatedOptionSpecSchema != 2 {
		t.Fatalf("generatedOptionSpecSchema = %d, want 2", generatedOptionSpecSchema)
	}
	if generatedOptionSourceTag != "3.7b" {
		t.Fatalf("generatedOptionSourceTag = %q, want 3.7b", generatedOptionSourceTag)
	}
	if generatedOptionFeatureFloor.String() != "3.2a" || generatedOptionFeatureCeiling.String() != "3.7" {
		t.Fatalf(
			"generated feature range = %s..%s, want 3.2a..3.7",
			generatedOptionFeatureFloor,
			generatedOptionFeatureCeiling,
		)
	}
	if len(generatedOptionDefinitions) != 153 || len(generatedHookDefinitions) != 68 {
		t.Fatalf(
			"definition counts = %d options, %d hooks, want 153, 68",
			len(generatedOptionDefinitions),
			len(generatedHookDefinitions),
		)
	}

	counts := [...]int{
		generatedServerOptionCount,
		generatedSessionOptionCount,
		generatedWindowOptionCount,
		generatedPaneOptionCount,
		generatedServerHookCount,
		generatedSessionHookCount,
		generatedWindowHookCount,
		generatedPaneHookCount,
	}
	want := [...]int{25, 54, 74, 19, 57, 57, 13, 7}
	if counts != want {
		t.Fatalf("generated scope counts = %v, want %v", counts, want)
	}
}
