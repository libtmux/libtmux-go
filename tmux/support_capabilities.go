package tmux

import "fmt"

// capabilityHandling is what the package does where a capability is missing.
type capabilityHandling string

const (
	handlingRefuse   capabilityHandling = "refuse"
	handlingFallback capabilityHandling = "fallback"
	handlingRepair   capabilityHandling = "repair"
)

// capabilityCommand names a tmux command and the flags of it a record
// depends on.
type capabilityCommand struct {
	Command string   `json:"command"`
	Flags   []string `json:"flags,omitempty"`
}

// capabilityRecord is one thing a tmux release can do and this package gates
// on. The same record sets the gate's floor and the manifest the docs read.
type capabilityRecord struct {
	APIs     []string            `json:"apis"`
	Commands []capabilityCommand `json:"commands"`
	Error    string              `json:"error,omitempty"`
	Except   []string            `json:"except,omitempty"`
	Handling capabilityHandling  `json:"handling"`
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Since    string              `json:"since"`
	Summary  string              `json:"summary"`
}

// workaroundRecord is a release-specific tmux defect the package refuses or
// repairs. It applies from the first of Releases up to, not including,
// FixedIn.
type workaroundRecord struct {
	APIs     []string            `json:"apis"`
	Commands []capabilityCommand `json:"commands"`
	FixedIn  string              `json:"-"`
	Handling capabilityHandling  `json:"handling"`
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Releases []string            `json:"releases"`
	Summary  string              `json:"summary"`
}

const versionTooLowAPI = "tmux.VersionTooLowError"

