package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools remain fixed to one tmux target. list_servers discovers other sockets
// but cannot retarget calls.

type getServerInfoInput struct {
	IncludeMessages bool `json:"includeMessages,omitempty" jsonschema:"add tmux's own server message log, which records what tmux refused and why"`
	MaxLines        int  `json:"maxLines,omitempty" jsonschema:"how many log messages to return at most, keeping the most recent"`
	MaxBytes        int  `json:"maxBytes,omitempty" jsonschema:"how many bytes of log messages to return at most, keeping the most recent"`
}

type attachedClient struct {
	Name    string `json:"name"`
	TTY     string `json:"tty,omitempty"`
	Session string `json:"session,omitempty"`
	// ControlMode reports a client driving tmux through control mode rather
	// than a person at a terminal, including this server's own connection.
	ControlMode bool `json:"controlMode"`
}

type getServerInfoOutput struct {
	SocketPath string `json:"socketPath"`
	Version    string `json:"version"`
	Alive      bool   `json:"alive"`
	Sessions   int    `json:"sessions"`
	Windows    int    `json:"windows"`
	Panes      int    `json:"panes"`
	// Clients includes this process when it holds a control connection.
	Clients         int              `json:"clients"`
	AttachedClients []attachedClient `json:"attachedClients"`
	Messages        []string         `json:"messages,omitempty"`
	// MessagesUnavailable says why Messages is missing, and is absent when it
	// is not. Before tmux 3.5, the log is unavailable without an attached client.
	MessagesUnavailable string `json:"messagesUnavailable,omitempty"`
	// InsideThisServer identifies caller-pane safety boundaries.
	InsideThisServer bool `json:"insideThisServer"`
	// CallerPaneID is reported even when it belongs to another tmux server.
	CallerPaneID         string   `json:"callerPaneId,omitempty"`
	SafetyLevel          string   `json:"safetyLevel"`
	Capabilities         []string `json:"capabilities"`
	RejectedCapabilities []string `json:"rejectedCapabilities,omitempty"`
	truncation
}

