package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCapabilityProfilesResolveToExplicitGrants(t *testing.T) {
	for _, test := range []struct {
		value string
		want  []Capability
	}{
		{"", []Capability{CapabilityMetadataRead}},
		{"inspect", []Capability{CapabilityMetadataRead, CapabilityContentRead}},
		{
			"operate",
			[]Capability{
				CapabilityMetadataRead, CapabilityContentRead, CapabilityPaneControl,
				CapabilityWorkspaceCreate, CapabilityTmuxLayout, CapabilityTmuxSettings,
			},
		},
		{"all", allCapabilities},
		{
			"content-read, tmux-layout",
			[]Capability{CapabilityContentRead, CapabilityTmuxLayout},
		},
		{"unknown", []Capability{CapabilityMetadataRead}},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv(CapabilitiesEnvironmentVariable, test.value)
			if got := ResolvedCapabilities(); !slices.Equal(got, test.want) {
				t.Fatalf("ResolvedCapabilities() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUnknownCapabilitiesAreReportedWithoutWideningTheGrant(t *testing.T) {
	t.Setenv(CapabilitiesEnvironmentVariable, "content-read, shell-exec, typo")
	if got := ResolvedCapabilities(); !slices.Equal(got, []Capability{CapabilityContentRead}) {
		t.Fatalf("ResolvedCapabilities() = %v, want only content-read", got)
	}
	wantRejected := []string{"shell-exec", "typo"}
	if got := RejectedCapabilityValues(); !slices.Equal(got, wantRejected) {
		t.Fatalf("RejectedCapabilityValues() = %v, want %v", got, wantRejected)
	}
}

func TestServerInfoFreezesRejectedCapabilities(t *testing.T) {
	t.Setenv(CapabilitiesEnvironmentVariable, "metadata-read,original-typo")
	registry := newToolRegistry()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "missing.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry.runtime = newRuntime(t.Context(), target, nil)

	t.Setenv(CapabilitiesEnvironmentVariable, "metadata-read,later-typo")
	_, output, err := registry.getServerInfo(t.Context(), nil, getServerInfoInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"original-typo"}
	if !slices.Equal(output.RejectedCapabilities, want) {
		t.Fatalf("rejected capabilities = %v, want frozen %v",
			output.RejectedCapabilities, want)
	}
}

func TestCapabilitySetRequiresTheNamedGrant(t *testing.T) {
	set := newCapabilitySet([]Capability{CapabilityContentRead, CapabilityTmuxLayout})
	if !set.permits(CapabilityContentRead) || !set.permits(CapabilityTmuxLayout) {
		t.Fatal("set refused a capability it contains")
	}
	if set.permits(CapabilityPaneControl) {
		t.Fatal("set permitted a capability it does not contain")
	}
}

func TestMetadataServerRefusesOptionalContent(t *testing.T) {
	toolset := &tools{capabilities: newCapabilitySet([]Capability{CapabilityMetadataRead})}
	_, _, err := toolset.getServerInfo(
		context.Background(), nil, getServerInfoInput{IncludeMessages: true},
	)
	if err == nil || !strings.Contains(err.Error(), "content-read") {
		t.Fatalf("getServerInfo(includeMessages) error = %v, want content-read refusal", err)
	}
}

func TestMetadataServerRefusesPaneContentSubscriptions(t *testing.T) {
	toolset := &tools{capabilities: newCapabilitySet([]Capability{CapabilityMetadataRead})}
	err := toolset.subscribe(context.Background(), &sdk.SubscribeRequest{
		Session: &sdk.ServerSession{},
		Params:  &sdk.SubscribeParams{URI: "tmux://panes/1/content"},
	})
	if err == nil || !strings.Contains(err.Error(), "content-read") {
		t.Fatalf("subscribe(content) error = %v, want content-read refusal", err)
	}
}

func TestSubscriptionHandlersRejectMissingRequests(t *testing.T) {
	toolset := &tools{capabilities: newCapabilitySet([]Capability{CapabilityMetadataRead})}
	if err := toolset.subscribe(context.Background(), nil); !errors.Is(err, ErrInstanceClosed) {
		t.Fatalf("subscribe(nil) error = %v, want ErrInstanceClosed", err)
	}
	if err := toolset.unsubscribe(context.Background(), nil); !errors.Is(err, ErrInstanceClosed) {
		t.Fatalf("unsubscribe(nil) error = %v, want ErrInstanceClosed", err)
	}
}

func TestCapabilitiesAndSafetyBothWithholdTools(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities string
		safety       string
		present      []string
		absent       []string
	}{
		{
			name: "default metadata", safety: "destructive",
			present: []string{"list_panes", "get_pane_info"},
			absent:  []string{"capture_pane", "send_keys", "create_session", "kill_pane"},
		},
		{
			name: "inspect", capabilities: "inspect", safety: "destructive",
			present: []string{"list_panes", "capture_pane", "show_option"},
			absent:  []string{"send_keys", "create_session", "kill_pane"},
		},
		{
			name: "pane control alone", capabilities: "pane-control", safety: "destructive",
			present: []string{"send_keys", "run_command", "display_message"},
			absent:  []string{"list_panes", "capture_pane", "create_session", "kill_pane"},
		},
		{
			name: "operate", capabilities: "operate", safety: "destructive",
			present: []string{"capture_pane", "send_keys", "create_session", "set_option"},
			absent:  []string{"kill_pane"},
		},
		{
			name: "safety still bounds all", capabilities: "all", safety: "readonly",
			present: []string{"capture_pane", "list_panes"},
			absent:  []string{"send_keys", "display_message", "create_session", "kill_pane"},
		},
		{
			name: "all", capabilities: "all", safety: "destructive",
			present: []string{"capture_pane", "send_keys", "create_session", "kill_pane"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CapabilitiesEnvironmentVariable, test.capabilities)
			t.Setenv(SafetyEnvironmentVariable, test.safety)
			listed := advertisedToolNames(t)
			for _, name := range test.present {
				if !listed[name] {
					t.Errorf("%s is withheld", name)
				}
			}
			for _, name := range test.absent {
				if listed[name] {
					t.Errorf("%s is advertised", name)
				}
			}
		})
	}
}

func TestChannelToolsRequireMutatingPaneControl(t *testing.T) {
	for _, test := range []struct {
		name, capabilities, safety string
		wantWait, wantSignal       bool
	}{
		{name: "metadata alone", capabilities: "metadata-read", safety: "destructive"},
		{name: "read-only pane control", capabilities: "pane-control", safety: "readonly"},
		{
			name: "mutating pane control", capabilities: "pane-control", safety: "mutating",
			wantWait: true, wantSignal: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CapabilitiesEnvironmentVariable, test.capabilities)
			t.Setenv(SafetyEnvironmentVariable, test.safety)
			listed := advertisedToolNames(t)
			if listed["wait_for_channel"] != test.wantWait {
				t.Errorf("wait_for_channel advertised = %t, want %t",
					listed["wait_for_channel"], test.wantWait)
			}
			if listed["signal_channel"] != test.wantSignal {
				t.Errorf("signal_channel advertised = %t, want %t",
					listed["signal_channel"], test.wantSignal)
			}
		})
	}
}

