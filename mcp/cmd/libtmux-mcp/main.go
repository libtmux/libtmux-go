// Command libtmux-mcp serves one tmux server to Model Context Protocol clients
// over stdin and stdout.
//
// The tmux server is chosen by flags at startup and cannot be changed by a
// client, so a client reaches only the socket the operator selected.
//
//	libtmux-mcp -socket-name my-application
//
// Three flags answer questions without a client, which is what a misconfigured
// entry in somebody's agent CLI needs:
//
//	libtmux-mcp -version
//	libtmux-mcp -tools -socket-name my-application
//	libtmux-mcp -doctor -socket-name my-application
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
	tmuxmcp "github.com/tmux-python/libtmux/golang/mcp"
)

func main() {
	socketName := flag.String("socket-name", "", "tmux socket name; empty uses tmux's default socket")
	socketPath := flag.String("socket-path", "", "explicit tmux socket path; overrides -socket-name")
	binary := flag.String("binary", "", "tmux executable; empty resolves tmux through PATH")
	version := flag.Bool("version", false, "print the version and exit")
	tools := flag.Bool("tools", false, "print the tools this server would advertise and exit")
	doctor := flag.Bool("doctor", false, "report what this server can see and exit")
	flag.Parse()

	if *version {
		fmt.Println("libtmux-mcp", tmuxmcp.Version)
		return
	}

	target := tmux.NewServer(tmux.ServerOptions{
		SocketName: *socketName,
		SocketPath: *socketPath,
		Binary:     *binary,
	})

	var err error
	switch {
	case *tools:
		err = reportTools(target)
	case *doctor:
		err = reportDoctor(target)
	default:
		err = serve(target)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "libtmux-mcp:", err)
		os.Exit(1)
	}
}

func serve(target tmux.Server) error {
	// Serve until the process is interrupted, so a client disconnect or a
	// terminating signal both stop the server rather than only the first.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fail at startup rather than on the first tool call when the configured
	// binary does not exist. An unreachable server is not an error here: tmux
	// starts one on demand.
	if _, err := target.Version(ctx); err != nil {
		return fmt.Errorf("resolve tmux: %w", err)
	}
	if err := tmuxmcp.Run(ctx, target); err != nil && !isClientHangup(err) {
		return err
	}
	return nil
}

// codeServerClosing is the JSON-RPC code the SDK reports when the connection
// is shutting down. It is defined in an internal package, so it is matched by
// its number here; the number is part of the wire protocol and does not move.
const codeServerClosing = -32004

// isClientHangup reports whether an error is a client closing the connection.
//
// A stdio server ends when its client stops talking to it, which is how every
// normal shutdown happens: the agent that started it exits, or a probe asks
// one question and goes. Reporting that as a failure puts an error in the log
// of whatever supervises this and makes an ordinary disconnect look like a
// crash. Only a client that hangs up mid-handshake reached here — a completed
// session already ends cleanly — which made the appearance arbitrary as well
// as wrong.
//
// The underlying read error is io.EOF, but the SDK formats it into the message
// rather than wrapping it, so errors.Is cannot see it and the shutdown is
// recognised by its JSON-RPC code instead.
func isClientHangup(err error) bool {
	if errors.Is(err, io.EOF) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, os.ErrClosed) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == codeServerClosing
}

