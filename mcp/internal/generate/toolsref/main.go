// Command toolsref regenerates the marked TOOLS.md region from tools and
// schemas advertised over MCP.
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
	// Generate the complete surface rather than one deployment profile.
	if err := os.Setenv(tmuxmcp.SafetyEnvironmentVariable, "destructive"); err != nil {
		return err
	}
	if err := os.Setenv(tmuxmcp.CapabilitiesEnvironmentVariable, "all"); err != nil {
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

// listTools reads the advertised protocol surface through an in-memory client.
func listTools() ([]*sdk.Tool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tools, err := tmuxmcp.AdvertisedTools(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(tools, func(a, b *sdk.Tool) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return tools, nil
}

type property struct {
	name, kind, description string
	required                bool
	values                  []string
}

func render(tools []*sdk.Tool) string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%d tools. Generated from the schemas by "+
		"`go generate ./...`; edit the tools, not this.\n", len(tools))

	for _, tool := range tools {
		fmt.Fprintf(&out, "\n### `%s`\n\n%s\n", tool.Name, sentence(tool.Description))
		if capability, ok := tool.Meta[tmuxmcp.CapabilityMetaKey].(string); ok {
			fmt.Fprintf(&out, "\nRequires the `%s` capability.\n", capability)
		}
		if kind := classify(tool); kind != "" {
			fmt.Fprintf(&out, "\n%s\n", kind)
		}
		writeTable(&out, "Argument", propertiesOf(tool.InputSchema))
		writeTable(&out, "Returns", propertiesOf(tool.OutputSchema))
	}
	return out.String()
}

// classify renders MCP tool annotations as caller guidance.
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

// propertiesOf flattens a schema's immediate properties.
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
		// Required fields sort first, then by name.
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

// typeName renders scalar or nullable-union JSON Schema types.
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

// enumNames preserves an empty string when it is a real enum member.
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
	// Omit an empty description column from generated result tables.
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

func sentence(description string) string {
	description = strings.TrimSpace(description)
	if cut := strings.Index(description, ". "); cut >= 0 {
		return description[:cut+1]
	}
	return description
}

// replaceRegion refuses missing markers instead of appending generated output.
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
