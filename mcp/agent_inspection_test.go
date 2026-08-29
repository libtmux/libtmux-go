package mcp_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestOptionsAreReadAndWrittenAtTheScopeAsked(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: settings\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	result := call(ctx, t, session, "set_option", map[string]any{
		"paneId": pane, "name": "remain-on-exit", "value": "on",
	}, nil)
	if result.IsError {
		t.Fatalf("set_option: %#v", result.Content)
	}

	var read struct {
		Value string `json:"value"`
		Set   bool   `json:"set"`
		Scope string `json:"scope"`
	}
	call(ctx, t, session, "show_option", map[string]any{
		"paneId": pane, "name": "remain-on-exit",
	}, &read)
	if read.Value != "on" || !read.Set {
		t.Errorf("read back %q set=%v, want on and set", read.Value, read.Set)
	}
	if read.Scope != "pane" {
		t.Errorf("scope = %q, want pane by default", read.Scope)
	}

	bad := call(ctx, t, session, "show_option", map[string]any{
		"name": "remain-on-exit", "scope": "galaxy",
	}, nil)
	if !bad.IsError {
		t.Error("an unknown scope was accepted")
	}
}

//libtmux:real-tmux
func TestTheServerSaysWhichSocketItAddresses(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: identity\nwindows:\n  - panes:\n      - {}\n")

	var info struct {
		SocketPath       string `json:"socketPath"`
		Alive            bool   `json:"alive"`
		Sessions         int    `json:"sessions"`
		SafetyLevel      string `json:"safetyLevel"`
		InsideThisServer bool   `json:"insideThisServer"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if !info.Alive || info.Sessions == 0 {
		t.Fatalf("the server reports itself as %+v", info)
	}
	if info.SocketPath == "" {
		t.Error("the server did not say which socket it addresses")
	}
	if info.SafetyLevel == "" {
		t.Error("the server did not say what the operator allowed")
	}
	// The test's tmux server is not the one this process runs in, whether or
	// not the process is inside tmux at all.
	if info.InsideThisServer {
		t.Errorf("the server claims to be running inside its own target %q", info.SocketPath)
	}

	var servers struct {
		Servers []struct {
			SocketPath string `json:"socketPath"`
			IsTarget   bool   `json:"isTarget"`
			Alive      bool   `json:"alive"`
		} `json:"servers"`
		SearchedIn string `json:"searchedIn"`
	}
	call(ctx, t, session, "list_servers", map[string]any{}, &servers)
	if servers.SearchedIn == "" {
		t.Error("list_servers did not say where it looked")
	}
	// The test server's socket is in the test's own directory rather than
	// tmux's, so it is not expected in the listing; what must hold is that
	// nothing else is marked as the target.
	for _, found := range servers.Servers {
		if found.IsTarget && found.SocketPath != target.SocketPath() {
			t.Errorf("%q is marked as the target, which is %q",
				found.SocketPath, target.SocketPath())
		}
	}
}

//libtmux:real-tmux
func TestTheEnvironmentIsWhatANewPaneWouldGet(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: environ\nwindows:\n  - panes:\n      - {}\n")

	result := call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE", "value": "set-by-the-test",
	}, nil)
	if result.IsError {
		t.Fatalf("set_environment: %#v", result.Content)
	}

	var read struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE",
	}, &read)
	if len(read.Variables) != 1 || read.Variables[0].Value != "set-by-the-test" {
		t.Fatalf("read back %+v", read.Variables)
	}

	call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE", "unset": true,
	}, nil)
	var gone struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_PROBE",
	}, &gone)
	if len(gone.Variables) != 0 {
		t.Errorf("an unset variable was still reported: %+v", gone.Variables)
	}

	// tmux keeps two layers and a new pane inherits both, the session's
	// overriding the server's. Reading only one of them answers a question
	// nobody asked: PATH lives in the server's, so a caller asking what a pane
	// will get was told it gets no PATH.
	if _, err := target.Cmd(ctx, "set-environment", "-g",
		"LIBTMUX_MCP_GLOBAL", "from-the-server"); err != nil {
		t.Fatalf("set a server-wide variable: %v", err)
	}
	if _, err := target.Cmd(ctx, "set-environment", "-g",
		"LIBTMUX_MCP_BOTH", "from-the-server"); err != nil {
		t.Fatalf("set a server-wide variable: %v", err)
	}
	call(ctx, t, session, "set_environment", map[string]any{
		"name": "LIBTMUX_MCP_BOTH", "value": "from-the-session",
	}, nil)

	var inherited struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Scope string `json:"scope"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_GLOBAL",
	}, &inherited)
	if len(inherited.Variables) != 1 ||
		inherited.Variables[0].Value != "from-the-server" {
		t.Errorf("a variable a new pane inherits from the server was not "+
			"reported: %+v", inherited.Variables)
	} else if inherited.Variables[0].Scope != "server" {
		t.Errorf("scope = %q, want server", inherited.Variables[0].Scope)
	}

	// The session's value is the one a pane gets, so it is the one reported.
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "LIBTMUX_MCP_BOTH",
	}, &inherited)
	if len(inherited.Variables) != 1 ||
		inherited.Variables[0].Value != "from-the-session" {
		t.Errorf("the session's value does not override the server's: %+v",
			inherited.Variables)
	} else if inherited.Variables[0].Scope != "session" {
		t.Errorf("scope = %q, want session", inherited.Variables[0].Scope)
	}

	// Listing everything covers both layers too.
	var all struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	call(ctx, t, session, "show_environment", map[string]any{}, &all)
	named := map[string]bool{}
	for _, variable := range all.Variables {
		named[variable.Name] = true
	}
	if !named["LIBTMUX_MCP_GLOBAL"] {
		t.Errorf("listing everything omits what the server holds: %d variables",
			len(all.Variables))
	}
}

