package mcp

import (
	"context"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The backstop cannot be reached through a tool, because every tool bounds
// itself well below it. That is the point of it and also why it is driven
// directly here: the case it exists for is the one that does not exist yet.

func TestTheBackstopReplacesAnOversizedResult(t *testing.T) {
	t.Parallel()
	oversized := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: strings.Repeat("x", backstopMaxBytes+1),
		}},
	}
	handler := backstop()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return oversized, nil
	})

	result, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "a_forgetful_tool"},
	})
	if err != nil {
		t.Fatalf("backstop returned an error rather than a refusal: %v", err)
	}
	call, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("backstop returned %T", result)
	}
	if !call.IsError {
		t.Error("an oversized result was passed through")
	}
	text, _ := call.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "a_forgetful_tool") {
		t.Errorf("the refusal does not name the tool: %#v", call.Content)
	}
}

func TestTheBackstopLeavesAnOrdinaryResultAlone(t *testing.T) {
	t.Parallel()
	ordinary := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "two lines\nof output"}},
	}
	handler := backstop()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return ordinary, nil
	})

	result, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "a_well_behaved_tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != mcp.Result(ordinary) {
		t.Error("an ordinary result was replaced rather than passed through")
	}
}

// Anything that is not a tool call passes through untouched, including the
// tool listing, which is larger than several tool results put together.
func TestTheBackstopIgnoresOtherMethods(t *testing.T) {
	t.Parallel()
	huge := &mcp.ListToolsResult{NextCursor: strings.Repeat("x", backstopMaxBytes+1)}
	handler := backstop()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return huge, nil
	})

	result, err := handler(context.Background(), "tools/list", &mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result != mcp.Result(huge) {
		t.Error("a listing was replaced by the tool-result backstop")
	}
}
