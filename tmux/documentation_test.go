package tmux_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/doc"
	"go/doc/comment"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var documentationLinkPattern = regexp.MustCompile(
	`\[(?:[A-Z_][A-Za-z0-9_]*(?:\.[A-Z_][A-Za-z0-9_]*)*|[a-z_][A-Za-z0-9_]*\.[A-Z_][A-Za-z0-9_]*(?:\.[A-Z_][A-Za-z0-9_]*)*)\]`,
)

// exampleWorkflows returns every runnable example program, discovered from the
// examples module rather than listed.
//
// A listed one is a list to keep in step with the directory, and the gates that
// consume it disagreed once already: an example was added, one list learned
// about it, and the gate that builds and runs each program did not, so the
// example nobody remembered to add was the one nothing ran.
func exampleWorkflows(t *testing.T) []string {
	t.Helper()

	root := filepath.Join(documentationModuleRoot(t), "examples")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read examples: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "internal" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), "main.go")); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) < 8 {
		t.Fatalf("found %d example programs, want at least 8", len(names))
	}
	return names
}

func exampleWorkflowSources(t *testing.T) []string {
	t.Helper()

	names := exampleWorkflows(t)
	sources := make([]string, 0, len(names))
	for _, name := range names {
		sources = append(sources, "examples/"+name+"/main.go")
	}
	return sources
}

// examplesWithoutOutput names the Example functions that compile without
// running, and says why each one does. An entry is a claim that the example
// cannot assert anything stable, which is why there are three of them.
var examplesWithoutOutput = map[string]string{
	"ExampleRunInPane":     "takes a testing.TB an Example cannot supply",
	"ExampleWaitForScreen": "takes a testing.TB an Example cannot supply",
	"ExampleRun":           "serves stdio until its client disconnects",
}

// TestEveryExampleRuns gates the difference between an example that is checked
// and one that is only compiled.
//
// Go runs an Example only when it ends in an output comment. Without one it is
// type-checked and never executed, so it renders on the package page exactly
// like a working example while being free to be wrong forever. Nothing about
// reading it reveals which kind it is, which is what this reports.
func TestEveryExampleRuns(t *testing.T) {
	t.Parallel()

	root := documentationModuleRoot(t)
	var unchecked []string
	for _, path := range exampleTestFiles(t, root) {
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, example := range doc.Examples(file) {
			name := "Example" + example.Suffix
			if example.Name != "" {
				name = "Example" + example.Name
				if example.Suffix != "" {
					name += "_" + example.Suffix
				}
			}
			if example.Output != "" || example.EmptyOutput {
				continue
			}
			if _, allowed := examplesWithoutOutput[name]; allowed {
				continue
			}
			unchecked = append(unchecked, filepath.ToSlash(relative)+": "+name)
		}
	}
	slices.Sort(unchecked)
	if len(unchecked) != 0 {
		t.Fatalf("examples that compile but never run (%d):\n%s",
			len(unchecked), strings.Join(unchecked, "\n"))
	}
}

// exampleSocketName matches a socket an example names for itself.
var exampleSocketName = regexp.MustCompile(`SocketName:\s*"(libtmux-go-example-[^"]*)"`)

// TestEveryExampleNamesItsOwnSocket gates examples against each other.
//
// Examples run one after another, each killing its server as it leaves, so two
// sharing a socket usually work. What they share is a failure: when the first
// one's teardown is slow or fails, the second finds a session that already
// exists, and the example that fails is the one that did nothing wrong.
func TestEveryExampleNamesItsOwnSocket(t *testing.T) {
	t.Parallel()

	root := documentationModuleRoot(t)
	owners := map[string][]string{}
	for _, path := range exampleTestFiles(t, root) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range exampleSocketName.FindAllStringSubmatch(string(content), -1) {
			owners[match[1]] = append(owners[match[1]], filepath.ToSlash(relative))
		}
	}

	if len(owners) == 0 {
		t.Fatal("no example socket found, so this gate proves nothing")
	}
	var shared []string
	for socket, files := range owners {
		if len(files) > 1 {
			shared = append(shared, socket+" in "+strings.Join(files, ", "))
		}
	}
	slices.Sort(shared)
	if len(shared) != 0 {
		t.Fatalf("examples share a socket:\n%s", strings.Join(shared, "\n"))
	}
}

