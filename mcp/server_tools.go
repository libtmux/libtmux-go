package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
)

// What this server is pointed at, and what else is out there.
//
// A client cannot retarget a tool, which is the property that makes handing
// one of these to a model reasonable at all. It can still be told what it is
// looking at and what it is not: get_server_info says which socket these tools
// address and whether this process is itself inside it, and list_servers says
// which other tmux servers exist. Neither reaches one.
//
// list_servers is discovery rather than a way around the limit. An operator
// who wants a client on another socket starts another server pointed at it,
// which is a decision made where the others were.

// getServerInfoInput takes no arguments.
type getServerInfoInput struct{}

// getServerInfoOutput describes the tmux server these tools address.
type getServerInfoOutput struct {
	// SocketPath is the socket this server's tools reach, which is the one
	// thing a client cannot change.
	SocketPath string `json:"socketPath"`
	// Version is the tmux version running there.
	Version string `json:"version"`
	// Alive reports whether tmux is running on that socket at all. Everything
	// below is empty when it is not.
	Alive bool `json:"alive"`
	// Sessions, Windows, and Panes are how much it holds.
	Sessions int `json:"sessions"`
	// Windows is how many windows there are across every session.
	Windows int `json:"windows"`
	// Panes is how many panes there are across every window.
	Panes int `json:"panes"`
	// Clients is how many tmux clients are attached, which includes this
	// process when it holds a control connection.
	Clients int `json:"clients"`
	// InsideThisServer reports whether this MCP server is itself running in a
	// pane of the tmux server it addresses. When true, CallerPaneID names that
	// pane and anything done to it is done to the terminal this is running in.
	InsideThisServer bool `json:"insideThisServer"`
	// CallerPaneID is the pane this process runs in, when it runs in one at
	// all. It is reported even when the pane belongs to a different tmux
	// server, where InsideThisServer is false: knowing which pane is not this
	// server's is worth as much as knowing which is.
	CallerPaneID string `json:"callerPaneId,omitempty"`
	// SafetyLevel is what the operator allowed, which explains a shorter tool
	// list than a client expected.
	SafetyLevel string `json:"safetyLevel"`
}

// getServerInfo says what this server is pointed at.
//
// A client that has been handed a tmux server it did not choose asks this
// first: which socket, which version, and whether the pane it is about to type
// into is the one it is running in. The last is the difference between running
// a command and running a command in its own terminal.
func (t *tools) getServerInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ getServerInfoInput,
) (*mcp.CallToolResult, getServerInfoOutput, error) {
	caller := t.callerIdentityFor(ctx)
	output := getServerInfoOutput{
		SafetyLevel:  string(t.level),
		CallerPaneID: caller.paneID,
	}
	if version, err := t.target.Version(ctx); err == nil {
		output.Version = version.String()
	}
	alive, err := t.target.IsAlive(ctx)
	// A server that cannot be asked whether it is alive is not, which is the
	// answer a client wants rather than an error about the asking.
	output.Alive = err == nil && alive
	if !output.Alive {
		return nil, output, nil
	}

	socket := t.socketPath(ctx)
	output.SocketPath = socket
	output.InsideThisServer = caller.inside && socket != "" &&
		resolvePath(socket) == caller.socket

	if snapshot, err := t.target.Snapshot(ctx); err == nil {
		output.Sessions = len(snapshot.Sessions())
		output.Windows = len(snapshot.Windows())
		output.Panes = len(snapshot.Panes())
	}
	if clients, err := t.target.Clients(ctx); err == nil {
		output.Clients = len(clients)
	}
	return nil, output, nil
}

// listServersInput takes no arguments.
type listServersInput struct{}

// serverSummary is one tmux server found on this machine.
type serverSummary struct {
	// SocketPath is where it listens.
	SocketPath string `json:"socketPath"`
	// Name is the socket's file name, which is what tmux's own -L takes.
	Name string `json:"name"`
	// IsTarget reports whether this is the server these tools address. Exactly
	// one entry has it, unless the configured server is not running.
	IsTarget bool `json:"isTarget"`
	// Alive reports whether something answered on that socket. A socket file
	// left behind by a server that died is listed with this false rather than
	// omitted, because its presence is why a new server on that name might
	// behave oddly.
	Alive bool `json:"alive"`
	// Sessions is how many sessions it holds, when it is alive.
	Sessions int `json:"sessions"`
}

// listServersOutput reports the tmux servers found.
type listServersOutput struct {
	// Servers are the tmux sockets found, the target first.
	Servers []serverSummary `json:"servers,omitempty"`
	// SearchedIn is the directory that was looked in, so a client that
	// expected a server and did not find one can tell whether it was looking
	// in the right place.
	SearchedIn string `json:"searchedIn"`
}

