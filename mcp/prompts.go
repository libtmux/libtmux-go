package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromptMetaKey is the MCP metadata key carrying the complete prompt row that
// also drives registration and tmux://capabilities.
const PromptMetaKey = "com.git-pull.libtmux-mcp/prompt"

// A prompt is text a person picks; it calls nothing and grants no authority.
// It is listed only when every tool its text names is in the startup
// selection, so it never points at a tool the client cannot call.

type promptArgument struct {
	name        string
	description string
	required    bool
	// valid, when set, checks a nonempty value.
	valid func(string) error
}

type promptDefinition struct {
	name        string
	title       string
	description string
	arguments   []promptArgument
	// requires lists every tool the rendered text names.
	requires []string
	render   func(arguments map[string]string) string
}

var authoritativePromptManifest = mustBuildPromptManifest()

func promptManifest() []promptDefinition {
	return slices.Clone(authoritativePromptManifest)
}

func mustBuildPromptManifest() []promptDefinition {
	definitions := buildPromptManifest()
	if err := validatePromptManifest(definitions, authoritativeToolManifest); err != nil {
		panic(fmt.Sprintf("invalid MCP prompt manifest: %v", err))
	}
	return definitions
}

func validatePromptManifest(prompts []promptDefinition, definitions []toolDefinition) error {
	tools := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		tools[definition.name] = true
	}
	names := map[string]bool{}
	for _, prompt := range prompts {
		if prompt.name == "" || prompt.title == "" || prompt.description == "" || prompt.render == nil {
			return errors.New("prompt manifest contains blank identity text or no renderer")
		}
		if names[prompt.name] {
			return fmt.Errorf("duplicate prompt %q", prompt.name)
		}
		names[prompt.name] = true
		if len(prompt.requires) == 0 {
			return fmt.Errorf("prompt %s names no tool", prompt.name)
		}
		for _, name := range prompt.requires {
			if !tools[name] {
				return fmt.Errorf("prompt %s requires unknown tool %q", prompt.name, name)
			}
		}
		arguments := map[string]bool{}
		for _, argument := range prompt.arguments {
			if argument.name == "" || argument.description == "" || arguments[argument.name] {
				return fmt.Errorf("prompt %s has a blank or duplicate argument %q", prompt.name, argument.name)
			}
			arguments[argument.name] = true
		}
	}
	return nil
}

func positiveSeconds(value string) error {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return fmt.Errorf("%q is not a positive number of seconds", value)
	}
	return nil
}

func buildPromptManifest() []promptDefinition {
	return []promptDefinition{
		{
			name:        "run_and_wait",
			title:       "Run a command and wait",
			description: "Run one shell command in a pane, wait for it to finish, and read its exit status.",
			arguments: []promptArgument{
				{"command", "the shell command to run", true, nil},
				{"pane_id", "the target pane, such as %1", true, nil},
				{"timeout", "seconds to wait for completion; defaults to 60", false, positiveSeconds},
			},
			requires: []string{"run_shell_command"},
			render:   renderRunAndWait,
		},
		{
			name:        "diagnose_failing_pane",
			title:       "Diagnose a failing pane",
			description: "Gather pane context and propose a root-cause hypothesis without running anything.",
			arguments: []promptArgument{
				{"pane_id", "the pane to diagnose, such as %1", true, nil},
			},
			requires: []string{"snapshot_pane", "capture_since"},
			render:   renderDiagnose,
		},
		{
			name:        "interrupt_gracefully",
			title:       "Interrupt a command gracefully",
			description: "Send C-c to a pane and verify the shell prompt came back.",
			arguments: []promptArgument{
				{"pane_id", "the target pane, such as %1", true, nil},
			},
			requires: []string{"get_pane_info", "send_keys", "wait_for_text"},
			render:   renderInterrupt,
		},
		{
			name:        "build_dev_workspace",
			title:       "Build a development workspace",
			description: "Create a three-pane session: editor on top, shell bottom-left, logs bottom-right.",
			arguments: []promptArgument{
				{"session_name", "name for the new session", true, nil},
				{"log_command", "command for the logs pane; defaults to watch -n 1 date", false, nil},
			},
			requires: []string{"create_session", "list_panes", "split_window", "send_keys"},
			render:   renderDevWorkspace,
		},
	}
}

