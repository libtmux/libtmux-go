package mcp

import (
	"os"
	"slices"
	"strings"
)

// Capability names one class of MCP access an operator may grant.
type Capability string

const (
	// CapabilityMetadataRead exposes tmux identities, topology, process state,
	// and geometry without pane contents or configuration values.
	CapabilityMetadataRead Capability = "metadata-read"
	// CapabilityContentRead exposes pane output, buffers, option values, hooks,
	// environment values, jobs, and tmux messages.
	CapabilityContentRead Capability = "content-read"
	// CapabilityPaneControl exposes pane input and tmux features that may run
	// shell commands, including arbitrary format expansion.
	CapabilityPaneControl Capability = "pane-control"
	// CapabilityWorkspaceCreate exposes session, window, pane, and workspace
	// creation, including the programs those operations start.
	CapabilityWorkspaceCreate Capability = "workspace-create"
	// CapabilityTmuxLayout exposes selection, movement, resizing, layout, and
	// naming operations that rearrange existing tmux objects.
	CapabilityTmuxLayout Capability = "tmux-layout"
	// CapabilityTmuxSettings exposes buffers, environment variables, and tmux
	// option changes.
	CapabilityTmuxSettings Capability = "tmux-settings"
	// CapabilityTmuxDestroy exposes operations that end panes, windows,
	// sessions, or the whole tmux server.
	CapabilityTmuxDestroy Capability = "tmux-destroy"
)

// CapabilitiesEnvironmentVariable names the comma-separated MCP capability
// allowlist. Empty or unset grants metadata-read only. The profiles inspect,
// operate, and all expand to documented capability sets.
const CapabilitiesEnvironmentVariable = "LIBTMUX_MCP_CAPABILITIES"

// CapabilityMetaKey is the MCP tool metadata key whose value names the
// capability required to advertise that tool.
const CapabilityMetaKey = "io.github.libtmux/capability"

var allCapabilities = []Capability{
	CapabilityMetadataRead,
	CapabilityContentRead,
	CapabilityPaneControl,
	CapabilityWorkspaceCreate,
	CapabilityTmuxLayout,
	CapabilityTmuxSettings,
	CapabilityTmuxDestroy,
}

var capabilityProfiles = map[string][]Capability{
	"inspect": {
		CapabilityMetadataRead,
		CapabilityContentRead,
	},
	"operate": {
		CapabilityMetadataRead,
		CapabilityContentRead,
		CapabilityPaneControl,
		CapabilityWorkspaceCreate,
		CapabilityTmuxLayout,
		CapabilityTmuxSettings,
	},
	"all": allCapabilities,
}

type capabilitySet map[Capability]struct{}

func newCapabilitySet(capabilities []Capability) capabilitySet {
	set := make(capabilitySet, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = struct{}{}
	}
	return set
}

func (set capabilitySet) permits(capability Capability) bool {
	_, ok := set[capability]
	return ok
}

func (set capabilitySet) list() []Capability {
	listed := make([]Capability, 0, len(set))
	for _, capability := range allCapabilities {
		if set.permits(capability) {
			listed = append(listed, capability)
		}
	}
	return listed
}

func (set capabilitySet) strings() []string {
	listed := set.list()
	values := make([]string, 0, len(listed))
	for _, capability := range listed {
		values = append(values, string(capability))
	}
	return values
}

func (set capabilitySet) describe() string {
	return "This server grants these independent capabilities: " +
		strings.Join(set.strings(), ", ") + ". Tools outside that allowlist are withheld."
}

func capabilitiesFromEnvironment() (capabilitySet, []string) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(CapabilitiesEnvironmentVariable)))
	if raw == "" {
		return newCapabilitySet([]Capability{CapabilityMetadataRead}), nil
	}

	known := newCapabilitySet(nil)
	var rejected []string
	for part := range strings.SplitSeq(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if profile, ok := capabilityProfiles[name]; ok {
			for _, capability := range profile {
				known[capability] = struct{}{}
			}
			continue
		}
		capability := Capability(name)
		if slices.Contains(allCapabilities, capability) {
			known[capability] = struct{}{}
			continue
		}
		rejected = append(rejected, name)
	}
	if len(known) == 0 {
		known[CapabilityMetadataRead] = struct{}{}
	}
	slices.Sort(rejected)
	return known, rejected
}

// ResolvedCapabilities reports the ordered capability allowlist a server
// started now would use.
func ResolvedCapabilities() []Capability {
	resolved, _ := capabilitiesFromEnvironment()
	return resolved.list()
}

// RejectedCapabilityValues reports unrecognized comma-separated values from
// CapabilitiesEnvironmentVariable. Recognized grants remain active; an input
// with no recognized grant falls back to metadata-read.
func RejectedCapabilityValues() []string {
	_, rejected := capabilitiesFromEnvironment()
	return rejected
}
