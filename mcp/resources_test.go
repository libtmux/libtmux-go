package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// resourceProtocols are the two protocol generations a resource is read on.
var resourceProtocols = []string{"2025-11-25", "2026-07-28"}

func connectResourceClient(
	ctx context.Context,
	t *testing.T,
	instance *Instance,
	version string,
	options *sdk.ClientOptions,
) *sdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "resource-test", Version: "1"}, options)
	session, err := client.Connect(ctx, clientTransport, &sdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// A resource is a second way to reach a tool, so the startup selection that
// withholds the tool withholds the resource too.
func TestResourcesFollowTheToolSelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		excluded  string
		refused   string
		resources []string
		templates []string
	}{
		{
			name:      "every read tool",
			resources: []string{CapabilitiesResourceURI, "tmux://sessions"},
			templates: []string{
				"tmux://sessions/{session_id}", "tmux://windows/{window_id}",
				"tmux://panes/{pane_id}", "tmux://panes/{pane_id}/content",
			},
		},
		{
			name:      "without capture_pane and get_session_info",
			excluded:  "capture_pane,get_session_info",
			refused:   "tmux://panes/%251/content",
			resources: []string{CapabilitiesResourceURI, "tmux://sessions"},
			templates: []string{"tmux://windows/{window_id}", "tmux://panes/{pane_id}"},
		},
		{
			name:      "without list_sessions",
			excluded:  "list_sessions",
			refused:   "tmux://sessions",
			resources: []string{CapabilitiesResourceURI},
			templates: []string{
				"tmux://sessions/{session_id}", "tmux://windows/{window_id}",
				"tmux://panes/{pane_id}", "tmux://panes/{pane_id}/content",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setCapabilityEnvironment(t, "inspect,manage,execute,teardown", "", test.excluded)
			target, err := tmux.NewServer(tmux.ServerOptions{
				SocketName: "libtmux-mcp-resource-test", ConfigFile: "/dev/null",
			})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := NewServer(target)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = instance.Close() })
			session := connectResourceClient(t.Context(), t, instance, "2025-11-25", nil)

			listed, err := session.ListResources(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var resources []string
			for _, resource := range listed.Resources {
				resources = append(resources, resource.URI)
			}
			templates, err := session.ListResourceTemplates(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var uriTemplates []string
			for _, template := range templates.ResourceTemplates {
				uriTemplates = append(uriTemplates, template.URITemplate)
			}
			slices.Sort(resources)
			slices.Sort(uriTemplates)
			if want := slices.Sorted(slices.Values(test.resources)); !slices.Equal(resources, want) {
				t.Errorf("resources = %v, want %v", resources, want)
			}
			if want := slices.Sorted(slices.Values(test.templates)); !slices.Equal(uriTemplates, want) {
				t.Errorf("templates = %v, want %v", uriTemplates, want)
			}
			if test.refused == "" {
				return
			}
			_, err = session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: test.refused})
			assertInvalidParams(t, err)
		})
	}
}

func assertInvalidParams(t *testing.T, err error) {
	t.Helper()
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v (%T), want a JSON-RPC error", err, err)
	}
	if rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("error code = %d (%s), want %d", rpcErr.Code, rpcErr.Message, jsonrpc.CodeInvalidParams)
	}
}

// A read goes through the tool's own dispatcher, so it returns what the tool
// returns, and it is meant for the caller's own cache alone.
//
//libtmux:real-tmux
func TestResourceReadsAnswerWhatTheirToolsAnswer(t *testing.T) {
	for _, version := range resourceProtocols {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			request := tmux.NewSessionRequest{Name: "res"}
			target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
				FixedShell: true, InitialSession: &request,
			})
			instance, err := NewServer(target)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = instance.Close() })
			session := connectResourceClient(ctx, t, instance, version, nil)

			snapshot, err := target.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tmuxSession := snapshot.Sessions()[0]
			window, err := tmuxSession.ResolveActiveWindow(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pane, err := window.ResolveActivePane(ctx)
			if err != nil {
				t.Fatal(err)
			}
			paneURI := "tmux://panes/" + escapeID(pane.ID().String())

			for _, test := range []struct {
				uri       string
				tool      string
				arguments map[string]any
				mime      string
			}{
				{"tmux://sessions", "list_sessions", nil, "application/json"},
				{
					"tmux://sessions/" + escapeID(tmuxSession.ID().String()),
					"get_session_info",
					map[string]any{"session_id": tmuxSession.ID().String()},
					"application/json",
				},
				{
					"tmux://windows/" + escapeID(window.ID().String()),
					"get_window_info",
					map[string]any{"window_id": window.ID().String()},
					"application/json",
				},
				{paneURI, "get_pane_info", map[string]any{"pane_id": pane.ID().String()}, "application/json"},
				{paneURI + "/content", "capture_pane", map[string]any{"pane_id": pane.ID().String()}, "text/plain"},
			} {
				read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: test.uri})
				if err != nil {
					t.Fatalf("%s: %v", test.uri, err)
				}
				if len(read.Contents) != 1 || read.Contents[0].URI != test.uri || read.Contents[0].MIMEType != test.mime {
					t.Fatalf("%s: contents = %+v", test.uri, read.Contents)
				}
				called, err := session.CallTool(ctx, &sdk.CallToolParams{Name: test.tool, Arguments: argumentsOrEmpty(test.arguments)})
				if err != nil || called.IsError {
					t.Fatalf("%s: %v %+v", test.tool, err, called)
				}
				if test.mime == "text/plain" {
					if !strings.Contains(textOf(called), strings.TrimSpace(read.Contents[0].Text)) {
						t.Errorf("%s: %q is not what %s returned (%q)", test.uri, read.Contents[0].Text, test.tool, textOf(called))
					}
				} else {
					var got any
					if err := json.Unmarshal([]byte(read.Contents[0].Text), &got); err != nil {
						t.Fatalf("%s: %v", test.uri, err)
					}
					if !reflect.DeepEqual(got, called.StructuredContent) {
						t.Errorf("%s: read %v, but %s returned %v", test.uri, got, test.tool, called.StructuredContent)
					}
				}
				if version == "2026-07-28" && read.CacheScope != "private" {
					t.Errorf("%s: cacheScope = %q, want private", test.uri, read.CacheScope)
				}
			}

			capabilities, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: CapabilitiesResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			if version == "2026-07-28" && capabilities.CacheScope != "private" {
				t.Errorf("capabilities cacheScope = %q, want private", capabilities.CacheScope)
			}
			var report struct {
				Resources []struct {
					URITemplate string `json:"uriTemplate"`
					Tool        string `json:"tool"`
				} `json:"resources"`
				Boundary map[string]any `json:"boundary"`
			}
			if err := json.Unmarshal([]byte(capabilities.Contents[0].Text), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Resources) != 5 || report.Boundary["dynamicResources"] != true {
				t.Errorf("capabilities resources = %+v, boundary = %v", report.Resources, report.Boundary)
			}

			for _, uri := range []string{
				"tmux://panes/%2599", "tmux://panes/%2599/content", "tmux://windows/%4099",
				"tmux://sessions/%2499", "tmux://sessions/%2499/content", "tmux://nothing",
			} {
				_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
				if err == nil {
					t.Fatalf("%s: read succeeded", uri)
				}
				assertInvalidParams(t, err)
			}
		})
	}
}

func argumentsOrEmpty(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	return arguments
}

func textOf(result *sdk.CallToolResult) string {
	var text []string
	for _, content := range result.Content {
		if part, ok := content.(*sdk.TextContent); ok {
			text = append(text, part.Text)
		}
	}
	return strings.Join(text, "\n")
}
