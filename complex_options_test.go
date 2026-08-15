package tmux

import (
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
)

// libtmux:parity libtmux._internal.constants.ServerOptions.terminal_features
// libtmux:parity libtmux._internal.constants.TerminalFeatures
// libtmux:parity libtmux.options.CommandAliases
// libtmux:parity libtmux.options.TerminalOverride
// libtmux:parity libtmux.options.TerminalOverrides
// libtmux:parity libtmux.options.explode_complex
func TestComplexOptionProjectionsPreservePresenceOriginAndPythonShapes(t *testing.T) {
	t.Parallel()

	aliasesRaw, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "splitp=split-window"},
		SparseEntry[string]{Index: 4, Value: "info=show-messages -JT"},
		SparseEntry[string]{Index: 7, Value: "splitp=split-window -v"},
	)
	if err != nil {
		t.Fatal(err)
	}
	featuresRaw, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "xterm*:clipboard:ccolour:cstyle:focus"},
		SparseEntry[string]{Index: 5, Value: "screen*:title"},
		SparseEntry[string]{Index: 8, Value: "xterm*:ignorefkeys"},
	)
	if err != nil {
		t.Fatal(err)
	}
	overridesRaw, err := NewSparseArray(
		SparseEntry[string]{Index: 0, Value: "xterm*:XT:Ms=clipboard"},
		SparseEntry[string]{Index: 3, Value: "tmux*:colors=256:Tc"},
		SparseEntry[string]{Index: 8, Value: "xterm*:Ms=primary"},
	)
	if err != nil {
		t.Fatal(err)
	}

	values := ServerOptionValues{
		commandAlias:      newInheritedOptionValue(aliasesRaw),
		terminalFeatures:  newLocalOptionValue(featuresRaw),
		terminalOverrides: newLocalOptionValue(overridesRaw),
	}

	aliasesValue, err := values.CommandAliases()
	if err != nil {
		t.Fatal(err)
	}
	if origin, ok := aliasesValue.Origin(); !ok || origin != OptionOriginInherited {
		t.Fatalf("CommandAliases() origin = (%v, %t)", origin, ok)
	}
	aliases, ok := aliasesValue.Get()
	if !ok {
		t.Fatal("CommandAliases() reported absent")
	}
	if command, found := aliases.Lookup("splitp"); !found || command != "split-window -v" {
		t.Fatalf("CommandAliases().Lookup(splitp) = (%q, %t)", command, found)
	}
	if got := aliases.Entries(); !slices.Equal(got, []CommandAlias{
		{Name: "splitp", Command: "split-window -v"},
		{Name: "info", Command: "show-messages -JT"},
	}) {
		t.Fatalf("CommandAliases().Entries() = %#v", got)
	}

	featuresValue, err := values.ParsedTerminalFeatures()
	if err != nil {
		t.Fatal(err)
	}
	features, ok := featuresValue.Get()
	if !ok {
		t.Fatal("ParsedTerminalFeatures() reported absent")
	}
	gotFeatures, found := features.Lookup("xterm*")
	if !found || !slices.Equal(gotFeatures, []string{"ignorefkeys"}) {
		t.Fatalf("ParsedTerminalFeatures().Lookup(xterm*) = (%#v, %t)", gotFeatures, found)
	}
	gotFeatures[0] = "mutated"
	if again, _ := features.Lookup("xterm*"); !slices.Equal(again, []string{"ignorefkeys"}) {
		t.Fatalf("terminal feature lookup aliases stored state: %#v", again)
	}

	overridesValue, err := values.ParsedTerminalOverrides()
	if err != nil {
		t.Fatal(err)
	}
	overrides, ok := overridesValue.Get()
	if !ok {
		t.Fatal("ParsedTerminalOverrides() reported absent")
	}
	xterm, found := overrides.Lookup("xterm*")
	if !found {
		t.Fatal("ParsedTerminalOverrides().Lookup(xterm*) reported absent")
	}
	if xt, found := xterm.Lookup("XT"); !found || !xt.IsFlag() {
		t.Fatalf("xterm XT = (%#v, %t)", xt, found)
	}
	if ms, found := xterm.Lookup("Ms"); !found {
		t.Fatal("xterm Ms reported absent")
	} else if text, textOK := ms.Text(); !textOK || text != "primary" {
		t.Fatalf("xterm Ms = (%q, %t)", text, textOK)
	}
	tmuxOverrides, found := overrides.Lookup("tmux*")
	if !found {
		t.Fatal("ParsedTerminalOverrides().Lookup(tmux*) reported absent")
	}
	colors, found := tmuxOverrides.Lookup("colors")
	if !found {
		t.Fatal("tmux colors reported absent")
	}
	integer, integerOK := colors.Integer()
	if !integerOK || integer.Cmp(big.NewInt(256)) != 0 {
		t.Fatalf("tmux colors = (%v, %t)", integer, integerOK)
	}
}

