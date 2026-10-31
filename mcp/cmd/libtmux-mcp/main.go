// Command libtmux-mcp serves one fixed tmux target over MCP stdio. Flags select
// the target, else LIBTMUX_SOCKET_PATH, else LIBTMUX_SOCKET. With none of them
// set, tmux picks the server: the one TMUX names, otherwise its default socket.
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
	socketName := flag.String("socket-name", "", "tmux socket name; with neither flag, LIBTMUX_SOCKET_PATH then LIBTMUX_SOCKET select the socket, else tmux picks the server")
	socketPath := flag.String("socket-path", "", "absolute tmux socket path; nonempty has highest precedence")
	binary := flag.String("binary", "", "tmux executable; empty uses LIBTMUX_TMUX_BIN, then resolves tmux through PATH")
	version := flag.Bool("version", false, "print the version and exit")
	tools := flag.Bool("tools", false, "print the tools this server would advertise and exit")
	doctor := flag.Bool("doctor", false, "report what this server can see and exit")
	flag.Parse()

	if *version {
		fmt.Println("libtmux-mcp", tmuxmcp.Version)
		return nil
	}
	resolvedName, resolvedPath, configFile, err := resolveTarget(*socketName, *socketPath)
	if err != nil {
		return err
	}
	if *tools {
		// The surface depends on whether a server answers on the socket
		// serving would use, so this asks the same socket.
		probe, err := tmux.NewServer(tmux.ServerOptions{
			SocketName: resolvedName,
			SocketPath: resolvedPath,
			Binary:     binaryFrom(*binary),
		})
		if err != nil {
			return err
		}
		return reportTools(probe)
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
		return reportDoctor(target)
	}
	return serve(target)
}

func serve(target tmux.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func reportTools(target tmux.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := tmuxmcp.AdvertisedToolsFor(ctx, target)
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
		summary := tool.Description
		if details, ok := tmuxmcp.ToolDetails(tool.Name); ok {
			summary = details
		}
		fmt.Printf("  %-28s %-10s %-20s %s\n",
			tool.Name, toolset, reach, firstSentence(summary))
	}
	return nil
}

// reportDoctor reports target selection and reachable tmux state.
func reportDoctor(target tmux.Server) error {
	selection, err := target.SocketSelection()
	if err != nil {
		return err
	}
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
		MinimumVersion   string `json:"minimumVersion"`
	}
	if err := callInto(ctx, session, "get_server_info", &info); err != nil {
		return err
	}

	fmt.Println("libtmux-mcp doctor")
	fmt.Printf("  tmux:    %s (requires %s or newer)\n", orUnknown(info.Version), orUnknown(info.MinimumVersion))
	fmt.Printf("  socket:  %s (from %s)\n", orUnknown(info.SocketPath), selection.Source)
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

// resolveTarget freezes the socket flags and the tmux configuration before MCP
// starts. A flag names the socket. With neither flag the core reads
// LIBTMUX_SOCKET_PATH and then LIBTMUX_SOCKET, and with neither set tmux picks
// the server.
func resolveTarget(name, path string) (
	socketName, socketPath, configFile string,
	err error,
) {
	if name != "" && path != "" {
		return "", "", "", errors.New("-socket-name and -socket-path are mutually exclusive")
	}
	if path != "" {
		if !filepath.IsAbs(path) {
			return "", "", "", errors.New("-socket-path must be an absolute path")
		}
		socketPath = filepath.Clean(path)
	}
	configured, configuredSet := os.LookupEnv(tmuxmcp.TmuxConfigEnvironmentVariable)
	if configuredSet {
		configured = strings.TrimSpace(configured)
		if configured == "" || !filepath.IsAbs(configured) {
			return "", "", "", fmt.Errorf(
				"%s must be an absolute config path", tmuxmcp.TmuxConfigEnvironmentVariable,
			)
		}
		configFile = filepath.Clean(configured)
	}
	return name, socketPath, configFile, nil
}

func binaryFrom(flagged string) string {
	if flagged != "" {
		return flagged
	}
	return strings.TrimSpace(os.Getenv(tmuxmcp.BinaryEnvironmentVariable))
}
