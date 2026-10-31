package tmux

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const capabilitySchema = "libtmux-capabilities/1"

// emitCapabilitiesEnv names the variable that makes the drift test rewrite
// the committed manifest instead of comparing against it.
const emitCapabilitiesEnv = "LIBTMUX_EMIT_CAPABILITIES"

type capabilityManifest struct {
	Capabilities []capabilityRecord `json:"capabilities"`
	Floor        struct {
		Minimum string `json:"minimum"`
	} `json:"floor"`
	Port        string             `json:"port"`
	Schema      string             `json:"schema"`
	Workarounds []workaroundRecord `json:"workarounds"`
}

func renderCapabilityManifest(t *testing.T) []byte {
	t.Helper()
	manifest := capabilityManifest{
		Capabilities: capabilityRegistry,
		Port:         "go",
		Schema:       capabilitySchema,
		Workarounds:  workaroundRegistry,
	}
	manifest.Floor.Minimum = MinimumSupportedVersion
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	return out.Bytes()
}

func capabilityManifestPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTheCommittedManifestIsTheRegistry fails when capabilities.json differs
// from the registry. Regenerate it with
// LIBTMUX_EMIT_CAPABILITIES=1 go test ./tmux -run TestTheCommittedManifestIsTheRegistry.
func TestTheCommittedManifestIsTheRegistry(t *testing.T) {
	want := renderCapabilityManifest(t)
	path := capabilityManifestPath(t)
	if os.Getenv(emitCapabilitiesEnv) != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v; run with %s=1 to write it", err, emitCapabilitiesEnv)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("capabilities.json differs from the registry; run with %s=1 to rewrite it", emitCapabilitiesEnv)
	}
}

// supportedStableReleases lists the tagged tmux releases the repository
// names, in release order: the supported releases in the CI manifest and every
// release the registry itself cites. The manifest schema has no
// field for FixedIn, so the docs read a workaround's Releases; this list is
// how FixedIn stays tied to them. A release that is in neither place is not
// known to it, so a new tmux release enters by joining the CI manifest.
func supportedStableReleases(t *testing.T) []string {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join("..", ".github", "ci-matrix.txt"))
	if err != nil {
		t.Fatalf("read the CI manifest: %v", err)
	}
	found := map[string]bool{}
	add := func(raw string) {
		raw = strings.Trim(strings.TrimSpace(raw), `'"`)
		if _, ok := versionKey(raw); ok && !strings.Contains(raw, "-rc") && !strings.HasPrefix(raw, "next") {
			found[raw] = true
		}
	}
	listed := false
	for line := range strings.Lines(string(manifest)) {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "supported" {
			add(fields[1])
			listed = true
		}
	}
	if !listed {
		t.Fatal("the CI manifest lists no supported tmux release")
	}
	for _, record := range capabilityRegistry {
		add(record.Since)
		for _, raw := range record.Except {
			add(raw)
		}
	}
	for _, record := range workaroundRegistry {
		add(record.FixedIn)
		for _, raw := range record.Releases {
			add(raw)
		}
	}
	releases := make([]string, 0, len(found))
	for raw := range found {
		releases = append(releases, raw)
	}
	slices.SortFunc(releases, func(a, b string) int {
		left, _ := versionKey(a)
		right, _ := versionKey(b)
		return slices.Compare(left[:], right[:])
	})
	return releases
}

// TestWorkaroundReleasesAreTheReleasesBeforeFixedIn fails when a workaround's
// FixedIn moves without its Releases, or the reverse.
func TestWorkaroundReleasesAreTheReleasesBeforeFixedIn(t *testing.T) {
	releases := supportedStableReleases(t)
	for _, record := range workaroundRegistry {
		first, fixed := registryVersion(record.Releases[0]), registryVersion(record.FixedIn)
		var want []string
		for _, release := range releases {
			version := registryVersion(release)
			if version.meetsMinimum(first) && !version.meetsMinimum(fixed) {
				want = append(want, release)
			}
		}
		if !slices.Equal(want, record.Releases) {
			t.Errorf("%s: Releases = %v, but the releases from %s up to %s are %v",
				record.ID, record.Releases, record.Releases[0], record.FixedIn, want)
		}
	}
}

func TestRegistryAgreesWithPublicVersions(t *testing.T) {
	if got := capabilitySince("control-mode").String(); got != MinimumConnectionVersion {
		t.Fatalf("control-mode since %q, MinimumConnectionVersion %q", got, MinimumConnectionVersion)
	}
	if _, err := ParseVersion(MinimumSupportedVersion); err != nil {
		t.Fatal(err)
	}
}

var capabilityIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func TestRegistryRecordsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	check := func(id string, versions ...string) {
		if !capabilityIDPattern.MatchString(id) || seen[id] {
			t.Errorf("id %q is not kebab-case or repeats", id)
		}
		seen[id] = true
		for _, raw := range versions {
			if _, ok := versionKey(raw); !ok {
				t.Errorf("%s names %q, which tmux never prints", id, raw)
			}
		}
	}
	for _, record := range capabilityRegistry {
		check(record.ID, append([]string{record.Since}, record.Except...)...)
		if record.Handling == handlingRefuse && record.Error == "" {
			t.Errorf("%s refuses without naming its error", record.ID)
		}
	}
	for _, record := range workaroundRegistry {
		check(record.ID, append([]string{record.FixedIn}, record.Releases...)...)
		if record.Handling == handlingFallback {
			t.Errorf("%s: a workaround refuses or repairs", record.ID)
		}
		for _, command := range record.Commands {
			for _, flag := range command.Flags {
				if len(flag) != 2 || flag[0] != '-' {
					t.Errorf("%s: flag %q is not a single letter", record.ID, flag)
				}
			}
		}
	}
}

// TestRegistryNamesPublicSymbols resolves every API name in the registry to
// an exported declaration of this package.
func TestRegistryNamesPublicSymbols(t *testing.T) {
	symbols := exportedSymbols(t)
	var names []string
	for _, record := range capabilityRegistry {
		names = append(names, append([]string{record.Error}, record.APIs...)...)
	}
	for _, record := range workaroundRegistry {
		names = append(names, record.APIs...)
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "tmux.") || !symbols[strings.TrimPrefix(name, "tmux.")] {
			t.Errorf("%q is not a public symbol of package tmux", name)
		}
	}
}

// exportedSymbols maps Name, Type.Method and Type.Field of the package's
// non-test files.
func exportedSymbols(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	symbols := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if !declaration.Name.IsExported() {
					continue
				}
				if declaration.Recv == nil {
					symbols[declaration.Name.Name] = true
					continue
				}
				if receiver := receiverName(declaration.Recv.List[0].Type); receiver != "" {
					symbols[receiver+"."+declaration.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						symbols[spec.Name.Name] = true
						if structure, ok := spec.Type.(*ast.StructType); ok {
							for _, field := range structure.Fields.List {
								for _, name := range field.Names {
									symbols[spec.Name.Name+"."+name.Name] = true
								}
							}
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							symbols[name.Name] = true
						}
					}
				}
			}
		}
	}
	return symbols
}

func receiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.StarExpr:
		return receiverName(expression.X)
	case *ast.Ident:
		return expression.Name
	case *ast.IndexExpr:
		return receiverName(expression.X)
	}
	return ""
}
