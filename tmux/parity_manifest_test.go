package tmux_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

//go:embed internal/parity/manifest.json
var parityManifestJSON []byte

var parityTranslations = []string{
	"blocking-test-helper",
	"canonical-snapshot",
	"context-first-method",
	"deprecated-alias",
	"deprecated-error",
	"deprecated-python-omission",
	"error-value",
	"fresh-slice-and-sequence",
	"identical-operation-alias",
	"named-method-or-value",
	"package-build-metadata",
	"python-package-metadata-omission",
	"query-addendum-replacement",
	"request-struct",
	"warning-handler",
}

var parityOmissionTranslations = map[string]bool{
	"deprecated-python-omission":       true,
	"python-package-metadata-omission": true,
}

var parityProofKinds = map[string]bool{
	"unit":          true,
	"real-tmux":     true,
	"compatibility": true,
	"compile":       true,
	"golden":        true,
	"differential":  true,
}

var parityGeneratedProofKinds = map[string]bool{
	"compile":      true,
	"golden":       true,
	"differential": true,
}

var parityVersionRangePattern = regexp.MustCompile(`^(?:>=|>|<=|<|==)[0-9]+(?:\.[0-9]+)*(?:[a-z][a-z0-9.-]*)?$`)

var parityComparabilityParameterPattern = regexp.MustCompile(`parameter:T[0-9]+`)

var parityStructuralKinds = map[string]bool{
	"class":         true,
	"private-class": true,
	"data":          true,
	"enum-member":   true,
	"export":        true,
	"field":         true,
	"overload":      true,
	"private-data":  true,
	"private-field": true,
}

var parityPolicyValues = map[string]map[string]bool{
	"tmux": {
		"not-applicable":       true,
		"minimum-3.2a":         true,
		"source-version-gated": true,
	},
	"warning": {
		"not-applicable":  true,
		"warning-handler": true,
	},
	"projection": {
		"not-applicable": true,
		"preserved":      true,
	},
	"difference": {
		"none":                 true,
		"language-translation": true,
		"list-empty-on-error":  true,
	},
}

type parityManifest struct {
	Schema           int                      `json:"schema"`
	Translations     []string                 `json:"translations"`
	SourceDigests    []paritySourceDigest     `json:"source_digests"`
	SelectedInternal []paritySelectedInternal `json:"selected_internal"`
	Snapshots        []paritySnapshot         `json:"snapshots"`
	Entries          []parityEntry            `json:"entries"`
}

type paritySourceDigest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type paritySelectedInternal struct {
	Path    string   `json:"path"`
	Symbols []string `json:"symbols"`
}

type paritySnapshot struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type parityEntry struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Source       string        `json:"source"`
	Digest       string        `json:"digest"`
	Status       string        `json:"status"`
	Go           []string      `json:"go,omitempty"`
	Proof        []parityProof `json:"proof,omitempty"`
	Spec         string        `json:"spec,omitempty"`
	Translation  string        `json:"translation,omitempty"`
	Tmux         string        `json:"tmux,omitempty"`
	Warning      string        `json:"warning,omitempty"`
	Difference   string        `json:"difference,omitempty"`
	Projection   string        `json:"projection,omitempty"`
	VersionRange string        `json:"version_range,omitempty"`
}

type parityProof struct {
	Kind string `json:"kind"`
	Test string `json:"test"`
}

type paritySymbol struct {
	Generated   bool
	Exported    bool
	Production  bool
	Importable  bool
	Portable    bool
	Canonical   string
	Fingerprint string
	ShapeKnown  bool
	Proof       bool
	Behavior    bool
	RealTmux    bool
	External    bool
	Package     string
	ParityIDs   []string
}

type paritySymbolIndex map[string]paritySymbol

type parityFingerprintScope struct {
	imports   map[string]string
	renames   map[string]string
	canonical string
	owners    *parityTypeOwners
	known     *bool
}

type parityTypeOwnerSource struct {
	canonical string
	spec      *ast.TypeSpec
	imports   map[string]string
	known     bool
}

type parityTypeOwner struct {
	header         string
	renamed        map[string]string
	comparability  string
	parameterCount int
	known          bool
}

type parityImportedPackage struct {
	canonical    string
	declaredName string
}

type parityTypeOwners struct {
	sources              map[string]parityTypeOwnerSource
	cache                map[string]parityTypeOwner
	resolving            map[string]bool
	aliases              map[string]parityDependencyFingerprint
	resolvingAliases     map[string]bool
	constraints          map[string]parityDependencyFingerprint
	resolvingConstraints map[string]bool
	packages             map[string]parityImportedPackage
}

type parityDependencyFingerprint struct {
	fingerprint string
	known       bool
}

func TestParityManifestFoundation(t *testing.T) {
	t.Parallel()
	document, err := decodeParityManifest(parityManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	root := parityModuleRoot(t)
	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	if issues := validateParityManifest(document, index, root); len(issues) != 0 {
		for _, issue := range issues {
			t.Error(issue)
		}
	}
}

func TestParityManifestRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries = append(document.Entries, document.Entries[0])

	issues := validateParityManifest(document, paritySymbolIndex{}, t.TempDir())

	assertParityIssue(t, issues, "duplicate entry id")
}

func TestParityManifestRejectsMalformedSourceDigests(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.SourceDigests = []paritySourceDigest{
		{Path: "src/libtmux/z.py", Digest: "invalid"},
		{Path: "src/libtmux/a.py", Digest: "sha256:" + strings.Repeat("0", 64)},
		{Path: "src/libtmux/a.py", Digest: "sha256:" + strings.Repeat("0", 64)},
	}

	issues := validateParityManifest(document, paritySymbolIndex{}, t.TempDir())

	assertParityIssue(t, issues, "invalid source digest: src/libtmux/z.py")
	assertParityIssue(t, issues, "source digests are not sorted")
	assertParityIssue(t, issues, "duplicate source digest path: src/libtmux/a.py")
}

func TestParityManifestRejectsUnresolvedCompletedMapping(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Missing"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestMissing"},
	}
	setParityPolicies(&document.Entries[0])

	issues := validateParityManifest(document, paritySymbolIndex{}, t.TempDir())

	assertParityIssue(t, issues, "Go symbol does not exist")
	assertParityIssue(t, issues, "proof test does not exist")
}

func TestParityManifestAcceptsResolvedHandwrittenMapping(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestParityManifestFoundation"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":                        {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestParityManifestFoundation": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	if len(issues) != 0 {
		t.Fatalf("validate resolved mapping: %v", issues)
	}
}

func TestParityManifestRejectsOpenStatusAndTranslation(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "excluded"
	second := document.Entries[0]
	second.ID = "python.second"
	second.Status = "translation"
	second.Translation = "skip-it"
	setParityPolicies(&second)
	second.Difference = "language-translation"
	document.Entries = append(document.Entries, second)

	issues := validateParityManifest(document, paritySymbolIndex{}, t.TempDir())

	assertParityIssue(t, issues, "invalid status")
	assertParityIssue(t, issues, "unknown translation")
}

func TestParityManifestAcceptsDeprecatedAliasTranslation(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "translation"
	document.Entries[0].Translation = "deprecated-alias"
	document.Entries[0].Difference = "language-translation"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestParityManifestFoundation"},
	}
	setParityPolicies(&document.Entries[0])
	document.Entries[0].Difference = "language-translation"
	index := paritySymbolIndex{
		"tmux.Server.Cmd": {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestParityManifestFoundation": {
			Proof: true, Behavior: true, External: true, Package: "tmux_test",
			ParityIDs: []string{"python.symbol"},
		},
	}

	if issues := validateParityManifest(document, index, t.TempDir()); len(issues) != 0 {
		t.Fatalf("validate deprecated alias translation: %v", issues)
	}
}

func TestParityManifestAcceptsClosedOmission(t *testing.T) {
	t.Parallel()
	for _, translation := range []string{
		"deprecated-python-omission",
		"python-package-metadata-omission",
	} {
		t.Run(translation, func(t *testing.T) {
			t.Parallel()
			document := minimalParityManifest()
			document.Entries[0].Status = "translation"
			document.Entries[0].Translation = translation
			document.Entries[0].Proof = []parityProof{
				{Kind: "unit", Test: "tmux_test.TestOmission"},
			}
			setParityPolicies(&document.Entries[0])
			document.Entries[0].Difference = "language-translation"
			index := paritySymbolIndex{
				"tmux_test.TestOmission": {
					Proof: true, Behavior: true, External: true, Package: "tmux_test",
					ParityIDs: []string{"python.symbol"},
				},
			}

			if issues := validateParityManifest(document, index, t.TempDir()); len(issues) != 0 {
				t.Fatalf("validate %s translation: %v", translation, issues)
			}
		})
	}
}

func TestParityManifestRejectsGoSymbolOnClosedOmission(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "translation"
	document.Entries[0].Translation = "deprecated-python-omission"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestOmission"},
	}
	setParityPolicies(&document.Entries[0])
	document.Entries[0].Difference = "language-translation"
	index := paritySymbolIndex{
		"tmux.Server.Cmd": {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestOmission": {
			Proof: true, Behavior: true, External: true, Package: "tmux_test",
			ParityIDs: []string{"python.symbol"},
		},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "omission translation must not declare Go symbols")
}

func TestParityManifestRequiresGoSymbolForOtherTranslation(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "translation"
	document.Entries[0].Translation = "deprecated-alias"
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestAlias"},
	}
	setParityPolicies(&document.Entries[0])
	document.Entries[0].Difference = "language-translation"
	index := paritySymbolIndex{
		"tmux_test.TestAlias": {
			Proof: true, Behavior: true, External: true, Package: "tmux_test",
			ParityIDs: []string{"python.symbol"},
		},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "Go symbol is required")
}