func TestCapabilitiesSeparateTopologyFromPaneContentResources(t *testing.T) {
	for _, test := range []struct {
		name, capabilities string
		wantSessions       bool
		wantContent        bool
	}{
		{name: "default metadata", wantSessions: true},
		{name: "content only", capabilities: "content-read", wantContent: true},
		{name: "inspect", capabilities: "inspect", wantSessions: true, wantContent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CapabilitiesEnvironmentVariable, test.capabilities)
			t.Setenv(SafetyEnvironmentVariable, "destructive")
			session, ctx := capabilitySession(t)
			resources, err := session.ListResources(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			gotSessions := false
			for _, resource := range resources.Resources {
				gotSessions = gotSessions || resource.URI == resourceSessions
			}
			if gotSessions != test.wantSessions {
				t.Errorf("sessions resource advertised = %v, want %v", gotSessions, test.wantSessions)
			}

			templates, err := session.ListResourceTemplates(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			gotContent := false
			for _, template := range templates.ResourceTemplates {
				if template.URITemplate != templatePaneContent {
					continue
				}
				gotContent = true
				if template.MIMEType != "text/plain" {
					t.Errorf("pane-content template MIME type = %q, want text/plain", template.MIMEType)
				}
			}
			if gotContent != test.wantContent {
				t.Errorf("pane-content template advertised = %v, want %v", gotContent, test.wantContent)
			}
		})
	}
}

