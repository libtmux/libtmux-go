package mcp

import (
	"context"
	"fmt"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompts name the jobs this server is for.
//
// Tools are verbs and resources are nouns; neither says what a person actually
// wants done. A prompt is the job with its method attached, invoked by name
// from a client's own menu, so someone who has never read a tool list can ask
// for the thing they want and the model is told which tools do it and in what
// order.
//
// These are deliberately few. A prompt for every tool would be a second, worse
// tool list; these are the tasks that take several tools in a particular order,
// which is exactly what a tool list cannot express.
func addPrompts(server *mcp.Server, level SafetyLevel) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "diagnose_pane",
		Title:       "Diagnose a Pane",
		Description: "Work out what a pane is doing and why it is stuck or failing.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "pane",
			Description: "The pane id to look at, such as %1. Omit to be told how to find it.",
		}},
	}, diagnosePane)

	server.AddPrompt(&mcp.Prompt{
		Name:        "watch_pane",
		Title:       "Watch a Pane",
		Description: "Follow a pane across several turns without re-reading what you have already read.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "pane",
			Description: "The pane id to follow, such as %1. Omit to be told how to find it.",
		}},
	}, followPane)

	// Getting a pane back changes tmux, so a read-only server should not offer
	// a recipe it cannot carry out.
	if level != SafetyReadOnly {
		server.AddPrompt(&mcp.Prompt{
			Name:        "recover_pane",
			Title:       "Recover a Stuck Pane",
			Description: "Get back a pane that has stopped answering, and work out why it stopped.",
			Arguments: []*mcp.PromptArgument{{
				Name:        "pane",
				Description: "The pane that is not answering, such as %1. Omit to be told how to find it.",
			}},
		}, recoverPane)
	}

	// Setting a workspace up changes tmux, so a server offering nothing that
	// changes tmux should not suggest it.
	if level != SafetyReadOnly {
		server.AddPrompt(&mcp.Prompt{
			Name:        "set_up_workspace",
			Title:       "Set Up a Workspace",
			Description: "Lay out a session for a piece of work, and start what runs in it.",
			Arguments: []*mcp.PromptArgument{{
				Name:        "task",
				Description: "What the workspace is for, such as \"the api and its tests\".",
			}},
		}, setUpWorkspace)
	}
}

// diagnosePane tells the model how to find out what a pane is doing, in the
// order that avoids the traps the tools are documented to have.
func diagnosePane(
	_ context.Context,
	request *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	pane := request.Params.Arguments["pane"]
	var text strings.Builder
	if pane == "" {
		text.WriteString("Find the pane first: list_panes reports every pane with what it " +
			"is running, and the one marked isCaller is this server's own. ")
	} else {
		fmt.Fprintf(&text, "Look at pane %s. ", pane)
	}
	text.WriteString(`Then:

1. get_pane_info first. It says whether the process has exited and with what
   status, and whether the pane is in copy mode, which swallows keys you send
   and is a common reason a pane looks unresponsive.
2. snapshot_pane for its contents and that state together. Add includeHistory
   if what went wrong has already scrolled off.
3. If it is running something and you need to know when that ends, do not read
   the pane in a loop: wait_for_text watches what the pane writes, and stop
   with markers like "error:" returns as soon as it fails rather than at the
   deadline.
4. If the pane is at a prompt, run_command runs something and reports the exit
   status and the output without reading the screen at all.
5. If the contents look fine and the behaviour does not, it is a setting:
   show_option for history-limit or remain-on-exit, show_hooks for something
   tmux is doing on its own.
6. find_pane_by_position tells you what is beside it, if the work spans panes.

A pane holding a command that never returned makes every later run_command
there time out; send_keys with "C-c" gets it back. A pane left in copy mode
needs exit_copy_mode.`)
	return &mcp.GetPromptResult{
		Description: "How to find out what a pane is doing",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: text.String()},
		}},
	}, nil
}