// stringPointer is the address of a value, which several tmux requests take to
// tell an absent field from an empty one.
//

//libtmux:real-tmux
func TestTheInstructionsNameTheToolsTheyRecommend(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	initialized := session.InitializeResult()
	if initialized == nil || strings.TrimSpace(initialized.Instructions) == "" {
		t.Fatal("the server sent no instructions")
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	advertised := map[string]bool{}
	for _, tool := range listed.Tools {
		advertised[tool.Name] = true
	}

	// Every word in the instructions that looks like one of this server's tool
	// names has to be one, which catches a rename that updated the code and
	// not the text.
	for _, word := range strings.FieldsFunc(initialized.Instructions, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z')
	}) {
		if !strings.Contains(word, "_") || advertised[word] {
			continue
		}
		// Words that merely contain an underscore and are not tool names, such
		// as a tmux format, are not this test's business.
		if strings.HasPrefix(word, "pane_") || strings.HasPrefix(word, "window_") ||
			strings.HasPrefix(word, "session_") || strings.HasPrefix(word, "client_") {
			continue
		}
		t.Errorf("the instructions recommend %q, which is not advertised", word)
	}

	for _, prompt := range []string{
		"diagnose_pane", "watch_pane", "recover_pane", "set_up_workspace",
	} {
		result, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: prompt})
		if err != nil {
			t.Errorf("get prompt %s: %v", prompt, err)
			continue
		}
		if len(result.Messages) == 0 {
			t.Errorf("prompt %s produced no message", prompt)
		}
	}
}

//libtmux:real-tmux
func TestDisplayMessageAnswersInTmuxsOwnLanguage(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: formats\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	var answered struct {
		Value  string `json:"value"`
		PaneID string `json:"paneId"`
	}
	result := call(ctx, t, session, "display_message", map[string]any{
		"paneId": pane, "format": "#{session_name}/#{pane_id}",
	}, &answered)
	if result.IsError {
		t.Fatalf("display_message: %#v", result.Content)
	}
	if answered.Value != "formats/"+pane {
		t.Errorf("tmux answered %q, want %q", answered.Value, "formats/"+pane)
	}
	if answered.PaneID != pane {
		t.Errorf("evaluated against %q, want %q", answered.PaneID, pane)
	}

	empty := call(ctx, t, session, "display_message", map[string]any{"format": "  "}, nil)
	if !empty.IsError {
		t.Error("an empty format was accepted")
	}
}

