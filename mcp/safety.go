package mcp

import (
	"os"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SafetyLevel bounds what an operator lets a client do to tmux.
//
// A description asking a model to be careful is a request. A level is a
// guarantee: a tool above it is never advertised, so no prompt reaches it.
// That is what lets someone hand a model a tmux server it cannot kill
// anything with.
type SafetyLevel string

const (
	// SafetyReadOnly advertises only the tools that read tmux.
	SafetyReadOnly SafetyLevel = "readonly"
	// SafetyMutating adds the tools that change tmux without ending anything,
	// and is the default.
	SafetyMutating SafetyLevel = "mutating"
	// SafetyDestructive adds the tools that end something no later call brings
	// back.
	SafetyDestructive SafetyLevel = "destructive"
)

// SafetyEnvironmentVariable names the variable that selects the level, matching
// the Python server so an operator configuring both writes one thing.
const SafetyEnvironmentVariable = "LIBTMUX_SAFETY"

// SocketEnvironmentVariable names the variable that selects the tmux socket
// when no flag does, matching the Python server for the same reason. A flag
// wins over it, and only an operator sets either.
const SocketEnvironmentVariable = "LIBTMUX_SOCKET"

// SocketPathEnvironmentVariable names a socket by path rather than by name,
// for a socket outside the directory tmux keeps them in. A client starts this
// server with an environment rather than an argument vector, so a path that
// only a flag could give was a path a client could not reach.
const SocketPathEnvironmentVariable = "LIBTMUX_SOCKET_PATH"

// BinaryEnvironmentVariable names the tmux executable, for the same reason:
// the -binary flag is unreachable from a client configuration that passes
// environment and nothing else.
const BinaryEnvironmentVariable = "LIBTMUX_TMUX_BIN"

// safetyFromEnvironment reads the level an operator asked for.
//
// An unreadable value selects the lowest level rather than failing. Refusing
// to start over a misspelled variable is worse than running, but the direction
// to fall back in is a separate question: someone who wrote LIBTMUX_SAFETY at
// all was bounding what a model may do, and a typo in that variable must not
// widen the bound. Only an absent variable means "no preference", and only
// that selects the default. Python's server resolves the same input the same
// way.
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

// RejectedSafetyValue is what the environment asked for when it was not a
// level, and empty when it was one or was absent.
//
// The fallback above is deliberate and silent, and silence is wrong in the one
// place that exists to explain a short tool list: an operator who wrote
// "destructve" gets the readonly surface and a report that reads exactly like
// one who asked for readonly on purpose.
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

// ResolvedSafetyLevel reports the level a server started now would run at.
//
// A tool that prints the variable instead names a level the server may not be
// running, which is exactly wrong for a value that is rejected rather than
// obeyed.
func ResolvedSafetyLevel() SafetyLevel {
	return safetyFromEnvironment()
}

// permits reports whether a tool with these annotations may be advertised.
//
// The annotations are the single source of truth. The Python server keeps tags
// beside them, which is one more thing to forget when a tool is added; here a
// tool that says it is destructive is governed by having said so.
func (level SafetyLevel) permits(annotations *mcp.ToolAnnotations) bool {
	if annotations == nil {
		// A tool that has not said what it does is treated as the most
		// dangerous thing it could be.
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

// describe reports the level for the instructions, so a client meeting a
// shorter tool list knows tools were withheld rather than absent.
func (level SafetyLevel) describe() string {
	switch level {
	case SafetyMutating:
		return "This server offers the tools that read and change tmux. The ones that " +
			"end something, such as kill_pane and kill_session, are withheld."
	case SafetyReadOnly:
		return "This server is running read-only: only the tools that read tmux are " +
			"offered, and nothing here can change or end anything."
	case SafetyDestructive:
		return "This server offers every tool, including the kill tools, which end a " +
			"pane, a window, a session, or the whole tmux server and every process in it."
	default:
		return "This server offers the tools that read and change tmux."
	}
}