// capabilityRegistry lists what the package gates on a tmux release. Gates
// read their floors from it through [capabilitySince], [workaroundApplies]
// and [workaroundFixedIn]; the root capabilities.json is emitted from it.
//
// It holds the gates that name a tmux command: a command or a whole API that
// needs a release, and a command a release mishandles. It does not hold the
// flag that one request field needs, such as a popup, search or
// display-message option, which follows UnsupportedPolicy where the request is
// built and is documented on the field, nor the floor every API has, the
// minimum supported release. Those never reach the manifest.
var capabilityRegistry = []capabilityRecord{
	{
		ID:      "control-mode",
		Name:    "Control mode",
		Summary: "Attach a library-owned control client and stream events.",
		Since:   "3.7", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs: []string{
			"tmux.Server.OpenControl",
			"tmux.Session.OpenControl",
			"tmux.Server.NewSessionConnection",
		},
		Commands: []capabilityCommand{{Command: "attach-session"}},
	},
	{
		ID:      "clipboard-query",
		Name:    "Clipboard query",
		Summary: "Ask a client for its clipboard; tmux 3.3a exits the server when the client has a terminal.",
		Since:   "3.4", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs: []string{
			"tmux.Server.Cmd",
			"tmux.Connection.Call",
			"tmux.ControlClient.Call",
			"tmux.Server.RefreshClient",
			"tmux.RefreshClientRequest.RequestClipboard",
		},
		Commands: []capabilityCommand{{Command: "refresh-client", Flags: []string{"-l"}}},
	},
	{
		ID:      "floating-pane",
		Name:    "Floating panes",
		Summary: "Create a floating pane with new-pane.",
		Since:   "3.7", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Window.NewPane", "tmux.Pane.NewPane", "tmux.NewPaneRequest"},
		Commands: []capabilityCommand{{Command: "new-pane"}},
	},
	{
		ID:      "buffer-delete-named",
		Name:    "Named buffer deletion",
		Summary: "Delete one paste buffer by name without risking another buffer.",
		Since:   "3.4", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Server.Cmd", "tmux.Connection.Call", "tmux.ControlClient.Call", "tmux.Pane.CaptureToFile"},
		Commands: []capabilityCommand{{Command: "delete-buffer", Flags: []string{"-b"}}},
	},
	{
		ID:      "buffer-rename",
		Name:    "Buffer rename",
		Summary: "Rename a paste buffer without risking another buffer.",
		Since:   "3.4", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Server.Cmd", "tmux.Connection.Call", "tmux.ControlClient.Call"},
		Commands: []capabilityCommand{{Command: "set-buffer", Flags: []string{"-b", "-n"}}},
	},
	{
		ID:      "layout-mirrored-main",
		Name:    "Mirrored main layouts",
		Summary: "Select the main-horizontal-mirrored and main-vertical-mirrored presets.",
		Since:   "3.5", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Window.SelectLayout", "tmux.SelectLayoutRequest.Layout"},
		Commands: []capabilityCommand{{Command: "select-layout"}},
	},
	{
		ID:      "layout-json",
		Name:    "JSON layouts",
		Summary: "Select a layout given in the JSON shape tmux reports from window_layout.",
		Since:   "3.8-rc2", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Window.SelectLayout", "tmux.SelectLayoutRequest.Layout"},
		Commands: []capabilityCommand{{Command: "select-layout"}},
	},
	{
		ID:      "capture-mode-screen",
		Name:    "Capture the mode screen",
		Summary: "Capture what a pane's active mode shows instead of the pane itself.",
		Since:   "3.6", Handling: handlingFallback,
		APIs:     []string{"tmux.CapturePaneRequest.ModeScreen"},
		Commands: []capabilityCommand{{Command: "capture-pane", Flags: []string{"-M"}}},
	},
	{
		ID:      "capture-line-flags",
		Name:    "Capture line numbers, flags and hyperlinks",
		Summary: "Prefix captured lines with their number or metadata flags, or keep hyperlinks.",
		Since:   "3.7", Handling: handlingFallback,
		APIs: []string{
			"tmux.CapturePaneRequest.Hyperlinks",
			"tmux.CapturePaneRequest.LineNumbers",
			"tmux.CapturePaneRequest.LineFlags",
		},
		Commands: []capabilityCommand{{Command: "capture-pane", Flags: []string{"-F", "-H", "-L"}}},
	},
	{
		ID:      "run-shell-start-directory",
		Name:    "run-shell working directory",
		Summary: "Start a run-shell job in a chosen directory.",
		Since:   "3.4", Handling: handlingFallback,
		APIs:     []string{"tmux.RunShellRequest.StartDirectory"},
		Commands: []capabilityCommand{{Command: "run-shell", Flags: []string{"-c"}}},
	},
	{
		ID:      "run-shell-show-stderr",
		Name:    "run-shell stderr",
		Summary: "Show a run-shell job's standard error as well as its output.",
		Since:   "3.6", Handling: handlingFallback,
		APIs:     []string{"tmux.RunShellRequest.ShowStderr"},
		Commands: []capabilityCommand{{Command: "run-shell", Flags: []string{"-E"}}},
	},
	{
		ID:      "run-shell-args",
		Name:    "run-shell job arguments",
		Summary: "Pass extra arguments to a run-shell job.",
		Since:   "3.7", Handling: handlingFallback,
		APIs:     []string{"tmux.RunShellRequest.Args"},
		Commands: []capabilityCommand{{Command: "run-shell"}},
	},
	{
		ID:      "paste-buffer-no-vis",
		Name:    "Paste raw bytes",
		Summary: "Paste a buffer without tmux rewriting its control characters.",
		Since:   "3.7", Handling: handlingFallback,
		APIs:     []string{"tmux.PasteBufferRequest.NoVis"},
		Commands: []capabilityCommand{{Command: "paste-buffer", Flags: []string{"-S"}}},
	},
	{
		ID:      "control-no-detach-on-destroy",
		Name:    "Control clients survive session destruction",
		Summary: "Keep a library-owned control client attached when its session is destroyed.",
		Since:   "3.6", Handling: handlingFallback,
		APIs: []string{
			"tmux.Server.OpenControl",
			"tmux.Session.OpenControl",
			"tmux.Server.NewSessionConnection",
		},
		Commands: []capabilityCommand{{Command: "attach-session", Flags: []string{"-f"}}},
	},
	{
		ID:      "capture-trim-blank-cells",
		Name:    "Trim blank capture cells",
		Summary: "Drop trailing positions that hold no character from a capture.",
		Since:   "3.4", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.CapturePaneRequest.TrimTrailing", "tmux.Pane.Capture"},
		Commands: []capabilityCommand{{Command: "capture-pane", Flags: []string{"-T"}}},
	},
	{
		ID:      "list-keys-format",
		Name:    "Formatted key listing",
		Summary: "Format each key binding with a caller-supplied template.",
		Since:   "3.7", Handling: handlingRefuse, Error: versionTooLowAPI,
		APIs:     []string{"tmux.Server.ListKeys", "tmux.ListKeysRequest.Format"},
		Commands: []capabilityCommand{{Command: "list-keys", Flags: []string{"-F"}}},
	},
}