func TestParityManifestRequiresGeneratedSpec(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "generated"
	document.Entries[0].Go = []string{"tmux.ServerOptions.Binary"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "compile", Test: "tmux_test.TestParityManifestFoundation"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.ServerOptions.Binary":              {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestParityManifestFoundation": {Proof: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "generated spec is required")
}

func TestParityIndexAcceptsOnlyTestDeclarations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"production.go": `package fixture

import "testing"

func TestProduction(*testing.T) {}

type privateType struct {
	ExportedField string
}

type PublicType struct {
	ExportedField string
}
`,
		"proof_test.go": `package fixture_test

import "testing"

func TestValid(*testing.T) {}
// libtmux:parity python.first
// libtmux:parity python.second
func TestBehavior(*testing.T) { exercise() }
func TestWrong() {}
func ExampleValid() {}
func ExampleBehavior() {
	println("ok")
	// Output:
	// ok
}
func Example() {
	println("root")
	// Output:
	// root
}

//libtmux:real-tmux
func TestRealTmux(*testing.T) { startTmux() }
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}

	if index["fixture.TestProduction"].Proof {
		t.Error("production declaration accepted as proof")
	}
	if !index["fixture_test.TestValid"].Proof {
		t.Error("valid test declaration was not accepted as proof")
	}
	if index["fixture_test.TestValid"].Behavior {
		t.Error("empty test accepted as behavioral proof")
	}
	if !index["fixture_test.TestBehavior"].Behavior {
		t.Error("test with executable evidence was not accepted as behavioral proof")
	}
	if !slices.Contains(index["fixture_test.TestBehavior"].ParityIDs, "python.first") ||
		!slices.Contains(index["fixture_test.TestBehavior"].ParityIDs, "python.second") {
		t.Error("test parity ID markers were not indexed")
	}
	if index["fixture_test.TestWrong"].Proof {
		t.Error("wrong test signature accepted as proof")
	}
	if !index["fixture_test.ExampleValid"].Proof {
		t.Error("valid example declaration was not accepted as proof")
	}
	if index["fixture_test.ExampleValid"].Behavior {
		t.Error("example without output accepted as behavioral proof")
	}
	if !index["fixture_test.ExampleBehavior"].Behavior {
		t.Error("example with output was not accepted as behavioral proof")
	}
	if !index["fixture_test.Example"].Behavior {
		t.Error("package-level example with output was not accepted as behavioral proof")
	}
	if !index["fixture_test.TestRealTmux"].RealTmux {
		t.Error("marked external test was not accepted as real-tmux proof")
	}
	if index["fixture.privateType.ExportedField"].Exported {
		t.Error("exported field on private type accepted as public destination")
	}
	if !index["fixture.PublicType.ExportedField"].Exported {
		t.Error("exported field on public type was not accepted as public destination")
	}
	if !index["fixture.PublicType"].Production || !index["fixture.PublicType"].Importable {
		t.Error("public production declaration was not indexed as an importable destination")
	}
	if index["fixture_test.TestBehavior"].Production {
		t.Error("test declaration was indexed as a production destination")
	}
}

func TestParityIndexSeparatesPackagesWithTheSameDeclaredName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	generatedDirectory := filepath.Join(root, "internal", "generate", "filters")
	handwrittenDirectory := filepath.Join(root, "internal", "generate", "formats")
	for _, directory := range []string{generatedDirectory, handwrittenDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(generatedDirectory, "main.go"),
		[]byte("// Code generated by parity fixture. DO NOT EDIT.\npackage main\n\nfunc Shared() {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(handwrittenDirectory, "main.go"),
		[]byte("package main\n\nfunc Shared() {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	generated := index["internal/generate/filters.Shared"]
	if !generated.Generated {
		t.Fatalf("filters.Shared = %#v, want generated declaration", generated)
	}
	handwritten, ok := index["internal/generate/formats.Shared"]
	if !ok || handwritten.Generated {
		t.Fatalf("formats.Shared = %#v, want distinct handwritten declaration", handwritten)
	}
	if _, ok := index["main.Shared"]; ok {
		t.Fatal("same-named package declarations were merged under main.Shared")
	}
}

func TestParityIndexRejectsTestOnlyAndInaccessibleDestinations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"fixture.go":           "package fixture\n\ntype PublicAPI struct{}\n",
		"only_test.go":         "package fixture\n\ntype TestOnlyAPI struct{}\n",
		"proof_test.go":        "package fixture_test\n\nimport \"testing\"\n\n// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
		"internal/hidden/x.go": "package hidden\n\ntype InternalAPI struct{}\n",
		"cmd/tool/main.go":     "package main\n\ntype MainAPI struct{}\n",
	}
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	if symbol := index["fixture.PublicAPI"]; !symbol.Production || !symbol.Importable {
		t.Fatalf("fixture.PublicAPI = %#v, want importable production destination", symbol)
	}
	if symbol := index["fixture.TestOnlyAPI"]; symbol.Production {
		t.Fatalf("fixture.TestOnlyAPI = %#v, want test-only declaration", symbol)
	}
	if symbol := index["internal/hidden.InternalAPI"]; !symbol.Production || symbol.Importable {
		t.Fatalf("internal/hidden.InternalAPI = %#v, want inaccessible production declaration", symbol)
	}
	if symbol := index["cmd/tool.MainAPI"]; !symbol.Production || symbol.Importable {
		t.Fatalf("cmd/tool.MainAPI = %#v, want nonimportable main declaration", symbol)
	}

	for _, test := range []struct {
		name  string
		issue string
	}{
		{name: "fixture.TestOnlyAPI", issue: "Go symbol is not a production declaration"},
		{name: "internal/hidden.InternalAPI", issue: "public Python mapping requires an importable Go destination"},
		{name: "cmd/tool.MainAPI", issue: "public Python mapping requires an importable Go destination"},
	} {
		document := minimalParityManifest()
		document.Entries[0].Status = "handwritten"
		document.Entries[0].Go = []string{test.name}
		document.Entries[0].Proof = []parityProof{{Kind: "unit", Test: "fixture_test.TestProof"}}
		setParityPolicies(&document.Entries[0])

		issues := validateParityManifest(document, index, root)

		assertParityIssue(t, issues, test.issue)
	}
}

func TestParityIndexUsesUnambiguousExternalTestPackageKeys(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"alpha/alpha.go":           "package alpha\n",
		"alpha/proof_test.go":      "package alpha_test\n\nimport \"testing\"\n\nfunc TestProof(*testing.T) { exercise() }\n",
		"alpha_test/api.go":        "package alpha_test\n\ntype ProductionAPI struct{}\n",
		"alpha_test/proof_test.go": "package alpha_test\n\nimport \"testing\"\n\nfunc TestInternal(*testing.T) { exercise() }\n",
	}
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	if !index["alpha#test.TestProof"].Proof {
		t.Fatalf("alpha external proof = %#v, want proof declaration", index["alpha#test.TestProof"])
	}
	if !index["alpha_test.ProductionAPI"].Production {
		t.Fatalf("alpha_test production = %#v, want production declaration", index["alpha_test.ProductionAPI"])
	}
	if !index["alpha_test.TestInternal"].Proof {
		t.Fatalf("alpha_test internal proof = %#v, want proof declaration", index["alpha_test.TestInternal"])
	}
}

func TestParityIndexRejectsAmbiguousPackageAliases(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"root.go":      "package tmux\n\ntype RootAPI struct{}\n",
		"tmux/tmux.go": "package tmux\n\ntype NestedAPI struct{}\n",
	}
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := indexParityGoSymbols(root)
	if err == nil || !strings.Contains(err.Error(), "ambiguous parity package alias") {
		t.Fatalf("index ambiguous aliases: %v", err)
	}
}

func TestParityIndexSkipsVendorAndNestedModules(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"root.go":                  "package fixture\n\ntype RootAPI struct{}\n",
		"nested/go.mod":            "module example.invalid/nested\n\ngo 1.23\n",
		"nested/nested.go":         "package nested\n\ntype NestedAPI struct{}\n",
		"vendor/acme/vendor.go":    "package acme\n\ntype VendorAPI struct{}\n",
		"testdata/data/ignored.go": "package data\n\ntype DataAPI struct{}\n",
	}
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	if !index["fixture.RootAPI"].Production {
		t.Fatal("root production declaration was not indexed")
	}
	for _, name := range []string{"nested.NestedAPI", "vendor/acme.VendorAPI", "testdata/data.DataAPI"} {
		if _, exists := index[name]; exists {
			t.Fatalf("excluded declaration %q was indexed", name)
		}
	}
}

func TestParityManifestRejectsHostOnlyPublicDestination(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("host-only parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := map[string]string{
		"api_linux.go":  "package fixture\n\ntype HostOnlyAPI struct{}\n",
		"proof_test.go": "package fixture_test\n\nimport \"testing\"\n\n// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"fixture.HostOnlyAPI"}
	document.Entries[0].Proof = []parityProof{{Kind: "unit", Test: "fixture_test.TestProof"}}
	setParityPolicies(&document.Entries[0])

	issues := validateParityManifest(document, index, root)

	assertParityIssue(t, issues, "public Python mapping requires a portable Go destination")
}

func TestParityManifestRejectsCrossPlatformPackageAlias(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := map[string]string{
		"api_linux.go":      "package tmux\n\ntype API struct{}\n",
		"proof_test.go":     "package tmux_test\n\nimport \"testing\"\n\n// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
		"tmux/api_other.go": "//go:build !linux\n\npackage tmux\n\ntype API struct{}\n",
	}
	writeParityFixtureFiles(t, root, files)

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedParityFixture("tmux.API", "tmux_test.TestProof")

	issues := validateParityManifest(document, index, root)

	assertParityIssue(t, issues, "public Python mapping requires a portable Go destination")
}

func TestParityManifestRejectsCrossPlatformSignatureDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := map[string]string{
		"api_unix.go":    "//go:build !windows && !plan9\n\npackage fixture\n\nfunc Portable(value int) int { return value }\n",
		"api_windows.go": "package fixture\n\nfunc Portable(value string) string { return value }\n",
		"api_plan9.go":   "package fixture\n\nfunc Portable(value bool) bool { return value }\n",
		"proof_test.go":  "package fixture_test\n\nimport \"testing\"\n\n// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
	}
	files["api_windows.go"] = "//go:build windows\n\n" + files["api_windows.go"]
	files["api_plan9.go"] = "//go:build plan9\n\n" + files["api_plan9.go"]
	writeParityFixtureFiles(t, root, files)

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedParityFixture("fixture.Portable", "fixture_test.TestProof")

	issues := validateParityManifest(document, index, root)

	assertParityIssue(t, issues, "public Python mapping requires a portable Go destination")
}

func TestParityManifestRequiresGeneratedDestinationOnEveryPlatform(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	generated := "// Code generated by parity fixture. DO NOT EDIT.\n"
	files := map[string]string{
		"api_unix.go":    generated + "//go:build !windows && !plan9\n\npackage fixture\n\ntype API struct{}\n",
		"api_windows.go": "//go:build windows\n\npackage fixture\n\ntype API struct{}\n",
		"api_plan9.go":   generated + "//go:build plan9\n\npackage fixture\n\ntype API struct{}\n",
		"proof_test.go":  "package fixture_test\n\nimport \"testing\"\n\n// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
		"spec.json":      "{}\n",
	}
	writeParityFixtureFiles(t, root, files)

	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedParityFixture("fixture.API", "fixture_test.TestProof")
	document.Entries[0].Status = "generated"
	document.Entries[0].Spec = "spec.json"
	document.Entries[0].Proof[0].Kind = "golden"

	issues := validateParityManifest(document, index, root)

	assertParityIssue(t, issues, "generated Go symbol is not in generated source")
}

func TestParityManifestRejectsCrossPlatformTypeConstraintDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type API[T any] struct{}\n",
		"type API[T comparable] struct{}\n",
		"type API[T ~int] struct{}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformContainerRoleDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type API struct { M func() }\n",
		"type API interface { M() }\n",
		"type API struct { M func() }\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API.M",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformInferredValueType(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"var platformValue int\n",
		"var platformValue string\n",
		"var platformValue bool\n",
	)
	files["api.go"] = "package fixture\n\nvar API = platformValue\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformImportedTypeIdentityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"import dep \"example.com/unix\"\n\nfunc API(dep.T) {}\n",
		"import dep \"example.com/windows\"\n\nfunc API(dep.T) {}\n",
		"import dep \"example.com/plan9\"\n\nfunc API(dep.T) {}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformLocalAliasDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type PlatformAlias = int\n\nfunc API(PlatformAlias) {}\n",
		"type PlatformAlias = string\n\nfunc API(PlatformAlias) {}\n",
		"type PlatformAlias = bool\n\nfunc API(PlatformAlias) {}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformImportedAliasDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\nfunc API(dep.PlatformAlias) {}\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/alias_unix.go"] = "//go:build !windows && !plan9\n\npackage dep\n\ntype PlatformAlias = int\n"
	files["dep/alias_windows.go"] = "//go:build windows\n\npackage dep\n\ntype PlatformAlias = string\n"
	files["dep/alias_plan9.go"] = "//go:build plan9\n\npackage dep\n\ntype PlatformAlias = bool\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsUnresolvedArrayLengthConstant(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"const arrayLength = 1\n\nfunc API([arrayLength]int) {}\n",
		"const arrayLength = 2\n\nfunc API([arrayLength]int) {}\n",
		"const arrayLength = 3\n\nfunc API([arrayLength]int) {}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsUnresolvedImportedArrayLengthConstant(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\nfunc API([dep.ArrayLength]int) {}\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/length_unix.go"] = "//go:build !windows && !plan9\n\npackage dep\n\nconst ArrayLength = 1\n"
	files["dep/length_windows.go"] = "//go:build windows\n\npackage dep\n\nconst ArrayLength = 2\n"
	files["dep/length_plan9.go"] = "//go:build plan9\n\npackage dep\n\nconst ArrayLength = 3\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestExcludesImplicitCgoFilesFromPortableDestinations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeParityFixtureFiles(t, root, map[string]string{
		"api.go":  "package fixture\n\nimport \"C\"\n\nfunc API() {}\n",
		"base.go": "package fixture\n\ntype Base struct{}\n",
	})

	buildContext := build.Default
	buildContext.CgoEnabled = false
	index, err := indexParityGoSymbolsForContext(root, buildContext)
	if err != nil {
		t.Fatal(err)
	}
	if symbol, exists := index["fixture.API"]; exists {
		t.Fatalf("implicit cgo symbol fixture.API = %#v, want excluded", symbol)
	}
}

func TestParityManifestRequiresMainstreamArm64DestinationCoverage(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("cross-architecture parity regression is exercised by the Linux amd64 quality lane")
	}
	root := t.TempDir()
	writeParityFixtureFiles(t, root, map[string]string{
		"api_amd64.go": "package fixture\n\nfunc API() {}\n",
		"api_ppc64.go": "package fixture\n\nfunc API() {}\n",
		"proof_test.go": "package fixture_test\n\nimport \"testing\"\n\n" +
			"// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
	})

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformOwnerConstraintDriftForField(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type API[T any] struct { Field T }\n",
		"type API[T comparable] struct { Field T }\n",
		"type API[T ~int] struct { Field T }\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API.Field",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsNamedConstraintDependencyDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type Constraint interface { ~int }\n\ntype API[T Constraint] struct{}\n",
		"type Constraint interface { ~string }\n\ntype API[T Constraint] struct{}\n",
		"type Constraint interface { ~bool }\n\ntype API[T Constraint] struct{}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsEmbeddedInterfaceDependencyDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type Embedded interface { Left() }\n\ntype API interface { Embedded }\n",
		"type Embedded interface { Right() }\n\ntype API interface { Embedded }\n",
		"type Embedded interface { Plan9() }\n\ntype API interface { Embedded }\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsImportedEmbeddedInterfaceDependencyDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\ntype API interface { dep.Embedded }\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/interface_unix.go"] = "//go:build !windows && !plan9\n\npackage dep\n\ntype Embedded interface { Left() }\n"
	files["dep/interface_windows.go"] = "//go:build windows\n\npackage dep\n\ntype Embedded interface { Right() }\n"
	files["dep/interface_plan9.go"] = "//go:build plan9\n\npackage dep\n\ntype Embedded interface { Plan9() }\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsInstantiatedConstraintDependencyDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type Constraint[T any] interface { ~[]T }\n\ntype API[T Constraint[int]] struct{}\n",
		"type Constraint[T any] interface { ~map[string]T }\n\ntype API[T Constraint[int]] struct{}\n",
		"type Constraint[T any] interface { ~chan T }\n\ntype API[T Constraint[int]] struct{}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsImportedInstantiatedConstraintDependencyDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\ntype API[T dep.Constraint[int]] struct{}\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/constraint_unix.go"] = "//go:build !windows && !plan9\n\npackage dep\n\ntype Constraint[T any] interface { ~[]T }\n"
	files["dep/constraint_windows.go"] = "//go:build windows\n\npackage dep\n\ntype Constraint[T any] interface { ~map[string]T }\n"
	files["dep/constraint_plan9.go"] = "//go:build plan9\n\npackage dep\n\ntype Constraint[T any] interface { ~chan T }\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCrossPlatformComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type API struct { hidden int }\n",
		"type API struct { hidden []int }\n",
		"type API struct { hidden int }\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsShadowedPredeclaredComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type int []byte\n\ntype API struct { hidden int }\n",
		"type int struct{}\n\ntype API struct { hidden int }\n",
		"type int []byte\n\ntype API struct { hidden int }\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsAnonymousStructComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"func API(struct { hidden int }) {}\n",
		"func API(struct { hidden []int }) {}\n",
		"func API(struct { hidden int }) {}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsAliasAnonymousStructComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"type Payload = struct { hidden int }\n\nfunc API(Payload) {}\n",
		"type Payload = struct { hidden []int }\n\nfunc API(Payload) {}\n",
		"type Payload = struct { hidden int }\n\nfunc API(Payload) {}\n",
	)
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsDoubleDigitTypeParameterComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	comparableSource := "type API struct { hidden Many[int, int, int, int, int, int, int, int, int, int, int] }\n"
	noncomparable := "type API struct { hidden Many[int, int, int, int, int, int, int, int, int, int, []int] }\n"
	files := crossPlatformParityFiles(comparableSource, noncomparable, comparableSource)
	files["common.go"] = "package fixture\n\ntype Many[T0, T1, T2, T3, T4, T5, T6, T7, T8, T9, T10 any] struct { Value T10 }\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsCapturedGenericComparabilitySubstitution(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	typeParameters := "T0, T1, T2, T3, T4, T5, T6, T7, T8, T9, T10 any"
	dependent := "type API[" + typeParameters + "] struct { hidden Many[int, int, int, int, int, int, int, int, int, int, T1] }\n"
	alwaysComparable := "type API[" + typeParameters + "] struct { hidden Many[int, int, int, int, int, int, int, int, int, int, int] }\n"
	files := crossPlatformParityFiles(dependent, alwaysComparable, dependent)
	files["common.go"] = "package fixture\n\ntype Many[" + typeParameters + "] struct { Value T10 }\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestRejectsExternalGenericArgumentComparabilityDrift(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	comparableSource := "import dep \"example.invalid/dep\"\n\ntype API struct { hidden dep.Box[int] }\n"
	noncomparable := "import dep \"example.invalid/dep\"\n\ntype API struct { hidden dep.Box[[]int] }\n"
	writeParityFixtureFiles(
		t,
		root,
		crossPlatformParityFiles(comparableSource, noncomparable, comparableSource),
	)

	assertParityDestinationIssue(
		t,
		root,
		"fixture.API",
		"public Python mapping requires a portable Go destination",
	)
}

func TestParityManifestAcceptsEquivalentCrossPlatformDeclarationShapes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	tests := []struct {
		name    string
		unix    string
		windows string
		plan9   string
		symbol  string
	}{
		{
			name:    "parameter names",
			unix:    "func API(value int) (result int) { return value }\n",
			windows: "func API(v int) (out int) { return v }\n",
			plan9:   "func API(int) int { return 0 }\n",
			symbol:  "fixture.API",
		},
		{
			name:    "field grouping",
			unix:    "type API struct { A, B int }\n",
			windows: "type API struct { A int; B int }\n",
			plan9:   "type API struct { A int; B int }\n",
			symbol:  "fixture.API",
		},
		{
			name:    "unrelated value sibling",
			unix:    "var ( API int; privateUnix string )\n",
			windows: "var ( privateWindows bool; API int )\n",
			plan9:   "var API int\n",
			symbol:  "fixture.API",
		},
		{
			name:    "import alias spelling",
			unix:    "import left \"example.com/dep\"\n\nfunc API(left.T) {}\n",
			windows: "import right \"example.com/dep\"\n\nfunc API(right.T) {}\n",
			plan9:   "import dependency \"example.com/dep\"\n\nfunc API(dependency.T) {}\n",
			symbol:  "fixture.API",
		},
		{
			name:    "transparent local aliases",
			unix:    "type Left = int\n\nfunc API(Left) {}\n",
			windows: "type Right = int\n\nfunc API(Right) {}\n",
			plan9:   "type Value = int\n\nfunc API(Value) {}\n",
			symbol:  "fixture.API",
		},
		{
			name:    "owner type parameter spelling",
			unix:    "type API[T any] struct { Field T }\n",
			windows: "type API[Value any] struct { Field Value }\n",
			plan9:   "type API[Element any] struct { Field Element }\n",
			symbol:  "fixture.API.Field",
		},
		{
			name: "generic method receiver spelling",
			unix: "type Box[T any] struct{}\n" +
				"func (Box[T]) API(value T) T { return value }\n",
			windows: "type Box[Value any] struct{}\n" +
				"func (Box[Value]) API(value Value) Value { return value }\n",
			plan9: "type Box[Element any] struct{}\n" +
				"func (Box[Element]) API(value Element) Element { return value }\n",
			symbol: "fixture.Box.API",
		},
		{
			name:    "nested function parameter spelling",
			unix:    "func API(callback func(value int) (result string)) {}\n",
			windows: "func API(callback func(v int) (out string)) {}\n",
			plan9:   "func API(func(int) string) {}\n",
			symbol:  "fixture.API",
		},
		{
			name:    "interface union term order",
			unix:    "type API interface { ~int | ~string }\n",
			windows: "type API interface { ~string | ~int }\n",
			plan9:   "type API interface { ~int | ~string }\n",
			symbol:  "fixture.API",
		},
		{
			name:    "unexported comparable representation",
			unix:    "type API struct { unixOnly int }\n",
			windows: "type API struct { windowsOnly string }\n",
			plan9:   "type API struct { plan9Only bool }\n",
			symbol:  "fixture.API",
		},
		{
			name:    "anonymous unexported comparable representation",
			unix:    "func API(struct { unixOnly int }) {}\n",
			windows: "func API(struct { windowsOnly string }) {}\n",
			plan9:   "func API(struct { plan9Only bool }) {}\n",
			symbol:  "fixture.API",
		},
		{
			name:    "external generic comparable arguments",
			unix:    "import dep \"example.invalid/dep\"\n\ntype API struct { hidden dep.Box[int] }\n",
			windows: "import dep \"example.invalid/dep\"\n\ntype API struct { hidden dep.Box[string] }\n",
			plan9:   "import dep \"example.invalid/dep\"\n\ntype API struct { hidden dep.Box[bool] }\n",
			symbol:  "fixture.API",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeParityFixtureFiles(
				t,
				root,
				crossPlatformParityFiles(test.unix, test.windows, test.plan9),
			)

			index, err := indexParityGoSymbols(root)
			if err != nil {
				t.Fatal(err)
			}
			document := resolvedParityFixture(test.symbol, "fixture_test.TestProof")
			if issues := validateParityManifest(document, index, root); len(issues) != 0 {
				t.Fatalf("validate equivalent declaration shapes: %v", issues)
			}
		})
	}
}

func TestParityManifestAcceptsInModuleDeclaredImportName(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	files := crossPlatformParityFiles(
		"import \"example.invalid/fixture/dep\"\n\nfunc API(actual.T) {}\n",
		"import \"example.invalid/fixture/dep\"\n\nfunc API(actual.T) {}\n",
		"import \"example.invalid/fixture/dep\"\n\nfunc API(actual.T) {}\n",
	)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/type.go"] = "package actual\n\ntype T int\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationAccepted(t, root, "fixture.API")
}

func TestParityManifestAcceptsInModuleImportedComparableField(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\ntype API struct { Field dep.T }\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/type.go"] = "package dep\n\ntype T int\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationAccepted(t, root, "fixture.API.Field")
}

func TestParityManifestAcceptsInModuleImportedGenericComparableField(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\ntype API struct { Field dep.Box[int] }\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/type.go"] = "package dep\n\ntype Box[T any] struct { Value T }\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationAccepted(t, root, "fixture.API.Field")
}

func TestParityManifestAcceptsStableExternalComparableContract(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"time\"\n\ntype API struct { Field time.Duration }\n"
	writeParityFixtureFiles(t, root, crossPlatformParityFiles(declaration, declaration, declaration))

	assertParityDestinationAccepted(t, root, "fixture.API.Field")
}

func TestParityManifestAcceptsStableInModuleImportedAlias(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("cross-platform parity regression is exercised by the Linux quality lane")
	}
	root := t.TempDir()
	declaration := "import \"example.invalid/fixture/dep\"\n\nfunc API(dep.StableAlias) {}\n"
	files := crossPlatformParityFiles(declaration, declaration, declaration)
	files["go.mod"] = "module example.invalid/fixture\n\ngo 1.23.0\n"
	files["dep/type.go"] = "package dep\n\ntype StableAlias = int\n"
	writeParityFixtureFiles(t, root, files)

	assertParityDestinationAccepted(t, root, "fixture.API")
}

func crossPlatformParityFiles(unix, windows, plan9 string) map[string]string {
	return map[string]string{
		"api_unix.go":    "//go:build !windows && !plan9\n\npackage fixture\n\n" + unix,
		"api_windows.go": "//go:build windows\n\npackage fixture\n\n" + windows,
		"api_plan9.go":   "//go:build plan9\n\npackage fixture\n\n" + plan9,
		"proof_test.go": "package fixture_test\n\nimport \"testing\"\n\n" +
			"// libtmux:parity python.symbol\nfunc TestProof(*testing.T) { exercise() }\n",
	}
}

func assertParityDestinationIssue(t *testing.T, root, symbol, issue string) {
	t.Helper()
	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedParityFixture(symbol, "fixture_test.TestProof")
	issues := validateParityManifest(document, index, root)
	assertParityIssue(t, issues, issue)
}

func assertParityDestinationAccepted(t *testing.T, root, symbol string) {
	t.Helper()
	index, err := indexParityGoSymbols(root)
	if err != nil {
		t.Fatal(err)
	}
	document := resolvedParityFixture(symbol, "fixture_test.TestProof")
	if issues := validateParityManifest(document, index, root); len(issues) != 0 {
		t.Fatalf("validate portable destination %s: %v", symbol, issues)
	}
}

func writeParityFixtureFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func resolvedParityFixture(symbol, proof string) parityManifest {
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{symbol}
	document.Entries[0].Proof = []parityProof{{Kind: "unit", Test: proof}}
	setParityPolicies(&document.Entries[0])
	return document
}

func TestParityProofGrammarAcceptsDottedPackagePaths(t *testing.T) {
	t.Parallel()
	if !parityTestName("example.test#test.TestProof") {
		t.Fatal("dotted external-test package key was rejected")
	}
	if !parityTestName("tmux_test.Example") {
		t.Fatal("valid package-level Example proof was rejected")
	}
}

func TestParityManifestRejectsNonTestProofDeclaration(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestLooksValid"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":          {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestLooksValid": {Package: "tmux_test"},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "proof is not a valid test declaration")
}

func TestParityManifestRequiresMarkedRealTmuxProof(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "real-tmux", Test: "tmux_test.TestExternal"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":        {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestExternal": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "real-tmux proof requires marked evidence")
}

func TestParityManifestRequiresGeneratedEvidenceKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "options.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := minimalParityManifest()
	document.Entries[0].Status = "generated"
	document.Entries[0].Go = []string{"tmux.ServerOptions.Binary"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestGeneratedOptions"},
	}
	document.Entries[0].Spec = "options.json"
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.ServerOptions.Binary":      {Generated: true, Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestGeneratedOptions": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, root)

	assertParityIssue(t, issues, "generated mapping requires generated evidence")
}

func TestParityManifestRejectsCompileOnlyBehaviorProof(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "compile", Test: "tmux_test.TestServerCmd"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":         {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestServerCmd": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "compile-only proof cannot prove behavior")
}

func TestParityManifestRequiresClosedPolicyMetadata(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestServerCmd"},
	}
	index := paritySymbolIndex{
		"tmux.Server.Cmd":         {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestServerCmd": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	for _, field := range []string{"tmux", "warning", "projection", "difference"} {
		assertParityIssue(t, issues, field+" policy is required")
	}
}

func TestParityManifestRejectsNoOutputBehaviorExample(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.ExampleServerCmd"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":            {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.ExampleServerCmd": {Proof: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "behavior proof example has no output")
}

func TestParityManifestRejectsNoOpBehaviorTest(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestNoOp"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":    {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestNoOp": {Proof: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "behavior proof test has no executable evidence")
}

func TestParityManifestRequiresExportedPublicDestination(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.server.cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestServerCmd"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.server.cmd":         {Production: true, Importable: true, Portable: true},
		"tmux_test.TestServerCmd": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.symbol"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "public Python mapping requires an exported Go destination")
}

func TestParityManifestAllowsPrivateDestinationForPrivateObligation(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].ID = "libtmux.server._helper"
	document.Entries[0].Kind = "private-function"
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.server.cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestServerCmd"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.server.cmd":         {Production: true, Importable: true, Portable: true},
		"tmux_test.TestServerCmd": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"libtmux.server._helper"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	if len(issues) != 0 {
		t.Fatalf("validate private mapping: %v", issues)
	}
}

func TestParityManifestRejectsUnboundProof(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Status = "handwritten"
	document.Entries[0].Go = []string{"tmux.Server.Cmd"}
	document.Entries[0].Proof = []parityProof{
		{Kind: "unit", Test: "tmux_test.TestServerCmd"},
	}
	setParityPolicies(&document.Entries[0])
	index := paritySymbolIndex{
		"tmux.Server.Cmd":         {Exported: true, Production: true, Importable: true, Portable: true},
		"tmux_test.TestServerCmd": {Proof: true, Behavior: true, External: true, Package: "tmux_test", ParityIDs: []string{"python.other"}},
	}

	issues := validateParityManifest(document, index, t.TempDir())

	assertParityIssue(t, issues, "proof does not claim parity entry")
	assertParityIssue(t, issues, "proof marker names unknown parity entry")
}

func TestParityManifestRequiresNormalizedVersionRange(t *testing.T) {
	t.Parallel()
	document := minimalParityManifest()
	document.Entries[0].Kind = "version-branch"
	document.Entries[0].VersionRange = "3.4+"

	issues := validateParityManifest(document, paritySymbolIndex{}, t.TempDir())

	assertParityIssue(t, issues, "invalid version range")
	document.Entries[0].VersionRange = ""
	issues = validateParityManifest(document, paritySymbolIndex{}, t.TempDir())
	assertParityIssue(t, issues, "version range is required")
}

func decodeParityManifest(data []byte) (parityManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document parityManifest
	if err := decoder.Decode(&document); err != nil {
		return parityManifest{}, fmt.Errorf("decode parity manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return parityManifest{}, fmt.Errorf("decode parity manifest trailing data: %w", err)
	}
	return document, nil
}

// parityModuleRoot is the directory holding go.mod. The parity index covers
// every package in the module, and this package is not the module root.
func parityModuleRoot(t *testing.T) string {
	t.Helper()
	return documentationModuleRoot(t)
}

func indexParityGoSymbols(root string) (paritySymbolIndex, error) {
	hostIndex, err := indexParityGoSymbolsForContext(root, build.Default)
	if err != nil {
		return nil, err
	}

	targets := []struct {
		goos   string
		goarch string
	}{
		{goos: "aix", goarch: "ppc64"},
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "dragonfly", goarch: "amd64"},
		{goos: "freebsd", goarch: "amd64"},
		{goos: "illumos", goarch: "amd64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "netbsd", goarch: "amd64"},
		{goos: "openbsd", goarch: "amd64"},
		{goos: "solaris", goarch: "amd64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "plan9", goarch: "amd64"},
	}
	contextIndexes := make([]paritySymbolIndex, 0, len(targets))
	for _, target := range targets {
		buildContext := build.Default
		buildContext.GOOS = target.goos
		buildContext.GOARCH = target.goarch
		buildContext.CgoEnabled = false
		index, err := indexParityGoSymbolsForContext(root, buildContext)
		if err != nil {
			return nil, fmt.Errorf(
				"index parity symbols for %s/%s: %w",
				target.goos,
				target.goarch,
				err,
			)
		}
		contextIndexes = append(contextIndexes, index)
	}

	for name, symbol := range hostIndex {
		if !symbol.Production {
			continue
		}
		portable := symbol.ShapeKnown
		importable := symbol.Importable
		generated := symbol.Generated
		for _, index := range contextIndexes {
			declaration, exists := index[name]
			if !exists || !declaration.Production {
				portable = false
				generated = false
				continue
			}
			importable = importable && declaration.Importable
			generated = generated && declaration.Generated
			portable = portable &&
				declaration.ShapeKnown &&
				declaration.Canonical == symbol.Canonical &&
				declaration.Fingerprint == symbol.Fingerprint
		}
		symbol.Portable = portable
		symbol.Importable = importable
		symbol.Generated = generated
		hostIndex[name] = symbol
	}
	return hostIndex, nil
}

func indexParityGoSymbolsForContext(root string, buildContext build.Context) (paritySymbolIndex, error) {
	type sourceFile struct {
		path              string
		directory         string
		file              *ast.File
		generated         bool
		test              bool
		external          bool
		packageKey        string
		canonical         string
		packageImportable bool
		imports           map[string]string
		importsKnown      bool
	}

	var sources []sourceFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			_, statErr := os.Stat(filepath.Join(path, "go.mod"))
			if statErr == nil {
				return filepath.SkipDir
			}
			if !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("inspect nested Go module %s: %w", path, statErr)
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		matched, err := buildContext.MatchFile(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return fmt.Errorf("match Go build constraints for %s: %w", path, err)
		}
		if !matched {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse Go source %s: %w", path, err)
		}
		if !buildContext.CgoEnabled && parityFileImportsC(file) {
			return nil
		}
		relativeDirectory, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("resolve Go package path for %s: %w", path, err)
		}
		sources = append(sources, sourceFile{
			path:      path,
			directory: filepath.ToSlash(relativeDirectory),
			file:      file,
			generated: ast.IsGenerated(file),
			test:      strings.HasSuffix(path, "_test.go"),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(sources, func(left, right sourceFile) int {
		return strings.Compare(left.path, right.path)
	})

	productionPackages := map[string]string{}
	for _, source := range sources {
		if source.test {
			continue
		}
		declaredName := source.file.Name.Name
		if previous, exists := productionPackages[source.directory]; exists && previous != declaredName {
			return nil, fmt.Errorf(
				"conflicting production package names in %s: %s and %s",
				source.directory,
				previous,
				declaredName,
			)
		}
		productionPackages[source.directory] = declaredName
	}
	modulePath, err := parityGoModulePath(root)
	if err != nil {
		return nil, err
	}
	importedPackages := map[string]parityImportedPackage{}
	if modulePath != "" {
		for directory, declaredName := range productionPackages {
			importPath := modulePath
			if directory != "." {
				importPath += "/" + directory
			}
			importedPackages[importPath] = parityImportedPackage{
				canonical:    directory + "\x00production\x00" + declaredName,
				declaredName: declaredName,
			}
		}
	}

	index := paritySymbolIndex{}
	aliasOrigins := map[string]string{}
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		declaredName := source.file.Name.Name
		productionName, hasProduction := productionPackages[source.directory]
		external := false
		if source.test {
			switch {
			case hasProduction && declaredName == productionName:
			case hasProduction && declaredName == productionName+"_test":
				external = true
			case hasProduction:
				return nil, fmt.Errorf(
					"test package %s in %s does not match production package %s",
					declaredName,
					source.path,
					productionName,
				)
			case strings.HasSuffix(declaredName, "_test") && declaredName != "_test":
				external = true
			}
		}

		packageKey := parityPackageAlias(source.directory, declaredName, external)
		role := "production"
		if external {
			role = "external-test"
		}
		canonical := source.directory + "\x00" + role + "\x00" + declaredName
		if previous, exists := aliasOrigins[packageKey]; exists && previous != canonical {
			return nil, fmt.Errorf("ambiguous parity package alias %q", packageKey)
		}
		aliasOrigins[packageKey] = canonical

		packageImportable := declaredName != "main" && !parityInternalPackage(source.directory)
		imports, importsKnown := parityFileImports(source.file, importedPackages)
		source.external = external
		source.packageKey = packageKey
		source.canonical = canonical
		source.packageImportable = packageImportable
		source.imports = imports
		source.importsKnown = importsKnown
	}

	owners := newParityTypeOwners(importedPackages)
	for _, source := range sources {
		if source.test {
			continue
		}
		for _, declaration := range source.file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				owners.add(parityOwnerKey(source.canonical, typeSpec.Name.Name), parityTypeOwnerSource{
					canonical: source.canonical,
					spec:      typeSpec,
					imports:   source.imports,
					known:     source.importsKnown,
				})
			}
		}
	}

	for _, source := range sources {
		indexParityFile(
			index,
			source.file,
			source.packageKey,
			source.canonical,
			source.generated,
			source.test,
			source.external,
			source.packageImportable,
			source.imports,
			source.importsKnown,
			owners,
		)
	}
	return index, nil
}

func parityFileImportsC(file *ast.File) bool {
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err == nil && path == "C" {
			return true
		}
	}
	return false
}

func parityPackageAlias(directory, declaredName string, external bool) string {
	if directory == "." {
		return declaredName
	}
	if external {
		return directory + "#test"
	}
	return directory
}

func parityInternalPackage(directory string) bool {
	return slices.Contains(strings.Split(directory, "/"), "internal")
}

func parityGoModulePath(root string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Go module path: %w", err)
	}
	for line := range strings.SplitSeq(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"`"), nil
		}
	}
	return "", errors.New("read Go module path: go.mod has no module directive")
}