//libtmux:real-tmux
func TestAnEmptyPaneIsStillAReadableResource(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: blank\nwindows:\n  - panes:\n      - {}\n")
	pane := firstPane(ctx, t, session)

	if result := call(ctx, t, session, "clear_pane", map[string]any{
		"paneId": pane, "history": true,
	}, nil); result.IsError {
		t.Fatalf("clear_pane: %#v", result.Content)
	}

	uri := "tmux://panes/" + strings.TrimPrefix(pane, "%") + "/content"
	result, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("read returned %d contents, want 1", len(result.Contents))
	}
	if result.Contents[0].Text == "" {
		t.Error("an empty pane produced contents with no text, which a client rejects")
	}
}

// listing is the shape the three list tools answer with, as a client reads it.
type listing struct {
	Total int `json:"total"`
	Panes []struct {
		ID       string `json:"id"`
		Session  string `json:"session"`
		WindowID string `json:"windowId"`
		Status   *struct {
			Dead         bool   `json:"dead"`
			Path         string `json:"path"`
			HistoryLines int    `json:"historyLines"`
		} `json:"status"`
	} `json:"panes"`
}

//libtmux:real-tmux
func TestAFilteredListingReturnsOnlyWhatMatched(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: filtering\nwindows:\n"+
		"  - window_name: one\n    panes:\n      - {}\n      - {}\n"+
		"  - window_name: two\n    panes:\n      - {}\n")

	var everything listing
	call(ctx, t, session, "list_panes", map[string]any{}, &everything)
	if everything.Total != 3 || len(everything.Panes) != 3 {
		t.Fatalf("an unfiltered listing gave %d of %d, want 3 of 3",
			len(everything.Panes), everything.Total)
	}

	var narrowed listing
	call(ctx, t, session, "list_panes", map[string]any{
		"windowId": everything.Panes[2].WindowID,
	}, &narrowed)
	if len(narrowed.Panes) != 1 {
		t.Errorf("filtering to one window gave %d panes, want 1", len(narrowed.Panes))
	}
	if narrowed.Total != 3 {
		t.Errorf("total = %d, want the 3 the filter selected from", narrowed.Total)
	}
}

//libtmux:real-tmux
func TestAFullListingCarriesStateWithoutASecondCall(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: stateful\nwindows:\n  - panes:\n      - {}\n")

	var standard listing
	call(ctx, t, session, "list_panes", map[string]any{}, &standard)
	if len(standard.Panes) != 1 || standard.Panes[0].Status != nil {
		t.Fatalf("a standard listing carried status: %+v", standard.Panes)
	}

	var full listing
	call(ctx, t, session, "list_panes", map[string]any{"detail": "full"}, &full)
	if len(full.Panes) != 1 || full.Panes[0].Status == nil {
		t.Fatalf("a full listing carried no status: %+v", full.Panes)
	}
	if full.Panes[0].Status.Path == "" {
		t.Error("a full listing reported no working directory")
	}
	if full.Panes[0].Status.Dead {
		t.Error("a live pane was reported dead")
	}
}

//libtmux:real-tmux
func TestAnUnknownDetailIsRefused(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: unknowable\nwindows:\n  - panes:\n      - {}\n")

	result := call(ctx, t, session, "list_panes", map[string]any{"detail": "verbose"}, nil)
	if !result.IsError {
		t.Fatal("an unknown detail level was accepted")
	}
}

