package mcp_test

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestPromptsNameTheJobs(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	names := map[string]bool{}
	for _, prompt := range listed.Prompts {
		names[prompt.Name] = true
		if prompt.Description == "" {
			t.Errorf("%s has no description", prompt.Name)
		}
	}
	for _, want := range []string{"diagnose_pane", "set_up_workspace"} {
		if !names[want] {
			t.Errorf("prompt %q is not offered", want)
		}
	}

	// Naming a pane produces advice about that pane; omitting one produces
	// advice on finding it first.
	got, err := session.GetPrompt(ctx, &sdk.GetPromptParams{
		Name: "diagnose_pane", Arguments: map[string]string{"pane": "%3"},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("the prompt carries no message")
	}
	text, ok := got.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		t.Fatalf("prompt content is %T, want text", got.Messages[0].Content)
	}
	if !strings.Contains(text.Text, "%3") {
		t.Errorf("the prompt does not mention the pane it was given: %q", text.Text)
	}
	// It must steer away from the loop every tool description warns about.
	if !strings.Contains(text.Text, "wait_for_text") ||
		!strings.Contains(text.Text, "snapshot_pane") {
		t.Errorf("the prompt does not name the tools that do the job: %q", text.Text)
	}

	unnamed, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "diagnose_pane"})
	if err != nil {
		t.Fatalf("get prompt without a pane: %v", err)
	}
	if first, ok := unnamed.Messages[0].Content.(*sdk.TextContent); !ok ||
		!strings.Contains(first.Text, "list_panes") {
		t.Error("the prompt does not say how to find the pane when none was given")
	}
}

//libtmux:real-tmux
func TestPromptsRespectTheSafetyLevel(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)
	listed, err := session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	for _, prompt := range listed.Prompts {
		// Both of these tell the model to run tools a read-only server does
		// not offer, so suggesting them is advice it cannot take.
		switch prompt.Name {
		case "set_up_workspace":
			t.Error("a read-only server offers to set a workspace up")
		case "recover_pane":
			t.Error("a read-only server offers to recover a pane")
		}
	}
	if len(listed.Prompts) == 0 {
		t.Error("a read-only server offers no prompts at all")
	}
}
