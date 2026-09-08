package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type getServerInfoInput struct {
	IncludeMessages bool `json:"includeMessages,omitempty" jsonschema:"add tmux's own server message log"`
	MaxLines        int  `json:"maxLines,omitempty" jsonschema:"maximum server log messages"`
	MaxBytes        int  `json:"maxBytes,omitempty" jsonschema:"maximum server log bytes"`
}

type attachedClient struct {
	Name        string `json:"name"`
	TTY         string `json:"tty,omitempty"`
	Session     string `json:"session,omitempty"`
	ControlMode bool   `json:"controlMode"`
}

type getServerInfoOutput struct {
	SocketPath          string           `json:"socketPath"`
	Version             string           `json:"version"`
	Alive               bool             `json:"alive"`
	Sessions            int              `json:"sessions"`
	Windows             int              `json:"windows"`
	Panes               int              `json:"panes"`
	Clients             int              `json:"clients"`
	AttachedClients     []attachedClient `json:"attachedClients"`
	Messages            []string         `json:"messages,omitempty"`
	MessagesUnavailable string           `json:"messagesUnavailable,omitempty"`
	InsideThisServer    bool             `json:"insideThisServer"`
	CallerPaneID        string           `json:"callerPaneId,omitempty"`
	truncation
}

func (t *tools) getServerInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getServerInfoInput,
) (*mcp.CallToolResult, getServerInfoOutput, error) {
	caller := callerFromEnvironment()
	output := getServerInfoOutput{CallerPaneID: caller.paneID}
	if version, err := t.tmux(ctx).Version(ctx); err == nil {
		output.Version = version.String()
	}
	alive, err := t.tmux(ctx).IsAlive(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.Alive = alive
	if !alive {
		output.AttachedClients = []attachedClient{}
		return nil, output, nil
	}
	caller, err = t.callerIdentityFor(ctx)
	if err != nil {
		return nil, getServerInfoOutput{}, err
	}
	output.CallerPaneID = caller.paneID
	output.SocketPath = t.socketPath(ctx)
	output.InsideThisServer = caller.inside && output.SocketPath != "" &&
		resolvePath(output.SocketPath) == caller.socket

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
			Name: client.Name().String(), TTY: tty, Session: session, ControlMode: control,
		})
	}
	return summaries
}
