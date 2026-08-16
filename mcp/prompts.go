package mcp

import (
	"context"
	"fmt"
	"os"
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

// RecipeToolEnvironmentVariable names the variable that also offers the
// recipes as a tool, for clients that do not speak the prompts protocol. It
// matches the Python server so an operator configuring both writes one thing.
const RecipeToolEnvironmentVariable = "LIBTMUX_MCP_PROMPTS_AS_TOOLS"

// recipe is one job, with the method that does it.
//
// The same text answers a prompt and the tool below, because a client that
// cannot read prompts should not be told something different from one that
// can. Writing it twice is how the two would come to disagree.
type recipe struct {
	name        string
	title       string
	description string
	// argument is what the job needs to know, and what a client completes.
	argument string
	// argumentHelp describes it in a client's own picker.
	argumentHelp string
	// mutates marks a recipe that tells the model to change tmux, which a
	// read-only server should not offer: it would be advice it cannot carry
	// out.
	mutates bool
	// build writes the recipe for one argument, and says what it is.
	build func(argument string) (summary, text string)
}

// recipes are the jobs worth naming.
var recipes = []recipe{
	{
		name:         "diagnose_pane",
		title:        "Diagnose a Pane",
		description:  "Work out what a pane is doing and why it is stuck or failing.",
		argument:     "pane",
		argumentHelp: "The pane id to look at, such as %1. Omit to be told how to find it.",
		build:        diagnosePaneText,
	},
	{
		name:  "watch_pane",
		title: "Watch a Pane",
		description: "Follow a pane across several turns without re-reading what " +
			"you have already read.",
		argument:     "pane",
		argumentHelp: "The pane id to follow, such as %1. Omit to be told how to find it.",
		build:        followPaneText,
	},
	{
		name:         "recover_pane",
		title:        "Recover a Stuck Pane",
		description:  "Get back a pane that has stopped answering, and work out why it stopped.",
		argument:     "pane",
		argumentHelp: "The pane that is not answering, such as %1. Omit to be told how to find it.",
		mutates:      true,
		build:        recoverPaneText,
	},
	{
		name:         "set_up_workspace",
		title:        "Set Up a Workspace",
		description:  "Lay out a session for a piece of work, and start what runs in it.",
		argument:     "task",
		argumentHelp: "What the workspace is for, such as \"the api and its tests\".",
		mutates:      true,
		build:        setUpWorkspaceText,
	},
}

// addPrompts advertises the recipes through the prompts protocol.
func addPrompts(server *mcp.Server, level SafetyLevel) {
	for _, offered := range recipes {
		if offered.mutates && level == SafetyReadOnly {
			continue
		}
		server.AddPrompt(&mcp.Prompt{
			Name:        offered.name,
			Title:       offered.title,
			Description: offered.description,
			Arguments: []*mcp.PromptArgument{{
				Name:        offered.argument,
				Description: offered.argumentHelp,
			}},
		}, promptFor(offered))
	}
}

// promptFor answers one prompt from the recipe behind it.
func promptFor(offered recipe) mcp.PromptHandler {
	return func(
		_ context.Context,
		request *mcp.GetPromptRequest,
	) (*mcp.GetPromptResult, error) {
		summary, text := offered.build(request.Params.Arguments[offered.argument])
		return &mcp.GetPromptResult{
			Description: summary,
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: text},
			}},
		}, nil
	}
}

// The recipes are worth reaching even from a client that cannot read prompts.
//
// Most of what is written here about which tool to use and in what order is in
// the prompts, and a client that does not implement the prompts protocol shows
// a model none of it. Offering them as one tool puts the same text somewhere
// every client can reach.
//
// It is off unless an operator asks for it, because a server that offers both
// is offering the same four things twice, and the tool list is the expensive
// place to say anything. One tool rather than the two a mirror of the prompts
// protocol would need: the names are few enough to list in its own
// description, so choosing one costs no call of its own.

