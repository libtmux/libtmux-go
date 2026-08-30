// Command libtmux-mcp serves one fixed tmux target over MCP stdio. Flags and
// LIBTMUX_SOCKET_PATH or LIBTMUX_SOCKET select the target.
//
//	libtmux-mcp -socket-name my-application
//
// Diagnostic flags answer without serving an external MCP client:
//
//	libtmux-mcp -version
//	libtmux-mcp -tools
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
	socketName := flag.String("socket-name", "", "tmux socket name; nonempty overrides socket environment variables")
	socketPath := flag.String("socket-path", "", "explicit tmux socket path; nonempty has highest precedence")
	binary := flag.String("binary", "", "tmux executable; empty uses LIBTMUX_TMUX_BIN, then resolves tmux through PATH")
	version := flag.Bool("version", false, "print the version and exit")
	tools := flag.Bool("tools", false, "print the tools this server would advertise and exit")
	doctor := flag.Bool("doctor", false, "report what this server can see and exit")
	flag.Parse()

	if *version {
		fmt.Println("libtmux-mcp", tmuxmcp.Version)
		return
	}
	if *tools {
		if err := reportTools(); err != nil {
			fmt.Fprintln(os.Stderr, "libtmux-mcp:", err)
			os.Exit(1)
		}
		return
	}

	resolvedName, resolvedPath, socketFrom := resolveSocket(*socketName, *socketPath)
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: resolvedName,
		SocketPath: resolvedPath,
		Binary:     binaryFrom(*binary),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "libtmux-mcp:", err)
		os.Exit(1)
	}

	switch {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Probe the binary at startup; the selected server may start on demand.
	if _, err := target.Version(ctx); err != nil {
		return fmt.Errorf("probe tmux version: %w", endedBy(ctx, err))
	}
	if err := tmuxmcp.Run(ctx, target); err != nil && !isClientHangup(err) {
		return endedBy(ctx, err)
	}
	return nil
}

// endedBy preserves a signal cause. Identity distinguishes it from plain
// cancellation because errors.Is matches both.
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

// codeServerClosing mirrors the SDK-internal shutdown error code.
const codeServerClosing = -32004

// isClientHangup recognizes the SDK's unwrapped JSON-RPC shutdown code; normal
// stdio disconnects must not be reported as server crashes.
func isClientHangup(err error) bool {
	if errors.Is(err, io.EOF) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, os.ErrClosed) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == codeServerClosing
}

// inspect uses an in-memory MCP client so reports honor advertised capabilities.
func inspect(target tmux.Server) (context.Context, *sdk.ClientSession, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance, err := tmuxmcp.NewServer(target)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	serverSession, err := instance.Connect(
		ctx, tmuxmcp.AssumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		_ = instance.Close()
		cancel()
		return nil, nil, nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "libtmux-mcp-cli", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		_ = instance.Close()
		cancel()
		return nil, nil, nil, err
	}
	return ctx, session, func() {
		_ = session.Close()
		_ = serverSession.Close()
		_ = instance.Close()
		cancel()
	}, nil
}

func reportTools() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := tmuxmcp.AdvertisedTools(ctx)
	if err != nil {
		return err
	}
	slices.SortFunc(tools, func(a, b *sdk.Tool) int {
		return strings.Compare(a.Name, b.Name)
	})
	level := string(tmuxmcp.ResolvedSafetyLevel())
	switch asked := os.Getenv(tmuxmcp.SafetyEnvironmentVariable); {
	case asked == "":
		level += " (default)"
	case !strings.EqualFold(strings.TrimSpace(asked), level):
		level += fmt.Sprintf(" (%s is not a level, so the lowest was taken)", asked)
	}
	fmt.Printf("%d tools at safety level %s\n\n", len(tools), level)
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
	for _, tool := range tools {
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

// reportDoctor reports target selection and reachable tmux state.
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

// firstSentence keeps a tool listing compact.
func firstSentence(description string) string {
	if index := strings.Index(description, ". "); index >= 0 {
		return description[:index+1]
	}
	return description
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

// resolveSocket reports the selected socket and its operator-controlled source.
func resolveSocket(name, path string) (socketName, socketPath, origin string) {
	switch {
	case path != "":
		return "", path, "-socket-path"
	case name != "":
		return name, "", "-socket-name"
	}
	if path := strings.TrimSpace(os.Getenv(tmuxmcp.SocketPathEnvironmentVariable)); path != "" {
		return "", path, tmuxmcp.SocketPathEnvironmentVariable
	}
	if named := strings.TrimSpace(os.Getenv(tmuxmcp.SocketEnvironmentVariable)); named != "" {
		return named, "", tmuxmcp.SocketEnvironmentVariable
	}
	return "", "", "tmux environment"
}

func binaryFrom(flagged string) string {
	if flagged != "" {
		return flagged
	}
	return strings.TrimSpace(os.Getenv(tmuxmcp.BinaryEnvironmentVariable))
}
