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

// safetyFromEnvironment reads the level an operator asked for.
//
// An unreadable value selects the default rather than failing: a server that
// refuses to start over a misspelled variable is worse than one that runs at
// the level it would have run at anyway, and the level it chose is reported in
// the instructions where a client sees it.
func safetyFromEnvironment() SafetyLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SafetyEnvironmentVariable))) {
	case string(SafetyReadOnly):
		return SafetyReadOnly
	case string(SafetyDestructive):
		return SafetyDestructive
	default:
		return SafetyMutating
	}
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