// getRecipeInput names the job to be told how to do.
type getRecipeInput struct {
	// Name is the recipe wanted.
	Name string `json:"name" jsonschema:"which recipe to read: diagnose_pane, watch_pane, recover_pane, or set_up_workspace"`
	// Argument is the pane or task it is about, and may be omitted to be told
	// how to find it.
	Argument string `json:"argument,omitempty" jsonschema:"the pane id the recipe is about, such as %1, or for set_up_workspace what the workspace is for"`
}

// getRecipeOutput carries the recipe.
type getRecipeOutput struct {
	// Name is the recipe that was read.
	Name string `json:"name"`
	// Summary says what it is for.
	Summary string `json:"summary"`
	// Steps is the recipe itself, as text meant to be followed.
	Steps string `json:"steps"`
}

// getRecipe answers with one recipe's text.
func (t *tools) getRecipe(
	_ context.Context,
	_ *mcp.CallToolRequest,
	input getRecipeInput,
) (*mcp.CallToolResult, getRecipeOutput, error) {
	for _, offered := range recipes {
		if offered.name != input.Name {
			continue
		}
		if offered.mutates && t.level == SafetyReadOnly {
			return nil, getRecipeOutput{}, fmt.Errorf(
				"%s tells you to change tmux, which this server is not allowed to do",
				offered.name)
		}
		summary, text := offered.build(input.Argument)
		return nil, getRecipeOutput{
			Name: offered.name, Summary: summary, Steps: text,
		}, nil
	}
	return nil, getRecipeOutput{}, fmt.Errorf(
		"%q is not a recipe; the recipes are %s", input.Name, recipeNames())
}

// recipeNames lists what may be asked for, for an error a caller can act on.
func recipeNames() string {
	names := make([]string, 0, len(recipes))
	for _, offered := range recipes {
		names = append(names, offered.name)
	}
	return strings.Join(names, ", ")
}

// addRecipeTools advertises the recipes as a tool when an operator asked.
func addRecipeTools(server *mcp.Server, t *tools) {
	if os.Getenv(RecipeToolEnvironmentVariable) != "1" {
		return
	}
	register(server, t, &mcp.Tool{
		Name:        "get_recipe",
		Annotations: readOnly("Read a tmux Recipe"),
		Description: "How to do one of the jobs this server is for, in the order " +
			"that avoids the traps its tools have: diagnose_pane (what a pane is " +
			"doing and why it is stuck), watch_pane (follow one across turns " +
			"without re-reading it), recover_pane (get back one that stopped " +
			"answering), set_up_workspace (lay out a session for a piece of " +
			"work). The same text this server offers as MCP prompts.",
	}, t.getRecipe)
}

// diagnosePaneText tells the model how to find out what a pane is doing, in
// the order that avoids the traps the tools are documented to have.
func diagnosePaneText(pane string) (summary, text string) {
	var body strings.Builder
	if pane == "" {
		body.WriteString("Find the pane first: list_panes reports every pane with what it " +
			"is running, and the one marked isCaller is this server's own. ")
	} else {
		fmt.Fprintf(&body, "Look at pane %s. ", pane)
	}
	body.WriteString(`Then:

1. get_pane_info first. It says whether the process has exited and with what
   status, and whether the pane is in copy mode, which swallows keys you send
   and is a common reason a pane looks unresponsive.
2. snapshot_pane for its contents and that state together. Add includeHistory
   if what went wrong has already scrolled off.
3. If it is running something and you need to know when that ends, do not read
   the pane in a loop: wait_for_text watches what the pane writes, and stop
   with markers like "error:" returns as soon as it fails rather than at the
   deadline. If you cannot predict what finishing prints, idleSeconds returns
   when the pane goes quiet.
4. If the pane is at a prompt, run_command runs something and reports the exit
   status and the output without reading the screen at all.
5. If the contents look fine and the behaviour does not, it is a setting:
   show_option for history-limit or remain-on-exit, show_hooks with a name for
   something tmux is doing on its own. get_server_info with includeMessages is
   tmux's own log of what it refused.
6. find_pane_by_position tells you what is beside it, if the work spans panes.
7. If the program says whether it passed by colouring a word rather than by
   saying so, capture_pane with styles keeps the colour a capture strips.

A pane holding a command that never returned makes every later run_command
there time out; send_keys with "C-c" gets it back. A pane left in copy mode
needs exit_copy_mode.`)
	return "How to find out what a pane is doing", body.String()
}

