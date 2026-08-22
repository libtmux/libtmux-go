package mcp

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestKeysReachAPaneOnlyThroughTheDeliveryResolver is the gate under the
// delivery guard.
//
// The guard refuses a pane that cannot read a key, and it is reached by
// obtaining the pane rather than by remembering to call it. That only holds
// while every handler that types takes its pane from resolvePaneToDeliver: one
// that takes resolvePaneToWrite instead, and types anyway, is back to the
// defect this replaced, and nothing about the code would look wrong.
//
// The claim is about the source rather than about behaviour, so it stays true
// for tools nobody has written yet, which is what a behavioural test of the
// tools that exist cannot do.
func TestKeysReachAPaneOnlyThroughTheDeliveryResolver(t *testing.T) {
	// Ways of putting a keystroke into a pane. paste_buffer reaches tmux by a
	// different call than the rest, so both are named.
	delivers := map[string]bool{"SendKeys": true, "PasteBuffer": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			var types, resolves []string
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case delivers[selector.Sel.Name]:
					types = append(types, selector.Sel.Name)
				case strings.HasPrefix(selector.Sel.Name, "resolvePaneTo"):
					resolves = append(resolves, selector.Sel.Name)
				}
				return true
			})
			if len(types) == 0 {
				continue
			}
			checked++
			for _, resolved := range resolves {
				if resolved != "resolvePaneToDeliver" {
					t.Errorf("%s in %s calls %s and then %s: a handler that "+
						"types has to take its pane from resolvePaneToDeliver, "+
						"which is what refuses a pane that cannot read it",
						function.Name.Name, filepath.Base(name), resolved,
						strings.Join(types, " and "))
				}
			}
		}
	}
	// Without this the test passes when it matched nothing, which is what
	// renaming either delivery call would cause.
	if checked < 3 {
		t.Errorf("only %d functions were found to type into a pane; the shape "+
			"this looks for has moved", checked)
	}
}

// TestEveryClosedSetReachesTheSchema gates closedArguments against the tools
// moving out from under it. An argument renamed or a tool withdrawn leaves an
// entry naming nothing, and the set stops being published while the table
// still claims it: on the wire the argument goes back to being any string.
func TestEveryClosedSetReachesTheSchema(t *testing.T) {
	t.Setenv(SafetyEnvironmentVariable, "destructive")
	t.Setenv(RecipeToolEnvironmentVariable, "1")

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := NewServer(
		tmux.NewServer(tmux.ServerOptions{SocketName: "closed-sets-unused"}),
	).Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "closed-sets", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Decoded from the wire rather than read off the Tool, because what a
	// client validates against is the JSON, not the value that produced it.
	advertised := map[string]struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
	}{}
	for _, tool := range listed.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
		}
		schema := advertised[tool.Name]
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("%s: decode input schema: %v", tool.Name, err)
		}
		advertised[tool.Name] = schema
	}
	for name, arguments := range closedArguments {
		schema, offered := advertised[name]
		if !offered {
			t.Errorf("closedArguments names %q, which is not a tool", name)
			continue
		}
		for argument, values := range arguments {
			property, carried := schema.Properties[argument]
			if !carried {
				t.Errorf("%s has no argument %q", name, argument)
				continue
			}
			if !slices.Equal(property.Enum, values) {
				t.Errorf("%s %s: enum = %v, want %v",
					name, argument, property.Enum, values)
			}
		}
	}
}

// TestASafetyValueThatIsNotALevelIsNamed covers the silent half of failing
// closed.
//
// A misspelled level selects the lowest, which is the right direction: someone
// who set the variable at all was bounding what a model may do. But the report
// that exists to explain a short tool list then reads exactly like one from an
// operator who asked for readonly on purpose.
func TestASafetyValueThatIsNotALevelIsNamed(t *testing.T) {
	for _, level := range []struct{ value, rejected string }{
		{"destructve", "destructve"},
		// Trimmed and folded before it is matched, so neither is a rejection.
		{"  Readonly ", ""},
		{"destructive", ""},
		{"", ""},
	} {
		value, want := level.value, level.rejected
		t.Setenv(SafetyEnvironmentVariable, value)
		if got := RejectedSafetyValue(); got != want {
			t.Errorf("%q was reported as rejected %q, want %q", value, got, want)
		}
	}
	// Absent is not the same as empty: only absent means no preference.
	t.Setenv(SafetyEnvironmentVariable, "nonsense")
	if ResolvedSafetyLevel() != SafetyReadOnly {
		t.Error("a value that is not a level did not fall back to the lowest")
	}
}
