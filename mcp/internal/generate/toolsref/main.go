// Command toolsref keeps the tool reference identical to the tools.
//
// Every argument already describes itself in the schema, and a gate requires
// it. Copying those descriptions into markdown by hand puts the same sentence
// in two places, and only one of them is checked: a tool that gains an
// argument, loses one, or changes what it says leaves the prose behind with
// nothing to catch it.
//
// So the reference is rendered from the server itself. The tools are listed
// through the protocol, at the level that offers all of them, and written
// between a marker pair in a markdown file:
//
//	<!-- toolsref -->
//	<!-- toolsref:end -->
//
// Everything between those lines is replaced. Markdown outside them is never
// touched, so the prose that explains when to reach for what stays
// hand-written. The result is checked in and regenerating it must leave the
// tree unchanged, which is the gate the other generators here are held to.
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	openMarker  = "<!-- toolsref -->"
	closeMarker = "<!-- toolsref:end -->"
)

func main() {
	output := flag.String("output", "TOOLS.md", "the markdown file to write into")
	flag.Parse()
	if err := run(*output); err != nil {
		fmt.Fprintln(os.Stderr, "toolsref:", err)
		os.Exit(1)
	}
}

func run(output string) error {
	// Every tool, including the ones a safety level would withhold: a
	// reference that omitted them would describe one deployment rather than
	// the server.
	if err := os.Setenv(tmuxmcp.SafetyEnvironmentVariable, "destructive"); err != nil {
		return err
	}
	tools, err := listTools()
	if err != nil {
		return err
	}
	rendered := render(tools)

	existing, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	replaced, err := replaceRegion(string(existing), rendered)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(output), err)
	}
	if replaced == string(existing) {
		fmt.Printf("toolsref: %s already matches the %d tools\n",
			filepath.Base(output), len(tools))
		return nil
	}
	return os.WriteFile(output, []byte(replaced), 0o644) //nolint:gosec // documentation
}

// listTools asks the server what it advertises, through the protocol, so the
// reference describes what a client is actually offered.
func listTools() ([]*sdk.Tool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// No tmux is contacted: listing the tools is answered from registration.
	target := tmux.NewServer(tmux.ServerOptions{SocketName: "toolsref-unused"})
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil); err != nil {
		return nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "toolsref", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(listed.Tools, func(a, b *sdk.Tool) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return listed.Tools, nil
}

// property is one argument or one result field, flattened out of the schema.
type property struct {
	name, kind, description string
	required                bool
	// values is the closed set the schema names, empty when any value goes.
	values []string
}

// render writes the reference for every tool.
func render(tools []*sdk.Tool) string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%d tools. Generated from the schemas by "+
		"`go generate ./...`; edit the tools, not this.\n", len(tools))

	for _, tool := range tools {
		fmt.Fprintf(&out, "\n### `%s`\n\n%s\n", tool.Name, sentence(tool.Description))
		if kind := classify(tool); kind != "" {
			fmt.Fprintf(&out, "\n%s\n", kind)
		}
		writeTable(&out, "Argument", propertiesOf(tool.InputSchema))
		writeTable(&out, "Returns", propertiesOf(tool.OutputSchema))
	}
	return out.String()
}

// classify says what a client may assume before calling, which is what the
// annotations are for.
func classify(tool *sdk.Tool) string {
	annotations := tool.Annotations
	if annotations == nil {
		return ""
	}
	switch {
	case annotations.ReadOnlyHint:
		return "Reads only. Repeating it changes nothing."
	case annotations.DestructiveHint != nil && *annotations.DestructiveHint:
		return "**Ends something.** Nothing brings it back, and it is withheld " +
			"below the `destructive` safety level."
	case annotations.IdempotentHint:
		return "Changes tmux to a state. Repeating it is safe."
	default:
		return "Changes tmux by a step. Repeating it compounds."
	}
}

// propertiesOf flattens one schema's own properties, in schema order where the
// schema records one and by name otherwise.
func propertiesOf(schema any) []property {
	if schema == nil {
		return nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var decoded struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
			Enum        []any  `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	required := map[string]bool{}
	for _, name := range decoded.Required {
		required[name] = true
	}
	out := make([]property, 0, len(decoded.Properties))
	for name, value := range decoded.Properties {
		out = append(out, property{
			name: name, kind: typeName(value.Type),
			description: value.Description, required: required[name],
			values: enumNames(value.Enum),
		})
	}
	slices.SortFunc(out, func(a, b property) int {
		// Required first, then by name: a reader wants the ones they must send.
		if a.required != b.required {
			if a.required {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.name, b.name)
	})
	return out
}

// typeName renders a JSON Schema type, which is a string or a list of them
// when a value may be absent.
func typeName(kind any) string {
	switch typed := kind.(type) {
	case string:
		return typed
	case []any:
		names := make([]string, 0, len(typed))
		for _, one := range typed {
			if name, ok := one.(string); ok && name != "null" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return "null"
		}
		return strings.Join(names, " or ")
	default:
		return "object"
	}
}

// enumNames renders a closed set for the type column, where naming the values
// says more than "string" does. An empty string is a real member wherever a
// tool documents one as its default, so it is shown rather than dropped.
func enumNames(values []any) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, ok := value.(string)
		if !ok {
			return nil
		}
		if name == "" {
			name = `""`
		}
		names = append(names, "`"+name+"`")
	}
	return names
}

func writeTable(out *bytes.Buffer, heading string, properties []property) {
	if len(properties) == 0 {
		return
	}
	// A result's fields carry no descriptions -- the schema for them is
	// generated from Go types, and their names are the documentation. A column
	// of dashes says less than no column.
	described := slices.ContainsFunc(properties, func(one property) bool {
		return one.description != ""
	})
	if described {
		fmt.Fprintf(out, "\n| %s | Type | |\n| --- | --- | --- |\n", heading)
	} else {
		fmt.Fprintf(out, "\n| %s | Type |\n| --- | --- |\n", heading)
	}
	for _, one := range properties {
		if len(one.values) > 0 {
			one.kind = strings.Join(one.values, ", ")
		}
		name := "`" + one.name + "`"
		if one.required {
			name += " **required**"
		}
		if described {
			fmt.Fprintf(out, "| %s | %s | %s |\n", name, one.kind,
				cmp.Or(one.description, "—"))
			continue
		}
		fmt.Fprintf(out, "| %s | %s |\n", name, one.kind)
	}
}

// sentence keeps a description to its first sentence, which is what a
// reference entry wants; the rest is guidance the prose around it carries.
func sentence(description string) string {
	description = strings.TrimSpace(description)
	if cut := strings.Index(description, ". "); cut >= 0 {
		return description[:cut+1]
	}
	return description
}

// replaceRegion swaps what lies between the markers, and refuses a file that
// does not carry both rather than appending to it.
func replaceRegion(document, rendered string) (string, error) {
	open := strings.Index(document, openMarker)
	if open < 0 {
		return "", fmt.Errorf("no %s marker", openMarker)
	}
	end := strings.Index(document[open:], closeMarker)
	if end < 0 {
		return "", fmt.Errorf("no %s marker after %s", closeMarker, openMarker)
	}
	end += open
	return document[:open+len(openMarker)] + "\n\n" + rendered +
		"\n" + document[end:], nil
}
