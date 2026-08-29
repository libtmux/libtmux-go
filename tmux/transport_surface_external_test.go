package tmux_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyEngineSurfaceIsAbsent(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"CommandKind":              true,
		"CommandServer":            true,
		"CommandProcess":           true,
		"Engine":                   true,
		"InstanceBoundEngine":      true,
		"EngineFallbackPolicy":     true,
		"EngineFallbackAllow":      true,
		"EngineFallbackReject":     true,
		"ErrEngineFallback":        true,
		"EngineFallbackError":      true,
		"WithEngine":               true,
		"WithEngineFallback":       true,
		"EngineFallback":           true,
		"SubprocessEngine":         true,
		"ControlPoolRequest":       true,
		"ControlPool":              true,
		"OpenControlPool":          true,
		"WarningControlPoolClosed": true,
		"WarningControlPoolUnused": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Ext(file) != ".go" || strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.FuncDecl:
				if forbidden[declaration.Name.Name] {
					t.Errorf("%s exports legacy transport identifier %s", file, declaration.Name)
				}
			case *ast.TypeSpec:
				if forbidden[declaration.Name.Name] {
					t.Errorf("%s exports legacy transport identifier %s", file, declaration.Name)
				}
			case *ast.ValueSpec:
				for _, name := range declaration.Names {
					if forbidden[name.Name] {
						t.Errorf("%s exports legacy transport identifier %s", file, name)
					}
				}
			}
			return true
		})
	}
}