func parityFileImports(
	file *ast.File,
	packages map[string]parityImportedPackage,
) (map[string]string, bool) {
	imports := make(map[string]string, len(file.Imports))
	known := true
	for _, specification := range file.Imports {
		importPath, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			known = false
			continue
		}
		name := pathpkg.Base(importPath)
		if imported, exists := packages[importPath]; exists {
			name = imported.declaredName
		}
		if specification.Name != nil {
			name = specification.Name.Name
		}
		switch name {
		case "_":
			continue
		case ".":
			known = false
			continue
		}
		if previous, exists := imports[name]; exists && previous != importPath {
			known = false
			continue
		}
		imports[name] = importPath
	}
	return imports, known
}

func newParityFingerprintScope(
	imports map[string]string,
	renamed map[string]string,
	known bool,
	canonical string,
	owners *parityTypeOwners,
) parityFingerprintScope {
	return parityFingerprintScope{
		imports:   imports,
		renames:   renamed,
		canonical: canonical,
		owners:    owners,
		known:     &known,
	}
}

func (s parityFingerprintScope) withRenames(renamed map[string]string) parityFingerprintScope {
	merged := make(map[string]string, len(s.renames)+len(renamed))
	maps.Copy(merged, s.renames)
	maps.Copy(merged, renamed)
	s.renames = merged
	return s
}

