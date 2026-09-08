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
	check := flag.Bool("check", false, "fail instead of writing when the reference has drifted")
	flag.Parse()
	if err := run(*output, *check); err != nil {
		fmt.Fprintln(os.Stderr, "toolsref:", err)
		os.Exit(1)
	}
}

func run(output string, check bool) error {
	// Generate the complete surface rather than one deployment profile.
	for _, name := range []string{
		tmuxmcp.ToolsEnvironmentVariable,
		tmuxmcp.ExcludeToolsEnvironmentVariable,
		tmuxmcp.SafetyEnvironmentVariable,
		tmuxmcp.RecipeToolEnvironmentVariable,
	} {
		if err := os.Unsetenv(name); err != nil {
			return err
		}
	}
	if err := os.Setenv(
		tmuxmcp.ToolsetsEnvironmentVariable,
		"inspect,manage,execute,teardown",
	); err != nil {
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
	if check {
		return fmt.Errorf("generated %s has drifted; run go generate ./... to refresh it", filepath.Base(output))
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
		fmt.Fprintf(&out, "\n### `%s`\n\n%s\n", tool.Name, strings.TrimSpace(tool.Description))
		if capability, ok := tool.Meta[tmuxmcp.CapabilityMetaKey].(map[string]any); ok {
			if toolset, ok := capability["toolset"].(string); ok {
				fmt.Fprintf(&out, "\nBelongs to the `%s` toolset.\n", toolset)
			}
			writeCapability(&out, capability)
		}
		if kind := classify(tool); kind != "" {
			fmt.Fprintf(&out, "\n%s\n", kind)
		}
		writeTable(&out, "Argument", propertiesOf(tool.InputSchema))
		writeTable(&out, "Returns", propertiesOf(tool.OutputSchema))
	}
	return out.String()
}

// writeCapability renders the security-relevant manifest row next to the
// native schemas. It deliberately reads the same SDK metadata clients receive,
// rather than maintaining a documentation-only classification table.
func writeCapability(out *bytes.Buffer, capability map[string]any) {
	fmt.Fprint(out, "\n| Capability | Manifest value |\n| --- | --- |\n")
	writeCapabilityRow(out, "processReach", codeValue(capability["processReach"]))
	writeCapabilityRow(out, "tmuxEffects", codeList(stringsOf(capability["tmuxEffects"])))
	writeCapabilityRow(out, "outputClasses", codeList(stringsOf(capability["outputClasses"])))
	writeCapabilityRow(out, "mayExposeSecrets", boolValue(capability["mayExposeSecrets"]))
	writeCapabilityRow(out, "mayReturnUntrustedContent", boolValue(capability["mayReturnUntrustedContent"]))
	writeCapabilityRow(out, "amplifiesFutureInput", boolValue(capability["amplifiesFutureInput"]))
	writeCapabilityRow(out, "inputLiteralization", keyedStrings(capability["inputLiteralization"]))
	writeCapabilityRow(out, "nestedAuthority", codeList(stringsOf(capability["nestedAuthority"])))
	writeCapabilityRow(out, "annotations", "`readOnlyHint=false`, `destructiveHint=true`, "+
		"`idempotentHint=false`, `openWorldHint=true`")
}

func writeCapabilityRow(out *bytes.Buffer, name, value string) {
	if value == "" {
		value = "none"
	}
	fmt.Fprintf(out, "| `%s` | %s |\n", name, value)
}

func codeValue(value any) string {
	text, _ := value.(string)
	if text == "" {
		return ""
	}
	return "`" + text + "`"
}

func boolValue(value any) string {
	boolean, _ := value.(bool)
	return fmt.Sprintf("`%t`", boolean)
}

func codeList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

func keyedStrings(value any) string {
	typed, ok := value.(map[string]string)
	if !ok || len(typed) == 0 {
		return ""
	}
	keys := make([]string, 0, len(typed))
	for key := range typed {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	rows := make([]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, "`"+key+"`: `"+typed[key]+"`")
	}
	return strings.Join(rows, "; ")
}

// classify renders the manifest's effect set as caller guidance. Runtime MCP
// annotations are deliberately conservative when configuration provenance is
// unknown, so they are not a semantic tool taxonomy.
func classify(tool *sdk.Tool) string {
	capability, ok := tool.Meta[tmuxmcp.CapabilityMetaKey].(map[string]any)
	if !ok {
		return ""
	}
	effects := stringsOf(capability["tmuxEffects"])
	if len(effects) == 0 {
		return ""
	}
	switch {
	case slices.Contains(effects, "delete"):
		return "**Deletes tmux state.** Repeating it can remove more state."
	case slices.Equal(effects, []string{"observe"}):
		return "Reads only. Repeating it changes nothing."
	default:
		return "Changes tmux state."
	}
}

func stringsOf(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			values = append(values, text)
		}
		return values
	default:
		return nil
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
		return strings.Compare(a.name, b.name)
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