// promptsFor returns the prompts whose required tools are all selected.
func (surface toolSurface) promptsFor() []promptDefinition {
	return slices.DeleteFunc(promptManifest(), func(prompt promptDefinition) bool {
		for _, name := range prompt.requires {
			if _, ok := surface.byName[name]; !ok {
				return true
			}
		}
		return false
	})
}

func (prompt promptDefinition) capability() map[string]any {
	arguments := make([]map[string]any, 0, len(prompt.arguments))
	for _, argument := range prompt.arguments {
		arguments = append(arguments, map[string]any{
			"name": argument.name, "description": argument.description, "required": argument.required,
		})
	}
	return map[string]any{
		"name":          prompt.name,
		"title":         prompt.title,
		"description":   prompt.description,
		"arguments":     arguments,
		"requiresTools": slices.Clone(prompt.requires),
	}
}

func (prompt promptDefinition) sdkPrompt() *sdk.Prompt {
	arguments := make([]*sdk.PromptArgument, 0, len(prompt.arguments))
	for _, argument := range prompt.arguments {
		arguments = append(arguments, &sdk.PromptArgument{
			Name: argument.name, Description: argument.description, Required: argument.required,
		})
	}
	return &sdk.Prompt{
		Name: prompt.name, Title: prompt.title, Description: prompt.description,
		Arguments: arguments,
		Meta:      sdk.Meta{PromptMetaKey: prompt.capability()},
	}
}

// check refuses an argument the prompt does not declare, a missing required
// one, and a value its validator rejects, all as invalid params.
func (prompt promptDefinition) check(given map[string]string) error {
	invalid := func(format string, args ...any) error {
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: fmt.Sprintf("prompt %s: ", prompt.name) + fmt.Sprintf(format, args...),
		}
	}
	for name := range given {
		if !slices.ContainsFunc(prompt.arguments, func(argument promptArgument) bool { return argument.name == name }) {
			return invalid("unknown argument %s", name)
		}
	}
	for _, argument := range prompt.arguments {
		value := strings.TrimSpace(given[argument.name])
		switch {
		case value == "" && argument.required:
			return invalid("argument %s is required", argument.name)
		case value != "" && argument.valid != nil:
			if err := argument.valid(value); err != nil {
				return invalid("argument %s: %v", argument.name, err)
			}
		}
	}
	return nil
}

func addPrompts(server *sdk.Server, t *tools) {
	for _, definition := range t.surface.promptsFor() {
		server.AddPrompt(definition.sdkPrompt(), func(
			_ context.Context, request *sdk.GetPromptRequest,
		) (*sdk.GetPromptResult, error) {
			given := request.Params.Arguments
			if err := definition.check(given); err != nil {
				return nil, err
			}
			return &sdk.GetPromptResult{
				Description: definition.description,
				Messages: []*sdk.PromptMessage{{
					Role:    "user",
					Content: &sdk.TextContent{Text: definition.render(given)},
				}},
			}, nil
		})
	}
}

func (surface toolSurface) promptReport() []map[string]any {
	rows := []map[string]any{}
	for _, prompt := range surface.promptsFor() {
		rows = append(rows, prompt.capability())
	}
	return rows
}

// AdvertisedPrompts returns the prompts a server started now would list, from
// the same startup selection as [AdvertisedTools]. It starts nothing.
func AdvertisedPrompts() ([]*sdk.Prompt, error) {
	surface, err := resolveToolSurface(unknownSocketProfile())
	if err != nil {
		return nil, err
	}
	prompts := []*sdk.Prompt{}
	for _, definition := range surface.promptsFor() {
		prompts = append(prompts, definition.sdkPrompt())
	}
	return prompts, nil
}