// followPane tells the model how to follow a pane over several turns without
// paying for the same screen each time, which is the loop it will otherwise
// invent out of capture_pane.
func followPane(
	_ context.Context,
	request *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	pane := request.Params.Arguments["pane"]
	var text strings.Builder
	if pane == "" {
		text.WriteString("Find the pane first with list_panes. ")
	} else {
		fmt.Fprintf(&text, "Follow pane %s. ", pane)
	}
	text.WriteString(`Then:

1. capture_since with no cursor. You get what the pane shows now and a cursor.
2. Keep the cursor. Every later turn, call capture_since with it and you get
   only what the pane wrote since, plus a fresh cursor to keep instead.
3. A reply with linesMissed true means tmux discarded scrollback between the
   two readings, so what came back is the current screen rather than everything
   since. Your record of the pane has a gap in it.
4. If the pane produces more than its scrollback holds, pipe_pane writes every
   byte to a file as it happens and nothing depends on reading in time.

Do not call capture_pane in a loop instead. It returns the whole screen every
time, most of which you read last turn, and it cannot tell you whether anything
changed.

If what you are waiting for is a single thing rather than a stream, stop
watching and wait: wait_for_text returns when the pane says it.`)
	return &mcp.GetPromptResult{
		Description: "How to follow a pane across turns",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: text.String()},
		}},
	}, nil
}

// recoverPane tells the model how to get a pane back, and how to tell the two
// reasons it is stuck apart.
//
// This is the trap the tool descriptions name most often and the one a model
// is least likely to reason its way out of: a pane holding a command makes
// every later run_command there time out, and the symptom is identical to a
// command that is merely slow.
func recoverPane(
	_ context.Context,
	request *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	pane := request.Params.Arguments["pane"]
	var text strings.Builder
	if pane == "" {
		text.WriteString("Find the pane first: list_panes reports what each one is running. ")
	} else {
		fmt.Fprintf(&text, "Pane %s has stopped answering. ", pane)
	}
	text.WriteString(`A pane stops answering for two reasons, and they look the
same from outside. Tell them apart before doing anything:

1. get_pane_info. Read currentCommand and inMode.
2. currentCommand is a shell — the command is still running and the wait was
   too short. Nothing is stuck. Run it again with a longer timeoutSeconds, or
   watch it with wait_for_text instead of waiting on it.
3. currentCommand is anything else — the pane is busy, and it read your command
   as that program's input rather than running it. send_keys with "C-c" to
   interrupt, then check again.
4. inMode is true — the pane is in tmux's copy mode, where keys are read by
   tmux and never reach the shell, so nothing you sent arrived at all.
   exit_copy_mode, then re-send.

A run_command that timed out reports what the pane was running as "running",
which answers step 2 without a second call.

Do not send "C-c" first and ask afterwards. Interrupting a command that was
merely slow throws away work that was about to finish.`)
	return &mcp.GetPromptResult{
		Description: "How to get a pane back that stopped answering",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: text.String()},
		}},
	}, nil
}

// setUpWorkspace tells the model which door to use, since the cheap one and
// the thorough one are easy to confuse.
func setUpWorkspace(
	_ context.Context,
	request *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	task := request.Params.Arguments["task"]
	if task == "" {
		task = "the work at hand"
	}
	text := fmt.Sprintf(`Set up a tmux workspace for %s.

Choose the door by size:

- One more window or pane in something that exists: create_window, or
  split_window with a side and a percentage. Both return the id to address next.
- A whole session laid out at once: build_workspace takes a tmuxp-style YAML
  document with windows, panes, layouts, and the commands each pane runs. It is
  one call for the lot and it names what it built.

Then start what runs in each pane with run_command, which waits for the command
and reports its exit status and its output, and pass suppressHistory so the
person whose shell this is does not find your commands in their history. If a
pane runs something long-lived, wait_for_text until it announces itself rather
than reading the pane in a loop.

Finish by making it readable: select_layout to arrange the panes,
set_pane_title to label which is which, rename_window to name the window after
the work, and select_window so the person is looking at it. A batch does the
lot in one request.`, task)
	return &mcp.GetPromptResult{
		Description: "How to lay out a session for a piece of work",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: text},
		}},
	}, nil
}
