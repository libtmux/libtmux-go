package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A published snippet is a promise that the code in it runs. The generator
// copies each block out of an example the suite executes, which proves the
// lines are real but not that they are complete: the region starts wherever
// the marker sits, so every binding created above it stays in scope for the
// compiler and disappears for the reader. Go, .NET and Java all shipped an
// opening example that could not run as printed for exactly that reason,
// with green example suites throughout.
//
// So each published region is compiled here on its own, in a module that
// contains nothing else. Bindings it does not create must be declared by its
// marker:
//
//	// docs:watching given:ctx context.Context; session tmux.Session
//
// The declarations are emitted as local variables, which makes the check
// exact in both directions: a binding the region uses and the marker omits
// fails as "undefined", and one the marker names and the region never touches
// fails as "declared and not used". Neither can be satisfied by prose.
//
// Bindings the region creates itself are exempt from that second half. A
// snippet answering "how do I list sessions" ends on the listing, and Go
// would reject the unused result even though showing it is the entire point,
// so each one is read once through the blank identifier.

var goDirective = regexp.MustCompile(`(?m)^go (\S+)$`)

var (
	packageUse  = regexp.MustCompile(`\b([a-z][a-z0-9_]*)\.[A-Z_]`)
	importBlock = regexp.MustCompile(`(?ms)^import \(\n(.*?)^\)$`)
)

func TestPublishedRegionsCompileAlone(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	regions, err := collectRegions(root)
	if err != nil {
		t.Fatalf("collect regions: %v", err)
	}
	names, err := publishedRegions(root)
	if err != nil {
		t.Fatalf("scan markdown: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no published regions found; the scan is broken, not the docs")
	}

	for _, name := range names {
		source, ok := regions[name]
		if !ok {
			t.Errorf("%s: published but defined in no Go file", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if out, err := compileAlone(t, absRoot, source); err != nil {
				t.Errorf("region %q does not compile on its own:\n%s\n"+
					"declare what it assumes on its marker in %s, as\n"+
					"\t// docs:%s given:name Type; name Type",
					name, out, source.origin, name)
			}
		})
	}
}

