package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
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

// getServerInfoInput chooses how much is reported.
type getServerInfoInput struct {
	// IncludeMessages adds tmux's own message log, which is where tmux
	// records what it refused and why. It is off by default because it is
	// diagnostic and can be long.
	IncludeMessages bool `json:"includeMessages,omitempty" jsonschema:"add tmux's own server message log, which records what tmux refused and why"`
	// MaxLines and MaxBytes bound the message log, which is the only part of
	// this reply whose size belongs to the server rather than to the request.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many log messages to return at most, keeping the most recent"`
	// MaxBytes bounds the same log by size, which a count cannot: one message
	// carries the whole format string of the command it records.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes of log messages to return at most, keeping the most recent"`
}

// attachedClient is one tmux client attached to this server.
//
// Whether anyone is looking changes what is polite to do. Selecting a window
// or splitting a pane moves what a person sees; doing it to a session nobody
// is attached to moves nothing. A caller cannot tell the difference from a
// count, because this server's own control connection is one of them.
type attachedClient struct {
	// Name is the client's name, which is usually its terminal device.
	Name string `json:"name"`
	// TTY is the terminal it is attached to.
	TTY string `json:"tty,omitempty"`
	// Session is the session it is looking at.
	Session string `json:"session,omitempty"`
	// ControlMode reports a client driving tmux through control mode rather
	// than a person at a terminal. This server's own connection is one, so a
	// caller asking whether anybody is watching wants the ones where this is
	// false.
	ControlMode bool `json:"controlMode"`
}

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
	// AttachedClients is what each of them is, so a caller can tell a person
	// watching a session from this server's own control connection.
	AttachedClients []attachedClient `json:"attachedClients"`
	// Messages is tmux's own message log, reported only when asked for.
	Messages []string `json:"messages,omitempty"`
	// MessagesUnavailable says why Messages is missing, and is absent when it
	// is not. Asked-for-and-empty and asked-for-and-not-delivered are
	// different answers, and this is what tells them apart without failing a
	// reply whose other fields are all present: tmux keeps the message log per
	// client, and before 3.5 it refuses the command outright when nothing is
	// attached.
	MessagesUnavailable string `json:"messagesUnavailable,omitempty"`
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
	// truncation reports what the message log lost to the bounds, in the same
	// fields every other bounded reply here uses.
	truncation
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
	input getServerInfoInput,
) (*mcp.CallToolResult, getServerInfoOutput, error) {
	caller := t.callerIdentityFor(ctx)
	output := getServerInfoOutput{
		SafetyLevel:  string(t.level),
		CallerPaneID: caller.paneID,
	}
	if version, err := t.tmux().Version(ctx); err == nil {
		output.Version = version.String()
	}
	// A server that is not running is an answer; a server that could not be
	// asked is not. IsAlive already reports the first as (false, nil), so an
	// error here means this could not be read -- and answering that with
	// alive:false and a reply full of zeros describes a healthy empty server
	// that nobody can tell from a broken connection.
	alive, err := t.tmux().IsAlive(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.Alive = alive
	if !alive {
		return nil, output, nil
	}

	socket := t.socketPath(ctx)
	output.SocketPath = socket
	output.InsideThisServer = caller.inside && socket != "" &&
		resolvePath(socket) == caller.socket

	// One snapshot answers all of these. A snapshot per question would let the
	// counts disagree with each other and would cost a listing each. A failure
	// is reported rather than left as zeros, for the same reason: a server with
	// no sessions and a server that could not be counted read identically.
	snapshot, err := t.tmux().Snapshot(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.Sessions = len(snapshot.Sessions())
	output.Windows = len(snapshot.Windows())
	output.Panes = len(snapshot.Panes())
	clients := snapshot.Clients()
	output.Clients = len(clients)
	output.AttachedClients = summarizeClients(clients)
	if input.IncludeMessages {
		messages, err := t.tmux().ShowMessages(ctx, tmux.ShowMessagesRequest{})
		if err != nil {
			// Said rather than swallowed, and said rather than raised: the
			// rest of this reply is correct, and a caller that asked for the
			// log as well should not lose the answer it came for.
			output.MessagesUnavailable = err.Error()
		} else {
			kept, dropped, err := boundMessages(messages, input.MaxLines, input.MaxBytes)
			if err != nil {
				return nil, getServerInfoOutput{}, err
			}
			output.Messages, output.truncation = kept, dropped
		}
	}
	return nil, output, nil
}

// serverMessagesMax and serverMessagesBytes bound the message log a reply
// carries when a caller names no bounds of their own.
//
// tmux keeps its last message-limit lines, which an operator can set into the
// thousands, and the recent ones are what explain what just happened. A count
// alone does not bound it: every line records a whole command, and the
// listings this server runs carry a format naming each field they want, so the
// log is largely this server quoted back to itself at thousands of characters
// a line. A hundred of those is a reply no caller can afford.
const (
	serverMessagesMax   = 100
	serverMessagesBytes = 16_000
)

// boundMessages keeps the most recent messages that fit, and reports the rest
// as dropped, through the same bounds every other reply here uses.
func boundMessages(messages []string, maxLines, maxBytes int) ([]string, truncation, error) {
	if maxLines == 0 {
		maxLines = serverMessagesMax
	}
	if maxBytes == 0 {
		maxBytes = serverMessagesBytes
	}
	limits, err := resolveBounds(maxLines, maxBytes)
	if err != nil {
		return nil, truncation{}, err
	}
	kept, dropped := limits.apply(messages)
	return kept, dropped, nil
}

// summarizeClients describes who is attached.
func summarizeClients(clients []tmux.Client) []attachedClient {
	summaries := make([]attachedClient, 0, len(clients))
	for _, client := range clients {
		formats := client.Formats()
		session, _ := formats.SessionName()
		tty, _ := formats.ClientTTY()
		control, _ := formats.ClientControlMode()
		summaries = append(summaries, attachedClient{
			Name:        client.Name().String(),
			TTY:         tty,
			Session:     session,
			ControlMode: control,
		})
	}
	return summaries
}

// listServersInput narrows the socket directory to the servers worth reporting.
//
// It has criteria because that directory is append-only in practice: tmux
// leaves a socket file behind when a server exits, and a machine that has run
// test suites accumulates hundreds. Reporting all of them by default made the
// one useful answer — which servers are running — the hardest to find in the
// reply, and cost a caller its context to say nothing.
type listServersInput struct {
	// Name keeps servers whose socket name contains this text.
	Name string `json:"name,omitempty" jsonschema:"keep servers whose name contains this text, ignoring case"`
	// IncludeDead adds the socket files no server is listening on.
	IncludeDead bool `json:"includeDead,omitempty" jsonschema:"include socket files with no server running, which tmux leaves behind when a server exits"`
	// MaxServers caps how many are reported.
	MaxServers int `json:"maxServers,omitempty" jsonschema:"how many servers to report at most; the target is always kept"`
}

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
	// Servers are the tmux sockets found, the target first. Always an array,
	// so a client can count it without checking that the field is there.
	Servers []serverSummary `json:"servers"`
	// Total is how many sockets the directory held before the criteria were
	// applied, so a caller can see what it selected from.
	Total int `json:"total"`
	// Skipped is how many sockets the criteria left out. A reply that quietly
	// dropped most of a directory would read as a machine with few servers.
	Skipped int `json:"skipped,omitempty"`
	// SearchedIn is the directory that was looked in, so a client that
	// expected a server and did not find one can tell whether it was looking
	// in the right place.
	SearchedIn string `json:"searchedIn"`
	// UnreachableNote says what to do about a server this one is not, and is
	// present only when there is a live one it is not. A caller that finds
	// another socket here would otherwise try every tool looking for the
	// argument that reaches it, and there is none: the target is fixed when
	// this command starts.
	UnreachableNote string `json:"unreachableNote,omitempty"`
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
	input listServersInput,
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
		output.Total++
		path := filepath.Join(directory, entry.Name())
		summary := serverSummary{
			SocketPath: path,
			Name:       entry.Name(),
			IsTarget:   target != "" && resolvePath(path) == resolvePath(target),
		}
		if input.Name != "" && !containsFold(summary.Name, input.Name) {
			output.Skipped++
			continue
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
		// The server these tools address is always reported, so a caller can
		// find itself in the reply whatever else it asked for.
		if !summary.Alive && !summary.IsTarget && !input.IncludeDead {
			output.Skipped++
			continue
		}
		output.Servers = append(output.Servers, summary)
	}
	// The target first, then the rest by name, so a client reading the first
	// entry reads the server it is talking to.
	// Only when there is somewhere the caller cannot get to. A note that
	// appears on every listing is a note nobody reads.
	for _, found := range output.Servers {
		if found.Alive && !found.IsTarget {
			output.UnreachableNote = "Only the server marked isTarget can be " +
				"reached from here; no tool takes a socket. To drive another " +
				"one, start a second libtmux-mcp with -socket-path and add it " +
				"to the client's configuration under its own name."
			break
		}
	}

	slices.SortStableFunc(output.Servers, func(a, b serverSummary) int {
		if a.IsTarget != b.IsTarget {
			if a.IsTarget {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	// Capped after sorting, so what a cap keeps is the target and the servers
	// nearest it by name rather than whichever the directory listed first.
	if input.MaxServers > 0 && len(output.Servers) > input.MaxServers {
		output.Skipped += len(output.Servers) - input.MaxServers
		output.Servers = output.Servers[:input.MaxServers]
	}
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
		return nil, displayMessageOutput{}, errors.New("format is required")
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
		Description: "The tmux servers running on this machine, with the one " +
			"these tools address marked. tmux leaves a socket file behind when " +
			"a server exits, so the ones nothing is listening on are left out " +
			"unless includeDead asks for them. Discovery only: no tool here can " +
			"be pointed at another one, which is decided when this server is " +
			"started.",
	}, t.listServers)
	register(server, t, &mcp.Tool{
		Name:        "display_message",
		Annotations: readOnly("Expand a tmux Format"),
		Description: "Ask tmux to expand one of its format strings, such as " +
			"#{pane_current_path} or #{window_flags}. This is how to read " +
			"anything about tmux that no tool here has its own answer for.",
	}, t.displayMessage)
}
