// Command libtmux-mcp serves one tmux server to Model Context Protocol clients
// over stdin and stdout.
//
// The tmux server is chosen at startup and cannot be changed by a client, so a
// client reaches only the socket the operator selected. A flag says which;
// LIBTMUX_SOCKET names the default when no flag does, which is how the Python
// server is configured, so one entry serves both.
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
	"slices"
	"strings"
	"syscall"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	socketName := flag.String("socket-name", "", "tmux socket name; empty uses LIBTMUX_SOCKET, then tmux's default socket")
	socketPath := flag.String("socket-path", "", "explicit tmux socket path; empty uses LIBTMUX_SOCKET_PATH; overrides -socket-name")
	binary := flag.String("binary", "", "tmux executable; empty uses LIBTMUX_TMUX_BIN, then resolves tmux through PATH")
	version := flag.Bool("version", false, "print the version and exit")
	tools := flag.Bool("tools", false, "print the tools this server would advertise and exit")
	doctor := flag.Bool("doctor", false, "report what this server can see and exit")
	flag.Parse()

	resolvedSocket, socketFrom := resolveSocket(*socketName, *socketPath)

	if *version {
		fmt.Println("libtmux-mcp", tmuxmcp.Version)
		return
	}

	target := tmux.NewServer(tmux.ServerOptions{
		SocketName: resolvedSocket,
		SocketPath: socketPathFrom(*socketPath),
		Binary:     binaryFrom(*binary),
	})

	var err error
	switch {
	case *tools:
		err = reportTools(target)
	case *doctor:
		err = reportDoctor(target, socketFrom)
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
		return fmt.Errorf("resolve tmux: %w", endedBy(ctx, err))
	}
	if err := tmuxmcp.Run(ctx, target); err != nil && !isClientHangup(err) {
		return endedBy(ctx, err)
	}
	return nil
}

// endedBy replaces a context cancellation with the signal that caused it.
// A client tearing the transport down sends one, and "context canceled" names
// the mechanism rather than the reason, which reads as a fault in the server.
//
// The cause is compared by identity rather than with errors.Is because a
// signal's cause wraps context.Canceled, so errors.Is holds for both it and a
// plain cancellation and cannot tell them apart.
func endedBy(ctx context.Context, err error) error {
	if !errors.Is(err, context.Canceled) {
		return err
	}
	cause := context.Cause(ctx)
	if cause == nil || cause == context.Canceled { //nolint:errorlint // identity is the distinction
		return err
	}
	return cause
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
// including whatever the safety level or capability allowlist withheld.
func inspect(target tmux.Server) (context.Context, *sdk.ClientSession, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := tmuxmcp.NewServer(target)
	if _, err := instance.Connect(ctx, serverTransport, nil); err != nil {
		_ = instance.Close()
		cancel()
		return nil, nil, nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "libtmux-mcp-cli", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = instance.Close()
		cancel()
		return nil, nil, nil, err
	}
	return ctx, session, func() {
		_ = session.Close()
		_ = instance.Close()
		cancel()
	}, nil
}

// reportTools prints what a client would be offered.
//
// A shorter list than expected usually means the safety level or capability
// allowlist withheld something, and reading both here beats inferring either
// from a refusal.
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
	slices.SortFunc(listed.Tools, func(a, b *sdk.Tool) int {
		return strings.Compare(a.Name, b.Name)
	})
	level := string(tmuxmcp.ResolvedSafetyLevel())
	switch asked := os.Getenv(tmuxmcp.SafetyEnvironmentVariable); {
	case asked == "":
		level += " (default)"
	case !strings.EqualFold(strings.TrimSpace(asked), level):
		level += fmt.Sprintf(" (%s is not a level, so the lowest was taken)", asked)
	}
	fmt.Printf("%d tools at safety level %s\n\n", len(listed.Tools), level)
	capabilities := tmuxmcp.ResolvedCapabilities()
	capabilityNames := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capabilityNames = append(capabilityNames, string(capability))
	}
	capabilityLabel := strings.Join(capabilityNames, ", ")
	if strings.TrimSpace(os.Getenv(tmuxmcp.CapabilitiesEnvironmentVariable)) == "" {
		capabilityLabel += " (default)"
	}
	fmt.Printf("capabilities: %s\n", capabilityLabel)
	if rejected := tmuxmcp.RejectedCapabilityValues(); len(rejected) > 0 {
		fmt.Printf("rejected capabilities: %s\n", strings.Join(rejected, ", "))
	}
	fmt.Println()
	for _, tool := range listed.Tools {
		kind := "changes tmux"
		switch {
		case tool.Annotations == nil:
		case tool.Annotations.ReadOnlyHint:
			kind = "reads"
		case tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint:
			kind = "ends something"
		}
		capability, _ := tool.Meta[tmuxmcp.CapabilityMetaKey].(string)
		fmt.Printf("  %-28s %-16s %-14s %s\n",
			tool.Name, capability, kind, firstSentence(tool.Description))
	}
	return nil
}