// workaroundRegistry lists the tmux defects the package refuses or repairs.
var workaroundRegistry = []workaroundRecord{
	{
		ID:       "run-shell-output-in-copy-mode",
		Name:     "run-shell output lands in copy mode",
		Summary:  "Foreground run-shell output goes to the pane's copy mode instead of the client, so the call is refused.",
		Releases: []string{"3.3a", "3.4"}, FixedIn: "3.5", Handling: handlingRefuse,
		APIs:     []string{"tmux.Server.RunShell"},
		Commands: []capabilityCommand{{Command: "run-shell"}},
	},
	{
		ID:       "break-pane-misnames-windows",
		Name:     "break-pane misnames windows",
		Summary:  "break-pane without a name crashes or misnames the new window; a typed call names it first and repairs the result, a raw one is refused.",
		Releases: []string{"3.7"}, FixedIn: "3.7a", Handling: handlingRepair,
		APIs:     []string{"tmux.Pane.BreakPane", "tmux.BreakPaneRequest.Name", "tmux.Server.Cmd", "tmux.Connection.Call", "tmux.ControlClient.Call"},
		Commands: []capabilityCommand{{Command: "break-pane"}},
	},
	{
		ID:       "split-window-percent-size",
		Name:     "split-window percentage size",
		Summary:  "A literal split-window -p fails with a size error; typed percentage requests convert it.",
		Releases: []string{"3.4"}, FixedIn: "3.5", Handling: handlingRefuse,
		APIs:     []string{"tmux.Server.Cmd", "tmux.Connection.Call", "tmux.ControlClient.Call"},
		Commands: []capabilityCommand{{Command: "split-window", Flags: []string{"-p"}}},
	},
	{
		ID:       "select-layout-invalid-string",
		Name:     "select-layout kills the server",
		Summary:  "A layout operand tmux cannot parse, or -o, exits the server; typed requests validate the layout and a literal command that could reach the parser is refused.",
		Releases: []string{"3.3a"}, FixedIn: "3.4", Handling: handlingRefuse,
		APIs:     []string{"tmux.Server.Cmd", "tmux.Connection.Call", "tmux.ControlClient.Call"},
		Commands: []capabilityCommand{{Command: "select-layout", Flags: []string{"-o"}}},
	},
	{
		ID:       "name-separators-rejected",
		Name:     "Colon and period in names",
		Summary:  "A window or session name holding a colon or period is rejected, so the call is refused before any effect.",
		Releases: []string{"3.7"}, FixedIn: "3.7a", Handling: handlingRefuse,
		APIs: []string{
			"tmux.Server.Cmd",
			"tmux.Connection.Call",
			"tmux.ControlClient.Call",
			"tmux.Server.NewSession",
			"tmux.Session.NewWindow",
			"tmux.Session.Rename",
			"tmux.Window.Rename",
		},
		Commands: []capabilityCommand{
			{Command: "new-session", Flags: []string{"-n", "-s"}},
			{Command: "new-window", Flags: []string{"-n"}},
			{Command: "rename-session"},
			{Command: "rename-window"},
		},
	},
}

func registryVersion(raw string) Version {
	version, err := ParseVersion(raw)
	if err != nil {
		panic(fmt.Sprintf("tmux: capability registry names %q: %v", raw, err))
	}
	return version
}

// capabilitySince returns the first release that has capability id.
func capabilitySince(id string) Version {
	for _, record := range capabilityRegistry {
		if record.ID == id {
			return registryVersion(record.Since)
		}
	}
	panic("tmux: no capability " + id)
}

func findWorkaround(id string) workaroundRecord {
	for _, record := range workaroundRegistry {
		if record.ID == id {
			return record
		}
	}
	panic("tmux: no workaround " + id)
}

// workaroundFixedIn returns the first release that no longer has the defect.
func workaroundFixedIn(id string) Version {
	return registryVersion(findWorkaround(id).FixedIn)
}

// workaroundApplies reports whether current carries the defect.
func workaroundApplies(id string, current Version) bool {
	record := findWorkaround(id)
	return current.meetsMinimum(registryVersion(record.Releases[0])) &&
		!current.meetsMinimum(registryVersion(record.FixedIn))
}