func (s parityFingerprintScope) shapeKnown() bool {
	return s.known != nil && *s.known
}

func (s parityFingerprintScope) markUnknown() {
	if s.known != nil {
		*s.known = false
	}
}

func newParityTypeOwners(packages map[string]parityImportedPackage) *parityTypeOwners {
	return &parityTypeOwners{
		sources:              map[string]parityTypeOwnerSource{},
		cache:                map[string]parityTypeOwner{},
		resolving:            map[string]bool{},
		aliases:              map[string]parityDependencyFingerprint{},
		resolvingAliases:     map[string]bool{},
		constraints:          map[string]parityDependencyFingerprint{},
		resolvingConstraints: map[string]bool{},
		packages:             packages,
	}
}

func parityOwnerKey(canonical, name string) string {
	return canonical + "\x00" + name
}

func (o *parityTypeOwners) add(key string, source parityTypeOwnerSource) {
	if _, exists := o.sources[key]; !exists {
		o.sources[key] = source
	}
}

func (o *parityTypeOwners) owner(canonical, name string) (parityTypeOwner, bool) {
	key := parityOwnerKey(canonical, name)
	if owner, exists := o.cache[key]; exists {
		return owner, true
	}
	source, exists := o.sources[key]
	if !exists {
		return parityTypeOwner{}, false
	}
	if o.resolving[key] {
		return parityTypeOwner{comparability: "recursive", known: false}, true
	}
	o.resolving[key] = true
	defer delete(o.resolving, key)

	scope := newParityFingerprintScope(source.imports, nil, source.known, source.canonical, o)
	typeParameters, renamed := parityTypeParametersFingerprint(source.spec.TypeParams, scope)
	scope = scope.withRenames(renamed)
	comparability, comparabilityKnown := parityTypeComparability(
		source.spec.Type,
		source.canonical,
		scope,
		o,
	)
	if !comparabilityKnown {
		scope.markUnknown()
	}
	kind := "defined"
	if source.spec.Assign.IsValid() {
		kind = "alias"
	}
	container := "type"
	switch source.spec.Type.(type) {
	case *ast.StructType:
		container = "struct"
	case *ast.InterfaceType:
		container = "interface"
	}
	owner := parityTypeOwner{
		header: strings.Join(
			[]string{kind, typeParameters, "container:" + container, "comparable:" + comparability},
			"|",
		),
		renamed:        renamed,
		comparability:  comparability,
		parameterCount: len(renamed),
		known:          scope.shapeKnown(),
	}
	o.cache[key] = owner
	return owner, true
}