// TestEveryPublicPackageHasAnExample gates the first thing a reader looks for.
//
// A reference page listing types and methods with nothing runnable beside them
// leaves the question it was opened to answer -- what do I write -- unanswered,
// and nothing about the page says an example was meant to be there.
func TestEveryPublicPackageHasAnExample(t *testing.T) {
	t.Parallel()

	root := documentationModuleRoot(t)
	for _, directory := range []string{"tmux", "tmuxq", "tmux/tmuxtest", "workspace", "mcp"} {
		t.Run(directory, func(t *testing.T) {
			t.Parallel()

			entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
			if err != nil {
				t.Fatal(err)
			}
			examples := 0
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				path := filepath.Join(root, filepath.FromSlash(directory), entry.Name())
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
				if err != nil {
					t.Fatalf("parse %s: %v", path, err)
				}
				examples += len(doc.Examples(file))
			}
			if examples == 0 {
				t.Errorf("%s has no example, so its reference page shows no usage", directory)
			}
		})
	}
}

// exampleTestFiles returns every test file in the repository that may declare
// an Example, across every module: a compiled-only example is the same defect
// wherever it renders.
func exampleTestFiles(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return paths
}

const tmuxModulePath = "github.com/libtmux/libtmux-go/tmux"

var removedReceiverMethods = map[string]map[string]bool{
	"Session": {
		"SessionName": true, "SessionAttached": true, "SessionWindows": true,
	},
	"Window": {
		"WindowName": true, "WindowActive": true, "WindowPanes": true,
		"WindowLinkedSessions": true,
	},
	"Pane": {
		"PaneCurrentCommand": true, "PaneTitle": true, "PaneActive": true,
		"PanePipe": true, "PanePID": true,
	},
	"Client": {
		"ClientReadonly": true, "ClientPID": true, "ClientUID": true,
		"ClientUser": true,
	},
}

func TestExportedAPIDeclarationsHaveDocumentation(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test path")
	}
	moduleRoot := documentationModuleRoot(t)
	packageDir := filepath.Dir(filename)
	packages := []string{packageDir, filepath.Join(moduleRoot, "tmuxq"), filepath.Join(moduleRoot, "tmux", "tmuxtest")}

	var violations []string
	for _, packageDirectory := range packages {
		violations = append(violations, exportedDocumentationViolations(t, moduleRoot, packageDirectory)...)
	}
	slices.Sort(violations)
	if len(violations) != 0 {
		t.Fatalf("exported API documentation violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestDeclarationContextCommentsDoNotUseDocumentationLinks(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test path")
	}
	moduleRoot := documentationModuleRoot(t)
	packageDir := filepath.Dir(filename)
	packages := []string{packageDir, filepath.Join(moduleRoot, "tmuxq"), filepath.Join(moduleRoot, "tmux", "tmuxtest")}

	var violations []string
	for _, packageDirectory := range packages {
		violations = append(violations, declarationContextDocumentationLinkViolations(t, moduleRoot, packageDirectory)...)
	}
	slices.Sort(violations)
	if len(violations) != 0 {
		t.Fatalf("documentation links in rendered declaration contexts:\n%s", strings.Join(violations, "\n"))
	}
}