// reportDoctor says what this server can see, which is the first thing to
// establish when a client says it cannot start or reaches the wrong tmux.
func reportDoctor(target tmux.Server, socketOrigin string) error {
	ctx, session, done, err := inspect(target)
	if err != nil {
		return err
	}
	defer done()

	var info struct {
		SocketPath           string   `json:"socketPath"`
		Version              string   `json:"version"`
		Alive                bool     `json:"alive"`
		Sessions             int      `json:"sessions"`
		Windows              int      `json:"windows"`
		Panes                int      `json:"panes"`
		Clients              int      `json:"clients"`
		InsideThisServer     bool     `json:"insideThisServer"`
		CallerPaneID         string   `json:"callerPaneId"`
		SafetyLevel          string   `json:"safetyLevel"`
		Capabilities         []string `json:"capabilities"`
		RejectedCapabilities []string `json:"rejectedCapabilities"`
	}
	if err := callInto(ctx, session, "get_server_info", &info); err != nil {
		return err
	}

	fmt.Println("libtmux-mcp doctor")
	fmt.Printf("  tmux:    %s\n", orUnknown(info.Version))
	fmt.Printf("  socket:  %s (from %s)\n", orUnknown(info.SocketPath), socketOrigin)
	if info.Alive {
		fmt.Printf("  holds:   %d sessions, %d windows, %d panes, %d clients attached\n",
			info.Sessions, info.Windows, info.Panes, info.Clients)
	} else {
		fmt.Println("  holds:   nothing — no tmux server is running on that socket")
		fmt.Println("           (not a fault; tmux starts one when something asks it to)")
	}
	fmt.Printf("  safety:  %s\n", info.SafetyLevel)
	if rejected := tmuxmcp.RejectedSafetyValue(); rejected != "" {
		fmt.Printf("           %s is %q, which is not a level; the lowest was taken\n",
			tmuxmcp.SafetyEnvironmentVariable, rejected)
	}
	fmt.Printf("  access:  %s\n", strings.Join(info.Capabilities, ", "))
	if len(info.RejectedCapabilities) > 0 {
		fmt.Printf("           rejected %s values: %s\n",
			tmuxmcp.CapabilitiesEnvironmentVariable,
			strings.Join(info.RejectedCapabilities, ", "))
	}

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

// resolveSocket reports the socket name to use and where it came from.
//
// A socket named in the environment is the default. An operator who wrote a
// configuration for the Python server, which reads this variable, would
// otherwise reach whatever sits on tmux's default socket with nothing said
// about it. A flag still wins: typing one is being more specific than the
// environment, and only the operator sets either -- a client cannot.
func resolveSocket(name, path string) (resolved, origin string) {
	switch {
	case path != "":
		return name, "-socket-path"
	case name != "":
		return name, "-socket-name"
	}
	if named := strings.TrimSpace(os.Getenv(tmuxmcp.SocketEnvironmentVariable)); named != "" {
		return named, tmuxmcp.SocketEnvironmentVariable
	}
	if path := strings.TrimSpace(os.Getenv(tmuxmcp.SocketPathEnvironmentVariable)); path != "" {
		return "", tmuxmcp.SocketPathEnvironmentVariable
	}
	return "", "tmux's default"
}

// socketPathFrom resolves the socket path, which the flag names and the
// environment can too.
//
// The two are not interchangeable: a name is joined to the directory tmux
// keeps sockets in, a path is taken as it stands. Reading a path out of the
// variable that takes a name produced a doubled path and an error naming a
// socket nobody asked for.
func socketPathFrom(flagged string) string {
	if flagged != "" {
		return flagged
	}
	return strings.TrimSpace(os.Getenv(tmuxmcp.SocketPathEnvironmentVariable))
}

// binaryFrom resolves the tmux executable from the flag or the environment.
func binaryFrom(flagged string) string {
	if flagged != "" {
		return flagged
	}
	return strings.TrimSpace(os.Getenv(tmuxmcp.BinaryEnvironmentVariable))
}