// publishedRegions names every region the Markdown quotes, sorted and
// deduplicated: a region shown in two documents is one subtest, not two.
func publishedRegions(root string) ([]string, error) {
	documents, err := findMarkdown(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, document := range documents {
		content, err := os.ReadFile(document)
		if err != nil {
			return nil, err
		}
		for line := range strings.SplitSeq(string(content), "\n") {
			match := markdownStart.FindStringSubmatch(line)
			if match != nil && !markdownEnd.MatchString(line) {
				names = append(names, match[1])
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// compileAlone builds the region as the only code in a throwaway module. The
// module lives outside the repository because the generate check fails on any
// untracked file, ignored or not.
func compileAlone(t *testing.T, absRoot string, source region) (string, error) {
	t.Helper()
	dir := t.TempDir()

	imports, err := importsFor(source)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("package main\n\n")
	if imports != "" {
		b.WriteString("import (\n" + imports + ")\n\n")
	}
	b.WriteString("func region() error {\n")
	for _, declaration := range givenDeclarations(source.given) {
		b.WriteString("\tvar " + declaration + "\n")
	}
	for _, line := range source.lines {
		b.WriteString("\t" + line + "\n")
	}
	for _, name := range regionDeclares(source.lines) {
		b.WriteString("\t_ = " + name + "\n")
	}
	b.WriteString("\treturn nil\n}\n\nfunc main() { _ = region() }\n")

	write := func(name, body string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if err := write("main.go", b.String()); err != nil {
		return "", err
	}
	language, err := languageVersion(absRoot)
	if err != nil {
		return "", err
	}
	if err := write("go.mod", "module isolationcheck\n\ngo "+language+"\n\n"+
		"require github.com/libtmux/libtmux-go v0.0.0\n\n"+
		"replace github.com/libtmux/libtmux-go => "+absRoot+"\n"); err != nil {
		return "", err
	}

	command := exec.Command("go", "build", "./...")
	command.Dir = dir
	// GOFLAGS keeps the throwaway module from consulting the network for a
	// dependency set that is entirely local: the root module requires nothing.
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := command.CombinedOutput()
	return string(out), err
}

// givenDeclarations splits "ctx context.Context; session tmux.Session" into
// one Go declaration per binding.
func givenDeclarations(given string) []string {
	var out []string
	for part := range strings.SplitSeq(given, ";") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// importsFor returns the lines of the origin file's import block that the
// region actually needs. Carrying the whole block over would fail as
// "imported and not used" and say nothing about the region.
func importsFor(source region) (string, error) {
	content, err := os.ReadFile(source.origin)
	if err != nil {
		return "", err
	}
	block := importBlock.FindStringSubmatch(string(content))
	if block == nil {
		return "", nil
	}

	used := map[string]bool{}
	for _, match := range packageUse.FindAllStringSubmatch(
		strings.Join(source.lines, "\n")+"\n"+source.given, -1) {
		used[match[1]] = true
	}

	var kept []string
	for line := range strings.SplitSeq(block[1], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if used[importedName(line)] {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return "", nil
	}
	return strings.Join(kept, "\n") + "\n", nil
}

// importedName is the identifier an import line binds: its alias when it has
// one, otherwise the last element of the path.
func importedName(line string) string {
	// A trailing comment would otherwise be read as the path, so the quoted
	// path is taken directly rather than by counting fields.
	quoted := strings.Index(line, `"`)
	if quoted == -1 {
		return ""
	}
	end := strings.Index(line[quoted+1:], `"`)
	if end == -1 {
		return ""
	}
	path := line[quoted+1 : quoted+1+end]
	if alias := strings.Fields(strings.TrimSpace(line[:quoted])); len(alias) == 1 {
		return alias[0]
	}
	return path[strings.LastIndex(path, "/")+1:]
}

// languageVersion is the go directive of the module under test, so the
// throwaway module states the floor once rather than keeping a copy of it.
func languageVersion(absRoot string) (string, error) {
	content, err := os.ReadFile(filepath.Join(absRoot, "go.mod"))
	if err != nil {
		return "", err
	}
	match := goDirective.FindStringSubmatch(string(content))
	if match == nil {
		return "", errors.New("no go directive in the module under test")
	}
	return match[1], nil
}

// regionDeclares names the bindings the region introduces at its own top
// level. Nested scopes are skipped: a name bound inside an if or a loop is
// gone by the closing brace, so nothing after the body could read it anyway.
func regionDeclares(lines []string) []string {
	source := "package p\n\nfunc region() (err error) {\n" +
		strings.Join(lines, "\n") + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "region.go", source, 0)
	if err != nil {
		// A body that does not parse fails the build below with a better
		// message than anything this could add.
		return nil
	}

	var names []string
	seen := map[string]bool{}
	add := func(expression ast.Expr) {
		identifier, ok := expression.(*ast.Ident)
		if !ok || identifier.Name == "_" || identifier.Name == "err" || seen[identifier.Name] {
			return
		}
		seen[identifier.Name] = true
		names = append(names, identifier.Name)
	}

	declaration, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		return nil
	}
	for _, statement := range declaration.Body.List {
		switch typed := statement.(type) {
		case *ast.AssignStmt:
			if typed.Tok == token.DEFINE {
				for _, target := range typed.Lhs {
					add(target)
				}
			}
		case *ast.DeclStmt:
			general, ok := typed.Decl.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, target := range value.Names {
					add(target)
				}
			}
		}
	}
	return names
}

// The check above passes when every region is honest, which is also what it
// would do if it had quietly stopped compiling anything. These pin the
// mechanism to the failures it exists to catch, and to the passing case
// between them, so a refactor that defeats it fails here rather than going
// unnoticed until a reader copies a snippet that cannot run.
func TestIsolationCheckFailsForTheIntendedReasons(t *testing.T) {
	t.Parallel()
	absRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	// A snippet shaped like the real ones: it needs exactly two bindings it
	// does not create, and it checks the error it does.
	body := []string{
		`session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "demo"})`,
		`if err != nil {`,
		`	return err`,
		`}`,
	}
	origin := filepath.Join("..", "..", "..", "..", "examples", "filter-query", "main.go")

	for _, testCase := range []struct {
		name  string
		given string
		want  string
	}{
		{
			name:  "declared exactly",
			given: "ctx context.Context; server tmux.Server",
		},
		{
			name:  "binding used but not declared",
			given: "ctx context.Context",
			want:  "undefined: server",
		},
		{
			name:  "binding declared but not used",
			given: "ctx context.Context; server tmux.Server; unused string",
			want:  "declared and not used: unused",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			out, err := compileAlone(t, absRoot,
				region{origin: origin, lines: body, given: testCase.given})
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("an exactly declared region must compile:\n%s", out)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a compile failure, got none:\n%s", out)
			}
			if !strings.Contains(out, testCase.want) {
				t.Errorf("failed for the wrong reason\nwant substring: %s\ngot:\n%s",
					testCase.want, out)
			}
		})
	}
}