// TestDocumentationLinksResolveToPackageSymbols keeps rendered documentation
// navigable. A link whose target does not exist renders as ordinary bracketed
// text rather than failing, so nothing else catches a stale one.
func TestDocumentationLinksResolveToPackageSymbols(t *testing.T) {
	t.Parallel()

	moduleRoot := documentationModuleRoot(t)
	packages := []string{documentationPackageDir(t), filepath.Join(moduleRoot, "tmuxq"), filepath.Join(moduleRoot, "tmux", "tmuxtest")}

	var violations []string
	for _, packageDirectory := range packages {
		violations = append(violations, unresolvedDocumentationLinks(t, moduleRoot, packageDirectory)...)
	}
	slices.Sort(violations)
	if len(violations) != 0 {
		t.Fatalf("documentation links without a package symbol:\n%s", strings.Join(violations, "\n"))
	}
}

func unresolvedDocumentationLinks(t *testing.T, moduleRoot, packageDirectory string) []string {
	t.Helper()

	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	relativePackage, err := filepath.Rel(moduleRoot, packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if relativePackage == "." {
		relativePackage = "tmux"
	}

	fileSet := token.NewFileSet()
	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(packageDirectory, entry.Name())
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		files = append(files, file)
	}

	topLevel, members := exportedPackageSymbols(files)
	var violations []string
	for _, file := range files {
		for _, group := range file.Comments {
			position := fileSet.Position(group.Pos())
			location := relativePackage + ": " + filepath.Base(position.Filename) + ":" + strconv.Itoa(position.Line)
			for _, link := range documentationLinkTargets(group.Text()) {
				receiver, name, qualified := strings.Cut(link, ".")
				switch {
				case !qualified:
					if !topLevel[link] {
						violations = append(violations, location+" links unknown symbol ["+link+"]")
					}
				case topLevel[receiver]:
					if !members[receiver+"."+name] {
						violations = append(violations, location+" links unknown member ["+link+"]")
					}
				}
			}
		}
	}
	return violations
}

// documentationLinkTargets reports the doc links go/doc/comment would resolve
// against this package rather than against an imported one.
func documentationLinkTargets(text string) []string {
	targets := make([]string, 0, 8)
	parser := comment.Parser{
		LookupSym: func(receiver, name string) bool {
			if receiver == "" {
				targets = append(targets, name)
			} else {
				targets = append(targets, receiver+"."+name)
			}
			return true
		},
	}
	parser.Parse(text)
	return targets
}