func TestPromptsRequireEveryCapabilityTheirStepsUse(t *testing.T) {
	for _, test := range []struct {
		name, capabilities string
		want               []string
	}{
		{name: "default metadata"},
		{name: "inspect", capabilities: "inspect", want: []string{"diagnose_pane", "watch_pane"}},
		{
			name: "operate", capabilities: "operate",
			want: []string{"diagnose_pane", "recover_pane", "set_up_workspace", "watch_pane"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CapabilitiesEnvironmentVariable, test.capabilities)
			t.Setenv(SafetyEnvironmentVariable, "mutating")
			session, ctx := capabilitySession(t)
			listed, err := session.ListPrompts(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(listed.Prompts))
			for _, prompt := range listed.Prompts {
				got = append(got, prompt.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, test.want) {
				t.Fatalf("prompts = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBatchCannotReachAToolWithheldByCapabilities(t *testing.T) {
	t.Setenv(CapabilitiesEnvironmentVariable, "metadata-read")
	t.Setenv(SafetyEnvironmentVariable, "destructive")
	session, ctx := capabilitySession(t)
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "call_readonly_tools_batch",
		Arguments: map[string]any{"calls": []map[string]any{{
			"tool": "capture_pane", "arguments": map[string]any{"paneId": "%1"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("read-only batch reached capture_pane without content-read")
	}
}

func TestEveryToolDeclaresItsRequiredCapability(t *testing.T) {
	t.Setenv(CapabilitiesEnvironmentVariable, "all")
	t.Setenv(SafetyEnvironmentVariable, "destructive")
	session, ctx := capabilitySession(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	groups := map[Capability][]string{
		CapabilityMetadataRead: {
			"call_readonly_tools_batch", "find_pane_by_position", "get_pane_info",
			"get_server_info", "get_session_info", "get_window_info", "list_panes",
			"list_servers", "list_sessions", "list_windows",
		},
		CapabilityContentRead: {
			"capture_pane", "capture_since", "get_job", "search_panes", "show_buffer",
			"show_environment", "show_hooks", "show_option", "snapshot_pane", "wait_for_text",
		},
		CapabilityPaneControl: {
			"call_mutating_tools_batch", "clear_pane", "display_message", "enter_copy_mode",
			"exit_copy_mode", "paste_buffer", "paste_text", "pipe_pane", "run_command",
			"send_keys", "send_keys_batch", "signal_channel", "wait_for_channel",
		},
		CapabilityWorkspaceCreate: {
			"build_workspace", "create_session", "create_window", "respawn_pane", "split_window",
		},
		CapabilityTmuxLayout: {
			"move_pane", "move_window", "rename_session", "rename_window", "resize_pane",
			"resize_window", "select_layout", "select_pane", "select_window", "set_pane_title",
			"swap_pane",
		},
		CapabilityTmuxSettings: {
			"delete_buffer", "load_buffer", "set_environment", "set_option",
		},
		CapabilityTmuxDestroy: {
			"call_destructive_tools_batch", "kill_pane", "kill_server", "kill_session", "kill_window",
		},
	}
	want := map[string]Capability{}
	for capability, names := range groups {
		for _, name := range names {
			if previous, duplicate := want[name]; duplicate {
				t.Fatalf("%s belongs to both %s and %s", name, previous, capability)
			}
			want[name] = capability
		}
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("listed %d tools, capability matrix names %d", len(listed.Tools), len(want))
	}
	for _, tool := range listed.Tools {
		got, _ := tool.Meta[CapabilityMetaKey].(string)
		if expected, ok := want[tool.Name]; !ok {
			t.Errorf("%s is missing from the capability matrix", tool.Name)
		} else if got != string(expected) {
			t.Errorf("%s capability = %q, want %q", tool.Name, got, expected)
		}
	}
}

func advertisedToolNames(t *testing.T) map[string]bool {
	t.Helper()
	session, ctx := capabilitySession(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names
}

func capabilitySession(t *testing.T) (*sdk.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "capabilities-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance := mustInternalMCPServer(t, target)
	t.Cleanup(func() { _ = instance.Close() })
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "capabilities", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}
