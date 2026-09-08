package mcp

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

const (
	// SocketEnvironmentVariable selects one named tmux socket.
	SocketEnvironmentVariable = "LIBTMUX_SOCKET"
	// SocketPathEnvironmentVariable selects one absolute tmux socket path.
	SocketPathEnvironmentVariable = "LIBTMUX_SOCKET_PATH"
	// TmuxConfigEnvironmentVariable selects an absolute tmux config path.
	TmuxConfigEnvironmentVariable = "LIBTMUX_TMUX_CONFIG"
	// BinaryEnvironmentVariable selects the tmux executable.
	BinaryEnvironmentVariable = "LIBTMUX_TMUX_BIN"
	// ToolsetsEnvironmentVariable selects unordered inspect, manage, execute,
	// and teardown groups. A present empty value selects no groups.
	ToolsetsEnvironmentVariable = "LIBTMUX_TOOLSETS"
	// ToolsEnvironmentVariable adds exact tool names after toolset expansion.
	ToolsEnvironmentVariable = "LIBTMUX_TOOLS"
	// ExcludeToolsEnvironmentVariable removes exact names after every inclusion.
	ExcludeToolsEnvironmentVariable = "LIBTMUX_EXCLUDE_TOOLS"
	// SafetyEnvironmentVariable is rejected because ordered safety tiers were retired.
	SafetyEnvironmentVariable = "LIBTMUX_SAFETY"
	// capabilitiesEnvironmentVariable is rejected because capability classes were retired.
	capabilitiesEnvironmentVariable = "LIBTMUX_MCP_CAPABILITIES"
	// RecipeToolEnvironmentVariable is rejected because prompt exports were retired.
	RecipeToolEnvironmentVariable = "LIBTMUX_MCP_PROMPTS_AS_TOOLS"
)

type socketProfile struct {
	Selector                string `json:"selector"`
	SelectionProvenance     string `json:"selectionProvenance"`
	ServerState             string `json:"serverState"`
	ConfigurationProvenance string `json:"configurationProvenance"`
	NamespaceBoundary       string `json:"namespaceBoundary"`
	ResolvedSocketPath      string `json:"-"`
	AttachCommand           string `json:"-"`
	defaultTeardown         bool
}

type toolSurface struct {
	toolsets []toolset
	included []string
	excluded []string
	tools    []toolDefinition
	byName   map[string]toolDefinition
	socket   socketProfile
}

type environmentValue struct {
	value string
	set   bool
}

type startupEnvironment map[string]environmentValue

func currentStartupEnvironment() startupEnvironment {
	values := make(startupEnvironment, 6)
	for _, name := range []string{
		ToolsetsEnvironmentVariable,
		ToolsEnvironmentVariable,
		ExcludeToolsEnvironmentVariable,
		SafetyEnvironmentVariable,
		capabilitiesEnvironmentVariable,
		RecipeToolEnvironmentVariable,
	} {
		value, set := os.LookupEnv(name)
		values[name] = environmentValue{value: value, set: set}
	}
	return values
}

func resolveToolSurface(profile socketProfile) (toolSurface, error) {
	return resolveToolSurfaceFrom(currentStartupEnvironment(), profile)
}

func resolveToolSurfaceFrom(environment startupEnvironment, profile socketProfile) (toolSurface, error) {
	if environment[SafetyEnvironmentVariable].set {
		return toolSurface{}, fmt.Errorf(
			"%s was retired; select unordered toolsets with %s",
			SafetyEnvironmentVariable,
			ToolsetsEnvironmentVariable,
		)
	}
	if environment[capabilitiesEnvironmentVariable].set {
		return toolSurface{}, fmt.Errorf(
			"%s was retired; select unordered toolsets with %s and exact tools with %s or %s",
			capabilitiesEnvironmentVariable,
			ToolsetsEnvironmentVariable,
			ToolsEnvironmentVariable,
			ExcludeToolsEnvironmentVariable,
		)
	}
	if environment[RecipeToolEnvironmentVariable].set {
		return toolSurface{}, fmt.Errorf(
			"%s was retired; use the typed tool workflows directly",
			RecipeToolEnvironmentVariable,
		)
	}
	definitions, err := toolManifest()
	if err != nil {
		return toolSurface{}, err
	}
	allByName := make(map[string]toolDefinition, len(definitions))
	for _, definition := range definitions {
		allByName[definition.name] = definition
	}

	selected := []toolset{toolsetInspect, toolsetManage, toolsetExecute}
	if profile.defaultTeardown {
		selected = slices.Clone(allToolsets)
	}
	if configured := environment[ToolsetsEnvironmentVariable]; configured.set {
		selected, err = parseToolsets(configured.value)
		if err != nil {
			return toolSurface{}, err
		}
	}
	included, err := parseToolNames(environment[ToolsEnvironmentVariable], ToolsEnvironmentVariable)
	if err != nil {
		return toolSurface{}, err
	}
	excluded, err := parseToolNames(
		environment[ExcludeToolsEnvironmentVariable],
		ExcludeToolsEnvironmentVariable,
	)
	if err != nil {
		return toolSurface{}, err
	}
	if err := rejectUnknownTools(included, allByName, ToolsEnvironmentVariable); err != nil {
		return toolSurface{}, err
	}
	if err := rejectUnknownTools(excluded, allByName, ExcludeToolsEnvironmentVariable); err != nil {
		return toolSurface{}, err
	}

	selectedSets := make(map[toolset]bool, len(selected))
	for _, name := range selected {
		selectedSets[name] = true
	}
	includedNames := make(map[string]bool, len(included))
	for _, name := range included {
		includedNames[name] = true
	}
	excludedNames := make(map[string]bool, len(excluded))
	for _, name := range excluded {
		excludedNames[name] = true
	}
	effectiveNames := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if (selectedSets[definition.toolset] || includedNames[definition.name]) &&
			!excludedNames[definition.name] {
			effectiveNames[definition.name] = true
		}
	}

	effective := make([]toolDefinition, 0, len(effectiveNames))
	byName := make(map[string]toolDefinition, len(effectiveNames))
	for _, definition := range definitions {
		if !effectiveNames[definition.name] {
			continue
		}
		bounded := definition.clone()
		bounded.nestedAuthority = slices.DeleteFunc(
			bounded.nestedAuthority,
			func(name string) bool { return excludedNames[name] },
		)
		if bounded.aggregate {
			deriveAggregateCapabilities(&bounded, allByName)
			bounded.inputSchema = bounded.inputSchema.CloneSchemas()
			bindAggregateSchema(&bounded, allByName)
		}
		effective = append(effective, bounded)
		byName[bounded.name] = bounded
	}
	return toolSurface{
		toolsets: slices.Clone(selected),
		included: slices.Clone(included),
		excluded: slices.Clone(excluded),
		tools:    effective,
		byName:   byName,
		socket:   profile,
	}, nil
}

