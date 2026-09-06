package main

import (
	"maps"
)

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
	// dialectOpencode packs argv into one array and calls the environment
	// "environment"; it rejects the standard shape.
	dialectOpencode
)

func renderEntry(entry map[string]any, dialect entryDialect) map[string]any {
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
			maps.Copy(environment, incoming)
			if len(environment) > 0 {
				merged[name] = environment
			}
		}
	}
	return merged
}
