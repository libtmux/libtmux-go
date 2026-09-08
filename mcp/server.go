package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported during MCP initialization. Installed binaries derive it
// from build metadata; source builds use fallbackVersion.
var Version = buildVersion()

const fallbackVersion = "v0.0.1-alpha.8"

// minimumTmuxVersion follows the terminal Connection capability floor because
// every MCP runtime owns one.
const minimumTmuxVersion = tmux.MinimumConnectionVersion

const (
	minimalOwnerEnvironment = "LIBTMUX_MCP_OWNER"
	minimalOwnerOption      = "@libtmux_mcp_owner"
)

func (i *Instance) requireTmuxVersion(ctx context.Context) error {
	minimum, err := tmux.ParseVersion(minimumTmuxVersion)
	if err != nil {
		return err
	}
	return i.runtime.base.RequireVersion(ctx, minimum)
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallbackVersion
	}
	return info.Main.Version
}

// closedArguments adds enums that jsonschema-go cannot express through these tags.
var closedArguments = map[string]map[string][]any{
	"show_option":           {"scope": scopeValues},
	"show_hooks":            {"scope": scopeValues},
	"split_window":          {"direction": placementValues},
	"call_read_tools_batch": {"on_error": {onErrorStop, onErrorContinue}},
	"send_keys_batch":       {"on_error": {onErrorStop, onErrorContinue}},
	"find_pane_by_position": {"position": {"top-left", "top-right", "bottom-left", "bottom-right"}},
	"create_window":         {"direction": {"before", "after"}},
}

// minimumArguments adds numeric lower bounds that jsonschema-go cannot express
// through struct tags.
var minimumArguments = map[string]map[string]float64{
	"wait_for_channel":  {"timeout": 0},
	"wait_for_text":     {"timeout": 0},
	"run_shell_command": {"timeout": 0, "max_lines": 0},
}

var maximumArguments = map[string]map[string]float64{
	"search_panes": {
		"max_matches_per_pane": searchCeilingMatchesPerPane,
		"max_lines":            searchCeilingPanes,
	},
}

var boundedStringArguments = map[string]map[string]int{
	"search_panes": {"pattern": patternBytesLimit},
}

var boundedPatternArrays = map[string][]string{
	"wait_for_text": {"patterns", "stop"},
}

var scopeValues = []any{"", "global", scopeServer, scopeSession, scopeWindow, scopePane}

var placementValues = []any{"", "below", "above", "right", "left"}

// listsAreLists removes nullable alternatives inferred for slices.
func listsAreLists(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if len(schema.Types) > 1 && slices.Contains(schema.Types, "array") {
		schema.Types = slices.DeleteFunc(schema.Types, func(kind string) bool {
			return kind == "null"
		})
	}
	listsAreLists(schema.Items)
	for _, property := range schema.Properties {
		listsAreLists(property)
	}
}

func constrain(name string, schema *jsonschema.Schema) {
	for argument, values := range closedArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			property.Enum = values
		}
	}
	for argument, minimum := range minimumArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			minimum := minimum
			property.Minimum = &minimum
		}
	}
	for argument, maximum := range maximumArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			maximum := maximum
			property.Maximum = &maximum
		}
	}
	for argument, maximum := range boundedStringArguments[name] {
		if property, ok := schema.Properties[argument]; ok {
			maximum := maximum
			property.MaxLength = &maximum
		}
	}
	for _, argument := range boundedPatternArrays[name] {
		if property, ok := schema.Properties[argument]; ok {
			maximumItems := patternCountLimit
			maximumLength := patternBytesLimit
			property.MaxItems = &maximumItems
			if property.Items == nil {
				property.Items = &jsonschema.Schema{Type: "string"}
			}
			property.Items.MaxLength = &maximumLength
		}
	}
}

// NewServer returns a closeable MCP instance exposing target. It rejects an
// invalid target before allocating instance-owned resources.
func NewServer(target tmux.Server) (*Instance, error) {
	profile, err := profileForTarget(target, "unknown")
	if err != nil {
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}
	if target.ConnectionBound() {
		return nil, ErrRuntimeTargetBound
	}
	surface, err := resolveToolSurface(profile)
	if err != nil {
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}
	return newServer(target, surface)
}