func (o *parityTypeOwners) aliasFingerprint(
	canonical string,
	name string,
) (string, bool, bool) {
	key := parityOwnerKey(canonical, name)
	source, exists := o.sources[key]
	if !exists || !source.spec.Assign.IsValid() {
		return "", false, false
	}
	if cached, exists := o.aliases[key]; exists {
		return cached.fingerprint, cached.known, true
	}
	if o.resolvingAliases[key] {
		return "alias-recursive:" + name, false, true
	}
	o.resolvingAliases[key] = true
	defer delete(o.resolvingAliases, key)

	if source.spec.TypeParams != nil {
		fingerprint := "alias-generic:" + name
		o.aliases[key] = parityDependencyFingerprint{fingerprint: fingerprint, known: false}
		return fingerprint, false, true
	}
	scope := newParityFingerprintScope(source.imports, nil, source.known, source.canonical, o)
	fingerprint := parityTypeExpressionFingerprint(source.spec.Type, scope)
	known := scope.shapeKnown()
	o.aliases[key] = parityDependencyFingerprint{fingerprint: fingerprint, known: known}
	return fingerprint, known, true
}

func (o *parityTypeOwners) constraintFingerprint(
	canonical string,
	name string,
) (string, bool, bool) {
	key := parityOwnerKey(canonical, name)
	source, exists := o.sources[key]
	if !exists {
		return "", false, false
	}
	if cached, exists := o.constraints[key]; exists {
		return cached.fingerprint, cached.known, true
	}
	if o.resolvingConstraints[key] {
		return "constraint-recursive:" + name, false, true
	}
	o.resolvingConstraints[key] = true
	defer delete(o.resolvingConstraints, key)

	owner, exists := o.owner(canonical, name)
	if !exists {
		return "", false, false
	}
	scope := newParityFingerprintScope(
		source.imports,
		owner.renamed,
		source.known && owner.known,
		source.canonical,
		o,
	)
	fingerprint := parityConstraintExpressionFingerprint(source.spec.Type, scope)
	if !source.spec.Assign.IsValid() {
		fingerprint = "owner:" + owner.header + "|underlying:" + fingerprint
	}
	known := scope.shapeKnown()
	o.constraints[key] = parityDependencyFingerprint{fingerprint: fingerprint, known: known}
	return fingerprint, known, true
}

func indexParityFile(
	index paritySymbolIndex,
	file *ast.File,
	packageKey string,
	canonical string,
	generated, testFile, external, packageImportable bool,
	imports map[string]string,
	importsKnown bool,
	owners *parityTypeOwners,
) {
	production := !testFile
	importable := production && packageImportable
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			name := packageKey + "." + node.Name.Name
			exported := ast.IsExported(node.Name.Name)
			scope := newParityFingerprintScope(imports, nil, importsKnown, canonical, owners)
			ownerHeader := ""
			var receiverExpression ast.Expr
			if node.Recv != nil && len(node.Recv.List) != 0 {
				receiverExpression = node.Recv.List[0].Type
				if receiver := parityReceiverName(node.Recv.List[0].Type); receiver != "" {
					name = packageKey + "." + receiver + "." + node.Name.Name
					exported = exported && ast.IsExported(receiver)
					if owner, exists := owners.owner(canonical, receiver); exists {
						ownerHeader = owner.header
						receiverRenames, receiverKnown := parityReceiverTypeParameterRenames(
							node.Recv.List[0].Type,
							owner.parameterCount,
						)
						scope = scope.withRenames(receiverRenames)
						if !receiverKnown {
							scope.markUnknown()
						}
						if !owner.known {
							scope.markUnknown()
						}
					}
				}
			}
			fingerprint := parityFunctionFingerprint(receiverExpression, node.Type, scope)
			if ownerHeader != "" {
				fingerprint = "owner:" + ownerHeader + "|" + fingerprint
			}
			indexParitySymbol(
				index,
				name,
				canonical,
				fingerprint,
				scope.shapeKnown(),
				generated,
				exported,
				production,
				importable,
			)
			if testFile && validParityProofDeclaration(file, node) {
				declaration := index[name]
				declaration.Proof = true
				declaration.Behavior = parityProofExecutesBehavior(file, node)
				declaration.External = external
				declaration.Package = packageKey
				declaration.RealTmux = parityRealTmuxScope(packageKey, external) && parityRealTmuxMarker(node)
				declaration.ParityIDs = parityProofIDs(node)
				index[name] = declaration
			}
		case *ast.GenDecl:
			var priorConstType ast.Expr
			var priorConstValues []ast.Expr
			constIndex := 0
			for _, specification := range node.Specs {
				switch spec := specification.(type) {
				case *ast.TypeSpec:
					typeName := packageKey + "." + spec.Name.Name
					typeExported := ast.IsExported(spec.Name.Name)
					owner, _ := owners.owner(canonical, spec.Name.Name)
					scope := newParityFingerprintScope(
						imports,
						owner.renamed,
						importsKnown && owner.known,
						canonical,
						owners,
					)
					indexParitySymbol(
						index,
						typeName,
						canonical,
						parityTypeSpecFingerprint(spec, owner, scope),
						scope.shapeKnown(),
						generated,
						typeExported,
						production,
						importable,
					)
					indexParityTypeFields(
						index,
						typeName,
						spec.Type,
						canonical,
						generated,
						typeExported,
						production,
						importable,
						owner,
						imports,
						importsKnown,
						owners,
					)
				case *ast.ValueSpec:
					effectiveType := spec.Type
					effectiveValues := spec.Values
					if node.Tok == token.CONST {
						if len(spec.Values) == 0 {
							effectiveType = priorConstType
							effectiveValues = priorConstValues
						} else {
							priorConstType = spec.Type
							priorConstValues = spec.Values
						}
					}
					for nameIndex, name := range spec.Names {
						scope := newParityFingerprintScope(imports, nil, importsKnown, canonical, owners)
						fingerprint, shapeKnown := parityValueFingerprint(
							node.Tok,
							effectiveType,
							effectiveValues,
							nameIndex,
							len(spec.Names),
							constIndex,
							scope,
						)
						shapeKnown = shapeKnown && scope.shapeKnown()
						indexParitySymbol(
							index,
							packageKey+"."+name.Name,
							canonical,
							fingerprint,
							shapeKnown,
							generated,
							ast.IsExported(name.Name),
							production,
							importable,
						)
					}
					constIndex++
				}
			}
		}
	}
}

func indexParityTypeFields(
	index paritySymbolIndex,
	typeName string,
	expression ast.Expr,
	canonical string,
	generated, typeExported, production, importable bool,
	owner parityTypeOwner,
	imports map[string]string,
	importsKnown bool,
	owners *parityTypeOwners,
) {
	var fields *ast.FieldList
	var container string
	switch node := expression.(type) {
	case *ast.StructType:
		fields = node.Fields
		container = "struct-field"
	case *ast.InterfaceType:
		fields = node.Methods
		container = "interface-method"
	default:
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			scope := newParityFingerprintScope(
				imports,
				owner.renamed,
				importsKnown && owner.known,
				canonical,
				owners,
			)
			fingerprint := "owner:" + owner.header + "|" + container + ":" +
				parityTypeExpressionFingerprint(field.Type, scope)
			if field.Tag != nil {
				fingerprint += ":tag:" + parityStructTagFingerprint(field.Tag, scope)
			}
			indexParitySymbol(
				index,
				typeName+"."+name.Name,
				canonical,
				fingerprint,
				scope.shapeKnown(),
				generated,
				typeExported && ast.IsExported(name.Name),
				production,
				importable,
			)
		}
	}
}

func indexParitySymbol(
	index paritySymbolIndex,
	name string,
	canonical string,
	fingerprint string,
	shapeKnown bool,
	generated, exported, production, importable bool,
) {
	previous := index[name]
	first := previous.Canonical == ""
	if first {
		previous.Canonical = canonical
	} else if previous.Canonical != canonical {
		previous.Canonical = "\x00conflict"
	}
	if previous.Fingerprint == "" {
		previous.Fingerprint = fingerprint
	} else if previous.Fingerprint != fingerprint {
		previous.Fingerprint = "\x00conflict"
		shapeKnown = false
	}
	if first {
		previous.ShapeKnown = shapeKnown
	} else {
		previous.ShapeKnown = previous.ShapeKnown && shapeKnown
	}
	previous.Generated = previous.Generated || generated
	previous.Exported = previous.Exported || exported
	previous.Production = previous.Production || production
	previous.Importable = previous.Importable || importable
	index[name] = previous
}

func parityReceiverTypeParameterRenames(
	receiver ast.Expr,
	want int,
) (map[string]string, bool) {
	for {
		switch node := receiver.(type) {
		case *ast.StarExpr:
			receiver = node.X
		case *ast.ParenExpr:
			receiver = node.X
		default:
			goto unwrapped
		}
	}

unwrapped:
	var arguments []ast.Expr
	switch node := receiver.(type) {
	case *ast.IndexExpr:
		arguments = []ast.Expr{node.Index}
	case *ast.IndexListExpr:
		arguments = node.Indices
	default:
		return nil, want == 0
	}
	if len(arguments) != want {
		return nil, false
	}
	renamed := make(map[string]string, len(arguments))
	for index, argument := range arguments {
		identifier, ok := argument.(*ast.Ident)
		if !ok {
			return nil, false
		}
		renamed[identifier.Name] = fmt.Sprintf("T%d", index)
	}
	return renamed, true
}