func parseToolsets(configured string) ([]toolset, error) {
	if configured == "" {
		return []toolset{}, nil
	}
	tokens, err := configuredTokens(configured, ToolsetsEnvironmentVariable)
	if err != nil {
		return nil, err
	}
	selected := make([]toolset, 0, len(tokens))
	seen := map[toolset]bool{}
	for _, token := range tokens {
		value := toolset(token)
		if !slices.Contains(allToolsets, value) {
			return nil, fmt.Errorf(
				"%s contains unknown toolset %q; expected inspect, manage, execute, or teardown",
				ToolsetsEnvironmentVariable,
				token,
			)
		}
		if !seen[value] {
			selected = append(selected, value)
			seen[value] = true
		}
	}
	return selected, nil
}

func parseToolNames(configured environmentValue, variable string) ([]string, error) {
	if !configured.set {
		return []string{}, nil
	}
	if configured.value == "" {
		return nil, fmt.Errorf("%s contains an empty name", variable)
	}
	return configuredTokens(configured.value, variable)
}

func configuredTokens(configured, variable string) ([]string, error) {
	parts := strings.Split(configured, ",")
	values := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("%s contains an empty name", variable)
		}
		if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values, nil
}

func rejectUnknownTools(selected []string, catalog map[string]toolDefinition, variable string) error {
	unknown := make([]string, 0)
	for _, name := range selected {
		if _, found := catalog[name]; !found {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	valid := make([]string, 0, len(catalog))
	for name := range catalog {
		valid = append(valid, name)
	}
	slices.Sort(valid)
	return fmt.Errorf("%s contains unknown tool(s) %v; valid tools: %v", variable, unknown, valid)
}

func (surface toolSurface) capabilityReport() map[string]any {
	toolsets := make([]string, 0, len(surface.toolsets))
	for _, selected := range surface.toolsets {
		toolsets = append(toolsets, string(selected))
	}
	rows := make([]map[string]any, 0, len(surface.tools))
	effective := make([]string, 0, len(surface.tools))
	for _, definition := range surface.tools {
		rows = append(rows, definition.capability())
		effective = append(effective, definition.name)
	}
	boundary := map[string]any{
		"oneSocketPerProcess":    true,
		"perCallSocketSelection": false,
		"hostCommandExecution":   false,
		"dynamicResources":       false,
	}
	connection := map[string]any{
		"socketSelector":          surface.socket.Selector,
		"socketProvenance":        surface.socket.SelectionProvenance,
		"resolvedSocketPath":      surface.socket.ResolvedSocketPath,
		"serverState":             surface.socket.ServerState,
		"configurationProvenance": surface.socket.ConfigurationProvenance,
		"attachCommand":           surface.socket.AttachCommand,
	}
	selection := map[string]any{
		"toolsets":      toolsets,
		"includedTools": slices.Clone(surface.included),
		"excludedTools": slices.Clone(surface.excluded),
	}
	return map[string]any{
		"schemaVersion":           1,
		"frozen":                  true,
		"socket":                  surface.socket,
		"connection":              connection,
		"selection":               selection,
		"toolsets":                toolsets,
		"includedTools":           slices.Clone(surface.included),
		"excludedTools":           slices.Clone(surface.excluded),
		"toolCount":               len(surface.tools),
		"effectiveTools":          effective,
		"tools":                   rows,
		"hostCommandTools":        0,
		"toolFilteringBoundary":   "interface-shaping-not-authorization",
		"executionAuthority":      "tmux-user",
		"operatingSystemBoundary": "none",
		"boundary":                boundary,
	}
}