func (t *tools) getServerInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getServerInfoInput,
) (*mcp.CallToolResult, getServerInfoOutput, error) {
	if input.IncludeMessages && !t.capabilities.permits(CapabilityContentRead) {
		return nil, getServerInfoOutput{}, errors.New(
			"includeMessages requires the content-read capability",
		)
	}
	caller := callerFromEnvironment()
	output := getServerInfoOutput{
		SafetyLevel:          string(t.level),
		Capabilities:         t.capabilities.strings(),
		RejectedCapabilities: slices.Clone(t.rejectedCapabilities),
		CallerPaneID:         caller.paneID,
	}
	if version, err := t.tmux(ctx).Version(ctx); err == nil {
		output.Version = version.String()
	}
	// An absent server is a valid result; a probe error is not.
	alive, err := t.tmux(ctx).IsAlive(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.Alive = alive
	if !alive {
		// Preserve the array shape on the absent-server path.
		output.AttachedClients = []attachedClient{}
		return nil, output, nil
	}
	caller, err = t.callerIdentityFor(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.CallerPaneID = caller.paneID

	socket := t.socketPath(ctx)
	output.SocketPath = socket
	output.InsideThisServer = caller.inside && socket != "" &&
		resolvePath(socket) == caller.socket

	// One snapshot keeps all topology counts observationally consistent.
	snapshot, err := t.tmux(ctx).Snapshot(ctx)
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
		messages, err := t.tmux(ctx).ShowMessages(ctx, tmux.ShowMessagesRequest{})
		if err != nil {
			if t.runtime.isTerminalError(err) {
				return nil, getServerInfoOutput{}, err
			}
			// Preserve the otherwise complete reply when only messages fail.
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

// Default message bounds cover both line count and potentially long commands.
const (
	serverMessagesMax   = 100
	serverMessagesBytes = 16_000
)

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

type listServersInput struct {
	Name        string `json:"name,omitempty" jsonschema:"keep servers whose name contains this text, ignoring case"`
	IncludeDead bool   `json:"includeDead,omitempty" jsonschema:"include socket files with no server running, which tmux leaves behind when a server exits"`
	MaxServers  int    `json:"maxServers,omitempty" jsonschema:"how many servers to report at most (default 100, maximum 1000); the target is always kept"`
}

type serverSummary struct {
	SocketPath string `json:"socketPath"`
	Name       string `json:"name"`
	IsTarget   bool   `json:"isTarget"`
	Alive      bool   `json:"alive"`
	Sessions   int    `json:"sessions"`
}

const (
	serverProbeConcurrency = 16
	serverProbeTimeout     = time.Second
	defaultMaxServers      = 100
	ceilingMaxServers      = 1000
)

type listServersOutput struct {
	// Servers is always an array with the target first when present.
	Servers []serverSummary `json:"servers"`
	// Total includes inspected sockets and the explicit target before filters.
	// It is a lower bound when Truncated is true.
	Total      int    `json:"total"`
	Skipped    int    `json:"skipped,omitempty"`
	SearchedIn string `json:"searchedIn"`
	// Truncated reports that the bounded directory scan did not reach its end.
	Truncated bool `json:"truncated"`
	// UnreachableNote explains that discovered servers cannot be retargeted.
	UnreachableNote string `json:"unreachableNote,omitempty"`
}

func resolveServerLimit(requested int) (int, error) {
	if requested < 0 {
		return 0, errors.New("maxServers must not be negative")
	}
	if requested == 0 {
		return defaultMaxServers, nil
	}
	return min(requested, ceilingMaxServers), nil
}

func readServerDirectory(path string, maxEntries int) ([]os.DirEntry, bool, error) {
	directory, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("open tmux socket directory: %w", err)
	}
	entries, readErr := directory.ReadDir(maxEntries + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, false, fmt.Errorf("read tmux socket directory: %w", readErr)
	}
	if closeErr != nil {
		return nil, false, fmt.Errorf("close tmux socket directory: %w", closeErr)
	}
	truncated := readErr == nil
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
		truncated = true
	}
	return entries, truncated, nil
}

// listServers discovers sockets without retargeting this server.
func (t *tools) listServers(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listServersInput,
) (*mcp.CallToolResult, listServersOutput, error) {
	binding, err := t.runtime.process(ctx)
	if err != nil {
		return nil, listServersOutput{}, err
	}
	targetProbe := t.tmux(ctx)
	selection, err := binding.SocketSelection()
	if err != nil {
		return nil, listServersOutput{}, err
	}
	directory := selection.NamedDirectory
	output := listServersOutput{SearchedIn: directory, Servers: []serverSummary{}}
	limit, err := resolveServerLimit(input.MaxServers)
	if err != nil {
		return nil, output, err
	}

	target := selection.Path
	targetPath := resolvePath(target)
	type candidate struct {
		path     string
		name     string
		isTarget bool
		probe    tmux.Server
	}
	candidates := make([]candidate, 0, limit)
	if target != "" {
		output.Total++
		candidates = append(candidates, candidate{
			path: target, name: filepath.Base(target), isTarget: true, probe: targetProbe,
		})
	}
	entries, truncated, err := readServerDirectory(directory, limit+1)
	if err != nil {
		return nil, output, err
	}
	output.Truncated = truncated
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if target != "" && resolvePath(path) == targetPath {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, os.ErrNotExist) {
				continue
			}
			return nil, output, fmt.Errorf("inspect tmux socket: %w", infoErr)
		}
		if info.Mode()&os.ModeSocket == 0 {
			continue
		}
		output.Total++
		if input.Name != "" && !containsFold(entry.Name(), input.Name) {
			output.Skipped++
			continue
		}
		if len(candidates) == limit {
			output.Skipped++
			output.Truncated = true
			continue
		}
		probe, err := binding.WithSocketPath(path)
		if err != nil {
			return nil, output, err
		}
		candidates = append(candidates, candidate{
			path: path, name: entry.Name(), probe: probe,
		})
	}

	type probeResult struct {
		summary serverSummary
		skipped bool
		err     error
	}
	probe := func(candidate candidate) probeResult {
		summary := serverSummary{
			SocketPath: candidate.path,
			Name:       candidate.name,
			IsTarget:   candidate.isTarget,
		}
		probeCtx := ctx
		cancelProbe := func() {}
		if !candidate.isTarget {
			probeCtx, cancelProbe = context.WithTimeout(ctx, serverProbeTimeout)
		}
		defer cancelProbe()
		if !candidate.isTarget {
			alive, sessions := t.runtime.deps.probeSibling(probeCtx, candidate.probe)
			if ctx.Err() != nil {
				return probeResult{err: ctx.Err()}
			}
			summary.Alive = alive
			summary.Sessions = sessions
			if !summary.Alive && !input.IncludeDead {
				return probeResult{skipped: true}
			}
			return probeResult{summary: summary}
		}
		alive, err := candidate.probe.IsAlive(probeCtx)
		if err != nil && candidate.isTarget && t.runtime.isTerminalError(err) {
			return probeResult{err: err}
		}
		if ctx.Err() != nil {
			return probeResult{err: ctx.Err()}
		}
		if err == nil && alive {
			summary.Alive = true
			sessions, sessionsErr := candidate.probe.Sessions(probeCtx)
			if sessionsErr != nil && candidate.isTarget &&
				t.runtime.isTerminalError(sessionsErr) {
				return probeResult{err: sessionsErr}
			}
			if ctx.Err() != nil {
				return probeResult{err: ctx.Err()}
			}
			if sessionsErr == nil {
				summary.Sessions = len(sessions)
			}
		}
		if !summary.Alive && !summary.IsTarget && !input.IncludeDead {
			return probeResult{skipped: true}
		}
		return probeResult{summary: summary}
	}

	results := make([]probeResult, len(candidates))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(serverProbeConcurrency, len(candidates)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				results[index] = probe(candidates[index])
			}
		}()
	}