//libtmux:real-tmux
func TestPathUnderDoesNotMatchASiblingPrefix(t *testing.T) {
	session, _, ctx := connect(t)
	root := t.TempDir()
	shortPath := filepath.Join(root, "wo")
	longPath := filepath.Join(root, "work")
	for _, directory := range []string{shortPath, longPath} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workspace(ctx, t, session, "session_name: paths\nwindows:\n  - panes:\n      - {}\n      - {}\n")

	var everything listing
	call(ctx, t, session, "list_panes", map[string]any{}, &everything)
	// send rather than run_command: run_command runs in a subshell, so a cd
	// inside it changes that subshell's directory and never the pane's. The
	// round trip afterwards is what proves the shell has finished the cd.
	send(ctx, t, session, everything.Panes[0].ID, "cd "+shortPath)
	send(ctx, t, session, everything.Panes[1].ID, "cd "+longPath)
	run(ctx, t, session, everything.Panes[0].ID, "true")
	run(ctx, t, session, everything.Panes[1].ID, "true")

	var under listing
	call(ctx, t, session, "list_panes", map[string]any{"pathUnder": shortPath}, &under)
	if len(under.Panes) != 1 {
		t.Fatalf("filtering under %s matched %d panes, want only the one in it",
			shortPath, len(under.Panes))
	}
	if under.Panes[0].ID != everything.Panes[0].ID {
		t.Errorf("matched %s, want %s", under.Panes[0].ID, everything.Panes[0].ID)
	}
}

//libtmux:real-tmux
func TestWindowsAndSessionsNarrowToo(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: narrowing\nwindows:\n"+
		"  - window_name: alpha\n    panes:\n      - {}\n"+
		"  - window_name: beta\n    panes:\n      - {}\n")
	workspace(ctx, t, session, "session_name: elsewhere\nwindows:\n"+
		"  - window_name: gamma\n    panes:\n      - {}\n")

	type windows struct {
		Total   int `json:"total"`
		Windows []struct {
			Name    string `json:"name"`
			Session string `json:"session"`
		} `json:"windows"`
	}
	var byName windows
	call(ctx, t, session, "list_windows", map[string]any{"name": "alph"}, &byName)
	if len(byName.Windows) != 1 || byName.Windows[0].Name != "alpha" {
		t.Errorf("filtering windows by name gave %+v", byName.Windows)
	}
	if byName.Total < 3 {
		t.Errorf("total = %d, want every window it selected from", byName.Total)
	}

	var bySession windows
	call(ctx, t, session, "list_windows", map[string]any{"sessionName": "narrowing"}, &bySession)
	if len(bySession.Windows) != 2 {
		t.Errorf("filtering windows to one session gave %d, want 2", len(bySession.Windows))
	}

	type sessions struct {
		Total    int `json:"total"`
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	var named sessions
	call(ctx, t, session, "list_sessions", map[string]any{"name": "elsew"}, &named)
	if len(named.Sessions) != 1 || named.Sessions[0].Name != "elsewhere" {
		t.Errorf("filtering sessions by name gave %+v", named.Sessions)
	}
	if named.Total < 2 {
		t.Errorf("total = %d, want every session it selected from", named.Total)
	}
}

//libtmux:real-tmux
func TestServerInfoTellsWhoIsWatching(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: watched\nwindows:\n  - panes:\n      - {}\n")

	sessions, err := target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("sessions: %v", err)
	}
	control, err := target.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("open a control client: %v", err)
	}
	defer func() { _ = control.Close() }()

	var info struct {
		Clients         int `json:"clients"`
		AttachedClients []struct {
			Name        string `json:"name"`
			Session     string `json:"session"`
			ControlMode bool   `json:"controlMode"`
		} `json:"attachedClients"`
	}
	call(ctx, t, session, "get_server_info", map[string]any{}, &info)
	if info.Clients != len(info.AttachedClients) {
		t.Errorf("counted %d clients but described %d",
			info.Clients, len(info.AttachedClients))
	}
	if len(info.AttachedClients) == 0 {
		t.Fatal("a server with a control client attached described none")
	}
	if !slices.ContainsFunc(info.AttachedClients, func(client struct {
		Name        string `json:"name"`
		Session     string `json:"session"`
		ControlMode bool   `json:"controlMode"`
	},
	) bool {
		return client.ControlMode
	}) {
		t.Errorf("a control-mode client was reported as a person watching: %+v",
			info.AttachedClients)
	}
}

