package mcp

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func promptTestClient(t *testing.T, toolsets string) *sdk.ClientSession {
	t.Helper()
	setCapabilityEnvironment(t, toolsets, "", "")
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "libtmux-mcp-prompts-unused", ConfigFile: "/dev/null",
	}))
	return connectInputTestClient(t.Context(), t, instance, nil)
}

func listedPromptNames(t *testing.T, session *sdk.ClientSession) []string {
	t.Helper()
	listed, err := session.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, prompt := range listed.Prompts {
		names = append(names, prompt.Name)
	}
	slices.Sort(names)
	return names
}

func TestPromptsFollowTheStartupSelection(t *testing.T) {
	for _, test := range []struct {
		toolsets string
		want     []string
	}{
		{"inspect,manage,execute", []string{
			"build_dev_workspace", "diagnose_failing_pane", "interrupt_gracefully", "run_and_wait",
		}},
		{"inspect", []string{"diagnose_failing_pane"}},
		{"execute", []string{"run_and_wait"}},
		{"", []string{}},
	} {
		t.Run(test.toolsets, func(t *testing.T) {
			got := listedPromptNames(t, promptTestClient(t, test.toolsets))
			if !slices.Equal(got, test.want) {
				t.Errorf("prompts = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAPromptNeedsEveryToolItNamesToBeSelected(t *testing.T) {
	setCapabilityEnvironment(t, "inspect,manage,execute", "", "wait_for_text")
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "libtmux-mcp-prompts-unused", ConfigFile: "/dev/null",
	}))
	got := listedPromptNames(t, connectInputTestClient(t.Context(), t, instance, nil))
	if slices.Contains(got, "interrupt_gracefully") {
		t.Errorf("interrupt_gracefully is listed without wait_for_text: %v", got)
	}
	if !slices.Contains(got, "run_and_wait") {
		t.Errorf("run_and_wait should survive excluding wait_for_text: %v", got)
	}
}

// promptSampleArguments fills every declared argument, so rendering shows the
// whole text a prompt can produce.
func promptSampleArguments(prompt promptDefinition) map[string]string {
	samples := map[string]string{
		"pane_id": "%1", "command": "make test", "session_name": "dev",
		"log_command": "tail -f app.log", "timeout": "12",
	}
	given := map[string]string{}
	for _, argument := range prompt.arguments {
		value, ok := samples[argument.name]
		if !ok {
			value = "sample"
		}
		given[argument.name] = value
	}
	return given
}

func TestEveryToolAPromptNamesIsDeclaredAndExists(t *testing.T) {
	definitions, err := toolManifest()
	if err != nil {
		t.Fatal(err)
	}
	catalog := map[string]toolDefinition{}
	for _, definition := range definitions {
		catalog[definition.name] = definition
	}
	call := regexp.MustCompile(`(\w+)\(([^)]*)\)`)
	key := regexp.MustCompile(`(\w+)=`)
	for _, prompt := range promptManifest() {
		text := prompt.render(promptSampleArguments(prompt))
		named := []string{}
		for name := range catalog {
			if regexp.MustCompile(`\b` + name + `\b`).MatchString(text) {
				named = append(named, name)
			}
		}
		slices.Sort(named)
		want := slices.Sorted(slices.Values(prompt.requires))
		if !slices.Equal(named, want) {
			t.Errorf("prompt %s text names tools %v, declares %v", prompt.name, named, want)
		}
		for _, match := range call.FindAllStringSubmatch(text, -1) {
			definition, isTool := catalog[match[1]]
			if !isTool {
				continue
			}
			for _, argument := range key.FindAllStringSubmatch(match[2], -1) {
				if _, ok := definition.inputSchema.Properties[argument[1]]; !ok {
					t.Errorf("prompt %s passes %s an argument %q its schema lacks",
						prompt.name, definition.name, argument[1])
				}
			}
		}
	}
}

func TestPromptManifestRefusesAnUndeclaredTool(t *testing.T) {
	definitions, err := toolManifest()
	if err != nil {
		t.Fatal(err)
	}
	render := func(map[string]string) string { return "x" }
	err = validatePromptManifest([]promptDefinition{{
		name: "p", title: "p", description: "p", requires: []string{"no_such_tool"}, render: render,
	}}, definitions)
	if err == nil || !strings.Contains(err.Error(), "no_such_tool") {
		t.Errorf("unknown required tool error = %v", err)
	}
}

func TestPromptGetRendersArguments(t *testing.T) {
	session := promptTestClient(t, "inspect,manage,execute")
	got, err := session.GetPrompt(t.Context(), &sdk.GetPromptParams{
		Name:      "run_and_wait",
		Arguments: map[string]string{"command": "echo hi", "pane_id": "%3", "timeout": "12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := got.Messages[0].Content.(*sdk.TextContent).Text
	for _, want := range []string{`pane_id="%3"`, `command="echo hi"`, "timeout=12"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestPromptGetRefusesWithInvalidParams(t *testing.T) {
	session := promptTestClient(t, "inspect,manage,execute")
	for name, params := range map[string]*sdk.GetPromptParams{
		"unknown prompt":   {Name: "no_such_prompt"},
		"missing required": {Name: "run_and_wait"},
		"blank required":   {Name: "run_and_wait", Arguments: map[string]string{"command": " ", "pane_id": "%1"}},
		"unknown argument": {Name: "run_and_wait", Arguments: map[string]string{
			"command": "ls", "pane_id": "%1", "shell": "sh",
		}},
		"bad timeout": {Name: "run_and_wait", Arguments: map[string]string{
			"command": "ls", "pane_id": "%1", "timeout": "soon",
		}},
		"negative timeout": {Name: "run_and_wait", Arguments: map[string]string{
			"command": "ls", "pane_id": "%1", "timeout": "-3",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := session.GetPrompt(t.Context(), params)
			var wire *jsonrpc.Error
			if !errors.As(err, &wire) || wire.Code != jsonrpc.CodeInvalidParams {
				t.Errorf("error = %#v, want JSON-RPC %d", err, jsonrpc.CodeInvalidParams)
			}
		})
	}
}

func TestCapabilitiesResourceListsTheListedPrompts(t *testing.T) {
	session := promptTestClient(t, "inspect")
	read, err := session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: testCapabilitiesURI})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Prompts []struct {
			Name          string   `json:"name"`
			RequiresTools []string `json:"requiresTools"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal([]byte(read.Contents[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, prompt := range payload.Prompts {
		names = append(names, prompt.Name)
	}
	if want := listedPromptNames(t, session); !slices.Equal(names, want) {
		t.Errorf("capabilities prompts = %v, prompts/list = %v", names, want)
	}
	if len(payload.Prompts) != 1 || !slices.Equal(payload.Prompts[0].RequiresTools, []string{"snapshot_pane", "capture_since"}) {
		t.Errorf("capabilities prompt rows = %#v", payload.Prompts)
	}
}