func exportedPackageSymbols(files []*ast.File) (topLevel, members map[string]bool) {
	topLevel = make(map[string]bool)
	members = make(map[string]bool)
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				if !typed.Name.IsExported() {
					continue
				}
				if typed.Recv == nil {
					topLevel[typed.Name.Name] = true
					continue
				}
				if receiver := receiverTypeName(typed.Recv); receiver != "" {
					members[receiver+"."+typed.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, specification := range typed.Specs {
					switch value := specification.(type) {
					case *ast.TypeSpec:
						if !value.Name.IsExported() {
							continue
						}
						topLevel[value.Name.Name] = true
						collectExportedFieldNames(members, value)
					case *ast.ValueSpec:
						for _, name := range value.Names {
							if name.IsExported() {
								topLevel[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return topLevel, members
}

func receiverTypeName(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) != 1 {
		return ""
	}
	expression := receiver.List[0].Type
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	if index, ok := expression.(*ast.IndexExpr); ok {
		expression = index.X
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return ""
}

func collectExportedFieldNames(members map[string]bool, specification *ast.TypeSpec) {
	var fields *ast.FieldList
	switch typed := specification.Type.(type) {
	case *ast.StructType:
		fields = typed.Fields
	case *ast.InterfaceType:
		fields = typed.Methods
	default:
		return
	}
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				members[specification.Name.Name+"."+name.Name] = true
			}
		}
	}
}

func TestExamplesUseFinalTypedAPI(t *testing.T) {
	moduleRoot := documentationModuleRoot(t)
	fileSet := token.NewFileSet()
	goImporter := documentationGoImporter(t, moduleRoot, fileSet)
	sources := append([]string{
		"tmux/example_test.go",
		"tmuxq/example_test.go",
		"tmux/tmuxtest/example_test.go",
	}, exampleWorkflowSources(t)...)
	for index, relative := range sources {
		path := filepath.Join(moduleRoot, filepath.FromSlash(relative))
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Errorf("parse required example %s: %v", relative, err)
			continue
		}
		info, typeErrors := typeCheckFinalAPISource(
			fileSet,
			file,
			fmt.Sprintf("documentation-example/%d", index),
			goImporter,
		)
		if len(typeErrors) != 0 {
			t.Errorf("type-check required example %s:\n%s", relative, strings.Join(typeErrors, "\n"))
			continue
		}
		for _, problem := range finalTypedAPIProblems(file, info) {
			t.Errorf("%s: %s", relative, problem)
		}
	}
}

func TestFinalTypedAPIGateNarrowsSelectorsAndComparisons(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "unrelated selectors and string comparisons",
			source: `package tmux
type Other struct{}
func (Other) PaneActive() string { return "1" }
func valid(other Other, value string) {
	_ = other.PaneActive()
	_ = other.PaneActive() == "1"
	_ = value == "0"
}`,
		},
		{
			name: "obsolete pane method",
			source: `package sample
import "github.com/libtmux/libtmux-go/tmux"
func invalid(pane tmux.Pane) { _ = pane.PaneActive() }
`,
			want: "removed receiver-stuttering method PaneActive",
		},
		{
			name: "typed boolean string comparison",
			source: `package sample
import "github.com/libtmux/libtmux-go/tmux"
func invalid(pane tmux.Pane) {
	active, _ := pane.Active()
	_ = active == "1"
}`,
			want: "compares a typed boolean workflow value with \"1\"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, "fixture.go", test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			goImporter := documentationGoImporter(t, documentationModuleRoot(t), fileSet)
			info, _ := typeCheckFinalAPISource(fileSet, file, "documentation-fixture", goImporter)
			problems := finalTypedAPIProblems(file, info)
			if test.want == "" && len(problems) != 0 {
				t.Fatalf("final API problems = %v, want none", problems)
			}
			if test.want != "" && !slices.Contains(problems, test.want) {
				t.Fatalf("final API problems = %v, want %q", problems, test.want)
			}
		})
	}
}

func typeCheckFinalAPISource(
	fileSet *token.FileSet,
	file *ast.File,
	packagePath string,
	goImporter types.Importer,
) (*types.Info, []string) {
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	var typeErrors []string
	configuration := types.Config{
		Importer: goImporter,
		Error: func(err error) {
			typeErrors = append(typeErrors, err.Error())
		},
	}
	_, _ = configuration.Check(packagePath, fileSet, []*ast.File{file}, info)
	return info, typeErrors
}

func documentationGoImporter(t *testing.T, moduleRoot string, fileSet *token.FileSet) types.Importer {
	t.Helper()
	return importer.ForCompiler(fileSet, "gc", func(importPath string) (io.ReadCloser, error) {
		command := exec.Command("go", "list", "-export", "-f={{.Export}}", importPath)
		command.Dir = moduleRoot
		command.Env = append(os.Environ(), "GOWORK=off")
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("go list export for %s: %w: %s", importPath, err, output)
		}
		export := strings.TrimSpace(string(output))
		if export == "" {
			return nil, fmt.Errorf("go list export for %s returned no file", importPath)
		}
		return os.Open(export)
	})
}