func renderRunAndWait(arguments map[string]string) string {
	timeout := "60"
	if value := strings.TrimSpace(arguments["timeout"]); value != "" {
		timeout = value
	}
	command := arguments["command"]
	history := ""
	if strings.ContainsAny(command, "\r\n") {
		history = ", suppress_history=false"
	}
	return fmt.Sprintf(`Run this shell command in tmux pane %[1]s, wait until it finishes, and read the typed result:

run_shell_command(pane_id=%[1]q, command=%[2]q, timeout=%[3]s, max_lines=100%[4]s)

Use exit_status, timed_out, and output to decide what happened. Do not poll with repeated keystrokes and captures for a command you wrote: the call already waits for completion and returns the last lines of output.

timeout is capped by the server's wait ceiling, so read the effective timeout in the result rather than assuming the value above was honoured. For longer work, call again; the command keeps running in the pane between calls.
`, arguments["pane_id"], command, timeout, history)
}

func renderDiagnose(arguments map[string]string) string {
	pane := arguments["pane_id"]
	return fmt.Sprintf(`Something went wrong in tmux pane %[1]s. Diagnose it:

1. Call snapshot_pane(pane_id=%[1]q) for content, cursor, and pane state in one call.
2. If the content looks truncated, call it again with a larger max_lines.
3. To watch the pane across turns, call capture_since(pane_id=%[1]q), keep the returned cursor, and pass it to later capture_since calls.
4. Identify the last command that ran (the prompt line and the line above it) and the last non-empty output line.
5. Propose a root-cause hypothesis and a minimal command to verify it. Do not execute anything yet: produce the plan first.
`, pane)
}

func renderInterrupt(arguments map[string]string) string {
	pane := arguments["pane_id"]
	return fmt.Sprintf(`Interrupt whatever is running in pane %[1]s and verify that control returns to the shell:

1. get_pane_info(pane_id=%[1]q): note pane.currentCommand and whether the pane is in a mode. If a person owns the mode, stop and ask instead of sending keys.
2. send_keys(pane_id=%[1]q, keys=["C-c"]): tmux reads C-c as a key name and the terminal turns it into SIGINT.
3. wait_for_text(pane_id=%[1]q, patterns=["\\$", "#", "%%", ">"], regex=true, timeout=5): waits for a common prompt glyph. Adjust the patterns to the user's shell. Do not wait for "^C": the terminal echoes it whenever SIGINT is delivered, whether or not the process exits.
4. get_pane_info(pane_id=%[1]q) again. If pane.currentCommand is back to a shell, the interrupt worked; trust this over the wait outcome, which can time out on a prompt glyph you did not predict.
5. If the command is unchanged, the process is ignoring SIGINT. Stop and ask the caller how to proceed. Do not escalate to C-\ (SIGQUIT) or kill on your own.
`, pane)
}

func renderDevWorkspace(arguments map[string]string) string {
	name := arguments["session_name"]
	logs := strings.TrimSpace(arguments["log_command"])
	if logs == "" {
		logs = "watch -n 1 date"
	}
	return fmt.Sprintf(`Set up a three-pane development workspace named %[1]q: editor on top, a shell bottom-left, logs bottom-right.

1. create_session(session_name=%[1]q). It returns the session id but not a pane id.
2. list_panes(): find the one row whose session is %[1]q and call its id %%A (the editor).
3. split_window(pane_id=%%A, direction="below"): the new pane_id is %%B (the shell).
4. split_window(pane_id=%%B, direction="right"): the new pane_id is %%C (the logs).
5. send_keys(pane_id=%%A, keys=["vim"], enter=true) and send_keys(pane_id=%%C, keys=[%[2]q], literal=true, enter=true). Leave %%B at its prompt. tmux buffers keystrokes into the pty, so no wait is needed after split_window.

Target every later call by pane id (%%N); ids survive layout changes and window renames do not.
`, name, logs)
}
