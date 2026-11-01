package main

import (
	"fmt"
	"maps"
)

const toolsetsVariable = "LIBTMUX_TOOLSETS"

// TOML and JSONC writers splice only the target server entry so neighboring
// bytes survive. Plain JSON is decoded and rendered again.

type configFormat int

const (
	formatJSON configFormat = iota
	formatTOML
	formatJSONC
)

type entryDialect int

const (
	dialectStandard entryDialect = iota
	// dialectClaude requires the explicit stdio discriminator.
	dialectClaude
	// dialectOpencode packs argv into one array and calls the environment
	// "environment"; it rejects the standard shape.
	dialectOpencode
)

func renderEntry(entry map[string]any, dialect entryDialect) map[string]any {
	if dialect == dialectClaude {
		rendered := map[string]any{"type": "stdio"}
		maps.Copy(rendered, entry)
		return rendered
	}
	if dialect != dialectOpencode {
		return entry
	}
	command := []any{entry["command"]}
	if arguments, ok := entry["args"].([]any); ok {
		command = append(command, arguments...)
	}
	rendered := map[string]any{"type": "local", "command": command}
	if environment, ok := entry["env"].(map[string]any); ok && len(environment) > 0 {
		rendered["environment"] = environment
	}
	return rendered
}

// mergeWithExisting preserves unknown client keys and environment while fresh
// command fields win.
func mergeWithExisting(existing, fresh map[string]any) map[string]any {
	merged := map[string]any{}
	maps.Copy(merged, fresh)
	for name, value := range existing {
		switch name {
		case "command", "args", "type":
		case "env", "environment":
			// Merge both environment dialects below.
		default:
			if _, replaced := merged[name]; !replaced {
				merged[name] = value
			}
		}
	}

	environment := map[string]any{}
	for _, name := range []string{"env", "environment"} {
		if previous, ok := existing[name].(map[string]any); ok {
			maps.Copy(environment, previous)
		}
	}
	for _, name := range []string{"env", "environment"} {
		if incoming, ok := fresh[name].(map[string]any); ok {
			if _, replacesSafety := incoming[toolsetsVariable]; replacesSafety {
				delete(environment, "LIBTMUX_SAFETY")
			}
		}
	}
	for _, name := range []string{"env", "environment"} {
		if incoming, ok := fresh[name].(map[string]any); ok {
			maps.Copy(environment, incoming)
			if len(environment) > 0 {
				merged[name] = environment
			}
		}
	}
	return merged
}

// refuseCarriedEmptyToolsets stops a swap from copying an empty
// LIBTMUX_TOOLSETS out of an entry the swapper did not write: the Go server
// reads it as no toolsets, and the other server may not have meant that.
func refuseCarriedEmptyToolsets(existing, requested, written map[string]any) error {
	if existing == nil || isLocal(existing) {
		return nil
	}
	if _, asked := entryEnvironment(requested)[toolsetsVariable]; asked {
		return nil
	}
	if value, set := entryEnvironment(written)[toolsetsVariable].(string); !set || value != "" {
		return nil
	}
	return fmt.Errorf("the existing %s entry sets %s to the empty string, "+
		"which the Go server reads as no toolsets; "+
		"pass --env %s=inspect,manage,execute for the default toolsets "+
		"or --env %s= to keep it empty",
		serverName, toolsetsVariable, toolsetsVariable, toolsetsVariable)
}
