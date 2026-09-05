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
	"path/filepath"
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
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, "libtmux-mcp:", err)
		os.Exit(1)
	}
}

func runMain() error {
	socketName := flag.String("socket-name", "", "tmux socket name; nonempty overrides socket environment variables")
	socketPath := flag.String("socket-path", "", "explicit tmux socket path; nonempty has highest precedence")
	binary := flag.String("binary", "", "tmux executable; empty uses LIBTMUX_TMUX_BIN, then resolves tmux through PATH")
	version := flag.Bool("version", false, "print the version and exit")
	tools := flag.Bool("tools", false, "print the tools this server would advertise and exit")
	doctor := flag.Bool("doctor", false, "report what this server can see and exit")
	flag.Parse()

	if *version {
		fmt.Println("libtmux-mcp", tmuxmcp.Version)
		return nil
	}
	if *tools {
		return reportTools()
	}

	resolvedName, resolvedPath, configFile, socketFrom, minimal, err := resolveTarget(*socketName, *socketPath)
	if err != nil {
		return err
	}
	if minimal {
		var cleanup func() error
		configFile, cleanup, err = tmuxmcp.MaterializeMinimalConfig()
		if err != nil {
			return err
		}
		defer func() { _ = cleanup() }()
	}
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: resolvedName,
		SocketPath: resolvedPath,
		ConfigFile: configFile,
		Binary:     binaryFrom(*binary),
	})
	if err != nil {
		return err
	}

	if *doctor {
		return reportDoctor(target, socketFrom)
	}
	return serve(target, minimal)
}

func serve(target tmux.Server, defaultMinimal bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run := tmuxmcp.Run
	if defaultMinimal {
		run = tmuxmcp.RunDefaultMinimal
	}
	if err := run(ctx, target); err != nil && !isClientHangup(err) {
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
	fmt.Printf("%d startup-frozen tools\n\n", len(tools))
	for _, tool := range tools {
		capability, _ := tool.Meta[tmuxmcp.CapabilityMetaKey].(map[string]any)
		toolset, _ := capability["toolset"].(string)
		reach, _ := capability["processReach"].(string)
		if toolset == "" {
			toolset = "unknown"
		}
		if reach == "" {
			reach = "unknown"
		}
		fmt.Printf("  %-28s %-10s %-20s %s\n",
			tool.Name, toolset, reach, firstSentence(tool.Description))
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
		SocketPath       string `json:"socketPath"`
		Version          string `json:"version"`
		Alive            bool   `json:"alive"`
		Sessions         int    `json:"sessions"`
		Windows          int    `json:"windows"`
		Panes            int    `json:"panes"`
		Clients          int    `json:"clients"`
		InsideThisServer bool   `json:"insideThisServer"`
		CallerPaneID     string `json:"callerPaneId"`
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
	switch {
	case info.InsideThisServer:
		fmt.Printf("  caller:  pane %s of this very server — acting on it acts on\n"+
			"           the terminal this process is running in\n", info.CallerPaneID)
	case info.CallerPaneID != "":
		fmt.Printf("  caller:  pane %s, but of a different tmux server\n", info.CallerPaneID)
	default:
		fmt.Println("  caller:  not running inside a tmux pane")
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

// resolveTarget freezes one socket and configuration before MCP starts.
func resolveTarget(name, path string) (
	socketName, socketPath, configFile, origin string,
	minimal bool,
	err error,
) {
	if name != "" && path != "" {
		return "", "", "", "", false, errors.New("-socket-name and -socket-path are mutually exclusive")
	}
	environmentName, nameSet := os.LookupEnv(tmuxmcp.SocketEnvironmentVariable)
	environmentPath, pathSet := os.LookupEnv(tmuxmcp.SocketPathEnvironmentVariable)
	if name == "" && path == "" && nameSet && pathSet {
		return "", "", "", "", false, fmt.Errorf(
			"%s and %s are mutually exclusive",
			tmuxmcp.SocketEnvironmentVariable,
			tmuxmcp.SocketPathEnvironmentVariable,
		)
	}
	defaultDedicated := false
	switch {
	case path != "":
		socketPath, origin = path, "-socket-path"
	case name != "":
		socketName, origin = name, "-socket-name"
	case pathSet:
		socketPath, origin = strings.TrimSpace(environmentPath), tmuxmcp.SocketPathEnvironmentVariable
	case nameSet:
		socketName, origin = strings.TrimSpace(environmentName), tmuxmcp.SocketEnvironmentVariable
	default:
		socketName, origin, defaultDedicated = "libtmux-mcp", "default dedicated socket", true
	}
	if socketName == "" && socketPath == "" {
		return "", "", "", "", false, fmt.Errorf("%s socket selector is empty", origin)
	}
	if socketPath != "" {
		if !filepath.IsAbs(socketPath) {
			return "", "", "", "", false, fmt.Errorf("%s socket path must be absolute", origin)
		}
		socketPath = filepath.Clean(socketPath)
	}
	configured, configuredSet := os.LookupEnv(tmuxmcp.TmuxConfigEnvironmentVariable)
	if configuredSet {
		configured = strings.TrimSpace(configured)
		if configured == "" || !filepath.IsAbs(configured) {
			return "", "", "", "", false, fmt.Errorf(
				"%s must be an absolute config path", tmuxmcp.TmuxConfigEnvironmentVariable,
			)
		}
		configFile = filepath.Clean(configured)
	}
	return socketName, socketPath, configFile, origin, defaultDedicated && !configuredSet, nil
}

func binaryFrom(flagged string) string {
	if flagged != "" {
		return flagged
	}
	return strings.TrimSpace(os.Getenv(tmuxmcp.BinaryEnvironmentVariable))
}