func TestComplexOptionProjectionsReturnRedactedPartialDecodeErrors(t *testing.T) {
	t.Parallel()

	aliasesRaw, err := NewSparseArray(
		SparseEntry[string]{Index: 1, Value: "private-malformed-alias"},
		SparseEntry[string]{Index: 4, Value: "copy=copy-mode"},
	)
	if err != nil {
		t.Fatal(err)
	}
	featuresRaw, err := NewSparseArray(
		SparseEntry[string]{Index: 2, Value: "private-malformed-feature"},
		SparseEntry[string]{Index: 6, Value: "screen*:title"},
	)
	if err != nil {
		t.Fatal(err)
	}
	values := ServerOptionValues{
		commandAlias:     newLocalOptionValue(aliasesRaw),
		terminalFeatures: newLocalOptionValue(featuresRaw),
	}

	aliasesValue, aliasErr := values.CommandAliases()
	if !errors.Is(aliasErr, ErrMalformedComplexOption) {
		t.Fatalf("CommandAliases() error = %v", aliasErr)
	}
	aliases, ok := aliasesValue.Get()
	if !ok {
		t.Fatal("CommandAliases() discarded partial projection")
	}
	if command, found := aliases.Lookup("copy"); !found || command != "copy-mode" {
		t.Fatalf("partial alias lookup = (%q, %t)", command, found)
	}
	assertRedactedComplexOptionError(t, aliasErr, "command-alias", 1, "private-malformed-alias")

	featuresValue, featureErr := values.ParsedTerminalFeatures()
	if !errors.Is(featureErr, ErrMalformedComplexOption) {
		t.Fatalf("ParsedTerminalFeatures() error = %v", featureErr)
	}
	features, ok := featuresValue.Get()
	if !ok {
		t.Fatal("ParsedTerminalFeatures() discarded partial projection")
	}
	if list, found := features.Lookup("screen*"); !found || !slices.Equal(list, []string{"title"}) {
		t.Fatalf("partial terminal feature lookup = (%#v, %t)", list, found)
	}
	assertRedactedComplexOptionError(t, featureErr, "terminal-features", 2, "private-malformed-feature")

	absent := ServerOptionValues{}
	if value, err := absent.CommandAliases(); err != nil {
		t.Fatal(err)
	} else if _, ok := value.Get(); ok {
		t.Fatal("absent CommandAliases() became present")
	}
}

func assertRedactedComplexOptionError(
	t *testing.T,
	err error,
	option string,
	index int,
	secret string,
) {
	t.Helper()
	var decodeError *ComplexOptionDecodeError
	if !errors.As(err, &decodeError) {
		t.Fatalf("error = %#v, want ComplexOptionDecodeError", err)
	}
	if decodeError.Option != option || decodeError.Index != index {
		t.Fatalf("decode error = %#v", decodeError)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("decode error retained raw option data: %v", err)
	}
}
