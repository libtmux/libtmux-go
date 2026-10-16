package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
)

func TestQueries(t *testing.T) {
	exampletest.Program(t, run)
}

// Each query prints under a string that says what its filter is. The string
// sits beside the expression it names, and this holds the two to being the
// same text, so the output cannot label one filter with another's source.
func TestEveryQueryIsLabeledWithItsOwnSource(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "main.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	squeeze := func(text string) string { return strings.Join(strings.Fields(text), "") }

	checked := 0
	ast.Inspect(file, func(node ast.Node) bool {
		entry, ok := node.(*ast.CompositeLit)
		if !ok || entry.Type != nil || len(entry.Elts) != 2 {
			return true
		}
		label, ok := entry.Elts[0].(*ast.BasicLit)
		if !ok || label.Kind != token.STRING {
			return true
		}
		named, err := strconv.Unquote(label.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", label.Value, err)
		}
		expression := entry.Elts[1]
		written := string(source[fileSet.Position(expression.Pos()).Offset:fileSet.Position(expression.End()).Offset])
		checked++
		if squeeze(named) != squeeze(written) {
			t.Errorf("a query is labeled\n\t%s\nbut its filter is\n\t%s", named, written)
		}
		return true
	})
	if checked < 5 {
		t.Fatalf("checked %d labeled queries, want at least 5; the scan is broken", checked)
	}
}
