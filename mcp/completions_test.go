package mcp_test

import (
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestCompletionsOfferValuesThatExist(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: completed\nwindows:\n  - window_name: first\n" +
			"    panes:\n      - {}\n      - {}\n",
	}, nil)

	// A pane variable offers the panes that exist, without their sigil, which
	// is the form the URIs take.
	got, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/resource", URI: "tmux://panes/{pane}"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete a pane: %v", err)
	}
	if len(got.Completion.Values) < 2 {
		t.Fatalf("offered %v, want the panes that exist", got.Completion.Values)
	}
	for _, value := range got.Completion.Values {
		if strings.HasPrefix(value, "%") {
			t.Errorf("offered %q with its sigil, which is not what a URI takes", value)
		}
	}

	// A session variable offers the session by name.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://sessions/{session}/windows",
		},
		Argument: sdk.CompleteParamsArgument{Name: "session", Value: "comp"},
	})
	if err != nil {
		t.Fatalf("complete a session: %v", err)
	}
	if !slices.Contains(got.Completion.Values, "completed") {
		t.Errorf("offered %v, want the session named completed", got.Completion.Values)
	}

	// A value that matches nothing offers nothing rather than everything.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/resource", URI: "tmux://panes/{pane}"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: "zzz"},
	})
	if err != nil {
		t.Fatalf("complete with no match: %v", err)
	}
	if len(got.Completion.Values) != 0 {
		t.Errorf("a prefix matching nothing offered %v", got.Completion.Values)
	}

	// A prompt argument is answered in the dialect the tools speak, because
	// what fills it is read back by a model and passed to paneId. Offering the
	// URI form there hands the model an id every tool rejects.
	got, err = session.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/prompt", Name: "diagnose_pane"},
		Argument: sdk.CompleteParamsArgument{Name: "pane", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete a prompt argument: %v", err)
	}
	if len(got.Completion.Values) == 0 {
		t.Fatal("a prompt argument offered nothing")
	}
	for _, value := range got.Completion.Values {
		if !strings.HasPrefix(value, "%") {
			t.Errorf("offered %q to a prompt, which no tool accepts as a pane", value)
		}
	}

	// Whatever is offered for a prompt has to be usable as a pane id, which is
	// the whole claim: hand it straight to a tool.
	if len(got.Completion.Values) > 0 {
		result := call(ctx, t, session, "get_pane_info", map[string]any{
			"paneId": got.Completion.Values[0],
		}, nil)
		if result.IsError {
			t.Errorf("a completed prompt value is not a pane a tool will take: %#v",
				result.Content)
		}
	}
}

//libtmux:real-tmux
func TestCompletionsEscapeWhatAUriMustCarry(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: spaced name\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - {}\n",
	}, nil)

	got, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://sessions/{session}/windows",
		},
		Argument: sdk.CompleteParamsArgument{Name: "session", Value: "spaced"},
	})
	if err != nil {
		t.Fatalf("complete a session: %v", err)
	}
	if !slices.Contains(got.Completion.Values, "spaced%20name") {
		t.Fatalf("offered %v, want the name escaped for a path", got.Completion.Values)
	}

	// The value a client was handed has to build a URI that reads.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://sessions/" + got.Completion.Values[0] + "/windows",
	})
	if err != nil {
		t.Fatalf("read the URI a completion built: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "only") {
		t.Errorf("the URI a completion built selects no windows: %+v", read.Contents)
	}
}

//libtmux:real-tmux
func TestCompletionsNarrowToWhatIsAlreadyChosen(t *testing.T) {
	session, _, ctx := connect(t)
	for _, name := range []string{"alpha", "bravo"} {
		call(ctx, t, session, "build_workspace", map[string]any{
			"document": "session_name: " + name + "\nwindows:\n  - window_name: only\n" +
				"    panes:\n      - shell: sleep 300\n",
		}, nil)
	}

	all, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://windows/{window}/panes",
		},
		Argument: sdk.CompleteParamsArgument{Name: "window", Value: ""},
	})
	if err != nil {
		t.Fatalf("complete windows: %v", err)
	}
	narrowed, err := session.Complete(ctx, &sdk.CompleteParams{
		Ref: &sdk.CompleteReference{
			Type: "ref/resource", URI: "tmux://windows/{window}/panes",
		},
		Argument: sdk.CompleteParamsArgument{Name: "window", Value: ""},
		Context:  &sdk.CompleteContext{Arguments: map[string]string{"session": "alpha"}},
	})
	if err != nil {
		t.Fatalf("complete windows within a session: %v", err)
	}
	if len(narrowed.Completion.Values) >= len(all.Completion.Values) {
		t.Errorf("naming a session offered %d windows, not fewer than %d",
			len(narrowed.Completion.Values), len(all.Completion.Values))
	}
}