func newServer(target tmux.Server, surface toolSurface) (*Instance, error) {
	instance := newInstance()
	runtime := newRuntime(instance.ctx, target, instance.terminal)
	instance.runtime = runtime
	tools := newToolRegistry(surface)
	tools.instance = instance
	tools.runtime = runtime
	serverOptions := &mcp.ServerOptions{
		Instructions: "Operate only on tmux objects through the startup-frozen tool surface. " +
			"Read tmux://capabilities before delegating executable or destructive work.",
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "libtmux",
		Version: Version,
	}, serverOptions)
	instance.server = server
	instance.tools = tools
	// Audit wraps the backstop so oversized refusals are recorded.
	server.AddReceivingMiddleware(backstop())
	writer, auditOwner := auditWriter()
	if writer != nil {
		server.AddReceivingMiddleware(audit(writer))
	}
	server.AddReceivingMiddleware(instance.scoped)

	if err := registerToolManifest(server, tools); err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("construct MCP server: %w", err)
	}
	addResources(server, tools)

	instance.audit = auditOwner
	return instance, nil
}

func newToolRegistry(configured ...toolSurface) *tools {
	var surface toolSurface
	var configurationErr error
	if len(configured) != 0 {
		surface = configured[0]
	} else {
		surface, configurationErr = resolveToolSurface(socketProfile{
			Selector:                "name:libtmux-mcp",
			SelectionProvenance:     "default-dedicated",
			ServerState:             "unknown",
			ConfigurationProvenance: "minimal",
			NamespaceBoundary:       "tmux-objects-only",
			AttachCommand:           "tmux -N -L 'libtmux-mcp' attach",
		})
	}
	return &tools{
		surface:         surface,
		waitCeiling:     waitCeilingFromEnvironment(),
		dispatchers:     map[string]dispatcher{},
		registrationErr: configurationErr,
	}
}

func registerToolManifest(server *mcp.Server, tools *tools) error {
	for _, definition := range tools.surface.tools {
		definition.register(server, tools, definition)
		if tools.registrationErr != nil {
			return tools.registrationErr
		}
	}
	definitions, err := toolManifest()
	if err != nil {
		return err
	}
	all := make(map[string]toolDefinition, len(definitions))
	for _, definition := range definitions {
		all[definition.name] = definition
	}
	for _, aggregate := range tools.surface.tools {
		if !aggregate.aggregate {
			continue
		}
		for _, name := range aggregate.nestedAuthority {
			if _, bound := tools.dispatchers[name]; bound {
				continue
			}
			definition := all[name]
			definition.bindDispatcher(tools, definition)
			if tools.registrationErr != nil {
				return tools.registrationErr
			}
		}
	}
	return nil
}

// Run serves target over stdin and stdout until ctx is done.
func Run(ctx context.Context, target tmux.Server) error {
	if target.ConnectionBound() {
		return ErrRuntimeTargetBound
	}
	environment := currentStartupEnvironment()
	profile, err := profileForTarget(target, "unknown")
	if err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	if _, err := resolveToolSurfaceFrom(environment, profile); err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	alive, err := target.IsAlive(ctx)
	if err != nil {
		return fmt.Errorf("inspect selected tmux socket: %w", err)
	}
	state := "absent"
	if alive {
		state = "existing"
	}
	profile, err = profileForTarget(target, state)
	if err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	surface, err := resolveToolSurfaceFrom(environment, profile)
	if err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	return runPinnedSurface(ctx, target, surface)
}