//libtmux:real-tmux
func TestOneHookIsReadableWithoutTheTable(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: hooked\nwindows:\n  - panes:\n      - {}\n")
	// tmux reports every hook it knows, set or not, and show_hooks keeps only
	// the ones with a command. So the hook read back here has to be one this
	// test set, and one every supported tmux knows by that name.
	// Two, so that filtering to one has something to leave out. A single hook
	// would make the comparison below true whether or not the name was read.
	for _, event := range []string{"after-split-window", "after-new-window"} {
		if _, err := target.Cmd(ctx, "set-hook", "-g", event, "display-message hi"); err != nil {
			t.Fatalf("set a hook to read back: %v", err)
		}
	}

	type hooks struct {
		Hooks []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	var everything hooks
	call(ctx, t, session, "show_hooks", map[string]any{"scope": "server"}, &everything)
	if len(everything.Hooks) == 0 {
		t.Fatal("no hooks were reported at all")
	}

	var one hooks
	call(ctx, t, session, "show_hooks", map[string]any{
		"scope": "server", "name": "after-split-window",
	}, &one)
	if len(one.Hooks) == 0 {
		t.Fatal("naming a hook that is set reported none")
	}
	for _, reported := range one.Hooks {
		if !strings.HasPrefix(reported.Name, "after-split-window") {
			t.Errorf("naming after-split-window also reported %q", reported.Name)
		}
	}
	if len(everything.Hooks) < 2 {
		t.Fatalf("only %d hooks were set, so filtering proves nothing", len(everything.Hooks))
	}
	if len(one.Hooks) >= len(everything.Hooks) {
		t.Errorf("naming one hook returned %d of %d, so the name was not read",
			len(one.Hooks), len(everything.Hooks))
	}
}

//libtmux:real-tmux
func TestRecipesAreOffAsAToolUnlessAsked(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		session, _, ctx := connect(t)
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		for _, tool := range listed.Tools {
			if tool.Name == "get_recipe" {
				t.Error("the recipe tool was advertised without being asked for")
			}
		}
	})

	t.Run("on when the operator asks", func(t *testing.T) {
		t.Setenv(tmuxmcp.RecipeToolEnvironmentVariable, "1")
		session, _, ctx := connect(t)

		var recipe struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
			Steps   string `json:"steps"`
		}
		result := call(ctx, t, session, "get_recipe", map[string]any{
			"name": "recover_pane", "argument": "%3",
		}, &recipe)
		if result.IsError {
			t.Fatalf("get_recipe: %#v", result.Content)
		}
		if !strings.Contains(recipe.Steps, "%3") {
			t.Errorf("the recipe did not mention the pane it was asked about: %q", recipe.Steps)
		}
		if !strings.Contains(recipe.Steps, "copy mode") {
			t.Errorf("the recipe was missing its own advice: %q", recipe.Steps)
		}

		// The same text a client reading prompts would get, because a client
		// that cannot read prompts must not be told something different.
		prompt, err := session.GetPrompt(ctx, &sdk.GetPromptParams{
			Name: "recover_pane", Arguments: map[string]string{"pane": "%3"},
		})
		if err != nil {
			t.Fatalf("get prompt: %v", err)
		}
		text, ok := prompt.Messages[0].Content.(*sdk.TextContent)
		if !ok {
			t.Fatalf("the prompt carried %T", prompt.Messages[0].Content)
		}
		if text.Text != recipe.Steps {
			t.Error("the recipe tool and the prompt disagree about the same job")
		}
	})

	t.Run("an unknown recipe names the ones there are", func(t *testing.T) {
		t.Setenv(tmuxmcp.RecipeToolEnvironmentVariable, "1")
		session, _, ctx := connect(t)
		result := call(ctx, t, session, "get_recipe", map[string]any{"name": "make_coffee"}, nil)
		if !result.IsError {
			t.Fatal("an unknown recipe was accepted")
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || !strings.Contains(text.Text, "diagnose_pane") {
			t.Errorf("the refusal did not say what may be asked for: %#v", result.Content)
		}
	})
}