func finalTypedAPIProblems(file *ast.File, info *types.Info) []string {
	var problems []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			receiver, ok := namedType(info.TypeOf(node.X))
			if ok && receiver.Obj().Pkg() != nil && receiver.Obj().Pkg().Path() == tmuxModulePath &&
				removedReceiverMethods[receiver.Obj().Name()][node.Sel.Name] {
				problems = append(problems, "removed receiver-stuttering method "+node.Sel.Name)
			}
		case *ast.BinaryExpr:
			if node.Op != token.EQL && node.Op != token.NEQ {
				return true
			}
			if literal, ok := typedBooleanStringComparison(node.X, node.Y, info); ok {
				problems = append(problems, "compares a typed boolean workflow value with "+literal)
			}
			if literal, ok := typedBooleanStringComparison(node.Y, node.X, info); ok {
				problems = append(problems, "compares a typed boolean workflow value with "+literal)
			}
		}
		return true
	})
	return problems
}

func namedType(value types.Type) (*types.Named, bool) {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	named, ok := value.(*types.Named)
	return named, ok
}

func typedBooleanStringComparison(
	literalExpression ast.Expr,
	valueExpression ast.Expr,
	info *types.Info,
) (string, bool) {
	literal, ok := literalExpression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING || (literal.Value != `"0"` && literal.Value != `"1"`) {
		return "", false
	}
	valueType := info.TypeOf(valueExpression)
	return literal.Value, valueType != nil && types.Identical(valueType, types.Typ[types.Bool])
}

func TestExampleWorkflowsBuildAndRun(t *testing.T) {
	moduleRoot := documentationModuleRoot(t)
	// Every example is a command, so every one of them is built and run the same
	// way. An example that could only be built as a test binary would be one the
	// README calls runnable and a reader cannot run.
	for _, workflow := range exampleWorkflows(t) {
		t.Run(workflow, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "example")
			build := exec.Command("go", "build", "-o", binary, "./"+workflow)
			build.Dir = filepath.Join(moduleRoot, "examples")
			build.Env = append(os.Environ(), "GOWORK=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", workflow, err, output)
			}

			// Not t.TempDir: it puts the test's name in the path it returns, and
			// a tmux socket path may be no longer than about a hundred bytes. The
			// longest example name alone overflows that budget once the socket
			// tmux appends to this root is counted, which fails as "File name too
			// long" from tmux rather than as anything about the example.
			//nolint:usetesting // t.TempDir is what overflows the socket path
			runtimeRoot, err := os.MkdirTemp("", "ltg-ex")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
			home := filepath.Join(runtimeRoot, "home")
			tmuxRoot := filepath.Join(runtimeRoot, "tmux")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(tmuxRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, binary)
			run.Env = isolatedExampleEnvironment(home, tmuxRoot)
			if output, err := run.CombinedOutput(); err != nil {
				t.Fatalf("run %s: %v\n%s", workflow, err, output)
			}

			probe := exec.Command("tmux", "list-sessions")
			probe.Env = isolatedExampleEnvironment(home, tmuxRoot)
			if output, err := probe.CombinedOutput(); err == nil {
				t.Fatalf("%s left an isolated tmux server running:\n%s", workflow, output)
			}
		})
	}
}

// documentationModuleRoot is the directory holding go.mod, which is where the
// paths naming sibling packages and examples are rooted.
func documentationModuleRoot(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locate the module root: %v", err)
	}
	return strings.TrimSpace(string(output))
}

// documentationPackageDir is the directory this package's own source lives in.
// It is not the module root: the package sits one directory below it.
func documentationPackageDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test path")
	}
	return filepath.Dir(filename)
}

func isolatedExampleEnvironment(home, tmuxRoot string) []string {
	result := make([]string, 0, len(os.Environ())+5)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "HOME", "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "XDG_CONFIG_HOME":
			continue
		}
		result = append(result, variable)
	}
	return append(result,
		"HOME="+home,
		"TMUX=",
		"TMUX_PANE=",
		"TMUX_TMPDIR="+tmuxRoot,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
}