// RunDefaultMinimal serves a package-created default dedicated target. It
// starts or joins the daemon with a one-use owner marker and grants default
// teardown only when the marker proves this process created that daemon.
func RunDefaultMinimal(ctx context.Context, target tmux.Server) error {
	if target.ConnectionBound() {
		return ErrRuntimeTargetBound
	}
	environment := currentStartupEnvironment()
	conservative, err := profileForTarget(target, "unknown")
	if err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	// Invalid selection must fail before a tmux subprocess can open the socket.
	if _, err := resolveToolSurfaceFrom(environment, conservative); err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	nonce, err := newMinimalOwnerNonce()
	if err != nil {
		return fmt.Errorf("construct MCP server: owner marker: %w", err)
	}
	profile, err := pinDefaultMinimal(ctx, target, nonce)
	if err != nil {
		return fmt.Errorf("pin default minimal tmux server: %w", err)
	}
	surface, err := resolveToolSurfaceFrom(environment, profile)
	if err != nil {
		return fmt.Errorf("construct MCP server: %w", err)
	}
	return runPinnedSurface(ctx, target, surface)
}

func runPinnedSurface(ctx context.Context, target tmux.Server, surface toolSurface) error {
	instance, err := newServer(target, surface)
	if err != nil {
		return err
	}
	defer func() { _ = instance.Close() }()
	return instance.Run(ctx, stdio())
}

func newMinimalOwnerNonce() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func pinDefaultMinimal(
	ctx context.Context,
	target tmux.Server,
	nonce string,
) (socketProfile, error) {
	minimal, err := isShippedMinimalConfig(target.ConfigFile())
	if err != nil {
		return socketProfile{}, err
	}
	if !minimal {
		return socketProfile{}, fmt.Errorf("default minimal target does not use the package-owned config")
	}
	launcher, err := target.WithProcessEnvironmentValue(minimalOwnerEnvironment, nonce)
	if err != nil {
		return socketProfile{}, err
	}
	if err := launcher.Start(ctx); err != nil {
		return socketProfile{}, err
	}
	marker, present, err := target.GlobalSessionScope().RawOption(ctx, minimalOwnerOption)
	if err != nil {
		return socketProfile{}, err
	}
	selection, err := target.SocketSelection()
	if err != nil {
		return socketProfile{}, err
	}
	owned := present && marker == nonce
	selector := "path:" + selection.Path
	if filepath.Base(selection.Path) == "libtmux-mcp" {
		selector = "name:libtmux-mcp"
	}
	profile := socketProfile{
		Selector:                selector,
		SelectionProvenance:     "default-dedicated",
		ServerState:             "existing",
		ConfigurationProvenance: "unknown",
		NamespaceBoundary:       "tmux-objects-only",
		ResolvedSocketPath:      selection.Path,
		AttachCommand: shellQuote(target.Executable()) + " -N -S " +
			shellQuote(selection.Path) + " attach",
		defaultTeardown: owned,
	}
	if selection.Path == "" {
		return socketProfile{}, fmt.Errorf("default minimal target has no socket path")
	}
	if owned {
		profile.ServerState = "created"
		profile.ConfigurationProvenance = "minimal"
	}
	return profile, nil
}

func profileForTarget(target tmux.Server, state string) (socketProfile, error) {
	selection, err := target.SocketSelection()
	if err != nil {
		return socketProfile{}, err
	}
	profile := socketProfile{
		Selector:                "path:" + selection.Path,
		SelectionProvenance:     "operator-current",
		ServerState:             state,
		ConfigurationProvenance: "unknown",
		NamespaceBoundary:       "tmux-objects-only",
		ResolvedSocketPath:      selection.Path,
		AttachCommand: shellQuote(target.Executable()) + " -N -S " +
			shellQuote(selection.Path) + " attach",
	}
	if state == "absent" {
		profile.ConfigurationProvenance = "user-configured"
	}
	return profile, nil
}

type tools struct {
	instance        *Instance
	runtime         *tmuxRuntime
	surface         toolSurface
	waitCeiling     time.Duration
	dispatchers     map[string]dispatcher
	registrationErr error
	// A process cannot change its containing pane. Failed discovery is not
	// cached because cancellation or transport loss may be transient.
	callerMutex  sync.Mutex
	caller       callerIdentity
	callerCached bool
}

// An invalid target has no socket identity and cannot match the caller.
func (t *tools) socketPath(_ context.Context) string {
	selection, err := t.runtime.base.SocketSelection()
	if err != nil {
		return ""
	}
	return selection.Path
}

// toolFailure preserves a partial result; returning err makes the SDK discard it.
func toolFailure(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