// inspect connects a client to this server in memory, so the two reports below
// ask exactly what a real client would and get exactly what it would get,
// including whatever the safety level withheld.
func inspect(target tmux.Server) (context.Context, *sdk.ClientSession, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, err := tmuxmcp.NewServer(target).Connect(ctx, serverTransport, nil); err != nil {
		cancel()
		return nil, nil, nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "libtmux-mcp-cli", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	return ctx, session, func() { _ = session.Close(); cancel() }, nil
}

// reportTools prints what a client would be offered.
//
// The point is the safety level: a shorter list than expected is the usual
// reason a client cannot do what someone asked it to, and reading it here
// beats inferring it from a refusal.
func reportTools(target tmux.Server) error {
	ctx, session, done, err := inspect(target)
	if err != nil {
		return err
	}
	defer done()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	sort.Slice(listed.Tools, func(i, j int) bool {
		return listed.Tools[i].Name < listed.Tools[j].Name
	})
	level := os.Getenv(tmuxmcp.SafetyEnvironmentVariable)
	if level == "" {
		level = "mutating (default)"
	}
	fmt.Printf("%d tools at safety level %s\n\n", len(listed.Tools), level)
	for _, tool := range listed.Tools {
		kind := "changes tmux"
		switch {
		case tool.Annotations == nil:
		case tool.Annotations.ReadOnlyHint:
			kind = "reads"
		case tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint:
			kind = "ends something"
		}
		fmt.Printf("  %-28s %-14s %s\n", tool.Name, kind, firstSentence(tool.Description))
	}
	return nil
}

// reportDoctor says what this server can see, which is the first thing to
// establish when a client says it cannot start or reaches the wrong tmux.
func reportDoctor(target tmux.Server) error {
	ctx, session, done, err := inspect(target)
	if err != nil {
		return err
	}
	defer done()

	var info struct {
		SocketPath       string `json:"socketPath"`
		Version          string `json:"version"`
		Alive            bool   `json:"alive"`
		Sessions         int    `json:"sessions"`
		Windows          int    `json:"windows"`
		Panes            int    `json:"panes"`
		Clients          int    `json:"clients"`
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
		SafetyLevel      string `json:"safetyLevel"`
	}
	if err := callInto(ctx, session, "get_server_info", &info); err != nil {
		return err
	}

	fmt.Println("libtmux-mcp doctor")
	fmt.Printf("  tmux:    %s\n", orUnknown(info.Version))
	fmt.Printf("  socket:  %s\n", orUnknown(info.SocketPath))
	if info.Alive {
		fmt.Printf("  holds:   %d sessions, %d windows, %d panes, %d clients attached\n",
			info.Sessions, info.Windows, info.Panes, info.Clients)
	} else {
		fmt.Println("  holds:   nothing — no tmux server is running on that socket")
		fmt.Println("           (not a fault; tmux starts one when something asks it to)")
	}
	fmt.Printf("  safety:  %s\n", info.SafetyLevel)

	switch {
	case info.InsideThisServer:
		fmt.Printf("  caller:  pane %s of this very server — acting on it acts on\n"+
			"           the terminal this process is running in\n", info.CallerPaneID)
	case info.CallerPaneID != "":
		fmt.Printf("  caller:  pane %s, but of a different tmux server\n", info.CallerPaneID)
	default:
		fmt.Println("  caller:  not running inside a tmux pane")
	}

	var servers struct {
		SearchedIn string `json:"searchedIn"`
		Servers    []struct {
			Name     string `json:"name"`
			Alive    bool   `json:"alive"`
			IsTarget bool   `json:"isTarget"`
			Sessions int    `json:"sessions"`
		} `json:"servers"`
	}
	if err := callInto(ctx, session, "list_servers", &servers); err != nil {
		return nil
	}
	fmt.Printf("\n  other tmux sockets in %s:\n", servers.SearchedIn)
	if len(servers.Servers) == 0 {
		fmt.Println("    none")
	}
	for _, found := range servers.Servers {
		mark := " "
		if found.IsTarget {
			mark = "*"
		}
		state := "no server running"
		if found.Alive {
			state = fmt.Sprintf("%d sessions", found.Sessions)
		}
		fmt.Printf("   %s %-24s %s\n", mark, found.Name, state)
	}
	return nil
}

// callInto runs one tool and decodes its structured result.
func callInto(
	ctx context.Context,
	session *sdk.ClientSession,
	name string,
	into any,
) error {
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if result.IsError {
		return fmt.Errorf("%s failed", name)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}

// firstSentence shortens a description to its first sentence, so a listing of
// fifty tools stays a listing rather than becoming the manual.
func firstSentence(description string) string {
	if index := strings.Index(description, ". "); index >= 0 {
		return description[:index+1]
	}
	return description
}

// orUnknown reports a value, or says it could not be read.
func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