sendCandidates:
	for index := range candidates {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendCandidates
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return nil, output, ctx.Err()
	}
	for _, result := range results {
		if result.err != nil {
			return nil, output, result.err
		}
		if result.skipped {
			output.Skipped++
			continue
		}
		output.Servers = append(output.Servers, result.summary)
	}
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
	return nil, output, nil
}

type displayMessageInput struct {
	Format      string `json:"format" jsonschema:"a tmux format string, such as #{pane_current_path}"`
	PaneID      string `json:"paneId,omitempty" jsonschema:"the pane the format is about; empty uses the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to evaluate against when paneId is empty"`
}

type displayMessageOutput struct {
	Value  string `json:"value"`
	PaneID string `json:"paneId"`
}

// displayMessage may execute #() shell commands directly or through E and T
// format modifiers; printing does not make arbitrary formats read-only.
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

func addServerTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "get_server_info",
		Annotations: readOnly("Describe the tmux Server"),
		Description: "Which tmux socket these tools address, its version, how " +
			"much it holds, and whether this MCP server is itself running in one " +
			"of its panes. Ask this first: insideThisServer true means a pane " +
			"you act on may be the terminal this is running in.",
	}, t.getServerInfo)
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "list_servers",
		Annotations: readOnly("List tmux Servers"),
		Description: "The tmux servers running on this machine, with the one " +
			"these tools address marked. tmux leaves a socket file behind when " +
			"a server exits, so the ones nothing is listening on are left out " +
			"unless includeDead asks for them. Discovery only: no tool here can " +
			"be pointed at another one, which is decided when this server is " +
			"started.",
	}, t.listServers)
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "display_message",
		Annotations: mutating("Expand a tmux Format"),
		Description: "Expand a tmux format; tmux's #() syntax runs a shell " +
			"command, so treat the format as operator-powerful. Use formats such " +
			"as #{pane_current_path} or #{window_flags} to inspect anything about " +
			"tmux that no tool here has its own answer for.",
	}, t.displayMessage)
}