func parityTypeComparability(
	expression ast.Expr,
	canonical string,
	scope parityFingerprintScope,
	owners *parityTypeOwners,
) (string, bool) {
	switch node := expression.(type) {
	case *ast.Ident:
		if replacement, exists := scope.renames[node.Name]; exists {
			return "parameter:" + replacement, true
		}
		if owner, exists := owners.owner(canonical, node.Name); exists {
			return owner.comparability, owner.known
		}
		if parityComparableBuiltin(node.Name) {
			return "yes", true
		}
		return "unknown:" + node.Name, false
	case *ast.SelectorExpr:
		if qualifier, ok := node.X.(*ast.Ident); ok {
			if importPath, exists := scope.imports[qualifier.Name]; exists {
				if imported, inModule := owners.packages[importPath]; inModule {
					if owner, ownerExists := owners.owner(imported.canonical, node.Sel.Name); ownerExists {
						return owner.comparability, owner.known
					}
				}
				// An external package's canonical exported type identity is its
				// comparability contract. Dependency internals are outside this
				// module's cross-platform declaration index.
				return "external:" + importPath + "." + node.Sel.Name, true
			}
		}
		return "unknown:" + parityNodeFingerprint(node), false
	case *ast.StarExpr, *ast.ChanType, *ast.InterfaceType:
		return "yes", true
	case *ast.MapType, *ast.FuncType:
		return "no", true
	case *ast.ArrayType:
		array := node
		if array.Len == nil {
			return "no", true
		}
		child, known := parityTypeComparability(array.Elt, canonical, scope, owners)
		if child == "yes" || child == "no" {
			return child, known
		}
		return "array:" + child, known
	case *ast.StructType:
		var dependencies []string
		known := true
		for _, field := range node.Fields.List {
			fieldComparability, fieldKnown := parityTypeComparability(
				field.Type,
				canonical,
				scope,
				owners,
			)
			if fieldComparability == "no" {
				return "no", true
			}
			if !fieldKnown {
				known = false
			}
			if fieldComparability != "yes" {
				dependencies = append(dependencies, fieldComparability)
			}
		}
		if len(dependencies) == 0 {
			return "yes", known
		}
		slices.Sort(dependencies)
		dependencies = slices.Compact(dependencies)
		return "depends:" + strings.Join(dependencies, ","), known
	case *ast.ParenExpr:
		return parityTypeComparability(node.X, canonical, scope, owners)
	case *ast.IndexExpr:
		return parityIndexedTypeComparability(
			node.X,
			[]ast.Expr{node.Index},
			canonical,
			scope,
			owners,
		)
	case *ast.IndexListExpr:
		return parityIndexedTypeComparability(
			node.X,
			node.Indices,
			canonical,
			scope,
			owners,
		)
	default:
		return "unknown:" + parityNodeFingerprint(node), false
	}
}

func parityIndexedTypeComparability(
	base ast.Expr,
	arguments []ast.Expr,
	canonical string,
	scope parityFingerprintScope,
	owners *parityTypeOwners,
) (string, bool) {
	ownerCanonical := canonical
	var ownerName string
	switch node := base.(type) {
	case *ast.Ident:
		ownerName = node.Name
	case *ast.SelectorExpr:
		qualifier, ok := node.X.(*ast.Ident)
		if !ok {
			return "unknown:" + parityNodeFingerprint(base), false
		}
		importPath, exists := scope.imports[qualifier.Name]
		if !exists {
			return "unknown:" + parityNodeFingerprint(base), false
		}
		imported, inModule := owners.packages[importPath]
		if !inModule {
			argumentContracts := make([]string, 0, len(arguments))
			known := true
			for _, argument := range arguments {
				contract, argumentKnown := parityTypeComparability(
					argument,
					canonical,
					scope,
					owners,
				)
				argumentContracts = append(argumentContracts, contract)
				known = known && argumentKnown
			}
			return "external:" + importPath + "." + node.Sel.Name + "[" +
				strings.Join(argumentContracts, ",") + "]", known
		}
		ownerCanonical = imported.canonical
		ownerName = node.Sel.Name
	default:
		return "unknown:" + parityNodeFingerprint(base), false
	}
	owner, exists := owners.owner(ownerCanonical, ownerName)
	if !exists || !owner.known || len(arguments) != owner.parameterCount {
		return "unknown:" + ownerName, false
	}
	argumentComparability := make([]string, len(arguments))
	known := true
	for index, argument := range arguments {
		contract, argumentKnown := parityTypeComparability(
			argument,
			canonical,
			scope,
			owners,
		)
		argumentComparability[index] = contract
		known = known && argumentKnown
	}
	comparability, substitutionKnown := paritySubstituteTypeParameters(
		owner.comparability,
		argumentComparability,
	)
	return comparability, known && substitutionKnown
}

func paritySubstituteTypeParameters(
	fingerprint string,
	arguments []string,
) (string, bool) {
	known := true
	result := parityComparabilityParameterPattern.ReplaceAllStringFunc(
		fingerprint,
		func(parameter string) string {
			index, err := strconv.Atoi(strings.TrimPrefix(parameter, "parameter:T"))
			if err != nil || index < 0 || index >= len(arguments) {
				known = false
				return parameter
			}
			return arguments[index]
		},
	)
	return result, known
}

func parityComparableBuiltin(name string) bool {
	switch name {
	case "any", "bool", "byte", "comparable", "complex128", "complex64", "error",
		"float32", "float64", "int", "int16", "int32", "int64", "int8",
		"rune", "string", "uint", "uint16", "uint32", "uint64", "uint8", "uintptr":
		return true
	default:
		return false
	}
}

func parityFunctionFingerprint(
	receiver ast.Expr,
	function *ast.FuncType,
	scope parityFingerprintScope,
) string {
	typeParameters, renamed := parityTypeParametersFingerprint(function.TypeParams, scope)
	scope = scope.withRenames(renamed)
	parts := []string{"func", typeParameters}
	if receiver != nil {
		parts[0] = "method"
		parts = append(parts, parityTypeExpressionFingerprint(receiver, scope))
	}
	parts = append(
		parts,
		"params:"+strings.Join(parityFieldListTypes(function.Params, scope), ","),
		"results:"+strings.Join(parityFieldListTypes(function.Results, scope), ","),
	)
	return strings.Join(parts, "|")
}

func parityTypeSpecFingerprint(
	specification *ast.TypeSpec,
	owner parityTypeOwner,
	scope parityFingerprintScope,
) string {
	return "owner:" + owner.header + "|underlying:" +
		parityTypeExpressionFingerprint(specification.Type, scope)
}

func parityTypeParametersFingerprint(
	fields *ast.FieldList,
	scope parityFingerprintScope,
) (string, map[string]string) {
	renamed := map[string]string{}
	if fields == nil {
		return "typeparams:", renamed
	}
	index := len(scope.renames)
	for _, field := range fields.List {
		for _, name := range field.Names {
			renamed[name.Name] = fmt.Sprintf("T%d", index)
			index++
		}
	}
	constraintScope := scope.withRenames(renamed)
	constraints := make([]string, 0, len(renamed))
	for _, field := range fields.List {
		constraint := parityConstraintExpressionFingerprint(field.Type, constraintScope)
		for range field.Names {
			constraints = append(constraints, constraint)
		}
	}
	return "typeparams:" + strings.Join(constraints, ","), renamed
}

