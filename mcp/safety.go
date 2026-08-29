package mcp

import (
	"os"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SafetyLevel is a ceiling over MCP tool annotation classes. It is not a
// sandbox or an authority boundary.
type SafetyLevel string

const (
	// SafetyReadOnly advertises only the tools that read tmux.
	SafetyReadOnly SafetyLevel = "readonly"
	// SafetyMutating adds tools not annotated destructive and is the default.
	SafetyMutating SafetyLevel = "mutating"
	// SafetyDestructive adds the tools that end something no later call brings
	// back.
	SafetyDestructive SafetyLevel = "destructive"
)

// SafetyEnvironmentVariable names the variable that selects the safety ceiling.
const SafetyEnvironmentVariable = "LIBTMUX_SAFETY"

// SocketEnvironmentVariable selects the tmux socket name when no flag does.
const SocketEnvironmentVariable = "LIBTMUX_SOCKET"

// SocketPathEnvironmentVariable selects a tmux socket by path when no flag does.
const SocketPathEnvironmentVariable = "LIBTMUX_SOCKET_PATH"

// BinaryEnvironmentVariable selects the tmux executable when no flag does.
const BinaryEnvironmentVariable = "LIBTMUX_TMUX_BIN"

// Invalid configured levels fail closed to readonly; absence selects the default.
func safetyFromEnvironment() SafetyLevel {
	raw, set := os.LookupEnv(SafetyEnvironmentVariable)
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if !set || trimmed == "" {
		return SafetyMutating
	}
	switch trimmed {
	case string(SafetyReadOnly):
		return SafetyReadOnly
	case string(SafetyMutating):
		return SafetyMutating
	case string(SafetyDestructive):
		return SafetyDestructive
	default:
		return SafetyReadOnly
	}
}

// RejectedSafetyValue reports an invalid configured value, or empty otherwise.
func RejectedSafetyValue() string {
	raw, set := os.LookupEnv(SafetyEnvironmentVariable)
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if !set || trimmed == "" {
		return ""
	}
	switch trimmed {
	case string(SafetyReadOnly), string(SafetyMutating), string(SafetyDestructive):
		return ""
	default:
		return raw
	}
}

// ResolvedSafetyLevel reports the level a server started now would use.
func ResolvedSafetyLevel() SafetyLevel {
	return safetyFromEnvironment()
}

// permits treats tool annotations as the safety source of truth.
func (level SafetyLevel) permits(annotations *mcp.ToolAnnotations) bool {
	if annotations == nil {
		// Missing annotations fail closed at the destructive tier.
		return level == SafetyDestructive
	}
	switch {
	case annotations.ReadOnlyHint:
		return true
	case annotations.DestructiveHint != nil && *annotations.DestructiveHint:
		return level == SafetyDestructive
	default:
		return level == SafetyMutating || level == SafetyDestructive
	}
}

func (level SafetyLevel) describe() string {
	switch level {
	case SafetyMutating:
		return "This server offers tools annotated read-only or mutating. Tools " +
			"annotated destructive are withheld; other tools may still execute commands."
	case SafetyReadOnly:
		return "This server offers only tools annotated read-only. Their results may " +
			"still contain sensitive tmux metadata or content."
	case SafetyDestructive:
		return "This server offers every tool, including the kill tools, which end a " +
			"pane, a window, a session, or the whole tmux server and every process in it."
	default:
		return "This server offers the tools that read and change tmux."
	}
}
