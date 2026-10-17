package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// The description is what an agent reads before it calls the tool, so it names
// both paths and every ending the reply can carry.
//
//libtmux:real-tmux
func TestWaitForTextDescriptionStatesItsPathsAndEndings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target := tmuxtest.NewServer(ctx, t)
	client, closeClient, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	listed, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "wait_for_text" {
			continue
		}
		if strings.Contains(strings.ToLower(tool.Description), "requires tmux 3.7") {
			t.Errorf("description still says the tool requires 3.7: %q", tool.Description)
		}
		for _, want := range []string{
			"polls capture-pane below", "3.7", "matched", "alreadyOnScreen", "stopped",
			"output", "idle", "timeout", "paneDied", "streamLost",
			"its program exited under remain-on-exit", "once a second",
		} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("description %q does not mention %q", tool.Description, want)
			}
		}
		return
	}
	t.Fatal("wait_for_text is not advertised")
}