func parityConstraintExpressionFingerprint(
	expression ast.Expr,
	scope parityFingerprintScope,
) string {
	switch node := expression.(type) {
	case *ast.Ident:
		if replacement, exists := scope.renames[node.Name]; exists {
			return "parameter:" + replacement
		}
		if scope.owners != nil {
			if fingerprint, known, exists := scope.owners.constraintFingerprint(
				scope.canonical,
				node.Name,
			); exists {
				if !known {
					scope.markUnknown()
				}
				return fingerprint
			}
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.SelectorExpr:
		qualifier, ok := node.X.(*ast.Ident)
		if ok && scope.owners != nil {
			if importPath, exists := scope.imports[qualifier.Name]; exists {
				if imported, inModule := scope.owners.packages[importPath]; inModule {
					if fingerprint, known, exists := scope.owners.constraintFingerprint(
						imported.canonical,
						node.Sel.Name,
					); exists {
						if !known {
							scope.markUnknown()
						}
						return fingerprint
					}
				}
			}
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.IndexExpr:
		if fingerprint, known, handled := parityInstantiatedConstraintFingerprint(
			node.X,
			[]ast.Expr{node.Index},
			scope,
		); handled {
			if !known {
				scope.markUnknown()
			}
			return fingerprint
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.IndexListExpr:
		if fingerprint, known, handled := parityInstantiatedConstraintFingerprint(
			node.X,
			node.Indices,
			scope,
		); handled {
			if !known {
				scope.markUnknown()
			}
			return fingerprint
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.InterfaceType:
		methods := make([]string, 0, len(node.Methods.List))
		for _, field := range node.Methods.List {
			fingerprint := parityConstraintExpressionFingerprint(field.Type, scope)
			if len(field.Names) == 0 {
				methods = append(methods, "embed:"+fingerprint)
				continue
			}
			for _, name := range field.Names {
				methods = append(methods, "method:"+name.Name+":"+fingerprint)
			}
		}
		slices.Sort(methods)
		return "interface{" + strings.Join(methods, ";") + "}"
	case *ast.UnaryExpr:
		if node.Op == token.TILDE {
			return "approx:" + parityConstraintExpressionFingerprint(node.X, scope)
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.BinaryExpr:
		if node.Op == token.OR {
			terms := parityConstraintUnionTerms(node, scope)
			slices.Sort(terms)
			return "union:" + strings.Join(terms, "|")
		}
		return parityTypeExpressionFingerprint(node, scope)
	case *ast.ParenExpr:
		return parityConstraintExpressionFingerprint(node.X, scope)
	default:
		return parityTypeExpressionFingerprint(expression, scope)
	}
}

func parityInstantiatedConstraintFingerprint(
	base ast.Expr,
	arguments []ast.Expr,
	scope parityFingerprintScope,
) (string, bool, bool) {
	if scope.owners == nil {
		return "", false, false
	}
	canonical := scope.canonical
	var name string
	switch node := base.(type) {
	case *ast.Ident:
		name = node.Name
	case *ast.SelectorExpr:
		qualifier, ok := node.X.(*ast.Ident)
		if !ok {
			return "", false, false
		}
		importPath, exists := scope.imports[qualifier.Name]
		if !exists {
			return "", false, false
		}
		imported, inModule := scope.owners.packages[importPath]
		if !inModule {
			return "", false, false
		}
		canonical = imported.canonical
		name = node.Sel.Name
	default:
		return "", false, false
	}
	owner, exists := scope.owners.owner(canonical, name)
	if !exists {
		return "", false, false
	}
	template, known, exists := scope.owners.constraintFingerprint(canonical, name)
	if !exists {
		return "", false, false
	}
	if len(arguments) != owner.parameterCount {
		return "constraint-arity:" + name, false, true
	}
	argumentFingerprints := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		argumentFingerprints = append(
			argumentFingerprints,
			parityTypeExpressionFingerprint(argument, scope),
		)
	}
	fingerprint, substitutionKnown := paritySubstituteTypeParameters(
		template,
		argumentFingerprints,
	)
	return fingerprint, known && substitutionKnown && scope.shapeKnown(), true
}

func parityConstraintUnionTerms(
	expression ast.Expr,
	scope parityFingerprintScope,
) []string {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.OR {
		return []string{parityConstraintExpressionFingerprint(expression, scope)}
	}
	terms := parityConstraintUnionTerms(binary.X, scope)
	return append(terms, parityConstraintUnionTerms(binary.Y, scope)...)
}

func parityFieldListTypes(
	fields *ast.FieldList,
	scope parityFingerprintScope,
) []string {
	if fields == nil {
		return nil
	}
	var result []string
	for _, field := range fields.List {
		fingerprint := parityTypeExpressionFingerprint(field.Type, scope)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			result = append(result, fingerprint)
		}
	}
	return result
}

func parityTypeExpressionFingerprint(
	expression ast.Expr,
	scope parityFingerprintScope,
) string {
	switch node := expression.(type) {
	case *ast.Ident:
		if replacement, exists := scope.renames[node.Name]; exists {
			return "parameter:" + replacement
		}
		if scope.owners != nil {
			if fingerprint, known, exists := scope.owners.aliasFingerprint(
				scope.canonical,
				node.Name,
			); exists {
				if !known {
					scope.markUnknown()
				}
				return fingerprint
			}
		}
		return "name:" + node.Name
	case *ast.SelectorExpr:
		if qualifier, ok := node.X.(*ast.Ident); ok {
			if importPath, exists := scope.imports[qualifier.Name]; exists {
				if scope.owners != nil {
					if imported, inModule := scope.owners.packages[importPath]; inModule {
						if fingerprint, known, alias := scope.owners.aliasFingerprint(
							imported.canonical,
							node.Sel.Name,
						); alias {
							if !known {
								scope.markUnknown()
							}
							return fingerprint
						}
					}
				}
				return "import:" + importPath + "." + node.Sel.Name
			}
		}
		scope.markUnknown()
		return "selector:" + parityNodeFingerprint(node)
	case *ast.StarExpr:
		return "pointer:" + parityTypeExpressionFingerprint(node.X, scope)
	case *ast.ArrayType:
		prefix := "slice"
		if node.Len != nil {
			prefix = "array:" + parityArrayLengthFingerprint(node.Len, scope)
		}
		return prefix + ":" + parityTypeExpressionFingerprint(node.Elt, scope)
	case *ast.MapType:
		return "map:" + parityTypeExpressionFingerprint(node.Key, scope) + ":" +
			parityTypeExpressionFingerprint(node.Value, scope)
	case *ast.ChanType:
		return fmt.Sprintf("chan:%d:%s", node.Dir, parityTypeExpressionFingerprint(node.Value, scope))
	case *ast.Ellipsis:
		return "ellipsis:" + parityTypeExpressionFingerprint(node.Elt, scope)
	case *ast.IndexExpr:
		return "index:" + parityTypeExpressionFingerprint(node.X, scope) + "[" +
			parityTypeExpressionFingerprint(node.Index, scope) + "]"
	case *ast.IndexListExpr:
		indices := make([]string, 0, len(node.Indices))
		for _, index := range node.Indices {
			indices = append(indices, parityTypeExpressionFingerprint(index, scope))
		}
		return "index:" + parityTypeExpressionFingerprint(node.X, scope) + "[" +
			strings.Join(indices, ",") + "]"
	case *ast.ParenExpr:
		return parityTypeExpressionFingerprint(node.X, scope)
	case *ast.FuncType:
		return parityFunctionFingerprint(nil, node, scope)
	case *ast.StructType:
		var fields []string
		for _, field := range node.Fields.List {
			typeFingerprint := parityTypeExpressionFingerprint(field.Type, scope)
			tag := ""
			if field.Tag != nil {
				tag = ":tag:" + parityStructTagFingerprint(field.Tag, scope)
			}
			if len(field.Names) == 0 {
				fields = append(fields, "embed:"+typeFingerprint+tag)
				continue
			}
			for _, name := range field.Names {
				if ast.IsExported(name.Name) {
					fields = append(fields, "field:"+name.Name+":"+typeFingerprint+tag)
				}
			}
		}
		comparability := "unknown"
		if scope.owners == nil {
			scope.markUnknown()
		} else {
			var known bool
			comparability, known = parityTypeComparability(
				node,
				scope.canonical,
				scope,
				scope.owners,
			)
			if !known {
				scope.markUnknown()
			}
		}
		return "struct:comparable:" + comparability + "{" + strings.Join(fields, ";") + "}"
	case *ast.InterfaceType:
		var methods []string
		for _, field := range node.Methods.List {
			if len(field.Names) == 0 {
				typeFingerprint := parityConstraintExpressionFingerprint(field.Type, scope)
				methods = append(methods, "embed:"+typeFingerprint)
				continue
			}
			typeFingerprint := parityTypeExpressionFingerprint(field.Type, scope)
			for _, name := range field.Names {
				methods = append(methods, "method:"+name.Name+":"+typeFingerprint)
			}
		}
		slices.Sort(methods)
		return "interface{" + strings.Join(methods, ";") + "}"
	case *ast.UnaryExpr:
		if node.Op == token.TILDE {
			return "approx:" + parityTypeExpressionFingerprint(node.X, scope)
		}
		scope.markUnknown()
		return parityTokenFingerprint(node, scope)
	case *ast.BinaryExpr:
		if node.Op == token.OR {
			terms := parityUnionTerms(node, scope)
			slices.Sort(terms)
			return "union:" + strings.Join(terms, "|")
		}
		scope.markUnknown()
		return parityTokenFingerprint(node, scope)
	default:
		scope.markUnknown()
		return parityTokenFingerprint(expression, scope)
	}
}

func parityUnionTerms(expression ast.Expr, scope parityFingerprintScope) []string {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.OR {
		return []string{parityTypeExpressionFingerprint(expression, scope)}
	}
	terms := parityUnionTerms(binary.X, scope)
	return append(terms, parityUnionTerms(binary.Y, scope)...)
}

func parityArrayLengthFingerprint(
	expression ast.Expr,
	scope parityFingerprintScope,
) string {
	if !paritySelfContainedConstant(expression) {
		scope.markUnknown()
	}
	return parityExpressionFingerprint(expression, scope)
}

func parityStructTagFingerprint(tag *ast.BasicLit, scope parityFingerprintScope) string {
	value, err := strconv.Unquote(tag.Value)
	if err != nil {
		scope.markUnknown()
		return tag.Value
	}
	return strconv.Quote(value)
}

func parityExpressionFingerprint(expression ast.Expr, scope parityFingerprintScope) string {
	switch node := expression.(type) {
	case *ast.Ident:
		if replacement, exists := scope.renames[node.Name]; exists {
			return "parameter:" + replacement
		}
		return "name:" + node.Name
	case *ast.BasicLit:
		return node.Kind.String() + ":" + node.Value
	case *ast.SelectorExpr:
		if qualifier, ok := node.X.(*ast.Ident); ok {
			if importPath, exists := scope.imports[qualifier.Name]; exists {
				return "import:" + importPath + "." + node.Sel.Name
			}
		}
		scope.markUnknown()
		return parityNodeFingerprint(node)
	case *ast.ParenExpr:
		return parityExpressionFingerprint(node.X, scope)
	case *ast.UnaryExpr:
		return node.Op.String() + ":" + parityExpressionFingerprint(node.X, scope)
	case *ast.BinaryExpr:
		return node.Op.String() + ":" + parityExpressionFingerprint(node.X, scope) + ":" +
			parityExpressionFingerprint(node.Y, scope)
	default:
		scope.markUnknown()
		return parityTokenFingerprint(node, scope)
	}
}

func parityValueFingerprint(
	kind token.Token,
	explicitType ast.Expr,
	values []ast.Expr,
	nameIndex, nameCount, constIndex int,
	scope parityFingerprintScope,
) (string, bool) {
	if kind == token.VAR {
		if explicitType == nil {
			return "var:inferred", false
		}
		return "var:" + parityTypeExpressionFingerprint(explicitType, scope), true
	}

	value := parityValueAt(values, nameIndex, nameCount)
	if value == nil || !paritySelfContainedConstant(value) {
		return "const:unresolved", false
	}
	typeFingerprint := "untyped"
	if explicitType != nil {
		typeFingerprint = parityTypeExpressionFingerprint(explicitType, scope)
	}
	fingerprint := "const:" + typeFingerprint + ":" + parityTokenFingerprint(value, scope)
	if parityContainsIdentifier(value, "iota") {
		fingerprint += fmt.Sprintf(":iota-index:%d", constIndex)
	}
	return fingerprint, true
}

func parityValueAt(values []ast.Expr, nameIndex, nameCount int) ast.Expr {
	if len(values) == nameCount {
		return values[nameIndex]
	}
	if len(values) == 1 && nameCount == 1 {
		return values[0]
	}
	return nil
}

func paritySelfContainedConstant(expression ast.Expr) bool {
	known := true
	allowedIdentifiers := map[string]bool{
		"false": true,
		"iota":  true,
		"true":  true,
	}
	ast.Inspect(expression, func(node ast.Node) bool {
		if !known {
			return false
		}
		switch value := node.(type) {
		case *ast.Ident:
			if !allowedIdentifiers[value.Name] {
				known = false
			}
		case *ast.SelectorExpr:
			known = false
		}
		return known
	})
	return known
}

func parityContainsIdentifier(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func parityNodeFingerprint(node any) string {
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		panic(fmt.Sprintf("format parity declaration fingerprint: %v", err))
	}
	return output.String()
}

func parityTokenFingerprint(node any, scope parityFingerprintScope) string {
	source := parityNodeFingerprint(node)
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("fingerprint.go", fileSet.Base(), len(source))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(source), nil, scanner.ScanComments)
	var output strings.Builder
	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind == token.COMMENT {
			continue
		}
		if literal == "" {
			literal = kind.String()
		}
		if kind == token.IDENT {
			if replacement, exists := scope.renames[literal]; exists {
				literal = replacement
			}
		}
		fmt.Fprintf(&output, "%d:%s;", kind, strconv.Quote(literal))
	}
	return output.String()
}

func parityProofExecutesBehavior(file *ast.File, declaration *ast.FuncDecl) bool {
	if validParityProofName(declaration.Name.Name, "Test") {
		return parityTestBodyHasEvidence(declaration)
	}
	want := strings.TrimPrefix(declaration.Name.Name, "Example")
	for _, example := range doc.Examples(file) {
		if example.Name == want && (example.Output != "" || example.EmptyOutput) {
			return true
		}
	}
	return false
}

func parityTestBodyHasEvidence(declaration *ast.FuncDecl) bool {
	testingParameters := map[string]bool{}
	if declaration.Type.Params != nil {
		for _, field := range declaration.Type.Params.List {
			for _, name := range field.Names {
				testingParameters[name.Name] = true
			}
		}
	}
	found := false
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || parityNoOpTestCall(call, testingParameters) {
			return true
		}
		found = true
		return false
	})
	return found
}

func parityNoOpTestCall(call *ast.CallExpr, testingParameters map[string]bool) bool {
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		switch identifier.Name {
		case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover":
			return true
		}
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || !testingParameters[receiver.Name] {
		return false
	}
	switch selector.Sel.Name {
	case "Chdir", "Cleanup", "Context", "Deadline", "Helper", "Log", "Logf", "Name", "Parallel", "Setenv", "Skip", "SkipNow", "Skipf", "TempDir":
		return true
	default:
		return false
	}
}

// parityRealTmuxScope reports whether a real-tmux proof may live in this
// package.
//
// Such a proof has to drive tmux the way a caller does, so it must sit outside
// the package it proves: an external test package, the harness, or the
// integration package, which is a package of its own importing the public API.
func parityRealTmuxScope(packageName string, external bool) bool {
	return external || packageName == "tmux/tmuxtest" || packageName == parityIntegrationPackage
}

// parityIntegrationPackage is the alias the symbol index gives the package
// holding the tests that drive a real tmux.
const parityIntegrationPackage = "tmux/internal/integration"

func parityRealTmuxMarker(declaration *ast.FuncDecl) bool {
	if declaration.Doc == nil {
		return false
	}
	for _, comment := range declaration.Doc.List {
		if parityCommentText(comment) == "libtmux:real-tmux" {
			return true
		}
	}
	return false
}

func parityProofIDs(declaration *ast.FuncDecl) []string {
	if declaration.Doc == nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, comment := range declaration.Doc.List {
		id, ok := strings.CutPrefix(parityCommentText(comment), "libtmux:parity ")
		if !ok || id == "" || strings.ContainsAny(id, " \t\r\n") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func parityCommentText(comment *ast.Comment) string {
	text := strings.TrimSpace(comment.Text)
	text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/"))
}

func validParityProofDeclaration(file *ast.File, declaration *ast.FuncDecl) bool {
	if declaration.Recv != nil || parityFieldCount(declaration.Type.Results) != 0 {
		return false
	}
	if declaration.Type.TypeParams != nil && len(declaration.Type.TypeParams.List) != 0 {
		return false
	}
	name := declaration.Name.Name
	if validParityProofName(name, "Example") {
		return parityFieldCount(declaration.Type.Params) == 0
	}
	if !validParityProofName(name, "Test") || parityFieldCount(declaration.Type.Params) != 1 {
		return false
	}
	if declaration.Type.Params == nil || len(declaration.Type.Params.List) != 1 {
		return false
	}
	return parityTestingT(file, declaration.Type.Params.List[0].Type)
}

func validParityProofName(name, prefix string) bool {
	suffix, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	if suffix == "" {
		return prefix == "Example"
	}
	first, _ := utf8.DecodeRuneInString(suffix)
	return !unicode.IsLower(first)
}

func parityFieldCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func parityTestingT(file *ast.File, expression ast.Expr) bool {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	aliases := map[string]bool{}
	dotImported := false
	for _, specification := range file.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		if specification.Name == nil {
			aliases["testing"] = true
		} else if specification.Name.Name == "." {
			dotImported = true
		} else if specification.Name.Name != "_" {
			aliases[specification.Name.Name] = true
		}
	}
	if identifier, ok := pointer.X.(*ast.Ident); ok {
		return dotImported && identifier.Name == "T"
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "T" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && aliases[identifier.Name]
}

func parityReceiverName(expression ast.Expr) string {
	switch node := expression.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.StarExpr:
		return parityReceiverName(node.X)
	case *ast.IndexExpr:
		return parityReceiverName(node.X)
	case *ast.IndexListExpr:
		return parityReceiverName(node.X)
	case *ast.ParenExpr:
		return parityReceiverName(node.X)
	default:
		return ""
	}
}

func validateParityManifest(
	document parityManifest,
	index paritySymbolIndex,
	root string,
) []string {
	var issues []string
	if document.Schema != 2 {
		issues = append(issues, fmt.Sprintf("schema is %d, want 2", document.Schema))
	}
	if !slices.Equal(document.Translations, parityTranslations) {
		issues = append(issues, "translations do not match the closed registry")
	}
	seenSourcePaths := map[string]bool{}
	previousSourcePath := ""
	for _, source := range document.SourceDigests {
		if source.Path == "" {
			issues = append(issues, "source digest path is required")
		}
		if seenSourcePaths[source.Path] {
			issues = append(issues, "duplicate source digest path: "+source.Path)
		}
		seenSourcePaths[source.Path] = true
		if previousSourcePath != "" && source.Path < previousSourcePath {
			issues = append(issues, "source digests are not sorted at: "+source.Path)
		}
		previousSourcePath = source.Path
		if !validParityDigest(source.Digest) {
			issues = append(issues, "invalid source digest: "+source.Path)
		}
	}
	seen := map[string]bool{}
	proofReferences := map[string]bool{}
	previousID := ""
	for _, entry := range document.Entries {
		if seen[entry.ID] {
			issues = append(issues, "duplicate entry id: "+entry.ID)
		}
		seen[entry.ID] = true
		if previousID != "" && entry.ID < previousID {
			issues = append(issues, "entries are not sorted at: "+entry.ID)
		}
		previousID = entry.ID
		for _, proof := range entry.Proof {
			proofReferences[parityProofReferenceKey(proof.Test, entry.ID)] = true
		}
		issues = append(issues, validateParityEntry(entry, index, root)...)
	}
	issues = append(issues, validateParityProofMarkers(seen, proofReferences, index)...)
	return issues
}

func validateParityProofMarkers(
	manifestIDs map[string]bool,
	proofReferences map[string]bool,
	index paritySymbolIndex,
) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	slices.Sort(names)

	var issues []string
	for _, name := range names {
		for _, id := range index[name].ParityIDs {
			if !manifestIDs[id] {
				issues = append(issues, "proof marker names unknown parity entry: "+name+" -> "+id)
			} else if !proofReferences[parityProofReferenceKey(name, id)] {
				issues = append(issues, "proof marker has no manifest proof reference: "+name+" -> "+id)
			}
		}
	}
	return issues
}