func TestDocumentationGatesInspectParenthesizedSingleSpecGroups(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `// Package fixture exercises documentation gates.
package fixture

// Referenced is a link target.
type Referenced struct{}

// GroupedType documents the group rather than its member.
type (
	GroupedType struct{}
)

// GroupedValue documents the group rather than its member.
var (
	GroupedValue = 1
)

var (
	// GroupedLinked refers to [Referenced].
	GroupedLinked = 2
)
`
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	documentationViolations := exportedDocumentationViolations(t, root, root)
	requireDocumentationViolation(t, documentationViolations, "GroupedType")
	requireDocumentationViolation(t, documentationViolations, "GroupedValue")

	linkViolations := declarationContextDocumentationLinkViolations(t, root, root)
	requireDocumentationViolation(t, linkViolations, "GroupedLinked")
	requireDocumentationViolation(t, linkViolations, "[Referenced]")
}

func TestDocumentationGatesInspectExportedEmbeddedFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `// Package fixture exercises documentation gates.
package fixture

// Referenced is a link target.
type Referenced struct{}

// MissingEmbedded is embedded without field documentation.
type MissingEmbedded struct{}

// LinkedEmbedded is embedded with field documentation.
type LinkedEmbedded struct{}

// Container holds embedded fields.
type Container struct {
	*MissingEmbedded
	// LinkedEmbedded refers to [Referenced].
	LinkedEmbedded
}
`
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	documentationViolations := exportedDocumentationViolations(t, root, root)
	requireDocumentationViolation(t, documentationViolations, "MissingEmbedded")

	linkViolations := declarationContextDocumentationLinkViolations(t, root, root)
	requireDocumentationViolation(t, linkViolations, "LinkedEmbedded")
	requireDocumentationViolation(t, linkViolations, "[Referenced]")
}

func requireDocumentationViolation(t *testing.T, violations []string, wanted string) {
	t.Helper()
	if !slices.ContainsFunc(violations, func(violation string) bool {
		return strings.Contains(violation, wanted)
	}) {
		t.Errorf("documentation violations %#v do not contain %q", violations, wanted)
	}
}

func declarationContextDocumentationLinkViolations(t *testing.T, moduleRoot, packageDirectory string) []string {
	t.Helper()

	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	relativePackage, err := filepath.Rel(moduleRoot, packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if relativePackage == "." {
		relativePackage = "tmux"
	}

	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(packageDirectory, entry.Name())
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range general.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					if !specification.Name.IsExported() {
						continue
					}
					fields := declarationFields(specification.Type)
					if fields == nil {
						continue
					}
					for _, field := range fields.List {
						for _, name := range fieldIdentifiers(field) {
							if name.IsExported() {
								violations = appendDocumentationLinkViolations(
									violations, fileSet, relativePackage, name, field.Doc, field.Comment,
								)
							}
						}
					}
				case *ast.ValueSpec:
					if !general.Lparen.IsValid() {
						continue
					}
					for _, name := range specification.Names {
						if name.IsExported() {
							violations = appendDocumentationLinkViolations(
								violations, fileSet, relativePackage, name, specification.Doc, specification.Comment,
							)
						}
					}
				}
			}
		}
	}
	return violations
}

func appendDocumentationLinkViolations(
	violations []string,
	fileSet *token.FileSet,
	packageName string,
	identifier *ast.Ident,
	comments ...*ast.CommentGroup,
) []string {
	for _, comment := range comments {
		if comment == nil {
			continue
		}
		for _, link := range documentationLinkPattern.FindAllString(comment.Text(), -1) {
			position := fileSet.Position(identifier.Pos())
			violations = append(violations, packageName+": "+identifier.Name+" at "+filepath.Base(position.Filename)+":"+strconv.Itoa(position.Line)+" contains "+link)
		}
	}
	return violations
}