// followPaneText tells the model how to follow a pane over several turns
// without paying for the same screen each time, which is the loop it will
// otherwise invent out of capture_pane.
func followPaneText(pane string) (summary, text string) {
	var body strings.Builder
	if pane == "" {
		body.WriteString("Find the pane first with list_panes. ")
	} else {
		fmt.Fprintf(&body, "Follow pane %s. ", pane)
	}
	body.WriteString(`Then:

1. capture_since with no cursor. You get what the pane shows now and a cursor.
2. Keep the cursor. Every later turn, call capture_since with it and you get
   only what the pane wrote since, plus a fresh cursor to keep instead.
3. A reply with linesMissed true means tmux discarded scrollback between the
   two readings, so what came back is the current screen rather than everything
   since. Your record of the pane has a gap in it.
4. If the pane produces more than its scrollback holds, pipe_pane writes every
   byte to a file as it happens and nothing depends on reading in time.

Watching several panes at once is a listing rather than a capture each:
list_panes with detail full reports every pane's history size, whether its
process has exited and with what status, without reading any of their
contents. Compare the history sizes against the last reading to see which
panes wrote anything, and capture only those.

Do not call capture_pane in a loop instead. It returns the whole screen every
time, most of which you read last turn, and it cannot tell you whether anything
changed.

If what you are waiting for is a single thing rather than a stream, stop
watching and wait: wait_for_text returns when the pane says it. If the pane is
running something you started, run_command with detach gives you a jobId and
get_job collects the exit status when you are ready for it.`)
	return "How to follow a pane across turns", body.String()
}

// recoverPaneText tells the model how to get a pane back, and how to tell the
// two reasons it is stuck apart.
//
// This is the trap the tool descriptions name most often and the one a model
// is least likely to reason its way out of: a pane holding a command makes
// every later run_command there time out, and the symptom is identical to a
// command that is merely slow.
func recoverPaneText(pane string) (summary, text string) {
	var body strings.Builder
	if pane == "" {
		body.WriteString("Find the pane first: list_panes reports what each one is running. ")
	} else {
		fmt.Fprintf(&body, "Pane %s has stopped answering. ", pane)
	}
	body.WriteString(`A pane stops answering for two reasons, and they look the
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
which answers step 2 without a second call. Every wait also reports
effectiveTimeoutSeconds, which is the wait it really ran: a timeout you asked
for above this server's ceiling was shortened rather than refused.

Do not send "C-c" first and ask afterwards. Interrupting a command that was
merely slow throws away work that was about to finish.`)
	return "How to get a pane back that stopped answering", body.String()
}

// setUpWorkspaceText tells the model which door to use, since the cheap one
// and the thorough one are easy to confuse.
func setUpWorkspaceText(task string) (summary, text string) {
	if task == "" {
		task = "the work at hand"
	}
	body := fmt.Sprintf(`Set up a tmux workspace for %s.

Choose the door by size:

- One more window or pane in something that exists: create_window, or
  split_window with a side and a percentage. Both return the id to address next.
- A whole session laid out at once: build_workspace takes a tmuxp-style YAML
  document with windows, panes, layouts, and the commands each pane runs. It is
  one call for the lot and it names what it built.
- A pane that is already running the right thing but in the wrong place:
  move_pane, which keeps the pane and whatever is in it.

Then start what runs in each pane with run_command, which waits for the command
and reports its exit status and its output, and pass suppressHistory so the
person whose shell this is does not find your commands in their history. For
something long, such as a build, pass detach as well: you get a jobId at once
and get_job collects the answer when you want it. If a pane runs something
long-lived, wait_for_text until it announces itself rather than reading the
pane in a loop.

Finish by making it readable: select_layout to arrange the panes,
set_pane_title to label which is which, rename_window to name the window after
the work, and select_window so the person is looking at it. Check
get_server_info first for whether anyone is attached: moving what a person is
looking at is a bigger change than making a window. A batch does the lot in one
request.`, task)
	return "How to lay out a session for a piece of work", body
}