func parityProofReferenceKey(testName, entryID string) string {
	return testName + "\x00" + entryID
}

func validateParityEntry(entry parityEntry, index paritySymbolIndex, root string) []string {
	var issues []string
	if entry.ID == "" {
		issues = append(issues, "entry id is required")
	}
	if entry.Kind == "" {
		issues = append(issues, "entry kind is required: "+entry.ID)
	}
	if entry.Source == "" {
		issues = append(issues, "entry source is required: "+entry.ID)
	}
	if !validParityDigest(entry.Digest) {
		issues = append(issues, "invalid digest: "+entry.ID)
	}
	if entry.Kind == "version-branch" {
		if entry.VersionRange == "" {
			issues = append(issues, "version range is required: "+entry.ID)
		} else if !parityVersionRangePattern.MatchString(entry.VersionRange) {
			issues = append(issues, "invalid version range: "+entry.ID)
		}
	} else if entry.VersionRange != "" {
		issues = append(issues, "version range requires version-branch kind: "+entry.ID)
	}
	switch entry.Status {
	case "planned":
		if len(entry.Go) != 0 || len(entry.Proof) != 0 || entry.Spec != "" || entry.Translation != "" || entry.Tmux != "" || entry.Warning != "" || entry.Projection != "" || entry.Difference != "" {
			issues = append(issues, "planned entry contains mapping fields: "+entry.ID)
		}
		return issues
	case "handwritten", "generated", "translation":
	default:
		issues = append(issues, fmt.Sprintf("invalid status for %s: %q", entry.ID, entry.Status))
		return issues
	}
	issues = append(issues, validateParityPolicies(entry)...)

	omission := entry.Status == "translation" && parityOmissionTranslations[entry.Translation]
	if omission && len(entry.Go) != 0 {
		issues = append(issues, "omission translation must not declare Go symbols: "+entry.ID)
	} else if !omission && len(entry.Go) == 0 {
		issues = append(issues, "Go symbol is required: "+entry.ID)
	}
	seenSymbols := map[string]bool{}
	exportedDestination := false
	importableDestination := false
	portableDestination := false
	for _, symbol := range entry.Go {
		if seenSymbols[symbol] {
			issues = append(issues, "duplicate Go symbol: "+entry.ID)
		}
		seenSymbols[symbol] = true
		declaration, exists := index[symbol]
		if !exists {
			issues = append(issues, "Go symbol does not exist: "+symbol)
		} else {
			if !declaration.Production {
				issues = append(issues, "Go symbol is not a production declaration: "+symbol)
			} else {
				exportedDestination = exportedDestination || declaration.Exported
				importableDestination = importableDestination || (declaration.Exported && declaration.Importable)
				portableDestination = portableDestination ||
					(declaration.Exported && declaration.Importable && declaration.Portable)
			}
			if entry.Status == "generated" && !declaration.Generated {
				issues = append(issues, "generated Go symbol is not in generated source: "+symbol)
			}
		}
	}
	if len(entry.Go) != 0 && !parityPrivatePythonEntry(entry) && !exportedDestination {
		issues = append(issues, "public Python mapping requires an exported Go destination: "+entry.ID)
	} else if len(entry.Go) != 0 && !parityPrivatePythonEntry(entry) && !importableDestination {
		issues = append(issues, "public Python mapping requires an importable Go destination: "+entry.ID)
	} else if len(entry.Go) != 0 && !parityPrivatePythonEntry(entry) && !portableDestination {
		issues = append(issues, "public Python mapping requires a portable Go destination: "+entry.ID)
	}

	if len(entry.Proof) == 0 {
		issues = append(issues, "proof is required: "+entry.ID)
	}
	seenProofs := map[string]bool{}
	proofKinds := map[string]bool{}
	generatedEvidence := false
	for _, proof := range entry.Proof {
		key := proof.Kind + "\x00" + proof.Test
		if seenProofs[key] {
			issues = append(issues, "duplicate proof: "+entry.ID)
		}
		seenProofs[key] = true
		proofKinds[proof.Kind] = true
		if !parityProofKinds[proof.Kind] {
			issues = append(issues, "invalid proof kind: "+proof.Kind)
		}
		if parityGeneratedProofKinds[proof.Kind] {
			generatedEvidence = true
		}
		if !parityTestName(proof.Test) {
			issues = append(issues, "invalid proof test: "+proof.Test)
		} else if declaration, exists := index[proof.Test]; !exists {
			issues = append(issues, "proof test does not exist: "+proof.Test)
		} else if !declaration.Proof {
			issues = append(issues, "proof is not a valid test declaration: "+proof.Test)
		} else if !slices.Contains(declaration.ParityIDs, entry.ID) {
			issues = append(issues, "proof does not claim parity entry: "+proof.Test+" -> "+entry.ID)
		} else if proof.Kind != "compile" && !declaration.Behavior {
			if parityProofFunctionPrefix(proof.Test, "Example") {
				issues = append(issues, "behavior proof example has no output: "+proof.Test)
			} else {
				issues = append(issues, "behavior proof test has no executable evidence: "+proof.Test)
			}
		} else if proof.Kind == "real-tmux" && !parityRealTmuxProof(declaration) {
			issues = append(issues, "real-tmux proof requires marked evidence: "+proof.Test)
		}
	}
	if len(proofKinds) == 1 && proofKinds["compile"] && !parityStructuralKinds[entry.Kind] {
		issues = append(issues, "compile-only proof cannot prove behavior: "+entry.ID)
	}

	if entry.Status == "generated" {
		if !generatedEvidence {
			issues = append(issues, "generated mapping requires generated evidence: "+entry.ID)
		}
		if entry.Spec == "" {
			issues = append(issues, "generated spec is required: "+entry.ID)
		} else if !paritySpecExists(root, entry.Spec) {
			issues = append(issues, "generated spec does not exist: "+entry.Spec)
		}
	} else if entry.Spec != "" {
		issues = append(issues, "spec requires generated status: "+entry.ID)
	}

	if entry.Status == "translation" {
		if !slices.Contains(parityTranslations, entry.Translation) {
			issues = append(issues, "unknown translation: "+entry.Translation)
		}
	} else if entry.Translation != "" {
		issues = append(issues, "translation requires translation status: "+entry.ID)
	}
	return issues
}

func validParityDigest(digest string) bool {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	for _, character := range digest[len("sha256:"):] {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func parityTestName(name string) bool {
	_, function, ok := parityProofNameParts(name)
	if !ok {
		return false
	}
	return validParityProofName(function, "Test") || validParityProofName(function, "Example")
}

func parityProofFunctionPrefix(name, prefix string) bool {
	_, function, ok := parityProofNameParts(name)
	return ok && strings.HasPrefix(function, prefix)
}

func parityProofNameParts(name string) (string, string, bool) {
	separator := strings.LastIndexByte(name, '.')
	if separator <= 0 || separator == len(name)-1 {
		return "", "", false
	}
	packageName, function := name[:separator], name[separator+1:]
	if strings.ContainsAny(packageName, " \t\r\n") {
		return "", "", false
	}
	return packageName, function, true
}

func parityRealTmuxProof(declaration paritySymbol) bool {
	return declaration.RealTmux
}

func parityPrivatePythonEntry(entry parityEntry) bool {
	if strings.HasPrefix(entry.Kind, "private-") {
		return true
	}
	for component := range strings.SplitSeq(filepath.ToSlash(entry.Source), "/") {
		name := strings.TrimSuffix(component, filepath.Ext(component))
		if parityPrivatePythonName(name) {
			return true
		}
	}
	baseID, _, _ := strings.Cut(entry.ID, "#")
	return slices.ContainsFunc(strings.Split(baseID, "."), parityPrivatePythonName)
}

func parityPrivatePythonName(name string) bool {
	return strings.HasPrefix(name, "_") &&
		(!strings.HasPrefix(name, "__") || !strings.HasSuffix(name, "__"))
}

func validateParityPolicies(entry parityEntry) []string {
	values := map[string]string{
		"tmux":       entry.Tmux,
		"warning":    entry.Warning,
		"projection": entry.Projection,
		"difference": entry.Difference,
	}
	var issues []string
	for field, value := range values {
		if value == "" {
			issues = append(issues, field+" policy is required: "+entry.ID)
		} else if !parityPolicyValues[field][value] {
			issues = append(issues, "invalid "+field+" policy: "+entry.ID)
		}
	}
	if entry.Kind == "version-branch" && entry.Tmux != "source-version-gated" {
		issues = append(issues, "version branch policy is required: "+entry.ID)
	}
	if entry.Kind == "warning" && entry.Warning != "warning-handler" {
		issues = append(issues, "warning handler policy is required: "+entry.ID)
	}
	if entry.Status == "translation" && entry.Difference != "language-translation" {
		issues = append(issues, "translation difference is required: "+entry.ID)
	}
	baseID, _, _ := strings.Cut(entry.ID, "#")
	if (strings.HasSuffix(baseID, "._fetch_or_empty") || parityListLeniencyID(baseID)) && entry.Difference != "list-empty-on-error" {
		issues = append(issues, "list leniency difference is required: "+entry.ID)
	}
	return issues
}

func parityListLeniencyID(id string) bool {
	switch id {
	case "libtmux.server.Server.attached_sessions", "libtmux.server.Server.clients", "libtmux.server.Server.sessions":
		return true
	default:
		return false
	}
}

func paritySpecExists(root, spec string) bool {
	if spec == "" || filepath.IsAbs(spec) {
		return false
	}
	clean := filepath.Clean(spec)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(filepath.Join(root, clean))
	return err == nil && !info.IsDir()
}

func minimalParityManifest() parityManifest {
	return parityManifest{
		Schema:       2,
		Translations: slices.Clone(parityTranslations),
		Entries: []parityEntry{
			{
				ID:     "python.symbol",
				Kind:   "method",
				Source: "src/libtmux/example.py",
				Digest: "sha256:" + strings.Repeat("0", 64),
				Status: "planned",
			},
		},
	}
}

func setParityPolicies(entry *parityEntry) {
	entry.Tmux = "not-applicable"
	entry.Warning = "not-applicable"
	entry.Projection = "not-applicable"
	entry.Difference = "none"
}

func assertParityIssue(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return
		}
	}
	t.Fatalf("issues %q do not contain %q", issues, want)
}