func exportedDocumentationViolations(t *testing.T, moduleRoot, packageDirectory string) []string {
	t.Helper()

	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]*ast.File, 0, len(entries))
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(packageDirectory, entry.Name())
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		files = append(files, file)
	}

	relativePackage, err := filepath.Rel(moduleRoot, packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if relativePackage == "." {
		relativePackage = "tmux"
	}
	packageDocumented := false
	var violations []string
	for _, file := range files {
		if documentationStartsWith(file.Doc, "Package "+file.Name.Name) {
			packageDocumented = true
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if declaration.Name.IsExported() && methodReceiverExported(declaration) &&
					!documentationStartsWith(declaration.Doc, declaration.Name.Name) {
					violations = append(violations, documentationViolation(fileSet, relativePackage, declaration.Name))
				}
			case *ast.GenDecl:
				violations = append(violations, declarationDocumentationViolations(fileSet, relativePackage, declaration)...)
			}
		}
	}
	if !packageDocumented {
		violations = append(violations, relativePackage+": package overview must start with \"Package "+files[0].Name.Name+"\"")
	}
	return violations
}

func declarationDocumentationViolations(
	fileSet *token.FileSet,
	packageName string,
	declaration *ast.GenDecl,
) []string {
	var violations []string
	for _, specification := range declaration.Specs {
		switch specification := specification.(type) {
		case *ast.TypeSpec:
			documentation := specification.Doc
			if documentation == nil && !declaration.Lparen.IsValid() {
				documentation = declaration.Doc
			}
			if specification.Name.IsExported() && !documentationStartsWith(documentation, specification.Name.Name) {
				violations = append(violations, documentationViolation(fileSet, packageName, specification.Name))
			}
			if specification.Name.IsExported() {
				violations = append(violations, fieldDocumentationViolations(fileSet, packageName, specification.Type)...)
			}
		case *ast.ValueSpec:
			documentation := specification.Doc
			if documentation == nil {
				documentation = specification.Comment
			}
			if documentation == nil && !declaration.Lparen.IsValid() {
				documentation = declaration.Doc
			}
			for _, name := range specification.Names {
				if name.IsExported() && !documentationStartsWith(documentation, name.Name) {
					violations = append(violations, documentationViolation(fileSet, packageName, name))
				}
			}
		}
	}
	return violations
}

func methodReceiverExported(declaration *ast.FuncDecl) bool {
	if declaration.Recv == nil {
		return true
	}
	receiver := declaration.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	if indexed, ok := receiver.(*ast.IndexExpr); ok {
		receiver = indexed.X
	}
	if indexed, ok := receiver.(*ast.IndexListExpr); ok {
		receiver = indexed.X
	}
	identifier, ok := receiver.(*ast.Ident)
	return ok && identifier.IsExported()
}

func fieldDocumentationViolations(fileSet *token.FileSet, packageName string, expression ast.Expr) []string {
	fields := declarationFields(expression)
	if fields == nil {
		return nil
	}

	var violations []string
	for _, field := range fields.List {
		documentation := field.Doc
		if documentation == nil {
			documentation = field.Comment
		}
		for _, name := range fieldIdentifiers(field) {
			if name.IsExported() && !documentationStartsWith(documentation, name.Name) {
				violations = append(violations, documentationViolation(fileSet, packageName, name))
			}
		}
	}
	return violations
}

func declarationFields(expression ast.Expr) *ast.FieldList {
	switch expression := expression.(type) {
	case *ast.StructType:
		return expression.Fields
	case *ast.InterfaceType:
		return expression.Methods
	default:
		return nil
	}
}

func fieldIdentifiers(field *ast.Field) []*ast.Ident {
	if len(field.Names) != 0 {
		return field.Names
	}
	identifier := embeddedFieldIdentifier(field.Type)
	if identifier == nil {
		return nil
	}
	return []*ast.Ident{identifier}
}

func embeddedFieldIdentifier(expression ast.Expr) *ast.Ident {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression
	case *ast.SelectorExpr:
		return expression.Sel
	case *ast.StarExpr:
		return embeddedFieldIdentifier(expression.X)
	case *ast.IndexExpr:
		return embeddedFieldIdentifier(expression.X)
	case *ast.IndexListExpr:
		return embeddedFieldIdentifier(expression.X)
	case *ast.ParenExpr:
		return embeddedFieldIdentifier(expression.X)
	default:
		return nil
	}
}