// listServers reports the tmux servers on this machine.
//
// This is discovery, not a way to reach one: nothing here retargets a tool,
// and a client that finds another socket cannot address it. What it is for is
// telling a person which servers exist, so they can point another instance of
// this command at the one they meant.
func (t *tools) listServers(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ listServersInput,
) (*mcp.CallToolResult, listServersOutput, error) {
	directory := socketDirectory()
	output := listServersOutput{SearchedIn: directory, Servers: []serverSummary{}}

	target := t.socketPath(ctx)
	entries, err := os.ReadDir(directory)
	if err != nil {
		// Nowhere to look is an empty list rather than a failure: a machine
		// with no tmux socket directory has no tmux servers, which is what was
		// asked.
		//nolint:nilerr // an absent socket directory means no servers, which is
		// the answer rather than a failure of the asking.
		return nil, output, nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		summary := serverSummary{
			SocketPath: path,
			Name:       entry.Name(),
			IsTarget:   target != "" && resolvePath(path) == resolvePath(target),
		}
		// Each is probed on its own handle. Asking through the configured one
		// would be the retargeting this tool exists not to do.
		probe := tmux.NewServer(tmux.ServerOptions{SocketPath: path})
		if alive, err := probe.IsAlive(ctx); err == nil && alive {
			summary.Alive = true
			if sessions, err := probe.Sessions(ctx); err == nil {
				summary.Sessions = len(sessions)
			}
		}
		output.Servers = append(output.Servers, summary)
	}
	// The target first, then the rest by name, so a client reading the first
	// entry reads the server it is talking to.
	sort.SliceStable(output.Servers, func(i, j int) bool {
		if output.Servers[i].IsTarget != output.Servers[j].IsTarget {
			return output.Servers[i].IsTarget
		}
		return output.Servers[i].Name < output.Servers[j].Name
	})
	return nil, output, nil
}

// socketDirectory is where tmux keeps its sockets.
//
// tmux reads TMUX_TMPDIR at exec and falls back to /tmp, then puts sockets in
// a directory named for the user. Reproducing that here rather than asking
// tmux is deliberate: the question is which servers exist, and asking one of
// them where it lives answers only for that one.
func socketDirectory() string {
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// displayMessageInput asks tmux to expand a format.
type displayMessageInput struct {
	// Format is a tmux format string, such as "#{pane_current_path}" or
	// "#{session_name}:#{window_index}".
	Format string `json:"format" jsonschema:"a tmux format string, such as #{pane_current_path}"`
	// PaneID gives the format a pane to be about. Empty uses the active pane,
	// which is what most pane formats need in order to mean anything.
	PaneID string `json:"paneId,omitempty" jsonschema:"the pane the format is about; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to evaluate against when paneId is empty"`
}

// displayMessageOutput carries what tmux made of the format.
type displayMessageOutput struct {
	// Value is the expanded format.
	Value string `json:"value"`
	// PaneID is the pane it was evaluated against.
	PaneID string `json:"paneId"`
}

// displayMessage asks tmux a question in its own language.
//
// The tools here answer the questions worth having a tool for. tmux knows
// hundreds of other things — every format in its manual — and a client that
// needs one of them would otherwise have no way to ask. This is that way:
// #{pane_current_path} for where a pane is, #{client_termname} for what a
// person is looking at, #{window_flags} for what tmux is marking.
//
// It is read-only despite the name. tmux's display-message can put a message
// on a person's status line; this always prints instead, so it answers rather
// than announces.
func (t *tools) displayMessage(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input displayMessageInput,
) (*mcp.CallToolResult, displayMessageOutput, error) {
	if strings.TrimSpace(input.Format) == "" {
		return nil, displayMessageOutput{}, fmt.Errorf("format is required")
	}
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, displayMessageOutput{}, err
	}
	printed, err := pane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{
			Print:   true,
			Message: input.Format,
		},
	})
	if err != nil {
		return nil, displayMessageOutput{}, err
	}
	return nil, displayMessageOutput{
		Value:  strings.Join(printed, "\n"),
		PaneID: pane.ID().String(),
	}, nil
}

// addServerTools advertises the tools about the server itself.
func addServerTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "get_server_info",
		Annotations: readOnly("Describe the tmux Server"),
		Description: "Which tmux socket these tools address, its version, how " +
			"much it holds, and whether this MCP server is itself running in one " +
			"of its panes. Ask this first: insideThisServer true means a pane " +
			"you act on may be the terminal this is running in.",
	}, t.getServerInfo)
	register(server, t, &mcp.Tool{
		Name:        "list_servers",
		Annotations: readOnly("List tmux Servers"),
		Description: "Every tmux socket on this machine, with the one these " +
			"tools address marked. Discovery only: no tool here can be pointed " +
			"at another one, which is decided when this server is started.",
	}, t.listServers)
	register(server, t, &mcp.Tool{
		Name:        "display_message",
		Annotations: readOnly("Expand a tmux Format"),
		Description: "Ask tmux to expand one of its format strings, such as " +
			"#{pane_current_path} or #{window_flags}. This is how to read " +
			"anything about tmux that no tool here has its own answer for.",
	}, t.displayMessage)
}