func documentationStartsWith(documentation *ast.CommentGroup, name string) bool {
	if documentation == nil {
		return false
	}
	text := strings.TrimSpace(documentation.Text())
	return text == name || strings.HasPrefix(text, name+" ") || strings.HasPrefix(text, name+"\n")
}

func documentationViolation(fileSet *token.FileSet, packageName string, identifier *ast.Ident) string {
	position := fileSet.Position(identifier.Pos())
	return packageName + ": " + identifier.Name + " at " + filepath.Base(position.Filename) + ":" + strconv.Itoa(position.Line)
}

// taskIndexHeading is the package doc section that points a new reader at an
// entry point. Every symbol it names is a destination a reader lands on.
const taskIndexHeading = "# Where to start"

// taskIndexLink matches a doc link inside the task index.
var taskIndexLink = regexp.MustCompile(`\[([A-Z]\w*(?:\.\w+)?)\]`)

// taskIndexSymbols returns the symbols the package doc's task index names.
func taskIndexSymbols(t *testing.T) []string {
	t.Helper()

	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, documentationPackageDir(t), nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for name, parsed := range packages {
		if name != "tmux" {
			continue
		}
		for _, file := range parsed.Files {
			if file.Doc == nil || !strings.Contains(file.Doc.Text(), taskIndexHeading) {
				continue
			}
			text := file.Doc.Text()
			start := strings.Index(text, taskIndexHeading) + len(taskIndexHeading)
			rest := text[start:]
			if end := strings.Index(rest, "\n# "); end >= 0 {
				rest = rest[:end]
			}
			var symbols []string
			for _, match := range taskIndexLink.FindAllStringSubmatch(rest, -1) {
				if !slices.Contains(symbols, match[1]) {
					symbols = append(symbols, match[1])
				}
			}
			slices.Sort(symbols)
			return symbols
		}
	}
	t.Fatalf("no package doc contains a %q section", taskIndexHeading)
	return nil
}

// TestTaskIndexEntryPointsCarryRunnableExamples holds the package doc's task
// index to the standard a reader expects of it.
//
// The index is the first screen pkg.go.dev renders, and it is where a reader
// who knows what they want to do finds the symbol that does it. Landing on a
// symbol page with a signature and prose but nothing to copy is the state this
// test exists to prevent. It is a gate rather than a one-time sweep because
// coverage decays: an entry added with the feature it documents passes review
// while quietly reopening the gap.
func TestTaskIndexEntryPointsCarryRunnableExamples(t *testing.T) {
	t.Parallel()

	symbols := taskIndexSymbols(t)
	if len(symbols) == 0 {
		t.Fatal("the task index names no symbols")
	}

	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, documentationPackageDir(t), nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	examples := map[string]bool{}
	for _, parsed := range packages {
		for _, file := range parsed.Files {
			for _, declaration := range file.Decls {
				function, isFunction := declaration.(*ast.FuncDecl)
				if !isFunction || function.Recv != nil {
					continue
				}
				if name, found := strings.CutPrefix(function.Name.Name, "Example"); found {
					examples[strings.ReplaceAll(name, "_", ".")] = true
				}
			}
		}
	}

	var uncovered []string
	for _, symbol := range symbols {
		if !examples[symbol] {
			uncovered = append(uncovered, symbol)
		}
	}
	if len(uncovered) != 0 {
		t.Fatalf(
			"task index entry points without a runnable example:\n  %s\n"+
				"add Example%s-shaped functions, or drop the entry from the index",
			strings.Join(uncovered, "\n  "),
			strings.ReplaceAll(uncovered[0], ".", "_"),
		)
	}
}
